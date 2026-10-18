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

	"code.ircforever.org/ircforever/evrd/internal/accounts"
	"code.ircforever.org/ircforever/evrd/internal/api"
	"code.ircforever.org/ircforever/evrd/internal/auth"
	"code.ircforever.org/ircforever/evrd/internal/bouncer"
	"code.ircforever.org/ircforever/evrd/internal/client"
	"code.ircforever.org/ircforever/evrd/internal/cluster"
	"code.ircforever.org/ircforever/evrd/internal/config"
	"code.ircforever.org/ircforever/evrd/internal/history"
	"code.ircforever.org/ircforever/evrd/internal/outbound"
	"code.ircforever.org/ircforever/evrd/internal/store"
	"code.ircforever.org/ircforever/evrd/internal/version"
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

	cfg := config.Default()
	if err := cfg.ApplyEnv(); err != nil {
		log.Error("environment", "err", err)
		os.Exit(1)
	}
	if *configPath != "" {
		loaded, err := config.Load(*configPath)
		if err != nil {
			log.Error("load config", "path", *configPath, "err", err)
			os.Exit(1)
		}
		cfg = loaded
	}

	st, err := store.Open(cfg.DB)
	if err != nil {
		log.Error("open store", "dsn", cfg.DB, "err", err)
		os.Exit(1)
	}
	defer st.Close()

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
	if cc.NodeID == "" {
		cc.NodeID, _ = os.Hostname()
	}
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
	b.SetDialer(ob.Dial)
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

	// Downstream IRC listener for native clients (Hexchat, irssi, …).
	irc := client.NewServer(b, acc, log, cfg.TLSCert, cfg.TLSKey)
	srv := api.NewServer(b, acc, am, cfg, irc, log)
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
