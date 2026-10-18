// Package bouncer is the core of evrd: it owns the set of live upstream
// connections on this node, connecting and disconnecting on demand. It also
// tracks attached IRC clients and web chat sockets per user, so the client
// limit can be enforced and a user stopped everywhere at once.
//
// In a cluster a user's connections run only on the node that owns the user
// (see internal/cluster): SetOwnership tells the bouncer which users those
// are, and the cluster starts and stops users as ownership moves.
package bouncer

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/4evrd/evrd/internal/history"
	"github.com/4evrd/evrd/internal/links"
	"github.com/4evrd/evrd/internal/store"
	"github.com/4evrd/evrd/internal/upstream"
	"github.com/4evrd/evrd/internal/version"
)

// ErrSuspended is returned when connecting a suspended user's network.
var ErrSuspended = errors.New("account suspended")

// ErrTooManyClients is returned when a user is at their client limit.
var ErrTooManyClients = errors.New("too many clients attached")

// ErrNotOwner is returned when connecting a network of a user this node
// doesn't own.
var ErrNotOwner = errors.New("this user is served by another node")

type Bouncer struct {
	store   store.Store
	history history.Store
	log     *slog.Logger

	mu      sync.Mutex
	ctx     context.Context
	conns   map[int64]*entry               // network id -> live connection
	clients map[int64]map[*client]struct{} // user id -> attached IRC clients
	streams map[int64]map[*client]struct{} // user id -> open web chat sockets

	rmu   sync.Mutex
	reads map[int64]*netReads // network id -> unread state (networks run here)

	// owns reports whether this node runs a user; nil means every user (a
	// node outside a cluster, or a test).
	owns func(userID int64) bool
	// dial opens upstream connections for a user from a bind host ("" =
	// the node's policy); nil dials directly (ignoring bind hosts).
	dial func(ctx context.Context, userID int64, bind, host string, port int) (net.Conn, error)
	// links, when set, is the connection holder upstream connections live in.
	links *links.Client
}

// SetDialer sets how upstream connections are opened (pacing, source
// addresses, bind hosts — see internal/outbound). Call it before Start.
func (b *Bouncer) SetDialer(dial func(ctx context.Context, userID int64, bind, host string, port int) (net.Conn, error)) {
	b.dial = dial
}

// SetLinks keeps upstream connections in the connection holder, so they
// survive evrd restarting (see internal/links). Call it before Start.
func (b *Bouncer) SetLinks(l *links.Client) { b.links = l }

func linkID(networkID int64) string { return "n" + strconv.FormatInt(networkID, 10) }

// CloseStrayLinks closes held links no running network wants any more — a
// network switched off or deleted while evrd was down. Call it once the
// networks this node runs have started.
func (b *Bouncer) CloseStrayLinks(ctx context.Context) {
	if b.links == nil {
		return
	}
	held, err := b.links.List(ctx)
	if err != nil {
		b.log.Warn("connection holder", "err", err)
		return
	}
	b.mu.Lock()
	want := map[string]bool{}
	for id := range b.conns {
		want[linkID(id)] = true
	}
	b.mu.Unlock()
	for _, l := range held {
		if !want[l.Link] {
			b.log.Info("closing a held link no network wants", "link", l.Link)
			_ = b.links.Close(ctx, l.Link, "")
		}
	}
}

type entry struct {
	conn   *upstream.Conn
	userID int64
	cancel context.CancelFunc
}

func New(st store.Store, hist history.Store, log *slog.Logger) *Bouncer {
	return &Bouncer{
		store: st, history: hist, log: log, conns: map[int64]*entry{},
		clients: map[int64]map[*client]struct{}{}, streams: map[int64]map[*client]struct{}{},
		reads: map[int64]*netReads{},
	}
}

// SetOwnership makes the bouncer run only the users owns reports. Call it
// before Start.
func (b *Bouncer) SetOwnership(owns func(userID int64) bool) { b.owns = owns }

// Owns reports whether this node runs the user.
func (b *Bouncer) Owns(userID int64) bool { return b.owns == nil || b.owns(userID) }

