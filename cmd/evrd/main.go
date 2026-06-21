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

	"code.ircforever.org/ircforever/evrd/internal/bouncer"
	"code.ircforever.org/ircforever/evrd/internal/config"
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

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	b := bouncer.New(cfg, log)
	if err := b.Run(ctx); err != nil && err != context.Canceled {
		log.Error("evrd exited with error", "err", err)
		os.Exit(1)
	}
}
