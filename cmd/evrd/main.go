// Command evrd is the IRCForever IRC bouncer daemon.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
	_ "time/tzdata" // time zones for playback timestamps, even in images without tzdata

	"github.com/4evrd/evrd/internal/accounts"
	"github.com/4evrd/evrd/internal/api"
	"github.com/4evrd/evrd/internal/auth"
	"github.com/4evrd/evrd/internal/bouncer"
	"github.com/4evrd/evrd/internal/client"
	"github.com/4evrd/evrd/internal/cluster"
	"github.com/4evrd/evrd/internal/config"
	"github.com/4evrd/evrd/internal/history"
	"github.com/4evrd/evrd/internal/metricsq"
	"github.com/4evrd/evrd/internal/outbound"
	"github.com/4evrd/evrd/internal/settings"
	"github.com/4evrd/evrd/internal/store"
	"github.com/4evrd/evrd/internal/telemetry"
	"github.com/4evrd/evrd/internal/version"
)

func main() {
	configPath := flag.String("config", "", "path to config file (optional)")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println("evrd", version.Version)
		return
	}

	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	// The file (or defaults); the environment and the settings saved in the
	// admin UI are layered on top (see internal/settings).
	base := config.Default()
	if *configPath != "" {
		loaded, err := config.LoadFile(*configPath)
		if err != nil {
			log.Error("load config", "path", *configPath, "err", err)
			os.Exit(1)
		}
		base = loaded
	}
	cfg, err := base.WithSettings(nil)
	if err != nil {
		log.Error("environment", "err", err)
		os.Exit(1)
	}

	cur, old, err := cfg.SecretKeys()
	if err == nil {
		err = store.SetSecretKeys(cur, old...)
	}
	if err != nil {
		log.Error("secret key", "err", err)
		os.Exit(1)
	}
	if cur == nil {
		log.Warn("no secret_key set: network passwords are stored unencrypted (evrctl genkey makes a key)")
	}
	st, err := store.Open(cfg.DB)
	if err != nil {
		log.Error("open store", "dsn", cfg.DB, "err", err)
		os.Exit(1)
	}
	defer st.Close()
	if n, err := st.EncryptSecrets(); err != nil {
		log.Error("encrypt saved secrets", "err", err)
		os.Exit(1)
	} else if n > 0 {
		log.Info("re-encrypted saved secrets", "networks", n)
	}

	nodeID := cfg.Cluster.NodeID
	if nodeID == "" {
		nodeID, _ = os.Hostname()
	}
	sm, err := settings.New(st, base, nodeID, log)
	if err != nil {
		log.Error("load settings", "err", err)
		os.Exit(1)
	}
	cfg = sm.Config()
	if cfg.WriteConf && cfg.Cluster.Enabled() {
		log.Warn("settings_write_conf is on in a cluster: every node rewrites its own config file — usually leave it off and let the database hold settings")
	}

	hist, err := history.Open(cfg.History)
	if err != nil {
		log.Error("open history", "store", redact(cfg.History), "err", err)
		os.Exit(1)
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
		os.Exit(1)
	}
	if n > 0 {
		log.Info("moved history out of the database", "messages", n, "to", redact(cfg.History))
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	acc := accounts.New(st, cfg.Limits)
	acc.SetBindHosts(cfg.Outbound.BindHosts)
	am, err := auth.New(ctx, st, acc, cfg.Auth, cfg.PublicURL, log)
	if err != nil {
		log.Error("set up sign-in", "issuer", cfg.Auth.OIDCIssuer, "err", err)
		os.Exit(1)
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
			os.Exit(1)
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
		os.Exit(1)
	}
	b.SetDialer(ob.DialFrom)
	if len(cfg.Outbound.Bind) > 0 || cfg.Outbound.Prefix6.IsValid() {
		log.Info("outbound addresses", "bind", cfg.Outbound.Bind, "prefix6", cfg.Outbound.Prefix6)
	}
	if err := b.Start(ctx); err != nil {
		log.Error("start bouncer", "err", err)
		os.Exit(1)
	}
	cl.Tick() // take this node's users before serving anyone
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
		os.Exit(1)
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
		os.Exit(1)
	}
	if cfg.Metrics.OTLPEndpoint != "" {
		log.Info("exporting metrics", "endpoint", cfg.Metrics.OTLPEndpoint)
	}

	// Downstream IRC listener for native clients (Hexchat, irssi, …).
	irc := client.NewServer(b, acc, log, cfg.TLSCert, cfg.TLSKey)
	srv := api.NewServer(b, acc, am, cfg, irc, log)
	srv.SetMetrics(metricsq.New(cfg.Metrics.QueryURL))
	srv.SetSettings(sm)

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
		srv.SetConfig(&live)
		if live.Metrics.QueryURL != metricsURL {
			metricsURL = live.Metrics.QueryURL
			srv.SetMetrics(metricsq.New(metricsURL))
		}
	})
	go sm.Run(ctx)
	if cc.Enabled() {
		irc.SetCluster(cl, cc.Secret)
		srv.SetCluster(cl, cc.Secret)
		go irc.ListenInternal(ctx, cc.InternalIRCListen)
		go func() {
			if err := srv.ListenInternal(ctx, cc.InternalListen); err != nil {
				log.Error("internal api", "err", err)
				stop()
			}
		}()
	}
	go irc.ListenAndServe(ctx, cfg.Listen)

	if err := srv.ListenAndServe(ctx, cfg.API); err != nil {
		log.Error("api server", "err", err)
		os.Exit(1)
	}
	// Shutting down: hand this node's users to the others before closing the
	// stores underneath them.
	<-clusterDone
}

// redact hides a password in a DSN for logging.
func redact(dsn string) string {
	if u, err := url.Parse(dsn); err == nil && u.User != nil {
		return u.Redacted()
	}
	return dsn
}
