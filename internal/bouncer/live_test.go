package bouncer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/4evrd/evrd/internal/history"
	"github.com/4evrd/evrd/internal/irctls"
	"github.com/4evrd/evrd/internal/links"
	"github.com/4evrd/evrd/internal/store"
	"github.com/4evrd/evrd/internal/testirc"
	"github.com/4evrd/evrd/internal/upstream"
)

// liveRig is a bouncer whose networks connect to a fake IRC server.
type liveRig struct {
	t      *testing.T
	b      *Bouncer
	st     store.Store
	hist   history.Store
	irc    *testirc.Log
	host   string
	port   int
	ctx    context.Context
	cancel context.CancelFunc
}

// newLiveRig starts a bouncer (not yet Started) beside a fake IRC server.
// Its data dir is removed best effort: the bouncer's goroutines may still be
// writing as the test returns.
func newLiveRig(t *testing.T) *liveRig {
	t.Helper()
	dir, err := os.MkdirTemp("", "evrd-bouncer-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	st, err := store.Open("sqlite:" + filepath.Join(dir, "evrd.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	hist, err := history.Open(filepath.Join(dir, "history"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { hist.Close() })
	log := &testirc.Log{}
	host, port, _ := net.SplitHostPort(testirc.StartLogged(t, log))
	var p int
	fmt.Sscan(port, &p)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return &liveRig{
		t: t, b: New(st, hist, slog.New(slog.NewTextHandler(io.Discard, nil))),
		st: st, hist: hist, irc: log, host: host, port: p, ctx: ctx, cancel: cancel,
	}
}

func (r *liveRig) user(name string, f func(*store.User)) store.User {
	r.t.Helper()
	u := store.User{Username: name}
	if f != nil {
		f(&u)
	}
	u, err := r.st.CreateUser(context.Background(), u)
	if err != nil {
		r.t.Fatal(err)
	}
	return u
}

func (r *liveRig) network(u store.User, name string, enabled bool) store.Network {
	r.t.Helper()
	n, err := r.st.CreateNetwork(context.Background(), store.Network{
		UserID: u.ID, Name: name, Host: r.host, Port: r.port, Nick: u.Username + "_" + name,
		Channels: []string{"#" + name}, Enabled: enabled,
	})
	if err != nil {
		r.t.Fatal(err)
	}
	return n
}

// eventually retries cond for a moment: connections and stores move
// asynchronously.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for i := 0; i < 150; i++ {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("never: %s", what)
}

func (r *liveRig) connected(id int64) {
	r.t.Helper()
	eventually(r.t, fmt.Sprintf("network %d connected", id), func() bool { return r.b.Status(id) == upstream.StatusConnected })
}

func logHas(entries []upstream.LogEntry, text string) bool {
	return slices.ContainsFunc(entries, func(e upstream.LogEntry) bool { return strings.Contains(e.Text, text) })
}

func TestStartConnectsEnabledNetworks(t *testing.T) {
	r := newLiveRig(t)
	alice := r.user("alice", nil)
	on := r.network(alice, "on", true)
	off := r.network(alice, "off", false)
	if err := r.b.Start(r.ctx); err != nil {
		t.Fatal(err)
	}
	r.connected(on.ID)
	if st := r.b.Status(off.ID); st != upstream.StatusDisconnected {
		t.Fatalf("a switched-off network started: %s", st)
	}
	// the server saw it register and join the saved channel
	eventually(t, "joined #on", func() bool { return r.irc.Has("JOIN #on") })
	if u := r.b.Usage(); u.ConnectedNetworks != 1 || u.AttachedClients != 0 {
		t.Fatalf("usage: %+v", u)
	}
	if got := r.b.UpstreamsByStatus(); got["connected"] != 1 || len(got) != 1 {
		t.Fatalf("by status: %v", got)
	}
	ls := r.b.Links()
	if len(ls) != 1 || !ls[0].Up || ls[0].Network != fmt.Sprint(on.ID) || ls[0].User != fmt.Sprint(alice.ID) {
		t.Fatalf("links: %+v", ls)
	}
	states := r.b.LinkStates()
	if len(states) != 1 || states[0].NetworkID != on.ID || states[0].UserID != alice.ID || states[0].Health.Status != upstream.StatusConnected {
		t.Fatalf("link states: %+v", states)
	}
	// connecting reaches the activity feed
	eventually(t, "connected event", func() bool {
		evs, _ := r.st.UserEvents(t.Context(), alice.ID, time.Time{}, 10)
		return slices.ContainsFunc(evs, func(e store.UserEvent) bool {
			return e.Kind == "connected" && e.Title == "Connected to on" && e.NetworkID == on.ID
		})
	})
	// and the connection log
	if l := r.b.NetworkLog(on.ID); !logHas(l, "Switched on") {
		t.Fatalf("network log: %+v", l)
	}
	if r.b.History() != r.hist {
		t.Fatal("History doesn't expose the bouncer's history")
	}
	if r.b.Ping(t.Context()) != nil {
		t.Fatal("Ping: the account database isn't reachable")
	}
}

func TestConnectDisconnectReconnect(t *testing.T) {
	r := newLiveRig(t)
	alice := r.user("alice", func(u *store.User) { u.QuitMsg = "see you" })
	n := r.network(alice, "net", false)
	r.b.Start(r.ctx)
	if len(r.b.LinkStates()) != 0 {
		t.Fatal("a disabled network was started")
	}

	if err := r.b.Connect(n.ID); err != nil {
		t.Fatal(err)
	}
	r.connected(n.ID)
	if got, _ := r.st.NetworkByID(t.Context(), n.ID); !got.Enabled {
		t.Fatal("connect didn't switch the network on")
	}
	first, _ := r.b.ConnFor(n.ID)
	// connecting a running network is a no-op
	if err := r.b.Connect(n.ID); err != nil {
		t.Fatal(err)
	}
	if again, _ := r.b.ConnFor(n.ID); again != first || r.irc.Connections() != 1 {
		t.Fatalf("connecting twice opened another link (%d connections)", r.irc.Connections())
	}

	// Reconnect replaces the connection.
	if err := r.b.Reconnect(n.ID); err != nil {
		t.Fatal(err)
	}
	if again, _ := r.b.ConnFor(n.ID); again == first {
		t.Fatal("reconnect kept the old connection")
	}
	r.connected(n.ID)
	eventually(t, "a second link", func() bool { return r.irc.Connections() == 2 })
	// switched off, the old link said goodbye with the user's quit message
	eventually(t, "quit sent", func() bool { return r.irc.Has("QUIT :see you") })
	if l := r.b.NetworkLog(n.ID); !logHas(l, "Settings changed: reconnecting") || !logHas(l, "Switched off") {
		t.Fatalf("network log: %+v", l)
	}

	if err := r.b.Disconnect(n.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := r.b.ConnFor(n.ID); ok || r.b.Status(n.ID) != upstream.StatusDisconnected {
		t.Fatal("still running after disconnect")
	}
	if got, _ := r.st.NetworkByID(t.Context(), n.ID); got.Enabled {
		t.Fatal("disconnect left the network switched on")
	}
	// Reconnect leaves a stopped network down.
	if err := r.b.Reconnect(n.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := r.b.ConnFor(n.ID); ok {
		t.Fatal("reconnect started a stopped network")
	}
	// disconnecting a stopped network is harmless
	if err := r.b.Disconnect(n.ID); err != nil {
		t.Fatal(err)
	}

	if err := r.b.Connect(999); err == nil {
		t.Fatal("connected a network that doesn't exist")
	}
}

func TestConnectRefusals(t *testing.T) {
	r := newLiveRig(t)
	sus := r.user("sus", func(u *store.User) { u.Suspended = true })
	sn := r.network(sus, "net", true)
	other := r.user("other", nil)
	on := r.network(other, "net", true)
	mine := r.user("mine", nil)
	mn := r.network(mine, "net", true)
	r.b.SetOwnership(func(id int64) bool { return id != other.ID })
	// in a cluster Start leaves starting users to the cluster
	if err := r.b.Start(r.ctx); err != nil {
		t.Fatal(err)
	}
	if len(r.b.LinkStates()) != 0 {
		t.Fatal("Start connected networks in a cluster")
	}

	if err := r.b.Connect(sn.ID); !errors.Is(err, ErrSuspended) {
		t.Fatalf("suspended: %v", err)
	}
	if err := r.b.Connect(on.ID); !errors.Is(err, ErrNotOwner) {
		t.Fatalf("not owner: %v", err)
	}
	if r.b.Owns(other.ID) || !r.b.Owns(mine.ID) {
		t.Fatal("Owns doesn't follow SetOwnership")
	}

	// ResumeUser starts only what this node runs.
	r.b.ResumeUser(other.ID)
	if _, ok := r.b.ConnFor(on.ID); ok {
		t.Fatal("resumed another node's user")
	}
	r.b.ResumeUser(mine.ID)
	r.connected(mn.ID)
	// a suspended user's networks stay down
	r.b.ResumeUser(sus.ID)
	if _, ok := r.b.ConnFor(sn.ID); ok {
		t.Fatal("resumed a suspended user")
	}
}

func TestDialerGetsTheBindHost(t *testing.T) {
	r := newLiveRig(t)
	alice := r.user("alice", func(u *store.User) { u.BindHost = "192.0.2.1" })
	n := r.network(alice, "net", false)
	m, _ := r.st.CreateNetwork(t.Context(), store.Network{UserID: alice.ID, Name: "own", Host: r.host, Port: r.port, Nick: "a", BindHost: "192.0.2.9"})
	type call struct {
		user int64
		bind string
	}
	var mu sync.Mutex
	var calls []call
	r.b.SetDialer(func(ctx context.Context, userID int64, bind, host string, port int) (net.Conn, error) {
		mu.Lock()
		calls = append(calls, call{userID, bind})
		mu.Unlock()
		var d net.Dialer
		return d.DialContext(ctx, "tcp", net.JoinHostPort(host, fmt.Sprint(port)))
	})
	r.b.Start(r.ctx)
	r.b.Connect(n.ID)
	r.b.Connect(m.ID)
	r.connected(n.ID)
	r.connected(m.ID)
	mu.Lock()
	defer mu.Unlock()
	// the network's own bind host wins over the user's
	want := []call{{alice.ID, "192.0.2.1"}, {alice.ID, "192.0.2.9"}}
	for _, w := range want {
		if !slices.Contains(calls, w) {
			t.Fatalf("dial calls %+v, want %+v", calls, want)
		}
	}
}

func TestLinesUpdateUnreadAndChannels(t *testing.T) {
	r := newLiveRig(t)
	alice := r.user("alice", nil)
	n := r.network(alice, "net", true)
	// read up to an hour ago; one unread line in #net since then, one before
	ago := time.Now().Add(-time.Hour)
	r.st.SetReadMarker(t.Context(), n.ID, "#net", ago)
	r.hist.Add(history.Message{NetworkID: n.ID, Target: "#net", Sender: "bob", Kind: "message", Text: "old", Time: ago.Add(-time.Minute)})
	r.hist.Add(history.Message{NetworkID: n.ID, Target: "#net", Sender: "bob", Kind: "message", Text: "alice_net: new", Time: ago.Add(time.Minute)})
	r.hist.Add(history.Message{NetworkID: n.ID, Target: "#net", Sender: "alice_net", Kind: "message", Text: "mine", Time: ago.Add(2 * time.Minute)})
	// a buffer without a marker counts as read
	r.hist.Add(history.Message{NetworkID: n.ID, Target: "#quiet", Sender: "bob", Kind: "message", Text: "hm", Time: ago})
	r.b.Start(r.ctx)
	r.connected(n.ID)

	buf := func(name string) BufferState {
		for _, s := range r.b.Buffers(n.ID) {
			if strings.EqualFold(s.Name, name) {
				return s
			}
		}
		return BufferState{}
	}
	if s := buf("#net"); s.Unread != 1 || s.Mentions != 1 || !s.ReadAt.Equal(ago) {
		t.Fatalf("#net from history: %+v", s)
	}
	if s := buf("#quiet"); s.Unread != 0 {
		t.Fatalf("#quiet from history: %+v", s)
	}
	if markers, _ := r.st.ReadMarkers(t.Context(), n.ID); markers["#quiet"].IsZero() {
		t.Fatal("no read marker saved for a buffer without one")
	}

	// a private message arrives: saved, counted, a mention
	r.irc.Send(":bob!u@h PRIVMSG alice_net :psst")
	eventually(t, "query saved and counted", func() bool {
		m, _ := r.hist.Recent(n.ID, "bob", 5)
		return len(m) == 1 && buf("bob").Unread == 1 && buf("bob").Mentions == 1
	})

	// joining a channel saves it to the network
	c, _ := r.b.ConnFor(n.ID)
	c.SendRaw("JOIN #more")
	eventually(t, "channel saved", func() bool {
		got, _ := r.st.NetworkByID(t.Context(), n.ID)
		return slices.Contains(got.Channels, "#more")
	})

	// marking everything read clears every buffer
	if err := r.b.MarkRead(n.ID, ""); err != nil {
		t.Fatal(err)
	}
	for _, s := range r.b.Buffers(n.ID) {
		if s.Unread != 0 || s.Mentions != 0 {
			t.Fatalf("after mark all: %+v", s)
		}
	}
	markers, _ := r.st.ReadMarkers(t.Context(), n.ID)
	if !markers["bob"].After(ago) || !markers["#net"].After(ago) {
		t.Fatalf("markers not moved: %v", markers)
	}
}

func TestMarkReadOfANetworkNotRunningHere(t *testing.T) {
	r := newLiveRig(t)
	alice := r.user("alice", nil)
	n := r.network(alice, "net", false)
	now := time.Now()
	r.hist.Add(history.Message{NetworkID: n.ID, Target: "#a", Sender: "bob", Text: "x", Time: now})
	r.hist.Add(history.Message{NetworkID: n.ID, Target: "Carol", Sender: "Carol", Text: "y", Time: now})
	if err := r.b.MarkRead(n.ID, ""); err != nil {
		t.Fatal(err)
	}
	markers, _ := r.st.ReadMarkers(t.Context(), n.ID)
	if markers["#a"].IsZero() || markers["carol"].IsZero() {
		t.Fatalf("mark all from history: %v", markers)
	}
	if err := r.b.MarkRead(n.ID, "#Elsewhere"); err != nil {
		t.Fatal(err)
	}
	if markers, _ := r.st.ReadMarkers(t.Context(), n.ID); markers["#elsewhere"].IsZero() {
		t.Fatalf("mark one unknown buffer: %v", markers)
	}
	// not running here: names and markers come from the stores
	bufs := r.b.Buffers(n.ID)
	if len(bufs) != 2 || bufs[0].Name != "#a" || bufs[1].Name != "Carol" || bufs[1].ReadAt.IsZero() {
		t.Fatalf("buffers: %+v", bufs)
	}
}

func TestClientsAndClientLimit(t *testing.T) {
	r := newLiveRig(t)
	alice := r.user("alice", nil)
	bob := r.user("bob", nil)
	r.b.Start(r.ctx)

	closed := map[string]bool{}
	var mu sync.Mutex
	closer := func(name string) func(string) {
		return func(string) { mu.Lock(); closed[name] = true; mu.Unlock() }
	}
	a1, err := r.b.AttachClient(alice.ID, 2, ClientInfo{Device: "laptop", IP: "192.0.2.1"}, closer("a1"))
	if err != nil {
		t.Fatal(err)
	}
	a2, err := r.b.AttachClient(alice.ID, 2, ClientInfo{Device: "phone"}, closer("a2"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.b.AttachClient(alice.ID, 2, ClientInfo{}, closer("a3")); !errors.Is(err, ErrTooManyClients) {
		t.Fatalf("third client: %v", err)
	}
	// 0 means no limit
	b1, err := r.b.AttachClient(bob.ID, 0, ClientInfo{}, closer("b1"))
	if err != nil {
		t.Fatal(err)
	}
	web := r.b.TrackStream(alice.ID, ClientInfo{Software: "Firefox"}, closer("web"))

	if r.b.Clients(alice.ID) != 2 || r.b.Attached(alice.ID) != 3 || r.b.Streams() != 1 {
		t.Fatalf("clients=%d attached=%d streams=%d", r.b.Clients(alice.ID), r.b.Attached(alice.ID), r.b.Streams())
	}
	if got := r.b.ClientsByUser(); got[alice.ID] != 2 || got[bob.ID] != 1 {
		t.Fatalf("by user: %v", got)
	}
	if got := r.b.OnlineByUser(); got[alice.ID] != 3 || got[bob.ID] != 1 {
		t.Fatalf("online: %v", got)
	}
	if u := r.b.Usage(); u.AttachedClients != 3 {
		t.Fatalf("usage: %+v", u)
	}

	a1.Update(func(ci *ClientInfo) { ci.Software = "HexChat" })
	list := r.b.ClientsOf(alice.ID)
	if len(list) != 3 || list[0].ID != a1.ID() || list[1].ID != a2.ID() || list[2].ID != web.ID() {
		t.Fatalf("clients of alice (irc first, oldest first): %+v", list)
	}
	if list[0].Kind != "irc" || list[0].Software != "HexChat" || list[0].Device != "laptop" || list[2].Kind != "web" || list[0].Since.IsZero() {
		t.Fatalf("client info: %+v", list)
	}
	if u, _ := r.st.UserByID(t.Context(), alice.ID); u.LastSeenAt == nil {
		t.Fatal("attaching didn't touch last seen")
	}

	// a slot frees up when a client goes
	a2.Release()
	a3, err := r.b.AttachClient(alice.ID, 2, ClientInfo{}, closer("a3"))
	if err != nil {
		t.Fatalf("after release: %v", err)
	}
	b1.Release()
	if _, ok := r.b.ClientsByUser()[bob.ID]; ok {
		t.Fatal("an empty user's entry was kept")
	}

	// StopUser kicks every client and web chat
	r.b.StopUser(alice.ID)
	mu.Lock()
	if !closed["a1"] || !closed["a3"] || !closed["web"] || closed["a2"] || closed["b1"] {
		t.Fatalf("closed: %v", closed)
	}
	mu.Unlock()
	a1.Release()
	a3.Release()
	web.Release()
	if r.b.Attached(alice.ID) != 0 || r.b.Streams() != 0 {
		t.Fatal("released clients still counted")
	}
}

func TestAutoAway(t *testing.T) {
	r := newLiveRig(t)
	alice := r.user("alice", func(u *store.User) { u.AutoAway = true })
	n := r.network(alice, "net", true)
	r.b.Start(r.ctx)
	r.connected(n.ID)
	// nobody attached: away once registered
	eventually(t, "away", func() bool { return r.irc.Has("AWAY :" + AwayMessage) })

	att, err := r.b.AttachClient(alice.ID, 0, ClientInfo{}, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	backs := func() int {
		n := 0
		for _, l := range r.irc.Lines() {
			if l == "AWAY" {
				n++
			}
		}
		return n
	}
	eventually(t, "back", func() bool { return backs() == 1 })
	att.Release()
	eventually(t, "away again", func() bool {
		n := 0
		for _, l := range r.irc.Lines() {
			if strings.HasPrefix(l, "AWAY :") {
				n++
			}
		}
		return n == 2
	})

	// turning auto-away off brings the user back
	u, _ := r.st.UserByID(t.Context(), alice.ID)
	u.AutoAway = false
	r.st.UpdateUser(t.Context(), u)
	r.b.RefreshAway(alice.ID)
	eventually(t, "back after turning it off", func() bool { return backs() == 2 })
}

func TestIdleUserGetsCTCPAnswered(t *testing.T) {
	r := newLiveRig(t)
	alice := r.user("alice", nil)
	n := r.network(alice, "net", true)
	r.b.Start(r.ctx)
	r.connected(n.ID)
	// nobody attached: evrd answers CTCP VERSION itself
	r.irc.Send(":bob!u@h PRIVMSG alice_net :\x01VERSION\x01")
	eventually(t, "version reply", func() bool { return r.irc.Has("NOTICE bob :\x01VERSION evrd") })
}

func TestStopUserDropsConnections(t *testing.T) {
	r := newLiveRig(t)
	alice := r.user("alice", nil)
	bob := r.user("bob", nil)
	a := r.network(alice, "a", true)
	b := r.network(bob, "b", true)
	r.b.Start(r.ctx)
	r.connected(a.ID)
	r.connected(b.ID)
	r.b.StopUser(alice.ID)
	if _, ok := r.b.ConnFor(a.ID); ok {
		t.Fatal("alice's network still runs")
	}
	if _, ok := r.b.ConnFor(b.ID); !ok {
		t.Fatal("bob's network was stopped")
	}
	// networks stay enabled so the next owner resumes them
	if got, _ := r.st.NetworkByID(t.Context(), a.ID); !got.Enabled {
		t.Fatal("StopUser switched the network off")
	}
	r.b.ResumeUser(alice.ID)
	r.connected(a.ID)
}

func TestReconfigurePushesSettings(t *testing.T) {
	r := newLiveRig(t)
	alice := r.user("alice", nil)
	n := r.network(alice, "net", true)
	idle := r.network(alice, "idle", false)
	r.b.Start(r.ctx)
	r.connected(n.ID)

	// a user-wide quit message reaches running networks
	u, _ := r.st.UserByID(t.Context(), alice.ID)
	u.QuitMsg = "user quit"
	r.st.UpdateUser(t.Context(), u)
	r.b.ReconfigureUser(alice.ID)
	r.b.Reconfigure(idle) // not running: nothing to do
	r.b.Disconnect(n.ID)
	eventually(t, "user quit message", func() bool { return r.irc.Has("QUIT :user quit") })

	// the network's own quit message wins
	r.b.Connect(n.ID)
	r.connected(n.ID)
	n.QuitMsg = "net quit"
	r.st.UpdateNetwork(t.Context(), n)
	r.b.Reconfigure(n)
	r.b.Disconnect(n.ID)
	eventually(t, "network quit message", func() bool { return r.irc.Has("QUIT :net quit") })
}

func TestSetChannelSettings(t *testing.T) {
	r := newLiveRig(t)
	alice := r.user("alice", nil)
	n := r.network(alice, "net", true)
	off := r.network(alice, "off", false)
	r.b.Start(r.ctx)
	r.connected(n.ID)

	// running: the connection applies and saves them
	if err := r.b.SetChannelSettings(n.ID, "#Net", func(cs *store.ChannelSettings) { cs.Detached = true }); err != nil {
		t.Fatal(err)
	}
	c, _ := r.b.ConnFor(n.ID)
	if !c.ChannelSettings("#net").Detached {
		t.Fatal("not applied to the connection")
	}
	if got, _ := r.st.NetworkByID(t.Context(), n.ID); !got.Channel("#net").Detached {
		t.Fatalf("not saved: %+v", got.ChannelSettings)
	}

	// not running: saved directly
	if err := r.b.SetChannelSettings(off.ID, "#Off", func(cs *store.ChannelSettings) { cs.Key = "sekrit" }); err != nil {
		t.Fatal(err)
	}
	if got, _ := r.st.NetworkByID(t.Context(), off.ID); got.Channel("#off").Key != "sekrit" {
		t.Fatalf("not saved: %+v", got.ChannelSettings)
	}
	// back to defaults: the entry goes
	if err := r.b.SetChannelSettings(off.ID, "#off", func(cs *store.ChannelSettings) { cs.Key = "" }); err != nil {
		t.Fatal(err)
	}
	if got, _ := r.st.NetworkByID(t.Context(), off.ID); len(got.ChannelSettings) != 0 {
		t.Fatalf("default settings kept: %+v", got.ChannelSettings)
	}
	if err := r.b.SetChannelSettings(999, "#x", func(*store.ChannelSettings) {}); err == nil {
		t.Fatal("no error for a missing network")
	}
}

func TestDeleteRunningNetwork(t *testing.T) {
	r := newLiveRig(t)
	alice := r.user("alice", nil)
	n := r.network(alice, "net", true)
	r.b.Start(r.ctx)
	r.connected(n.ID)
	r.hist.Add(history.Message{NetworkID: n.ID, Target: "#net", Text: "x", Time: time.Now()})
	if err := r.b.DeleteNetwork(alice.ID, n.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := r.b.ConnFor(n.ID); ok {
		t.Fatal("still connected")
	}
	if l := r.b.NetworkLog(n.ID); len(l) != 0 {
		t.Fatalf("connection log kept: %+v", l)
	}
	if bufs := r.b.Buffers(n.ID); len(bufs) != 0 {
		t.Fatalf("buffers kept: %+v", bufs)
	}
}

func TestPruneTrimsBacklogAndHousekeeps(t *testing.T) {
	r := newLiveRig(t)
	alice := r.user("alice", nil)
	bob := r.user("bob", nil)
	elsewhere := r.user("elsewhere", nil)
	a := r.network(alice, "a", false)
	b := r.network(bob, "b", false)
	e := r.network(elsewhere, "e", false)
	r.b.SetOwnership(func(id int64) bool { return id != elsewhere.ID })
	now := time.Now()
	for _, id := range []int64{a.ID, b.ID, e.ID} {
		for i := 0; i < 5; i++ {
			r.hist.Add(history.Message{NetworkID: id, Target: "#x", Text: fmt.Sprint(i), Time: now.Add(time.Duration(i) * time.Second)})
		}
	}
	// history of a network that's gone
	r.hist.Add(history.Message{NetworkID: 4242, Target: "#gone", Text: "x", Time: now})
	r.st.AddUserEvent(t.Context(), store.UserEvent{UserID: alice.ID, At: now.AddDate(0, 0, -40), Kind: "old", Title: "old"})
	r.st.AddUserEvent(t.Context(), store.UserEvent{UserID: alice.ID, At: now, Kind: "new", Title: "new"})
	count := func(id int64) int { m, _ := r.hist.Recent(id, "#x", 50); return len(m) }

	// alice keeps 2 lines, bob everything; the other node's user is left alone
	backlog := func(u store.User) int {
		if u.ID == alice.ID || u.ID == elsewhere.ID {
			return 2
		}
		return 0
	}
	// not the leader: only backlog trimming
	r.b.prune(backlog, false)
	if count(a.ID) != 2 || count(b.ID) != 5 || count(e.ID) != 5 {
		t.Fatalf("after prune: a=%d b=%d e=%d", count(a.ID), count(b.ID), count(e.ID))
	}
	if m, _ := r.hist.Recent(a.ID, "#x", 50); m[0].Text != "3" || m[1].Text != "4" {
		t.Fatalf("kept the wrong lines: %+v", m)
	}
	if ids, _ := r.hist.Networks(); !slices.Contains(ids, 4242) {
		t.Fatal("a non-leader dropped orphan history")
	}

	// the leader also drops orphans and old activity, via PruneLoop
	ctx, cancel := context.WithCancel(r.ctx)
	cancel() // one pass, then stop
	r.b.PruneLoop(ctx, time.Hour, backlog, func() bool { return true })
	if ids, _ := r.hist.Networks(); slices.Contains(ids, 4242) {
		t.Fatal("orphan history kept")
	}
	evs, _ := r.st.UserEvents(t.Context(), alice.ID, time.Time{}, 10)
	if len(evs) != 1 || evs[0].Kind != "new" {
		t.Fatalf("activity feed: %+v", evs)
	}
}

func TestNetworkLogKeepsTheNewest(t *testing.T) {
	var l netLogs
	for i := 0; i < netLogSize+20; i++ {
		l.add(1, upstream.LogEntry{Text: fmt.Sprint(i)})
	}
	got := l.get(1)
	if len(got) != netLogSize || got[0].Text != "20" || got[len(got)-1].Text != fmt.Sprint(netLogSize+19) {
		t.Fatalf("log: %d entries, first %q", len(got), got[0].Text)
	}
	got[0].Text = "changed"
	if l.get(1)[0].Text != "20" {
		t.Fatal("get returned the log itself, not a copy")
	}
	if len(l.get(2)) != 0 {
		t.Fatal("another network has entries")
	}
}

func TestCloseStrayLinks(t *testing.T) {
	r := newLiveRig(t)
	// no holder: nothing to do
	r.b.CloseStrayLinks(r.ctx)

	dir, err := os.MkdirTemp("", "links")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	path := filepath.Join(dir, "l.sock") // short: unix socket paths are limited
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	go links.NewHolder(slog.New(slog.NewTextHandler(io.Discard, nil))).Serve(r.ctx, ln)
	cl := links.NewClient(path)
	r.b.SetLinks(cl)

	alice := r.user("alice", nil)
	n := r.network(alice, "net", true)
	r.b.Start(r.ctx)
	r.connected(n.ID)

	// a link left over from a network that's gone
	tcp, err := net.Dial("tcp", net.JoinHostPort(r.host, fmt.Sprint(r.port)))
	if err != nil {
		t.Fatal(err)
	}
	stray, err := cl.Open(r.ctx, "n999", tcp, false, r.host, irctls.Trust{})
	if err != nil {
		t.Fatal(err)
	}
	defer stray.Close()
	held := func() (ids []string) {
		ls, _ := cl.List(r.ctx)
		for _, l := range ls {
			ids = append(ids, l.Link)
		}
		slices.Sort(ids)
		return ids
	}
	if got := held(); len(got) != 2 {
		t.Fatalf("held before: %v", got)
	}
	r.b.CloseStrayLinks(r.ctx)
	eventually(t, "stray closed", func() bool {
		got := held()
		return len(got) == 1 && got[0] == linkID(n.ID)
	})

	// an unreachable holder is only logged
	r.b.SetLinks(links.NewClient(filepath.Join(dir, "nobody.sock")))
	r.b.CloseStrayLinks(r.ctx)
}
