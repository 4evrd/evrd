package upstream

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/4evrd/evrd/internal/irc"
	"github.com/4evrd/evrd/internal/store"
)

// guardNet is a server with services enforcing registered nicks: a client
// on one of registered gets NickServ's notice (Anope's words), or with
// rename set is renamed to Guest… at once, or with kill set is killed.
// taken nicks get 433 while registering; NICKLEN is nicklen (0: none).
type guardNet struct {
	mu         sync.Mutex
	registered map[string]bool
	taken      map[string]bool
	rename     bool
	kill       bool
	nicklen    int
	// identifying (grace tests): IDENTIFY's answer is services' notice
	// (unless quiet), the user mode mode ("" none) and, with whois, 307
	// in WHOIS; account, when set, comes as 900 too. truncate cuts a nick
	// longer than nicklen while registering (ngIRCd) instead of 432;
	// software is 004's version.
	identified map[string]bool
	quiet      bool
	mode       string
	whois      bool
	account    string
	truncate   bool
	software   string
}

// identify handles an IDENTIFY for p's nick.
func (g *guardNet) identify(p *peer) {
	nick := p.me()
	g.mu.Lock()
	if g.identified == nil {
		g.identified = map[string]bool{}
	}
	g.identified[strings.ToLower(nick)] = true
	g.mu.Unlock()
	if !g.quiet {
		p.send(":NickServ!services@services. NOTICE %s :Password accepted - you are now recognized.", nick)
	}
	if g.mode != "" {
		p.send(":NickServ!services@services. MODE %s :%s", nick, g.mode)
	}
	if g.account != "" {
		p.send(":srv 900 %s %s!u@h %s :You are now logged in as %s", nick, nick, g.account, g.account)
	}
}

func (g *guardNet) answerWhois(p *peer, nick string) {
	me := p.me()
	p.send(":srv 311 %s %s u h * :Real", me, nick)
	g.mu.Lock()
	id := g.identified[strings.ToLower(nick)]
	g.mu.Unlock()
	if id && g.whois {
		p.send(":srv 307 %s %s :is a registered nick", me, nick)
	}
	p.send(":srv 318 %s %s :End of WHOIS list", me, nick)
}

func (g *guardNet) isRegistered(n string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.registered[strings.ToLower(n)]
}

func (g *guardNet) enforce(p *peer, nick string) {
	if !g.isRegistered(nick) {
		return
	}
	switch {
	case g.kill:
		p.send(":NickServ!services@services. KILL %s :Nick kill enforced", nick)
		p.send("ERROR :Closing Link: %s (Killed (NickServ (Nick kill enforced)))", nick)
		p.c.Close()
	case g.rename:
		p.send(":%s!u@h NICK Guest4242", nick)
		p.mu.Lock()
		p.welcomed = "Guest4242"
		p.mu.Unlock()
	default:
		p.send(":NickServ!services@services. NOTICE %s :This nickname is registered and protected. If it is your nick, type \x02/msg NickServ IDENTIFY \x1fpassword\x1f\x02. Otherwise, please choose a different nick.", nick)
		p.send(":NickServ!services@services. NOTICE %s :If you do not change within 20 seconds, I will change your nick.", nick)
	}
}

func (g *guardNet) refused(n string) string {
	switch {
	case g.taken[strings.ToLower(n)]:
		return "433 * " + n + " :Nickname is already in use"
	case g.nicklen > 0 && len(n) > g.nicklen && !g.truncate:
		return "432 * " + n + " :Erroneous nickname"
	}
	return ""
}

func (g *guardNet) welcome(p *peer) {
	if g.truncate && g.nicklen > 0 && len(p.nick) > g.nicklen {
		p.nick = p.nick[:g.nicklen]
	}
	p.send(":srv 001 %s :Welcome", p.nick)
	if g.software != "" {
		p.send(":srv 004 %s srv %s io o", p.nick, g.software)
	}
	if g.nicklen > 0 {
		p.send(":srv 005 %s NICKLEN=%d :are supported", p.nick, g.nicklen)
	}
	p.mu.Lock()
	p.welcomed = p.nick
	p.mu.Unlock()
	g.enforce(p, p.nick)
}

