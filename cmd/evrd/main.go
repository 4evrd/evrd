// Command evrd is the IRCForever IRC bouncer daemon.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"code.ircforever.org/ircforever/evrd/internal/api"
	"code.ircforever.org/ircforever/evrd/internal/bouncer"
	"code.ircforever.org/ircforever/evrd/internal/client"
	"code.ircforever.org/ircforever/evrd/internal/config"
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

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	b := bouncer.New(st, log)
	if err := b.Start(ctx); err != nil {
		log.Error("start bouncer", "err", err)
		os.Exit(1)
	}

	// Downstream IRC listener for native clients (Hexchat, irssi, …).
	go client.NewServer(b, log).ListenAndServe(ctx, cfg.Listen)

	srv := api.NewServer(b, log)
	if err := srv.ListenAndServe(ctx, cfg.API); err != nil {
		log.Error("api server", "err", err)
		os.Exit(1)
	}
}
