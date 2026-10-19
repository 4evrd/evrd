// Command evrd is the IRCForever IRC bouncer daemon.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
	_ "time/tzdata" // time zones for playback timestamps, even in images without tzdata

	"github.com/4evrd/evrd/internal/accounts"
	"github.com/4evrd/evrd/internal/api"
	"github.com/4evrd/evrd/internal/auth"
	"github.com/4evrd/evrd/internal/bouncer"
	"github.com/4evrd/evrd/internal/certs"
	"github.com/4evrd/evrd/internal/client"
	"github.com/4evrd/evrd/internal/cluster"
	"github.com/4evrd/evrd/internal/config"
	"github.com/4evrd/evrd/internal/directory"
	"github.com/4evrd/evrd/internal/history"
	"github.com/4evrd/evrd/internal/identfile"
	"github.com/4evrd/evrd/internal/links"
	"github.com/4evrd/evrd/internal/listen"
	"github.com/4evrd/evrd/internal/metricsq"
	"github.com/4evrd/evrd/internal/outbound"
	"github.com/4evrd/evrd/internal/sandbox"
	"github.com/4evrd/evrd/internal/settings"
	"github.com/4evrd/evrd/internal/store"
	"github.com/4evrd/evrd/internal/telemetry"
	"github.com/4evrd/evrd/internal/version"
)

func main() {
	os.Exit(run(context.Background(), os.Args, os.Stdout, os.Stderr))
}

