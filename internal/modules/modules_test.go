package modules

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/4evrd/evrd/internal/irc"
)

// fakeHost is a connection a test drives: it records what modules send and
// log, and runs timers when the test says.
type fakeHost struct {
	sent, logs     []string
	nick, wanted   string
	joined, ops    map[string]bool
	members        map[string][]Member
	user, password string
	timers         []func()
}

func newFake() *fakeHost {
	return &fakeHost{nick: "alice", wanted: "alice", joined: map[string]bool{}, ops: map[string]bool{}, members: map[string][]Member{}}
}

func (f *fakeHost) Send(l string)        { f.sent = append(f.sent, l) }
func (f *fakeHost) SendAsUser(l string)  { f.sent = append(f.sent, l) }
func (f *fakeHost) Nick() string         { return f.nick }
func (f *fakeHost) WantedNick() string   { return f.wanted }
func (f *fakeHost) Joined(c string) bool { return f.joined[strings.ToLower(c)] }
func (f *fakeHost) Channels() (out []string) {
	for c := range f.joined {
		out = append(out, c)
	}
	return out
}
func (f *fakeHost) Members(c string) []Member       { return f.members[strings.ToLower(c)] }
func (f *fakeHost) IsOp(c string) bool              { return f.ops[strings.ToLower(c)] }
func (f *fakeHost) Account() (string, string)       { return f.user, f.password }
func (f *fakeHost) After(_ time.Duration, g func()) { f.timers = append(f.timers, g) }
func (f *fakeHost) Log(t string)                    { f.logs = append(f.logs, t) }
func (f *fakeHost) Reply(string)                    {}

// fire runs the timers set so far (not ones they set).
func (f *fakeHost) fire() {
	ts := f.timers
	f.timers = nil
	for _, t := range ts {
		t()
	}
}

func start(t *testing.T, f *fakeHost, name string, settings map[string]string) Module {
	t.Helper()
	ms := Start(f, map[string]Config{name: {Enabled: true, Settings: settings}}, nil)
	if len(ms) != 1 {
		t.Fatalf("%s didn't start", name)
	}
	return ms[0]
}

func line(s string) irc.Message { return irc.Parse(s) }

func TestPerform(t *testing.T) {
	f := newFake()
	start(t, f, "perform", map[string]string{"commands": "/msg NickServ ghost alice\n\n/mode %nick% +x\nOPER me secret\n/join #a,#b"}).Registered()
	want := []string{"PRIVMSG NickServ :ghost alice", "MODE alice +x", "OPER me secret", "JOIN #a,#b"}
	if !slices.Equal(f.sent, want) {
		t.Fatalf("sent %q", f.sent)
	}
	if len(f.logs) != 1 || f.logs[0] != "perform: sent 4 commands" {
		t.Fatalf("logged %q (and never the commands: they can carry passwords)", f.logs)
	}
}

func TestKeepNick(t *testing.T) {
	f := newFake()
	f.nick, f.wanted = "alice_", "alice"
	m := start(t, f, "keepnick", nil)
	m.Registered()
	f.fire() // 30 seconds later
	if !slices.Equal(f.sent, []string{"NICK alice"}) {
		t.Fatalf("sent %q", f.sent)
	}
	m.Line(line(":alice!x@y QUIT :bye")) // the holder left
	if f.sent[len(f.sent)-1] != "NICK alice" || len(f.sent) != 2 {
		t.Fatalf("sent %q", f.sent)
	}
	f.nick = "alice" // got it: the retries stop
	f.sent = nil
	f.fire()
	m.Line(line(":alice2!x@y NICK :bob"))
	if len(f.sent) != 0 || len(f.timers) != 0 {
		t.Fatalf("kept trying: %q", f.sent)
	}
}

func TestKickRejoin(t *testing.T) {
	f := newFake()
	m := start(t, f, "kickrejoin", map[string]string{"delay": "3"})
	m.Line(line(":op!o@h KICK #home bob :not you"))
	if len(f.timers) != 0 {
		t.Fatal("rejoined someone else's kick")
	}
	m.Line(line(":op!o@h KICK #home alice :out"))
	f.fire()
	if !slices.Equal(f.sent, []string{"JOIN #home"}) {
		t.Fatalf("sent %q", f.sent)
	}
	f.sent = nil
	f.joined["#home"] = true // rejoined meanwhile: nothing to do
	m.Line(line(":op!o@h KICK #home alice :out"))
	f.joined["#home"] = true
	f.fire()
	if len(f.sent) != 0 {
		t.Fatalf("sent %q", f.sent)
	}
}

func TestAutocycle(t *testing.T) {
	f := newFake()
	f.joined["#home"], f.joined["#work"] = true, true
	f.members["#home"] = []Member{{Nick: "alice"}}
	f.members["#work"] = []Member{{Nick: "alice"}}
	m := start(t, f, "autocycle", map[string]string{"channels": "*\n!#work"})
	m.Line(line(":bob!b@h PART #home"))
	if !slices.Equal(f.sent, []string{"PART #home", "JOIN #home"}) {
		t.Fatalf("sent %q", f.sent)
	}
	f.sent = nil
	m.Line(line(":bob!b@h PART #home")) // within a minute: not again
	m.Line(line(":bob!b@h PART #work")) // left out
	if len(f.sent) != 0 {
		t.Fatalf("sent %q", f.sent)
	}
	f.members["#lobby"], f.joined["#lobby"] = []Member{{Nick: "alice"}}, true
	m.Line(line(":carol!c@h QUIT :bye")) // a quit: every channel is checked
	if !slices.Equal(f.sent, []string{"PART #lobby", "JOIN #lobby"}) {
		t.Fatalf("sent %q", f.sent)
	}
	f.sent = nil
	f.ops["#busy"], f.joined["#busy"], f.members["#busy"] = true, true, []Member{{Nick: "alice", Prefix: "@"}}
	m.Line(line(":bob!b@h PART #busy")) // already op
	if len(f.sent) != 0 {
		t.Fatalf("sent %q", f.sent)
	}
}