func (g *guardNet) reply(p *peer, l string) {
	m := irc.Parse(l)
	userSent := index(p.got(), "USER ") >= 0
	switch {
	case strings.HasPrefix(l, "CAP LS"):
		p.send(":srv CAP * LS :")
	case m.Command == "NICK" && p.registered():
		old := p.me()
		p.send(":%s!u@h NICK %s", old, m.Params[0])
		p.mu.Lock()
		p.welcomed = m.Params[0]
		p.mu.Unlock()
		g.enforce(p, m.Params[0])
	case m.Command == "NICK":
		if r := g.refused(m.Params[0]); r != "" {
			p.send(":srv " + r)
		} else if userSent {
			g.welcome(p)
		}
	case strings.HasPrefix(l, "USER "):
		if g.refused(p.nick) == "" {
			g.welcome(p)
		}
	case m.Command == "JOIN":
		p.send(":%s!u@h JOIN %s", p.me(), m.Params[0])
	case m.Command == "PRIVMSG" && len(m.Params) > 1 && strings.EqualFold(m.Params[0], "NickServ") && strings.HasPrefix(strings.ToUpper(m.Params[1]), "IDENTIFY"):
		g.identify(p)
	case m.Command == "WHOIS" && len(m.Params) > 0:
		g.answerWhois(p, m.Params[0])
	case strings.HasPrefix(l, "PING "):
		p.send(":srv PONG srv %s", strings.TrimPrefix(l, "PING "))
	}
}

func shortenGuard(t *testing.T) {
	old := guardSettle
	guardSettle = 100 * time.Millisecond
	t.Cleanup(func() { guardSettle = old })
}

func startGuarded(t *testing.T, g *guardNet, n store.Network, guard bool) (*scripted, *Conn) {
	t.Helper()
	shortenGuard(t)
	s := startScripted(t, g.reply)
	if n.Nick == "" {
		n.Nick = "alice-home"
	}
	if n.AuthMethod == "" {
		n.AuthMethod = store.AuthNone
	}
	c := New(s.network(n), quiet, Options{NickGuard: guard})
	run(t, c)
	eventually(t, "registered", func() bool { return c.Status() == StatusConnected })
	return s, c
}

func noIdentify(t *testing.T, s *scripted) {
	t.Helper()
	for _, p := range s.peers {
		for _, l := range p.got() {
			if strings.Contains(strings.ToUpper(l), "IDENTIFY") || strings.HasPrefix(l, "AUTHENTICATE") {
				t.Fatalf("logged in to services: %q", l)
			}
		}
	}
}

func TestNickGuardMovesOffARegisteredNick(t *testing.T) {
	g := &guardNet{registered: map[string]bool{"alice-home": true}}
	s, c := startGuarded(t, g, store.Network{AltNick: "alice-home_", Channels: []string{"#home"}}, true)
	eventually(t, "on the alternate nick", func() bool { return c.Nick() == "alice-home_" })
	eventually(t, "joined", func() bool { return c.Joined("#home") })
	got := s.last().got()
	nick, join := index(got, "NICK alice-home_"), index(got, "JOIN #home")
	if nick < 0 || join < nick {
		t.Fatalf("the join went out before the nick was safe: %q", got)
	}
	if s.count("NICK ") != 2 {
		t.Errorf("NICKs: %q", got)
	}
	noIdentify(t, s)
	if !c.badNicks["alice-home"] {
		t.Error("the registered nick isn't remembered")
	}
}

func TestNickGuardSkipsEveryRegisteredCandidate(t *testing.T) {
	g := &guardNet{registered: map[string]bool{"alice-home": true, "alice-home_": true, "alice-home_2": true}}
	_, c := startGuarded(t, g, store.Network{AltNick: "alice-home_"}, true)
	eventually(t, "on a free nick", func() bool { return c.Nick() == "alice-home_3" })
}

