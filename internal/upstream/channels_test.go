package upstream

import (
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/4evrd/evrd/internal/history"
	"github.com/4evrd/evrd/internal/store"
)

// connected runs a connection to a fresh scripted server (registering as
// welcome does) and waits for registration.
func connected(t *testing.T, n store.Network, opts Options) (*scripted, *Conn) {
	t.Helper()
	s := startScripted(t, nil)
	c := New(s.network(n), quiet, opts)
	run(t, c)
	eventually(t, "registered", func() bool { return c.Status() == StatusConnected })
	return s, c
}

// sync waits until everything the server sent so far has been handled:
// lines are handled in order, so once a PING is answered the ones before it
// are too.
func settle(t *testing.T, s *scripted) {
	t.Helper()
	tok := "sync-" + time.Now().Format("150405.000000000")
	s.send("PING :%s", tok)
	eventually(t, "the server's PING answered", func() bool { return s.has("PONG :" + tok) })
}

// msgRec records persisted messages.
type msgRec struct {
	mu sync.Mutex
	m  []history.Message
}

func (r *msgRec) add(m history.Message) { r.mu.Lock(); r.m = append(r.m, m); r.mu.Unlock() }
func (r *msgRec) all() []history.Message {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]history.Message(nil), r.m...)
}
func (r *msgRec) find(kind, target string) []history.Message {
	var out []history.Message
	for _, m := range r.all() {
		if m.Kind == kind && strings.EqualFold(m.Target, target) {
			out = append(out, m)
		}
	}
	return out
}

func memberList(c *Conn, ch string) string {
	var out []string
	for _, m := range c.Members(ch) {
		out = append(out, m.Prefix+m.Nick)
	}
	return strings.Join(out, " ")
}

