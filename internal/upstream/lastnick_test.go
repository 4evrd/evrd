package upstream

import (
	"sync"
	"testing"
	"time"

	"github.com/4evrd/evrd/internal/store"
)

// savedNicks records what OnLastNick was told.
type savedNicks struct {
	mu   sync.Mutex
	list []string
}

func (s *savedNicks) save(n string) {
	s.mu.Lock()
	s.list = append(s.list, n)
	s.mu.Unlock()
}

func (s *savedNicks) got() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.list...)
}

func (s *savedNicks) last() (string, bool) {
	l := s.got()
	if len(l) == 0 {
		return "", false
	}
	return l[len(l)-1], true
}

func firstNick(lines []string) string {
	if i := index(lines, "NICK "); i >= 0 {
		return lines[i]
	}
	return ""
}

func TestLastNickRegistersWithIt(t *testing.T) {
	g := &guardNet{}
	s := startScripted(t, g.reply)
	c := New(s.network(store.Network{Nick: "lion-home", AuthMethod: store.AuthNone}), quiet, Options{LastNick: "lion"})
	run(t, c)
	eventually(t, "registered", func() bool { return c.Status() == StatusConnected })
	if got := firstNick(s.last().got()); got != "NICK lion" || c.Nick() != "lion" {
		t.Fatalf("registered with %q, on %s", got, c.Nick())
	}
}

func TestLastNickNoneRegistersWithTheNick(t *testing.T) {
	g := &guardNet{}
	s := startScripted(t, g.reply)
	c := New(s.network(store.Network{Nick: "lion-home", AuthMethod: store.AuthNone}), quiet, Options{})
	run(t, c)
	eventually(t, "registered", func() bool { return c.Status() == StatusConnected })
	if got := firstNick(s.last().got()); got != "NICK lion-home" {
		t.Fatalf("registered with %q", got)
	}
}

func TestLastNickSavesAClientsNick(t *testing.T) {
	g := &guardNet{}
	s := startScripted(t, g.reply)
	saved := &savedNicks{}
	c := New(s.network(store.Network{Nick: "lion-home", AuthMethod: store.AuthNone}), quiet, Options{OnLastNick: saved.save})
	run(t, c)
	eventually(t, "registered", func() bool { return c.Status() == StatusConnected })
	c.SendRaw("NICK TheLion")
	eventually(t, "saved", func() bool { n, ok := saved.last(); return ok && n == "TheLion" })
	if c.LastNick() != "TheLion" {
		t.Fatalf("last nick %q", c.LastNick())
	}
	// the same nick again: nothing new to save
	c.SendRaw("NICK TheLion")
	time.Sleep(100 * time.Millisecond)
	// a rename nobody asked for isn't the user's pick
	p := s.last()
	p.send(":TheLion!u@h NICK Guest4242")
	eventually(t, "renamed", func() bool { return c.Nick() == "Guest4242" })
	time.Sleep(100 * time.Millisecond)
	if got := saved.got(); len(got) != 1 || c.LastNick() != "TheLion" {
		t.Fatalf("saved %q, last %q", got, c.LastNick())
	}
}

func TestLastNickNotForTheGuardsOwnMoves(t *testing.T) {
	g := &guardNet{registered: map[string]bool{"alice-home": true}}
	saved := &savedNicks{}
	_, c, _ := graced(t, g, store.Network{}, Options{OnLastNick: saved.save, Idle: func() bool { return true }})
	eventually(t, "moved", func() bool { return c.Nick() == "alice-home_" })
	time.Sleep(100 * time.Millisecond)
	if got := saved.got(); len(got) != 0 {
		t.Fatalf("saved %q", got)
	}
}

func TestLastNickGuardedTriesItFirst(t *testing.T) {
	g := &guardNet{}
	s, c, _ := graced(t, g, store.Network{}, Options{LastNick: "alice"})
	if got := firstNick(s.last().got()); got != "NICK alice" || c.Nick() != "alice" {
		t.Fatalf("registered with %q, on %s", got, c.Nick())
	}
}

func TestLastNickGuardedForgetsARegisteredOne(t *testing.T) {
	g := &guardNet{registered: map[string]bool{"alice": true}}
	saved := &savedNicks{}
	_, c, _ := graced(t, g, store.Network{}, Options{LastNick: "alice", OnLastNick: saved.save, Idle: func() bool { return true }})
	eventually(t, "moved", func() bool { return c.Nick() == "alice-home" })
	eventually(t, "forgotten", func() bool { n, ok := saved.last(); return ok && n == "" })
	if c.LastNick() != "" {
		t.Fatalf("still %q", c.LastNick())
	}
}

func TestLastNickGuardedIdleSkipsTheIdentifiedNick(t *testing.T) {
	g := &guardNet{registered: map[string]bool{"lion": true}}
	s, c, _ := graced(t, g, store.Network{Nick: "lion-home"}, Options{PreferNick: "lion", LastNick: "lion", Idle: func() bool { return true }})
	if got := firstNick(s.last().got()); got != "NICK lion-home" || c.Nick() != "lion-home" {
		t.Fatalf("registered with %q, on %s", got, c.Nick())
	}
	for _, l := range s.last().got() {
		if l == "NICK lion" {
			t.Fatalf("tried the registered nick with nobody to identify: %q", s.last().got())
		}
	}
	if c.LastNick() != "lion" {
		t.Fatalf("lost the last nick: %q", c.LastNick())
	}
}

func TestLastNickGuardedKilledForgetsIt(t *testing.T) {
	c := New(store.Network{Nick: "alice-home"}, quiet, Options{NickGuard: true, LastNick: "alice"})
	saved := make(chan string, 1)
	c.onLastNick = func(n string) { saved <- n }
	c.nick = "alice"
	c.guardKilled("Killed (NickServ (Nick kill enforced))")
	select {
	case n := <-saved:
		if n != "" {
			t.Fatalf("saved %q", n)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("not forgotten")
	}
	if c.LastNick() != "" {
		t.Fatalf("still %q", c.LastNick())
	}
}