// Store exposes the account database to callers (the API).
func (b *Bouncer) Store() store.Store { return b.store }

// History exposes the chat history store.
func (b *Bouncer) History() history.Store { return b.history }

// DeleteNetwork disconnects a network and deletes it with its history.
func (b *Bouncer) DeleteNetwork(userID, networkID int64) error {
	b.Disconnect(networkID)
	b.dropReads(networkID)
	if err := b.store.DeleteNetwork(userID, networkID); err != nil {
		return err
	}
	return b.history.DeleteNetwork(networkID)
}

// DeleteUser stops a user's connections and deletes the account, its
// networks and their history.
func (b *Bouncer) DeleteUser(userID int64) error {
	b.StopUser(userID)
	nets, err := b.store.ListNetworks(userID)
	if err != nil {
		return err
	}
	if err := b.store.DeleteUser(userID); err != nil {
		return err
	}
	for _, n := range nets {
		if err := b.history.DeleteNetwork(n.ID); err != nil {
			return err
		}
	}
	return nil
}

// Start records the governing context. Outside a cluster it also connects
// every enabled network of users who aren't suspended; in one, the cluster
// starts each user as this node takes them on.
func (b *Bouncer) Start(ctx context.Context) error {
	b.mu.Lock()
	b.ctx = ctx
	b.mu.Unlock()
	if b.owns != nil {
		return nil
	}

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
	u, err := b.store.UserByID(n.UserID)
	if err != nil {
		return err
	} else if u.Suspended {
		return ErrSuspended
	}
	if !b.Owns(n.UserID) {
		return ErrNotOwner
	}
	if _, ok := b.ConnFor(networkID); ok {
		return nil
	}
	b.initReads(n)

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
	var dial func(ctx context.Context, host string, port int) (net.Conn, error)
	if b.dial != nil {
		userID, bind := n.UserID, orElse(n.BindHost, u.BindHost)
		dial = func(ctx context.Context, host string, port int) (net.Conn, error) {
			return b.dial(ctx, userID, bind, host, port)
		}
	}
	var conn *upstream.Conn
	opts := upstream.Options{
		Persist: func(m history.Message) {
			if err := b.history.Add(m); err != nil {
				b.log.Warn("persist message", "err", err)
			}
			b.observe(n, conn.Nick(), m)
		},
		OnEvent:     func(e upstream.Event) { b.AddEvent(n.UserID, e.Kind, e.Title, e.Detail) },
		AltNick:     u.DefaultAltNick,
		QuitMsg:     u.QuitMsg,
		CTCPReplies: u.CTCPReplies,
		Idle:        func() bool { return b.idle(n.UserID) },
		Version:     version.Version,
		OnChannels: func(channels []string) {
			if err := b.store.SetNetworkChannels(networkID, channels); err != nil {
				b.log.Warn("save channels", "network", n.Name, "err", err)
			}
		},
		Dial: dial,
	}
	if b.links != nil {
		opts.Links, opts.LinkID = b.links, linkID(networkID)
		// evrd stopping (not this network): the holder keeps the link
		opts.Detaching = func() bool { return b.ctx != nil && b.ctx.Err() != nil }
	}
	conn = upstream.New(n, b.log, opts)
	b.conns[networkID] = &entry{conn: conn, userID: n.UserID, cancel: cancel}
	if u.AutoAway && len(b.clients[n.UserID])+len(b.streams[n.UserID]) == 0 {
		conn.SetAway(AwayMessage) // applied once registered
	}
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

// Reconfigure pushes settings that apply without reconnecting (quit
// message, flood protection, channel options, CTCP replies) to a running
// network.
func (b *Bouncer) Reconfigure(n store.Network) {
	conn, ok := b.ConnFor(n.ID)
	if !ok {
		return
	}
	u, err := b.store.UserByID(n.UserID)
	if err != nil {
		return
	}
	conn.Reconfigure(n, u.QuitMsg, u.CTCPReplies)
}

// ReconfigureUser pushes a user's settings (quit message, CTCP replies) to
// all their running networks.
func (b *Bouncer) ReconfigureUser(userID int64) {
	nets, err := b.store.ListNetworks(userID)
	if err != nil {
		return
	}
	for _, n := range nets {
		b.Reconfigure(n)
	}
}

// SetChannelSettings changes one channel's options on a network (running
// or not) and saves them.
func (b *Bouncer) SetChannelSettings(networkID int64, ch string, f func(*store.ChannelSettings)) error {
	if conn, ok := b.ConnFor(networkID); ok {
		return b.store.SetNetworkChannelSettings(networkID, conn.SetChannelSettings(ch, f))
	}
	n, err := b.store.NetworkByID(networkID)
	if err != nil {
		return err
	}
	m := map[string]store.ChannelSettings{}
	for k, v := range n.ChannelSettings {
		m[k] = v
	}
	cs := m[strings.ToLower(ch)]
	f(&cs)
	if cs == (store.ChannelSettings{}) {
		delete(m, strings.ToLower(ch))
	} else {
		m[strings.ToLower(ch)] = cs
	}
	return b.store.SetNetworkChannelSettings(networkID, m)
}

// idle reports whether a user has no client attached (IRC or web).
func (b *Bouncer) idle(userID int64) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.clients[userID])+len(b.streams[userID]) == 0
}