func TestChannelMembersFollowTheServer(t *testing.T) {
	var msgs msgRec
	var savedMu sync.Mutex
	var saved [][]string
	s, c := connected(t, store.Network{ID: 3, Nick: "me", Channels: []string{"#keep", " "}}, Options{
		Persist: msgs.add,
		OnChannels: func(ch []string) {
			savedMu.Lock()
			saved = append(saved, ch)
			savedMu.Unlock()
		},
	})
	eventually(t, "joined the saved channel", func() bool { return s.has("JOIN #keep") })

	s.send(":srv 005 me PREFIX=(qaohv)~&@%%+ CHANTYPES=# :are supported")
	s.send(":me!u@h JOIN #Chat")
	s.send(":srv 332 me #chat :hello world")
	s.send(":srv 353 me = #chat :me ~owner &admin @op %%half +voice plain carol!c@h")
	s.send(":srv 366 me #chat :End of /NAMES list.")
	settle(t, s)

	if !c.Joined("#chat") || c.Joined("#nope") {
		t.Fatal("Joined")
	}
	if got := memberList(c, "#CHAT"); got != "~owner &admin @op %half +voice carol me plain" {
		t.Fatalf("members by rank then nick: %q", got)
	}
	if c.MemberCount("#chat") != 8 || c.Topic("#CHAT") != "hello world" {
		t.Fatalf("count %d topic %q", c.MemberCount("#chat"), c.Topic("#chat"))
	}
	if got := c.Channels(); !slices.Equal(got, []string{"#keep", "#Chat"}) {
		t.Fatalf("channels: %q", got)
	}

	// Others come and go.
	s.send(":dave!d@h JOIN #chat")
	s.send(":plain!p@h PART #chat :later")
	s.send(":op!o@h KICK #chat voice :behave")
	s.send(":carol!c@h NICK carol2")
	s.send(":op!o@h TOPIC #chat :new topic")
	s.send(":op!o@h MODE #chat +o-v+bl dave half *!*@spam 10")
	s.send(":op!o@h MODE #chat -h+v half carol2")
	s.send(":op!o@h MODE me +i") // a user mode: no channel event
	settle(t, s)
	if got := memberList(c, "#chat"); got != "~owner &admin @dave @op +carol2 half me" {
		t.Fatalf("after changes: %q", got)
	}
	if c.Topic("#chat") != "new topic" {
		t.Fatalf("topic %q", c.Topic("#chat"))
	}
	for kind, want := range map[string]string{"join": "dave", "part": "plain", "kick": "op", "nick": "carol", "topic": "op", "mode": "op"} {
		if ms := msgs.find(kind, "#chat"); len(ms) == 0 || ms[len(ms)-1].Sender != want {
			t.Errorf("%s event: %+v", kind, ms)
		}
	}
	if k := msgs.find("kick", "#chat"); k[0].Extra != "voice" || k[0].Text != "behave" {
		t.Errorf("kick: %+v", k[0])
	}
	if p := msgs.find("part", "#chat"); p[0].Text != "later" {
		t.Errorf("part reason: %+v", p[0])
	}
	if n := msgs.find("nick", "#chat"); n[0].Extra != "carol2" {
		t.Errorf("nick: %+v", n[0])
	}
	if m := msgs.find("mode", "#chat"); m[0].Text != "+o-v+bl dave half *!*@spam 10" {
		t.Errorf("mode: %+v", m[0])
	}
	if len(msgs.find("mode", "me")) != 0 {
		t.Error("a user mode was recorded as a channel event")
	}

	// A QUIT is filed under every channel the nick shared with us.
	s.send(":me!u@h JOIN #other")
	s.send(":dave!d@h JOIN #other")
	s.send(":dave!d@h QUIT :Quit: bye")
	s.send(":stranger!s@h QUIT :bye")
	settle(t, s)
	quits := msgs.find("quit", "#chat")
	if len(quits) != 1 || quits[0].Text != "Quit: bye" || len(msgs.find("quit", "#other")) != 1 {
		t.Fatalf("quits: %+v", msgs.all())
	}
	// one line, one ID, in every buffer it's filed under
	if other := msgs.find("quit", "#other")[0]; other.ID() != quits[0].ID() || quits[0].Seq == 0 {
		t.Fatalf("quit IDs: %v %v", quits[0].ID(), other.ID())
	}
	if strings.Contains(memberList(c, "#chat"), "dave") || strings.Contains(memberList(c, "#other"), "dave") {
		t.Fatal("dave still listed after quitting")
	}

	// Our own nick change is filed under all our channels.
	s.send(":me!u@h NICK me2")
	settle(t, s)
	if c.Nick() != "me2" || len(msgs.find("nick", "#other")) != 1 || !strings.Contains(memberList(c, "#chat"), "me2") {
		t.Fatalf("own nick change: %q %+v", c.Nick(), msgs.find("nick", "#other"))
	}

	// Kicked: out of the channel, but it stays saved for the next connect.
	s.send(":op!o@h KICK #other me2 :out")
	settle(t, s)
	if c.Joined("#other") || c.MemberCount("#other") != 0 || !slices.Contains(c.Channels(), "#other") {
		t.Fatalf("after a kick: joined %v, channels %q", c.Joined("#other"), c.Channels())
	}
	// Parting forgets it.
	s.send(":me2!u@h PART #chat")
	settle(t, s)
	if c.Joined("#chat") || c.Topic("#chat") != "" || slices.Contains(c.Channels(), "#Chat") {
		t.Fatalf("after parting: %q", c.Channels())
	}
	savedMu.Lock()
	last := saved[len(saved)-1]
	savedMu.Unlock()
	if !slices.Equal(last, []string{"#keep", "#other"}) {
		t.Fatalf("saved list: %q", saved)
	}

	// Malformed lines are ignored.
	for _, l := range []string{":x NICK", ":x JOIN", ":x PART", ":x KICK #a", ":x TOPIC #a", ":x MODE #a", ":srv 332 me #a", ":srv 353 me = #a", ":x CAP *"} {
		s.send("%s", l)
	}
	settle(t, s)
	if c.Status() != StatusConnected {
		t.Fatal("malformed lines broke the connection")
	}
}

func TestNetsplitIsReportedOnce(t *testing.T) {
	var events eventRec
	s, c := connected(t, store.Network{Name: "Net", Nick: "me"}, Options{OnEvent: events.add})
	s.send(":me!u@h JOIN #c")
	var names []string
	for i := range 8 {
		names = append(names, "u"+string(rune('a'+i)))
	}
	s.send(":srv 353 me = #c :me %s", strings.Join(names, " "))
	for _, n := range names {
		s.send(":%s!x@h QUIT :hub.example.net leaf.example.net", n)
	}
	settle(t, s)
	if n := events.count("netsplit"); n != 1 {
		t.Fatalf("%d netsplit events, want 1", n)
	}
	if !events.has("netsplit", "Netsplit on Net") {
		t.Fatal("netsplit title")
	}
	if c.MemberCount("#c") != 1 {
		t.Fatalf("split users still listed: %d", c.MemberCount("#c"))
	}
}

