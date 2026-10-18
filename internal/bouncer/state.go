package bouncer

// Per-user live state the owner node keeps beside the connections: unread and
// mention counts per buffer (from read markers), the attached clients, the
// activity feed, presence (last seen, auto-away).

import (
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/4evrd/evrd/internal/history"
	"github.com/4evrd/evrd/internal/store"
	"github.com/4evrd/evrd/internal/telemetry"
	"github.com/4evrd/evrd/internal/upstream"
)

// AwayMessage is set on every network while auto-away is on and no client
// is attached.
const AwayMessage = "Away — evrd is keeping my seat"

// --- attached clients ----------------------------------------------------

// ClientInfo describes an attached IRC client or web chat.
type ClientInfo struct {
	ID       string    `json:"id"`
	Kind     string    `json:"kind"`     // irc | web
	Software string    `json:"software"` // "HexChat", "irssi"… (from CTCP VERSION); web: the browser
	Device   string    `json:"device"`   // the client password's name, if one was used
	Networks []string  `json:"networks"`
	IP       string    `json:"ip"`
	Since    time.Time `json:"since"`
}

type client struct {
	close func()
	info  ClientInfo
}

// Attachment is a registered client; Release it when the client goes.
type Attachment struct {
	b      *Bouncer
	userID int64
	c      *client
	web    bool
}

// ID is the client's id.
func (a *Attachment) ID() string { return a.c.info.ID }

// Update changes the client's description (software learned, network jumped).
func (a *Attachment) Update(f func(*ClientInfo)) {
	a.b.mu.Lock()
	f(&a.c.info)
	a.b.mu.Unlock()
}

// Release unregisters the client.
func (a *Attachment) Release() {
	b := a.b
	b.mu.Lock()
	set := b.clients
	if a.web {
		set = b.streams
	}
	delete(set[a.userID], a.c)
	if len(set[a.userID]) == 0 {
		delete(set, a.userID)
	}
	b.mu.Unlock()
	b.seen(a.userID)
	b.refreshAway(a.userID)
}

var clientSeq atomic.Int64

func nextClientID() string { return strconv.FormatInt(clientSeq.Add(1), 10) }

// ClientsOf lists a user's attached clients, IRC first, oldest first.
func (b *Bouncer) ClientsOf(userID int64) []ClientInfo {
	b.mu.Lock()
	var out []ClientInfo
	for c := range b.clients[userID] {
		out = append(out, c.info)
	}
	for c := range b.streams[userID] {
		out = append(out, c.info)
	}
	b.mu.Unlock()
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind == "irc"
		}
		return out[i].Since.Before(out[j].Since)
	})
	return out
}

// Attached is the number of IRC clients and web chats a user has attached.
func (b *Bouncer) Attached(userID int64) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.clients[userID]) + len(b.streams[userID])
}

// --- presence --------------------------------------------------------------

func (b *Bouncer) seen(userID int64) {
	if err := b.store.TouchUserSeen(userID, time.Now()); err != nil {
		b.log.Warn("touch last seen", "user", userID, "err", err)
	}
}

// refreshAway applies auto-away: away on every network while the user has
// auto-away on and nothing attached, back as soon as something attaches.
func (b *Bouncer) refreshAway(userID int64) {
	u, err := b.store.UserByID(userID)
	if err != nil {
		return
	}
	msg := ""
	if u.AutoAway && b.Attached(userID) == 0 {
		msg = AwayMessage
	}
	b.mu.Lock()
	var conns []*upstream.Conn
	for _, e := range b.conns {
		if e.userID == userID {
			conns = append(conns, e.conn)
		}
	}
	b.mu.Unlock()
	for _, c := range conns {
		c.SetAway(msg)
	}
}

// RefreshAway re-applies auto-away after the setting changed.
func (b *Bouncer) RefreshAway(userID int64) { b.refreshAway(userID) }

// --- activity feed -----------------------------------------------------------