// run is evrd: args are its command line (args[0] the program name), and the
// result is the exit status. It serves until ctx is cancelled or SIGINT or
// SIGTERM arrives. Failures are logged to stderr where they happen.
func run(parent context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "", "path to config file (optional; default $EVRD_CONFIG)")
	showVersion := flags.Bool("version", false, "print version and exit")
	if err := flags.Parse(args[1:]); err != nil {
		return flagExit(err)
	}
	if *configPath == "" {
		*configPath = os.Getenv("EVRD_CONFIG") // e.g. set in a container image or compose file
	}

	if *showVersion {
		fmt.Fprintln(stdout, "evrd", version.Version)
		return 0
	}

	log := slog.New(slog.NewTextHandler(stderr, nil))

	// The file (or defaults); the environment and the settings saved in the
	// admin UI are layered on top (see internal/settings).
	base := config.Default()
	if *configPath != "" {
		loaded, err := config.LoadFile(*configPath)
		if err != nil {
			log.Error("load config", "path", *configPath, "err", err)
			return 1
		}
		base = loaded
	}
	cfg, err := base.WithSettings(nil)
	if err != nil {
		log.Error("environment", "err", err)
		return 1
	}

	cur, old, err := cfg.SecretKeys()
	if err == nil {
		err = store.SetSecretKeys(cur, old...)
	}
	if err != nil {
		log.Error("secret key", "err", err)
		return 1
	}
	if cur == nil {
		log.Warn("no secret_key set: network passwords are stored unencrypted (evrctl genkey makes a key)")
	}

	// Certificates for the IRC ports and evrd's own HTTPS: files are read
	// now, while they're in reach.
	certMgr := certs.New(log)
	if err := certMgr.LoadFiles(cfg.TLSCert, cfg.TLSKey); err != nil && cfg.TLSSource() == "files" {
		log.Error("tls_cert", "err", err)
		return 1
	}

	// Confine evrd (chroot, user; unveil and pledge on OpenBSD) now that it
	// has read what lives outside: the config, the keys, the certificate.
	hostname, _ := os.Hostname()
	dbDSN, histDSN, webRoot := cfg.DB, cfg.History, cfg.WebRoot
	linksSocket := cfg.LinksSocket
	if linksSocket != "" && cfg.Cluster.Enabled() {
		log.Warn("links_socket is for a single node — in a cluster, users move between nodes instead; ignoring it")
		linksSocket = ""
	}
	if sb := (sandbox.Config{Chroot: cfg.Chroot, User: cfg.User}); sb.Enabled() {
		// Ports below 1024 need root: bind every port first.
		addrs := append([]string{cfg.API, cfg.HTTPSListen, cfg.HTTPListen, cfg.Cluster.InternalListen, cfg.Cluster.InternalIRCListen}, cfg.Listen...)
		if err := listen.Prebind(addrs...); err != nil {
			log.Error("listen", "err", err)
			return 1
		}
		jail := *cfg
		if err := jail.Jail(); err != nil {
			log.Error("chroot", "err", err)
			return 1
		}
		dbDSN, histDSN, webRoot = jail.DB, jail.History, jail.WebRoot
		if base.WriteConf && cfg.Chroot != "" {
			log.Warn("settings_write_conf is off: the config file is outside the chroot")
			base.WriteConf = false
		}
		sb.Read = []string{"/etc/resolv.conf", "/etc/hosts"}
		if webRoot != "" {
			sb.Read = append(sb.Read, webRoot)
		}
		for _, f := range []string{cfg.TLSCert, cfg.TLSKey} {
			if in, ok := cfg.InChroot(f); ok {
				sb.Read = append(sb.Read, in) // re-read on SIGHUP after a renewal
			}
		}
		if p, ok := localPath(dbDSN, "sqlite:"); ok {
			sb.Write = append(sb.Write, filepath.Dir(p)) // the db and its -wal, -shm
		}
		if p, ok := localPath(histDSN, "pebble:"); ok {
			sb.Write = append(sb.Write, p)
		}
		if p, ok := directoryFile(cfg.NetworkDirectory); ok {
			if in, ok := cfg.InChroot(p); ok || cfg.Chroot == "" {
				sb.Read = append(sb.Read, in) // a network directory kept locally
			}
		}
		sb.Write = append(sb.Write, os.TempDir())
		if cfg.IdentFile != "" {
			in, ok := cfg.InChroot(cfg.IdentFile)
			if cfg.Chroot != "" && !ok {
				log.Error("ident_file must be inside the chroot", "path", cfg.IdentFile, "chroot", cfg.Chroot)
				return 1
			}
			sb.Write = append(sb.Write, filepath.Dir(in)) // replaced by a rename
		}
		if linksSocket != "" {
			in, ok := cfg.InChroot(linksSocket)
			if cfg.Chroot != "" && !ok {
				log.Error("links_socket must be inside the chroot", "path", linksSocket, "chroot", cfg.Chroot)
				return 1
			}
			linksSocket = in
			sb.Write = append(sb.Write, linksSocket)
			// pass sockets to the holder
			sb.Promises = "stdio rpath wpath cpath fattr flock inet dns unix sendfd"
		}
		sandbox.Preload()
		if err := sandbox.Enter(sb); err != nil {
			log.Error("sandbox", "err", err)
			return 1
		}
		log.Info("sandboxed", "chroot", cfg.Chroot, "user", cfg.User)
		if cfg.Chroot != "" {
			// Certificate files inside the chroot can be re-read after a
			// renewal (SIGHUP); ones outside it need a restart.
			jailed := *cfg
			certMgr.SetPathMap(func(p string) string { q, _ := jailed.InChroot(p); return q })
		}
	}

	st, err := store.Open(dbDSN)
	if err != nil {
		log.Error("open store", "dsn", dbDSN, "err", err)
		return 1
	}
	defer st.Close()
	if n, err := st.EncryptSecrets(); err != nil {
		log.Error("encrypt saved secrets", "err", err)
		return 1
	} else if n > 0 {
		log.Info("re-encrypted saved secrets", "rows", n)
	}

	nodeID := cfg.Cluster.NodeID
	if nodeID == "" {
		nodeID = hostname
	}
	sm, err := settings.New(st, base, nodeID, log)
	if err != nil {
		log.Error("load settings", "err", err)
		return 1
	}
	cfg = sm.Config()
	certMgr.SetStore(st)
	if err := certMgr.Apply(cfg); err != nil {
		log.Error("tls", "err", err)
		return 1
	}
	if cfg.WriteConf && cfg.Cluster.Enabled() {
		log.Warn("settings_write_conf is on in a cluster: every node rewrites its own config file — usually leave it off and let the database hold settings")
	}

	hist, err := history.Open(histDSN)
	if err != nil {
		log.Error("open history", "store", redact(histDSN), "err", err)
		return 1
	}
	defer hist.Close()
	// History used to live in the SQL database: move any left there, once.
	n, err := st.MigrateLegacyMessages(func(m store.LegacyMessage) error {
		return hist.Add(history.Message{
			NetworkID: m.NetworkID, Target: m.Target, Sender: m.Sender,
			Kind: m.Kind, Text: m.Text, Extra: m.Extra, Time: m.Time,
		})
	})
	if err != nil {
		log.Error("move history out of the database", "moved", n, "err", err)
		return 1
	}
	if n > 0 {
		log.Info("moved history out of the database", "messages", n, "to", redact(cfg.History))
	}

	ctx, stop := signal.NotifyContext(parent, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	acc := accounts.New(st, cfg.Limits)
	acc.SetBindHosts(cfg.Outbound.BindHosts)
	am, err := auth.New(ctx, st, acc, cfg.Auth, cfg.PublicURL, log)
	if err != nil {
		log.Error("set up sign-in", "issuer", cfg.Auth.OIDCIssuer, "err", err)
		return 1
	}
	if !cfg.Auth.PasswordLogin && !cfg.Auth.OIDCEnabled() {
		log.Warn("password_login is off and no oidc_issuer is set — nobody can sign in to the web app")
	}

	// Cluster membership: which users this node runs. A lone node owns
	// everyone; with several, users spread across them and move on failure.
	cc := cfg.Cluster
	cc.NodeID = nodeID
	if cc.Enabled() {
		if cc.Secret == "" || cc.InternalURL == "" || cc.InternalIRCListen == "" || cc.InternalIRCAddr == "" {
			log.Error("cluster: internal_listen needs cluster_secret, internal_url, internal_irc_listen and internal_irc_addr")
			return 1
		}
		if !strings.HasPrefix(cfg.History, "scylla://") && !strings.HasPrefix(cfg.History, "cassandra://") {
			log.Warn("cluster: history is on local disk — each node will only see its own; use a scylla:// history store")
		}
	}

	b := bouncer.New(st, hist, log)
	cl := cluster.New(st, store.Node{ID: cc.NodeID, HTTPURL: cc.InternalURL, IRCAddr: cc.InternalIRCAddr}, cluster.Hooks{
		Acquire: b.ResumeUser,
		Lose:    b.StopUser,
	}, log)
	if cc.LeaseTTL > 0 {
		cl.SetLeaseTTL(cc.LeaseTTL)
	}
	b.SetOwnership(cl.Owns)
	ob, err := outbound.New(outbound.Config{Rate: cfg.Outbound.ConnectRate, Bind: cfg.Outbound.Bind, Prefix6: cfg.Outbound.Prefix6})
	if err != nil {
		log.Error("outbound", "err", err)
		return 1
	}
	b.SetDialer(ob.DialFrom)
	b.SetModulesOff(cfg.ModulesOff)
	if linksSocket != "" {
		b.SetLinks(links.NewClient(linksSocket))
		log.Info("connection holder on: IRC connections survive restarts", "socket", linksSocket)
	}
	if len(cfg.Outbound.Bind) > 0 || cfg.Outbound.Prefix6.IsValid() {
		log.Info("outbound addresses", "bind", cfg.Outbound.Bind, "prefix6", cfg.Outbound.Prefix6)
	}
	if err := b.Start(ctx); err != nil {
		log.Error("start bouncer", "err", err)
		return 1
	}
	cl.Tick() // take this node's users before serving anyone
	b.CloseStrayLinks(ctx)
	if n := len(cl.Nodes()); n > 1 && !cc.Enabled() {
		log.Error("cluster: other evrd nodes are running against this database but internal_listen isn't set — users on other nodes can't be reached from here", "nodes", n)
	}
	log.Info("cluster member", "node", cc.NodeID, "nodes", len(cl.Nodes()))
	clusterDone := make(chan struct{})
	go func() { cl.Run(ctx); close(clusterDone) }()
	go b.PruneLoop(ctx, 10*time.Minute, func(u store.User) int { return acc.LimitsFor(u).BacklogLines }, cl.Leader)

	// Metrics: pushed over OTLP (to VictoriaMetrics), read back for the
	// dashboards from metrics_url.
	stopTelemetry, err := telemetry.Setup(ctx, telemetry.Config{
		Endpoint: cfg.Metrics.OTLPEndpoint, Interval: cfg.Metrics.Interval, NodeID: cc.NodeID, Version: version.Version,
	})
	if err != nil {
		log.Error("telemetry", "err", err)
		return 1
	}
	defer func() {
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		stopTelemetry(sctx)
	}()
	gauges := telemetry.Gauges{
		Upstreams: b.UpstreamsByStatus,
		Links:     b.Links,
		Clients:   func() int64 { return int64(b.Usage().AttachedClients) },
		Streams:   b.Streams,
		Users:     func() int64 { return int64(cl.HeldCount()) },
		Nodes:     func() int64 { return int64(len(cl.Nodes())) },
	}
	if p, ok := hist.(interface{ Pending() int64 }); ok {
		gauges.Queue = p.Pending
	}
	if err := telemetry.RegisterGauges(gauges); err != nil {
		log.Error("telemetry gauges", "err", err)
		return 1
	}
	if cfg.Metrics.OTLPEndpoint != "" {
		log.Info("exporting metrics", "endpoint", redact(cfg.Metrics.OTLPEndpoint))
	}

	// Downstream IRC listener for native clients (Hexchat, irssi, …).
	irc := client.NewServer(b, acc, log, cfg.TLSCert, cfg.TLSKey)
	srv := api.NewServer(b, acc, am, cfg, irc, log)
	srv.SetMetrics(metricsq.New(cfg.Metrics.QueryURL))
	srv.SetSettings(sm)
	irc.SetTLSConfig(certMgr.IRCConfig())
	irc.SetPublicURL(cfg.PublicURL)
	srv.SetCerts(certMgr)
	// The network directory users pick from when adding a network: fetched
	// now and twice a day, the last good copy kept in the database.
	dir := directory.New(cfg.NetworkDirectory, st, log)
	if cfg.Chroot != "" {
		jailed := *cfg
		dir.PathMap = func(p string) string { q, _ := jailed.InChroot(p); return q }
	}
	go dir.Run(ctx)
	srv.SetDirectory(dir)
	// The oidentd file that gives users with a bind host their ident.
	if cfg.IdentFile != "" {
		path, _ := cfg.InChroot(cfg.IdentFile)
		iw := identfile.New(path, st, log)
		iw.Exclude = cfg.Outbound.Bind
		go iw.Run(ctx)
	}
	irc.SetDirectory(dir)
	if webRoot != "" {
		srv.SetWebApp(webRoot)
		log.Info("serving the web app", "dir", webRoot)
	}

	// Settings changed in the admin UI (here or on another node) apply live;
	// the restart-only ones (public URL, the SSO provider, metrics export)
	// keep their running values until evrd restarts.
	running := cfg
	metricsURL := cfg.Metrics.QueryURL
	sm.OnChange(func(c *config.Config) {
		live := *c
		live.PublicURL = running.PublicURL
		live.Auth.OIDCIssuer, live.Auth.OIDCClientID, live.Auth.OIDCClientSecret =
			running.Auth.OIDCIssuer, running.Auth.OIDCClientID, running.Auth.OIDCClientSecret
		live.Metrics.OTLPEndpoint, live.Metrics.Interval = running.Metrics.OTLPEndpoint, running.Metrics.Interval
		acc.SetDefaults(live.Limits)
		acc.SetBindHosts(live.Outbound.BindHosts)
		am.SetConfig(live.Auth)
		ob.SetRate(live.Outbound.ConnectRate)
		b.SetModulesOff(live.ModulesOff)
		if err := certMgr.Apply(&live); err != nil {
			log.Warn("tls", "err", err)
		}
		srv.SetConfig(&live)
		dir.SetSource(live.NetworkDirectory)
		if live.Metrics.QueryURL != metricsURL {
			metricsURL = live.Metrics.QueryURL
			srv.SetMetrics(metricsq.New(metricsURL))
		}
	})
	go sm.Run(ctx)
	// A listener that fails stops evrd; it then exits 1, not 0, so a service
	// manager sees the failure.
	var failed atomic.Bool
	if cc.Enabled() {
		irc.SetCluster(cl, cc.Secret)
		srv.SetCluster(cl, cc.Secret)
		go irc.ListenInternal(ctx, cc.InternalIRCListen)
		go func() {
			if err := srv.ListenInternal(ctx, cc.InternalListen); err != nil {
				log.Error("internal api", "err", err)
				failed.Store(true)
				stop()
			}
		}()
	}
	go irc.ListenAndServe(ctx, cfg.Listen)
	go certMgr.Run(ctx)
	// SIGHUP (rcctl reload evrd): re-read the certificate files, e.g. after a
	// renewal — no restart, so nobody's connections drop.
	hup := make(chan os.Signal, 1)
	signal.Notify(hup, syscall.SIGHUP)
	defer signal.Stop(hup)
	go func() {
		for range hup {
			if err := certMgr.Reload(); err != nil {
				log.Warn("reload certificate", "err", err)
			}
		}
	}()
	if cfg.HTTPSListen != "" {
		go func() {
			if err := srv.ListenAndServeTLS(ctx, cfg.HTTPSListen, certMgr.TLSConfig()); err != nil {
				log.Error("https", "err", err)
				failed.Store(true)
				stop()
			}
		}()
	}
	if cfg.HTTPListen != "" {
		go func() {
			if err := srv.ListenAndServeHTTP(ctx, cfg.HTTPListen, certMgr.HTTPHandler(cfg.HTTPSListen)); err != nil {
				log.Error("http", "err", err)
				failed.Store(true)
				stop()
			}
		}()
	}

	if err := srv.ListenAndServe(ctx, cfg.API); err != nil {
		log.Error("api server", "err", err)
		return 1
	}
	// Shutting down: hand this node's users to the others before closing the
	// stores underneath them.
	<-clusterDone
	if failed.Load() {
		return 1
	}
	return 0
}

// flagExit is the exit status for a command-line parse error, as
// flag.ExitOnError would exit: 0 for -h or -help, else 2. The flag package
// has already printed the error and usage.
func flagExit(err error) int {
	if errors.Is(err, flag.ErrHelp) {
		return 0
	}
	return 2
}

// localPath is the file path in an SQLite or Pebble DSN (not a server's).
func localPath(dsn, scheme string) (string, bool) {
	if dsn == "" || strings.Contains(dsn, "://") {
		return "", false
	}
	p, _, _ := strings.Cut(strings.TrimPrefix(dsn, scheme), "?")
	return p, p != ""
}

// directoryFile is the path of a network directory kept in a local file
// (not fetched over HTTP).
func directoryFile(src string) (string, bool) {
	if src == "" || (strings.Contains(src, "://") && !strings.HasPrefix(src, "file://")) {
		return "", false
	}
	return strings.TrimPrefix(src, "file://"), true
}

// redact hides a password in a DSN for logging.
func redact(dsn string) string {
	if u, err := url.Parse(dsn); err == nil && u.User != nil {
		return u.Redacted()
	}
	return dsn
}
