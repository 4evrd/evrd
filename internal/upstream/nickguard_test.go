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
	case g.nicklen > 0 && len(n) > g.nicklen:
		return "432 * " + n + " :Erroneous nickname"
	}
	return ""
}

func (g *guardNet) welcome(p *peer) {
	p.send(":srv 001 %s :Welcome", p.nick)
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
	} {
		if got := fitNick(tc.base, tc.suffix, tc.max); got != tc.want {
			t.Errorf("fitNick(%q, %q, %d) = %q, want %q", tc.base, tc.suffix, tc.max, got, tc.want)
		}
	}
}