func TestNickGuardDefaultAltNick(t *testing.T) {
	g := &guardNet{registered: map[string]bool{"bob": true}}
	_, c := startGuarded(t, g, store.Network{Nick: "bob"}, true)
	eventually(t, "on bob_", func() bool { return c.Nick() == "bob_" })
}

func TestNickGuardFollowsAForcedRename(t *testing.T) {
	g := &guardNet{registered: map[string]bool{"alice-home": true}, rename: true}
	s, c := startGuarded(t, g, store.Network{AltNick: "alice-home_", Channels: []string{"#home"}}, true)
	eventually(t, "off the guest nick", func() bool { return c.Nick() == "alice-home_" })
	eventually(t, "joined", func() bool { return c.Joined("#home") })
	got := s.last().got()
	if index(got, "JOIN #home") < index(got, "NICK alice-home_") {
		t.Fatalf("joined as the guest nick: %q", got)
	}
}

func TestNickGuardRemembersAKill(t *testing.T) {
	g := &guardNet{registered: map[string]bool{"alice-home": true}, kill: true}
	shortenGuard(t)
	s := startScripted(t, g.reply)
	c := New(s.network(store.Network{Nick: "alice-home", AltNick: "alice-home_", AuthMethod: store.AuthNone}), quiet, Options{NickGuard: true})
	run(t, c)
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) && !(s.conns() >= 2 && c.Status() == StatusConnected) {
		time.Sleep(20 * time.Millisecond)
	}
	if s.conns() != 2 || c.Status() != StatusConnected {
		t.Fatalf("connections %d, status %s", s.conns(), c.Status())
	}
	for _, l := range s.last().got() {
		if l == "NICK alice-home" {
			t.Fatalf("tried the registered nick again: %q", s.last().got())
		}
	}
	time.Sleep(300 * time.Millisecond)
	if s.conns() != 2 || c.Nick() != "alice-home_" {
		t.Fatalf("not settled: %d connections, nick %s", s.conns(), c.Nick())
	}
}

func TestNickGuardTakenWhileRegistering(t *testing.T) {
	g := &guardNet{taken: map[string]bool{"alice-home": true, "alice-home_": true}}
	_, c := startGuarded(t, g, store.Network{AltNick: "alice-home_"}, true)
	eventually(t, "numbered", func() bool { return c.Nick() == "alice-home_2" })
	if len(c.badNicks) != 0 {
		t.Errorf("nicks in use taken for registered: %v", c.badNicks)
	}
}

func TestNickGuardNickLen(t *testing.T) {
	g := &guardNet{registered: map[string]bool{"alice-hom": true, "alice-ho_": true}, nicklen: 9}
	_, c := startGuarded(t, g, store.Network{Nick: "alice-home", AltNick: "alice-home_"}, true)
	// alice-home: 432; then the alternate nick, cut to 9 once NICKLEN is known
	eventually(t, "a short free nick", func() bool {
		n := c.Nick()
		return len(n) <= 9 && !g.isRegistered(n) && c.Status() == StatusConnected && n != "alice-hom"
	})
}

func TestNickGuardHoldsClientJoins(t *testing.T) {
	g := &guardNet{registered: map[string]bool{"alice-home": true}}
	old := guardSettle
	s := startScripted(t, g.reply)
	guardSettle = 300 * time.Millisecond
	t.Cleanup(func() { guardSettle = old })
	c := New(s.network(store.Network{Nick: "alice-home", AltNick: "alice-home_", AuthMethod: store.AuthNone}), quiet, Options{NickGuard: true})
	run(t, c)
	eventually(t, "registered", func() bool { return c.Status() == StatusConnected })
	c.SendRaw("JOIN #now")
	eventually(t, "joined", func() bool { return c.Joined("#now") })
	got := s.last().got()
	if index(got, "JOIN #now") < index(got, "NICK alice-home_") {
		t.Fatalf("the client's join went out on the registered nick: %q", got)
	}
}