// AddEvent records an entry in a user's activity feed.
func (b *Bouncer) AddEvent(userID int64, kind, title, detail string) {
	e := store.UserEvent{UserID: userID, At: time.Now(), Kind: kind, Title: title, Detail: detail}
	if err := b.store.AddUserEvent(e); err != nil {
		b.log.Warn("record user event", "user", userID, "err", err)
	}
}

// --- unread and mentions -----------------------------------------------------

// BufferState is a buffer's unread state.
type BufferState struct {
	Name     string    `json:"name"`
	Unread   int       `json:"unread"`
	Mentions int       `json:"mentions"`
	LastAt   time.Time `json:"lastAt"`
	ReadAt   time.Time `json:"readAt"`
}

type netReads struct {
	userID int64
	bufs   map[string]*BufferState // by lowercased name
}

// initReads works out a network's unread counts from its read markers and
// history, once, when this node starts running it. A buffer with no marker
// counts as read up to now.
func (b *Bouncer) initReads(n store.Network) {
	b.rmu.Lock()
	_, done := b.reads[n.ID]
	b.rmu.Unlock()
	if done {
		return
	}
	markers, err := b.store.ReadMarkers(n.ID)
	if err != nil {
		b.log.Warn("load read markers", "network", n.ID, "err", err)
		markers = map[string]time.Time{}
	}
	names, _ := b.history.Buffers(n.ID)
	nr := &netReads{userID: n.UserID, bufs: map[string]*BufferState{}}
	now := time.Now()
	for _, name := range names {
		key := strings.ToLower(name)
		st := &BufferState{Name: name}
		readAt, ok := markers[key]
		if !ok {
			readAt = now
			_ = b.store.SetReadMarker(n.ID, key, now)
		}
		st.ReadAt = readAt
		msgs, _ := b.history.Recent(n.ID, name, 500)
		for _, m := range msgs {
			if m.Time.After(st.LastAt) {
				st.LastAt = m.Time
			}
			if !m.Time.After(readAt) || !isChat(m.Kind) || strings.EqualFold(m.Sender, n.Nick) {
				continue
			}
			st.Unread++
			if !isChannelName(name) || mentions(m.Text, n.Nick) {
				st.Mentions++
			}
		}
		nr.bufs[key] = st
	}
	b.rmu.Lock()
	if _, raced := b.reads[n.ID]; !raced {
		b.reads[n.ID] = nr
	}
	b.rmu.Unlock()
}

// observe updates unread state for a line as it's recorded. me is the user's
// current nick on the network.
func (b *Bouncer) observe(n store.Network, me string, m history.Message) {
	b.rmu.Lock()
	nr := b.reads[n.ID]
	if nr == nil {
		nr = &netReads{userID: n.UserID, bufs: map[string]*BufferState{}}
		b.reads[n.ID] = nr
	}
	key := strings.ToLower(m.Target)
	st := nr.bufs[key]
	if st == nil {
		st = &BufferState{Name: m.Target}
		nr.bufs[key] = st
	}
	st.LastAt = m.Time
	mark := false
	switch {
	case !isChat(m.Kind):
	case strings.EqualFold(m.Sender, me):
		// Saying something in a buffer means you've read it.
		st.Unread, st.Mentions, st.ReadAt, mark = 0, 0, m.Time, true
	default:
		st.Unread++
		if !isChannelName(m.Target) || mentions(m.Text, me) {
			st.Mentions++
		}
	}
	b.rmu.Unlock()
	if mark {
		_ = b.store.SetReadMarker(n.ID, key, m.Time)
	}
}