func TestAutoOpAndVoice(t *testing.T) {
	f := newFake()
	f.ops["#home"] = true
	f.members["#home"] = []Member{{Nick: "alice", Prefix: "@"}, {Nick: "bob"}, {Nick: "eve"}}
	op := start(t, f, "autoop", map[string]string{"users": "*!*@friend.example.org #home,#work\ncarol"})
	op.Line(line(":bob!b@friend.example.org JOIN #home"))
	op.Line(line(":eve!e@evil.example.org JOIN #home"))
	op.Line(line(":bob!b@friend.example.org JOIN #elsewhere"))
	f.fire()
	if !slices.Equal(f.sent, []string{"MODE #home +o bob"}) {
		t.Fatalf("sent %q", f.sent)
	}
	// not when we aren't op, nor when they already are
	f.sent = nil
	f.members["#home"][1].Prefix = "@"
	op.Line(line(":bob!b@friend.example.org JOIN #home"))
	f.fire()
	f.ops["#home"] = false
	f.members["#home"] = append(f.members["#home"], Member{Nick: "carol"})
	op.Line(line(":carol!c@anywhere JOIN #home"))
	f.fire()
	if len(f.sent) != 0 {
		t.Fatalf("sent %q", f.sent)
	}
	v := start(t, f, "autovoice", map[string]string{"users": "carol"})
	f.ops["#home"] = true
	v.Line(line(":carol!c@anywhere JOIN #home"))
	f.fire()
	if !slices.Equal(f.sent, []string{"MODE #home +v carol"}) {
		t.Fatalf("sent %q", f.sent)
	}
}

func TestAlias(t *testing.T) {
	f := newFake()
	m := start(t, f, "alias", map[string]string{"aliases": "op = /mode %1 +o %2\nsay = /msg %1 %2+\nboth = /join %1 ; /msg %1 hi, I'm %nick%\nbroken"}).(ClientFilter)
	for _, l := range []string{"OP #home bob", "say #home hello there", "both #new", "PRIVMSG #home :not an alias"} {
		m.ClientLine(l)
	}
	want := []string{"MODE #home +o bob", "PRIVMSG #home :hello there", "JOIN #new", "PRIVMSG #new :hi, I'm alice"}
	if !slices.Equal(f.sent, want) {
		t.Fatalf("sent %q", f.sent)
	}
	if m.ClientLine("PRIVMSG #home :x") {
		t.Fatal("a normal line was taken as an alias")
	}
}

func TestCTCPFlood(t *testing.T) {
	f := newFake()
	m := start(t, f, "ctcpflood", map[string]string{"limit": "2", "seconds": "60"}).(CTCPFilter)
	got := []bool{m.CTCP("a", "VERSION"), m.CTCP("b", "PING"), m.CTCP("c", "TIME"), m.CTCP("d", "TIME")}
	if !slices.Equal(got, []bool{false, false, true, true}) {
		t.Fatalf("dropped %v", got)
	}
	if len(f.logs) != 1 {
		t.Fatalf("logged %q — once per flood", f.logs)
	}
}

func TestQ(t *testing.T) {
	f := newFake()
	f.user, f.password = "alice", "pw"
	m := start(t, f, "q", nil)
	if lh, ok := m.(LoginHandler); !ok || !lh.HandlesLogin() {
		t.Fatal("q must stop the NickServ login")
	}
	m.Registered()
	if !slices.Equal(f.sent, []string{"PRIVMSG Q@CServe.quakenet.org :AUTH alice pw", "MODE alice +x"}) {
		t.Fatalf("sent %q", f.sent)
	}
	for _, l := range f.logs {
		if strings.Contains(l, "pw") {
			t.Fatalf("logged the password: %q", l)
		}
	}
}

func TestStartAndCheck(t *testing.T) {
	f := newFake()
	cfgs := map[string]Config{"perform": {Enabled: true}, "keepnick": {Enabled: false}, "kickrejoin": {Enabled: true}}
	if n := len(Start(f, cfgs, nil)); n != 2 {
		t.Fatalf("%d started, want 2 (keepnick is off)", n)
	}
	if n := len(Start(f, cfgs, map[string]bool{"perform": true})); n != 1 {
		t.Fatalf("%d started with perform switched off server-wide", n)
	}
	if err := Check(map[string]Config{"nosuch": {}}); err == nil {
		t.Fatal("an unknown module passed")
	}
	if err := Check(map[string]Config{"kickrejoin": {Settings: map[string]string{"delay": "soon"}}}); err == nil {
		t.Fatal("a non-number passed")
	}
	if err := Check(map[string]Config{"perform": {Settings: map[string]string{"bogus": "x"}}}); err == nil {
		t.Fatal("an unknown setting passed")
	}
	if err := Check(map[string]Config{"kickrejoin": {Enabled: true, Settings: map[string]string{"delay": "5"}}}); err != nil {
		t.Fatal(err)
	}
}

func TestMatch(t *testing.T) {
	for _, c := range []struct {
		mask, s string
		want    bool
	}{
		{"*!*@friend.example.org", "bob!b@FRIEND.example.org", true},
		{"bob!*@*", "bob!x@y", true},
		{"bob!*@*", "bobby!x@y", false},
		{"#h?me", "#home", true},
		{"*", "", true},
	} {
		if Match(c.mask, c.s) != c.want {
			t.Errorf("Match(%q, %q) != %v", c.mask, c.s, c.want)
		}
	}
}