func TestSplitTracker(t *testing.T) {
	var s splitTracker
	t0 := time.Now()
	for i := range 4 {
		if s.quit(t0.Add(time.Duration(i) * time.Second)) {
			t.Fatal("reported before 5 quits")
		}
	}
	if !s.quit(t0.Add(4 * time.Second)) {
		t.Fatal("5 quits within 10s is a split")
	}
	if s.quit(t0.Add(5 * time.Second)) {
		t.Fatal("reported twice")
	}
	// Quits spread out aren't a split.
	var s2 splitTracker
	for i := range 10 {
		if s2.quit(t0.Add(time.Duration(i) * 11 * time.Second)) {
			t.Fatal("spread-out quits reported")
		}
	}
	// After a quiet while, a new split is reported again.
	later := t0.Add(10 * time.Minute)
	for i := range 4 {
		s.quit(later.Add(time.Duration(i) * time.Second))
	}
	if !s.quit(later.Add(4 * time.Second)) {
		t.Fatal("a later split not reported")
	}
	if !isSplitReason("irc.a.net irc.b.org") || isSplitReason("Quit: leaving now") || isSplitReason("Ping timeout") {
		t.Fatal("isSplitReason")
	}
}

func TestMessagesAreFiledAndBroadcast(t *testing.T) {
	var msgs msgRec
	s, c := connected(t, store.Network{ID: 4, Nick: "me"}, Options{Persist: msgs.add})
	live, unsub := c.Subscribe()
	raw, unsubRaw := c.SubscribeRaw()

	s.send(":bob!b@h PRIVMSG #chan :hi all")
	s.send(":bob!b@h PRIVMSG me :psst")
	s.send(":bob!b@h NOTICE me :note")
	s.send(":bob!b@h PRIVMSG #chan :\x01ACTION waves\x01")
	s.send(":bob!b@h PRIVMSG me")
	settle(t, s)

	want := []history.Message{
		{Target: "#chan", Sender: "bob", Kind: "message", Text: "hi all"},
		{Target: "bob", Sender: "bob", Kind: "message", Text: "psst"},
		{Target: "bob", Sender: "bob", Kind: "notice", Text: "note"},
		{Target: "#chan", Sender: "bob", Kind: "action", Text: "waves"},
	}
	got := msgs.all()
	if len(got) != len(want) {
		t.Fatalf("messages: %+v", got)
	}
	for i, w := range want {
		g := got[i]
		if g.NetworkID != 4 || g.Target != w.Target || g.Sender != w.Sender || g.Kind != w.Kind || g.Text != w.Text {
			t.Errorf("%d: %+v, want %+v", i, g, w)
		}
	}
	for i := range want {
		select {
		case m := <-live:
			if m.Text != want[i].Text {
				t.Errorf("live %d: %q", i, m.Text)
			}
		case <-time.After(time.Second):
			t.Fatal("a subscriber missed a message")
		}
	}
	sawRaw := false
	for len(raw) > 0 {
		if l := <-raw; l.Text == ":bob!b@h PRIVMSG #chan :hi all" {
			sawRaw = true
			// with the ID it was recorded under
			if l.ID.IsZero() || l.ID != got[0].ID() {
				t.Errorf("raw line's ID %v, recorded %v", l.ID, got[0].ID())
			}
		} else if l.Text == ":bob!b@h PRIVMSG me" {
			if !l.ID.IsZero() {
				t.Errorf("a line that recorded nothing has an ID: %v", l.ID)
			}
		}
	}
	if !sawRaw {
		t.Error("raw subscribers get the lines verbatim")
	}
	unsub()
	unsub() // twice is fine
	unsubRaw()
	unsubRaw()
	if _, ok := <-live; ok {
		t.Error("unsubscribing doesn't close the channel")
	}
	if _, ok := <-raw; ok {
		t.Error("unsubscribing doesn't close the raw channel")
	}
	s.send(":bob!b@h PRIVMSG #chan :after")
	settle(t, s) // no panic sending to closed subscribers
}

func TestSendFromRecordsWhatItSends(t *testing.T) {
	var msgs msgRec
	s, c := connected(t, store.Network{ID: 5, Nick: "me"}, Options{Persist: msgs.add})
	c.SendMessage("#c", "hello")
	notice := c.SendFrom("sess1", "NOTICE", "bob", "psst")
	c.SendFrom("sess1", "PRIVMSG", "#c", "\x01ACTION dances\x01")
	if id := c.SendFrom("sess1", "PRIVMSG", "bob", "\x01VERSION\x01"); !id.IsZero() {
		t.Errorf("an unrecorded CTCP has an ID: %v", id)
	}
	eventually(t, "all sent", func() bool { return s.has("PRIVMSG bob :\x01VERSION\x01") })
	for _, l := range []string{"PRIVMSG #c :hello", "NOTICE bob :psst", "PRIVMSG #c :\x01ACTION dances\x01"} {
		if !s.has(l) {
			t.Errorf("not sent: %q", l)
		}
	}
	got := msgs.all()
	if len(got) != 3 {
		t.Fatalf("recorded %+v; a CTCP other than ACTION isn't", got)
	}
	if got[0].Kind != "message" || got[0].Sender != "me" || got[0].Origin != "" ||
		got[1].Kind != "notice" || got[1].Origin != "sess1" ||
		got[2].Kind != "action" || got[2].Text != "dances" {
		t.Fatalf("recorded: %+v", got)
	}
	if got[1].ID() != notice || notice.Seq == 0 {
		t.Fatalf("SendFrom returned %v, recorded %v", notice, got[1].ID())
	}
	if c.NetworkID() != 5 {
		t.Fatal("NetworkID")
	}
}