// Buffers is a network's unread state per buffer, sorted channels first.
func (b *Bouncer) Buffers(networkID int64) []BufferState {
	b.rmu.Lock()
	nr := b.reads[networkID]
	var out []BufferState
	if nr != nil {
		for _, st := range nr.bufs {
			out = append(out, *st)
		}
	}
	b.rmu.Unlock()
	if nr == nil {
		// Not running here: just the names, from history.
		names, _ := b.history.Buffers(networkID)
		markers, _ := b.store.ReadMarkers(networkID)
		for _, name := range names {
			out = append(out, BufferState{Name: name, ReadAt: markers[strings.ToLower(name)]})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		ci, cj := isChannelName(out[i].Name), isChannelName(out[j].Name)
		if ci != cj {
			return ci
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out
}

// MarkRead marks a buffer read up to now ("" marks every buffer of the
// network).
func (b *Bouncer) MarkRead(networkID int64, buffer string) error {
	now := time.Now()
	b.rmu.Lock()
	var keys []string
	if nr := b.reads[networkID]; nr != nil {
		for key, st := range nr.bufs {
			if buffer == "" || key == strings.ToLower(buffer) {
				st.Unread, st.Mentions, st.ReadAt = 0, 0, now
				keys = append(keys, key)
			}
		}
	}
	b.rmu.Unlock()
	if buffer != "" && len(keys) == 0 {
		keys = []string{strings.ToLower(buffer)}
	}
	if buffer == "" && len(keys) == 0 {
		names, _ := b.history.Buffers(networkID)
		for _, n := range names {
			keys = append(keys, strings.ToLower(n))
		}
	}
	for _, k := range keys {
		if err := b.store.SetReadMarker(networkID, k, now); err != nil {
			return err
		}
	}
	return nil
}

func (b *Bouncer) dropReads(networkID int64) {
	b.rmu.Lock()
	delete(b.reads, networkID)
	b.rmu.Unlock()
}

func isChat(kind string) bool {
	return kind == "message" || kind == "notice" || kind == "action" || kind == ""
}

func isChannelName(s string) bool { return s != "" && strings.ContainsRune("#&+!", rune(s[0])) }

// mentions reports whether text names nick as a word (case-insensitive).
func mentions(text, nick string) bool {
	if nick == "" {
		return false
	}
	lt, ln := strings.ToLower(text), strings.ToLower(nick)
	for i := 0; ; {
		j := strings.Index(lt[i:], ln)
		if j < 0 {
			return false
		}
		start, end := i+j, i+j+len(ln)
		if (start == 0 || !nickChar(lt[start-1])) && (end == len(lt) || !nickChar(lt[end])) {
			return true
		}
		i = start + 1
	}
}

func nickChar(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || strings.IndexByte("[]\\`_^{|}-", c) >= 0
}

// --- metrics -----------------------------------------------------------------

// Links reports each upstream connection's state for the metrics gauges.
func (b *Bouncer) Links() []telemetry.Link {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]telemetry.Link, 0, len(b.conns))
	for id, e := range b.conns {
		h := e.conn.Health()
		out = append(out, telemetry.Link{
			User: strconv.FormatInt(e.userID, 10), Network: strconv.FormatInt(id, 10),
			Up: h.Status == upstream.StatusConnected, Lag: h.Lag,
		})
	}
	return out
}

// Streams is the number of open web chat sockets on this node.
func (b *Bouncer) Streams() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := 0
	for _, set := range b.streams {
		n += len(set)
	}
	return int64(n)
}

// UpstreamsByStatus counts this node's connections by status.
func (b *Bouncer) UpstreamsByStatus() map[string]int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := map[string]int64{}
	for _, e := range b.conns {
		out[string(e.conn.Status())]++
	}
	return out
}

// LinkState is one connection's health, for the admin overview.
type LinkState struct {
	UserID, NetworkID int64
	Health            upstream.Health
}

// LinkStates reports every connection this node runs.
func (b *Bouncer) LinkStates() []LinkState {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]LinkState, 0, len(b.conns))
	for id, e := range b.conns {
		out = append(out, LinkState{UserID: e.userID, NetworkID: id, Health: e.conn.Health()})
	}
	return out
}

// OnlineByUser is the number of attached IRC clients and web chats per user.
func (b *Bouncer) OnlineByUser() map[int64]int {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := map[int64]int{}
	for uid, set := range b.clients {
		out[uid] += len(set)
	}
	for uid, set := range b.streams {
		out[uid] += len(set)
	}
	return out
}