func TestNickGuardLetsAClientPickANick(t *testing.T) {
	g := &guardNet{registered: map[string]bool{}}
	s, c := startGuarded(t, g, store.Network{AltNick: "alice-home_"}, true)
	c.SendRaw("NICK alice-away")
	eventually(t, "renamed", func() bool { return c.Nick() == "alice-away" })
	time.Sleep(100 * time.Millisecond)
	if c.Nick() != "alice-away" || s.count("NICK ") != 2 {
		t.Fatalf("the guard undid the client's nick: %s %q", c.Nick(), s.last().got())
	}
}

func TestNoNickGuardKeepsTheNick(t *testing.T) {
	g := &guardNet{registered: map[string]bool{"alice-home": true}}
	s, c := startGuarded(t, g, store.Network{AltNick: "alice-home_", Channels: []string{"#home"}}, false)
	eventually(t, "joined", func() bool { return c.Joined("#home") })
	time.Sleep(150 * time.Millisecond)
	if c.Nick() != "alice-home" || s.count("NICK ") != 1 {
		t.Fatalf("without the guard the nick changed: %s", c.Nick())
	}
}

func TestFitNick(t *testing.T) {
	for _, tc := range []struct {
		base, suffix string
		max          int
		want         string
	}{
		{"alice", "", 0, "alice"},
		{"alice-home", "", 9, "alice-hom"},
		{"alice-home_", "12", 9, "alice-h12"},
		{"a", "123456", 3, "456"},
		{"thelion-windows-bosonchat", "", 16, "thelion-windows"},
		{"TheLion-windows-bosonchat", "_", 16, "TheLion-windows_"},
		{"TheLion-windows-bosonchat_", "2", 16, "TheLion-windows2"},
		{"a-b-c-d-e", "", 5, "a-b-c"},
		{"bob-x", "", 3, "bob"},
		{"alice--", "", 6, "alice"},
	} {
		if got := fitNick(tc.base, tc.suffix, tc.max); got != tc.want {
			t.Errorf("fitNick(%q, %q, %d) = %q, want %q", tc.base, tc.suffix, tc.max, got, tc.want)
		}
	}
}

// graced starts a guarded connection with a grace, a client attached
// (idle false) and its raw feed, which it returns.
func graced(t *testing.T, g *guardNet, n store.Network, opts Options) (*scripted, *Conn, <-chan Line) {
	t.Helper()
	shortenGuard(t)
	old := guardWhoisWait
	guardWhoisWait = 2 * time.Second
	t.Cleanup(func() { guardWhoisWait = old })
	s := startScripted(t, g.reply)
	if n.Nick == "" {
		n.Nick = "alice-home"
	}
	n.AuthMethod = store.AuthNone
	opts.NickGuard = true
	if opts.NickGuardGrace == 0 {
		opts.NickGuardGrace = 400 * time.Millisecond
	}
	if opts.Idle == nil {
		opts.Idle = func() bool { return false }
	}
	if opts.ReplyFrom == "" {
		opts.ReplyFrom = "irc.example.org"
	}
	c := New(s.network(n), quiet, opts)
	raw, unsub := c.SubscribeRaw()
	t.Cleanup(unsub)
	run(t, c)
	eventually(t, "registered", func() bool { return c.Status() == StatusConnected })
	return s, c, raw
}

// drainInto collects what the raw feed has had so far.
func drainInto(raw <-chan Line, into *[]string) {
	for {
		select {
		case l, ok := <-raw:
			if !ok {
				return
			}
			*into = append(*into, l.Text)
		default:
			return
		}
	}
}