// Reconnect restarts a running network's connection so new settings take
// effect; a network that isn't running stays down.
func (b *Bouncer) Reconnect(networkID int64) error {
	if _, ok := b.ConnFor(networkID); !ok {
		return nil
	}
	b.Disconnect(networkID)
	return b.Connect(networkID)
}

// StopUser drops every upstream connection, attached client and web chat
// socket of a user — for suspension, deletion, and handing the user to
// another node. Networks stay enabled, so ResumeUser (here or on the next
// owner) brings them back.
func (b *Bouncer) StopUser(userID int64) {
	b.mu.Lock()
	var stop []*entry
	for id, e := range b.conns {
		if e.userID == userID {
			stop = append(stop, e)
			delete(b.conns, id)
		}
	}
	var kick []*client
	for c := range b.clients[userID] {
		kick = append(kick, c)
	}
	for c := range b.streams[userID] {
		kick = append(kick, c)
	}
	b.mu.Unlock()
	for _, e := range stop {
		e.cancel()
		b.dropReads(e.conn.NetworkID())
	}
	for _, c := range kick {
		c.close()
	}
}

// ResumeUser reconnects a user's enabled networks, if this node runs them.
func (b *Bouncer) ResumeUser(userID int64) {
	if !b.Owns(userID) {
		return // the owner, if any, starts them
	}
	nets, err := b.store.ListNetworks(userID)
	if err != nil {
		return
	}
	for _, n := range nets {
		if n.Enabled {
			if err := b.Connect(n.ID); err != nil {
				b.log.Warn("resume network", "network", n.Name, "err", err)
			}
		}
	}
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

// AttachClient registers an attached IRC client for a user, refusing it when
// the user already has max clients (0 = unlimited). close is called to kick
// the client; Release the attachment when it detaches.
func (b *Bouncer) AttachClient(userID int64, max int, info ClientInfo, close func()) (*Attachment, error) {
	b.mu.Lock()
	set := b.clients[userID]
	if max > 0 && len(set) >= max {
		b.mu.Unlock()
		return nil, ErrTooManyClients
	}
	if set == nil {
		set = map[*client]struct{}{}
		b.clients[userID] = set
	}
	info.ID, info.Kind, info.Since = nextClientID(), "irc", time.Now()
	c := &client{close: close, info: info}
	set[c] = struct{}{}
	b.mu.Unlock()
	b.seen(userID)
	b.refreshAway(userID)
	return &Attachment{b: b, userID: userID, c: c}, nil
}

// TrackStream registers an open web chat socket, so StopUser can close it
// and it counts as attached. Release it when the socket closes.
func (b *Bouncer) TrackStream(userID int64, info ClientInfo, close func()) *Attachment {
	b.mu.Lock()
	if b.streams[userID] == nil {
		b.streams[userID] = map[*client]struct{}{}
	}
	info.ID, info.Kind, info.Since = nextClientID(), "web", time.Now()
	c := &client{close: close, info: info}
	b.streams[userID][c] = struct{}{}
	b.mu.Unlock()
	b.seen(userID)
	b.refreshAway(userID)
	return &Attachment{b: b, userID: userID, c: c, web: true}
}

// Clients is the number of IRC clients a user has attached.
func (b *Bouncer) Clients(userID int64) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.clients[userID])
}

