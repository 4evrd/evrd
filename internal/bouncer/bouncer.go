// Package bouncer is the core of evrd: it owns the store and the set of live
// upstream connections, auto-connecting enabled networks on startup and
// connecting/disconnecting on demand.
package bouncer

import (
	"context"
	"log/slog"
	"sync"

	"code.ircforever.org/ircforever/evrd/internal/store"
	"code.ircforever.org/ircforever/evrd/internal/upstream"
)

type Bouncer struct {
	store *store.Store
	log   *slog.Logger

	mu    sync.Mutex
	ctx   context.Context
	conns map[int64]*entry
}

type entry struct {
	conn   *upstream.Conn
	cancel context.CancelFunc
}

func New(st *store.Store, log *slog.Logger) *Bouncer {
	return &Bouncer{store: st, log: log, conns: map[int64]*entry{}}
}

// Store exposes the persistence layer to callers (the API).
func (b *Bouncer) Store() *store.Store { return b.store }

// Start records the governing context and auto-connects enabled networks.
func (b *Bouncer) Start(ctx context.Context) error {
	b.mu.Lock()
	b.ctx = ctx
	b.mu.Unlock()

	nets, err := b.store.AllEnabledNetworks()
	if err != nil {
		return err
	}
	for _, n := range nets {
		if err := b.Connect(n.ID); err != nil {
			b.log.Warn("auto-connect failed", "network", n.Name, "err", err)
		}
	}
	return nil
}

// Connect starts (and marks enabled) the upstream connection for a network. A
// no-op if it's already running.
func (b *Bouncer) Connect(networkID int64) error {
	n, err := b.store.NetworkByID(networkID)
	if err != nil {
		return err
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.conns[networkID]; ok {
		return nil
	}
	parent := b.ctx
	if parent == nil {
		parent = context.Background()
	}
	cctx, cancel := context.WithCancel(parent)
	conn := upstream.New(n, b.log, func(m store.Message) {
		if err := b.store.AddMessage(m); err != nil {
			b.log.Warn("persist message", "err", err)
		}
	})
	b.conns[networkID] = &entry{conn: conn, cancel: cancel}
	go conn.Run(cctx)
	b.log.Info("connecting network", "network", n.Name, "host", n.Host)

	return b.store.SetNetworkEnabled(networkID, true)
}

// Disconnect stops (and marks disabled) a network's connection.
func (b *Bouncer) Disconnect(networkID int64) error {
	b.mu.Lock()
	e := b.conns[networkID]
	delete(b.conns, networkID)
	b.mu.Unlock()
	if e != nil {
		e.cancel()
	}
	return b.store.SetNetworkEnabled(networkID, false)
}

// ConnFor returns the running connection for a network, if any.
func (b *Bouncer) ConnFor(networkID int64) (*upstream.Conn, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	e := b.conns[networkID]
	if e == nil {
		return nil, false
	}
	return e.conn, true
}

// Status returns the live status for a network.
func (b *Bouncer) Status(networkID int64) upstream.Status {
	if c, ok := b.ConnFor(networkID); ok {
		return c.Status()
	}
	return upstream.StatusDisconnected
}