func TestNickGuardGraceWarnsThenMoves(t *testing.T) {
	g := &guardNet{registered: map[string]bool{"alice-home": true}}
	s, c, raw := graced(t, g, store.Network{AltNick: "alice-home_"}, Options{
		GuardWarning: "{nick} is registered with {services}. Identify within {grace} or you'll be moved to {next}",
	})
	time.Sleep(200 * time.Millisecond)
	if c.Nick() != "alice-home" {
		t.Fatalf("moved during the grace: %s", c.Nick())
	}
	eventually(t, "moved after the grace", func() bool { return c.Nick() == "alice-home_" })
	var seen []string
	time.Sleep(50 * time.Millisecond)
	drainInto(raw, &seen)
	warned := 0
	for _, l := range seen {
		if strings.HasPrefix(l, ":irc.example.org NOTICE alice-home :alice-home is registered with NickServ. Identify within 1 second or you'll be moved to alice-home_") {
			warned++
		}
		if strings.Contains(l, " 311 ") || strings.Contains(l, " 318 ") {
			t.Errorf("the guard's WHOIS reached clients: %q", l)
		}
	}
	if warned != 1 {
		t.Errorf("warned %d times: %q", warned, seen)
	}
	if index(s.last().got(), "WHOIS alice-home") < 0 {
		t.Errorf("no WHOIS before moving: %q", s.last().got())
	}
	if !c.badNicks["alice-home"] {
		t.Error("not remembered as registered")
	}
	noIdentify(t, s)
}