// ClientsByUser is the number of attached IRC clients per user.
func (b *Bouncer) ClientsByUser() map[int64]int {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make(map[int64]int, len(b.clients))
	for uid, set := range b.clients {
		out[uid] = len(set)
	}
	return out
}

// Usage is a server-wide snapshot for the admin overview.
type Usage struct {
	ConnectedNetworks int `json:"connectedNetworks"`
	AttachedClients   int `json:"attachedClients"`
}

func (b *Bouncer) Usage() Usage {
	b.mu.Lock()
	defer b.mu.Unlock()
	u := Usage{}
	for _, e := range b.conns {
		if e.conn.Status() == upstream.StatusConnected {
			u.ConnectedNetworks++
		}
	}
	for _, set := range b.clients {
		u.AttachedClients += len(set)
	}
	return u
}

// PruneLoop is periodic housekeeping until ctx is done. Every node trims the
// backlog of the users it runs to their limit. The leader (one node in a
// cluster; nil leader means this node) also drops the history of networks
// that no longer exist (deleted by evrctl, or while their owner was down),
// expired web sessions, and leases of deleted users.
// backlog returns a user's line limit (0 = keep everything).
func (b *Bouncer) PruneLoop(ctx context.Context, interval time.Duration, backlog func(store.User) int, leader func() bool) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		b.prune(backlog, leader == nil || leader())
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (b *Bouncer) prune(backlog func(store.User) int, leader bool) {
	users, err := b.store.ListUsers()
	if err != nil {
		b.log.Warn("prune: list users", "err", err)
		return
	}
	for _, u := range users {
		keep := backlog(u)
		if keep <= 0 || !b.Owns(u.ID) {
			continue
		}
		nets, err := b.store.ListNetworks(u.ID)
		if err != nil {
			continue
		}
		for _, n := range nets {
			if err := b.history.Prune(n.ID, keep); err != nil {
				b.log.Warn("prune backlog", "network", n.ID, "err", err)
			}
		}
	}
	if !leader {
		return
	}
	b.dropOrphanHistory(users)
	if err := b.store.PruneUserEvents(time.Now().AddDate(0, 0, -30)); err != nil {
		b.log.Warn("prune activity feed", "err", err)
	}
	if err := b.store.DeleteExpiredSessions(time.Now()); err != nil {
		b.log.Warn("expire sessions", "err", err)
	}
	if err := b.store.DeleteOrphanLeases(); err != nil {
		b.log.Warn("drop leases of deleted users", "err", err)
	}
}

// dropOrphanHistory deletes history whose network is gone.
func (b *Bouncer) dropOrphanHistory(users []store.User) {
	exists := map[int64]bool{}
	for _, u := range users {
		nets, err := b.store.ListNetworks(u.ID)
		if err != nil {
			return // can't tell what exists; don't delete anything
		}
		for _, n := range nets {
			exists[n.ID] = true
		}
	}
	ids, err := b.history.Networks()
	if err != nil {
		b.log.Warn("list history", "err", err)
		return
	}
	for _, id := range ids {
		if !exists[id] {
			if err := b.history.DeleteNetwork(id); err != nil {
				b.log.Warn("drop orphan history", "network", id, "err", err)
			} else {
				b.log.Info("dropped history of a deleted network", "network", id)
			}
		}
	}
}

func orElse(s, fallback string) string {
	if s != "" {
		return s
	}
	return fallback
}
