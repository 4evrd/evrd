// Package bouncer is the core of evrd: it owns users, their upstream network
// connections, message buffers, and attached client sessions.
package bouncer

import (
	"context"
	"log/slog"

	"code.ircforever.org/ircforever/evrd/internal/config"
	"code.ircforever.org/ircforever/evrd/internal/server"
)

// Bouncer is the running core.
type Bouncer struct {
	cfg *config.Config
	log *slog.Logger
}

// New constructs a Bouncer from the given config.
func New(cfg *config.Config, log *slog.Logger) *Bouncer {
	return &Bouncer{cfg: cfg, log: log}
}

// Run starts the core and blocks until ctx is cancelled.
//
// Skeleton: opens the listeners. The store (users/networks/history), upstream
// dialing, and message routing are layered on from here.
func (b *Bouncer) Run(ctx context.Context) error {
	b.log.Info("evrd core starting", "db", b.cfg.DB, "listeners", len(b.cfg.Listen))

	srv := server.New(b.cfg.Listen, b.log)
	err := srv.Start(ctx)

	b.log.Info("evrd core stopped")
	return err
}