func TestAwayIsKeptAcrossReconnects(t *testing.T) {
	s, c := connected(t, store.Network{Nick: "me"}, Options{})
	c.SetAway("gone")
	eventually(t, "away sent", func() bool { return s.has("AWAY :gone") })
	c.SetAway("gone") // unchanged: not sent again
	c.SetAway("")
	eventually(t, "back sent", func() bool { return s.has("AWAY") })
	if s.count("AWAY :gone") != 1 {
		t.Fatalf("away sent twice: %q", s.last().got())
	}
	c.SetAway("brb")
	eventually(t, "away again", func() bool { return s.has("AWAY :brb") })

	// The link drops; the next connection sets it again after 001.
	first := s.last()
	first.c.Close()
	eventually(t, "reconnected", func() bool { return s.conns() == 2 && c.Status() == StatusConnected })
	eventually(t, "away restored", func() bool { return slices.Contains(s.last().got(), "AWAY :brb") })
}

func TestSetAwayWhileDisconnectedWaits(t *testing.T) {
	c := New(store.Network{Nick: "me"}, quiet, Options{})
	c.SetAway("away") // not connected: nothing to send to yet
	if c.away != "away" {
		t.Fatal("away not kept")
	}
}

func TestChannelSettingsAndReconfigure(t *testing.T) {
	c := New(store.Network{Nick: "me", QuitMsg: "net quit", ChannelSettings: map[string]store.ChannelSettings{"#a": {Key: "k"}}}, quiet, Options{QuitMsg: "default"})
	if c.quitMsg != "net quit" {
		t.Fatalf("quit %q", c.quitMsg)
	}
	if c.ChannelSettings("#A").Key != "k" {
		t.Fatal("ChannelSettings is case-insensitive")
	}
	m := c.SetChannelSettings("#B", func(cs *store.ChannelSettings) { cs.Detached = true })
	if !m["#b"].Detached || m["#a"].Key != "k" || !c.ChannelSettings("#b").Detached {
		t.Fatalf("set: %+v", m)
	}
	m = c.SetChannelSettings("#a", func(cs *store.ChannelSettings) { cs.Key = "" })
	if _, ok := m["#a"]; ok {
		t.Fatalf("empty settings are dropped: %+v", m)
	}

	c.Reconfigure(store.Network{JoinDelay: 3, FloodRate: 1, FloodBurst: 2,
		ChannelSettings: map[string]store.ChannelSettings{"#z": {Disabled: true}}}, "fallback", map[string]string{" version ": "mine"})
	if c.quitMsg != "fallback" || c.joinDelay != 3*time.Second || !c.ChannelSettings("#z").Disabled || c.ctcpReplies["VERSION"] != "mine" {
		t.Fatalf("reconfigure: %q %v %+v", c.quitMsg, c.joinDelay, c.ctcpReplies)
	}
	if c.flood.rate != 1 || c.flood.burst != 2 {
		t.Fatalf("flood: %v %v", c.flood.rate, c.flood.burst)
	}
}

func TestJoinDelay(t *testing.T) {
	start := time.Now()
	s, _ := connected(t, store.Network{Nick: "me", JoinDelay: 1, Channels: []string{"#late"}}, Options{})
	time.Sleep(300 * time.Millisecond)
	if s.has("JOIN #late") {
		t.Fatal("joined before the delay")
	}
	eventually(t, "joined after the delay", func() bool { return s.has("JOIN #late") })
	if time.Since(start) < time.Second {
		t.Fatal("joined too early")
	}
}

func TestFloodProtectionPacesUserLines(t *testing.T) {
	s, c := connected(t, store.Network{Nick: "me", FloodRate: 20, FloodBurst: 1}, Options{})
	start := time.Now()
	for range 5 {
		c.SendMessage("#c", "x")
	}
	eventually(t, "all sent", func() bool { return s.count("PRIVMSG #c") == 5 })
	if d := time.Since(start); d < 150*time.Millisecond {
		t.Fatalf("5 lines at 20/s with burst 1 took %v", d)
	}
}