func TestNickGuardGraceKeepsAnIdentifiedNick(t *testing.T) {
	for _, tc := range []struct {
		name string
		g    *guardNet
	}{
		{"services' notice", &guardNet{}},
		{"+r", &guardNet{quiet: true, mode: "+r"}},
		{"+R on ngIRCd", &guardNet{quiet: true, mode: "+R", software: "ngircd-28"}},
		{"900", &guardNet{quiet: true, account: "alice-home"}},
		{"307 at the end", &guardNet{quiet: true, whois: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := tc.g
			g.registered = map[string]bool{"alice-home": true}
			saved := make(chan string, 1)
			_, c, _ := graced(t, g, store.Network{AltNick: "alice-home_", Channels: []string{"#home"}}, Options{
				OnIdentifiedNick: func(n string) { saved <- n },
			})
			c.SendRaw("PRIVMSG NickServ :IDENTIFY secret")
			select {
			case n := <-saved:
				if n != "alice-home" {
					t.Errorf("saved %q", n)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("the identified nick wasn't saved")
			}
			time.Sleep(700 * time.Millisecond) // past the grace
			if c.Nick() != "alice-home" {
				t.Fatalf("moved off an identified nick: %s", c.Nick())
			}
			eventually(t, "joined", func() bool { return c.Joined("#home") })
			if c.badNicks["alice-home"] {
				t.Error("an identified nick kept as someone else's")
			}
		})
	}
}

func TestNickGuardGraceIgnoresTheWrongModes(t *testing.T) {
	// +r is restricted on ngIRCd, +R is regdeaf elsewhere: neither counts
	for _, g := range []*guardNet{{quiet: true, mode: "+r", software: "ngircd-28"}, {quiet: true, mode: "+R"}, {quiet: true, mode: "-r"}} {
		g.registered = map[string]bool{"alice-home": true}
		_, c, _ := graced(t, g, store.Network{AltNick: "alice-home_"}, Options{})
		c.SendRaw("PRIVMSG NickServ :IDENTIFY secret")
		eventually(t, "moved", func() bool { return c.Nick() == "alice-home_" })
	}
}

func TestNickGuardGraceAccountNotLikeTheNick(t *testing.T) {
	// logged in to another account: the server's WHOIS has the last word
	g := &guardNet{registered: map[string]bool{"alice-home": true}, quiet: true, account: "someone"}
	s, c, _ := graced(t, g, store.Network{AltNick: "alice-home_"}, Options{})
	c.SendRaw("PRIVMSG NickServ :IDENTIFY secret")
	eventually(t, "asked", func() bool { return s.count("WHOIS ") >= 1 })
	eventually(t, "moved", func() bool { return c.Nick() == "alice-home_" })
}

func TestNickGuardNoGraceWithoutAClient(t *testing.T) {
	g := &guardNet{registered: map[string]bool{"alice-home": true}}
	s, c, _ := graced(t, g, store.Network{AltNick: "alice-home_"}, Options{NickGuardGrace: time.Hour, Idle: func() bool { return true }})
	eventually(t, "moved at once", func() bool { return c.Nick() == "alice-home_" })
	if s.count("WHOIS ") != 0 {
		t.Error("asked WHOIS")
	}
}

func TestNickGuardGraceEndsWithTheNick(t *testing.T) {
	g := &guardNet{registered: map[string]bool{"alice-home": true}}
	s, c, _ := graced(t, g, store.Network{AltNick: "alice-home_"}, Options{})
	c.SendRaw("NICK alice-away")
	eventually(t, "renamed", func() bool { return c.Nick() == "alice-away" })
	time.Sleep(700 * time.Millisecond)
	if c.Nick() != "alice-away" || s.count("WHOIS ") != 0 {
		t.Fatalf("the grace went on: %s %q", c.Nick(), s.last().got())
	}
	// back on it: a new grace, then moved
	c.SendRaw("NICK alice-home")
	eventually(t, "back", func() bool { return c.Nick() == "alice-home" })
	eventually(t, "moved", func() bool { return c.Nick() == "alice-home_" })
}

func TestNickGuardPrefersTheIdentifiedNick(t *testing.T) {
	g := &guardNet{registered: map[string]bool{"lion": true}}
	s, c, _ := graced(t, g, store.Network{Nick: "lion-home"}, Options{PreferNick: "lion"})
	if got := s.last().got(); index(got, "NICK lion") != index(got, "NICK ") || c.Nick() != "lion" {
		t.Fatalf("didn't start on the preferred nick: %q", got)
	}
	c.SendRaw("PRIVMSG NickServ :IDENTIFY secret")
	time.Sleep(700 * time.Millisecond)
	if c.Nick() != "lion" {
		t.Fatalf("moved: %s", c.Nick())
	}
	// not identified (another time): moved to the nick
	g.mu.Lock()
	g.identified = nil
	g.mu.Unlock()
	s2, c2, _ := graced(t, g, store.Network{Nick: "lion-home"}, Options{PreferNick: "lion"})
	eventually(t, "moved to the nick", func() bool { return c2.Nick() == "lion-home" })
	_ = s2
	// nobody attached: the nick itself
	s3, c3, _ := graced(t, g, store.Network{Nick: "lion-home"}, Options{PreferNick: "lion", Idle: func() bool { return true }})
	if c3.Nick() != "lion-home" || index(s3.last().got(), "NICK lion\x00") >= 0 {
		t.Fatalf("started on %s", c3.Nick())
	}
	for _, l := range s3.last().got() {
		if l == "NICK lion" {
			t.Fatalf("tried the preferred nick while idle: %q", s3.last().got())
		}
	}
}

func TestNickGuardRefitsATruncatedNick(t *testing.T) {
	g := &guardNet{registered: map[string]bool{"thelion-windows-": true}, nicklen: 16, truncate: true}
	shortenGuard(t)
	s := startScripted(t, g.reply)
	c := New(s.network(store.Network{Nick: "thelion-windows-bosonchat", AuthMethod: store.AuthNone}), quiet, Options{NickGuard: true})
	run(t, c)
	eventually(t, "on the fitted nick", func() bool { return c.Nick() == "thelion-windows" })
	time.Sleep(200 * time.Millisecond)
	if c.Nick() != "thelion-windows" {
		t.Fatalf("then %s", c.Nick())
	}
}

func TestGraceText(t *testing.T) {
	for d, want := range map[time.Duration]string{
		time.Second: "1 second", 400 * time.Millisecond: "1 second", 60 * time.Second: "60 seconds",
		90 * time.Second: "90 seconds", 2 * time.Minute: "2 minutes", 150 * time.Second: "150 seconds",
	} {
		if got := graceText(d); got != want {
			t.Errorf("graceText(%s) = %q, want %q", d, got, want)
		}
	}
}
