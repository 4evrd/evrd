package upstream

import (
	"encoding/base64"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/4evrd/evrd/internal/store"
)

// capServer registers with the CAP answers a test picks: ls is the LS
// reply's capability list, ack whether REQ is acknowledged, saslResult the
// numeric sent after the SASL payload.
func capServer(ls string, ack bool, saslResult string) func(p *peer, l string) {
	return func(p *peer, l string) {
		switch {
		case strings.HasPrefix(l, "CAP LS"):
			p.send(":srv CAP * LS :%s", ls)
		case strings.HasPrefix(l, "CAP REQ"):
			if ack {
				p.send(":srv CAP * ACK :%s", strings.TrimPrefix(l, "CAP REQ :"))
			} else {
				p.send(":srv CAP * NAK :%s", strings.TrimPrefix(l, "CAP REQ :"))
			}
		case l == "AUTHENTICATE PLAIN":
			p.send("AUTHENTICATE +")
		case strings.HasPrefix(l, "AUTHENTICATE "):
			p.send(":srv %s %s :SASL result", saslResult, p.nick)
		case l == "CAP END":
			p.send(":srv 001 %s :Welcome", p.nick)
		}
	}
}

func TestSASLPlainPayload(t *testing.T) {
	s := startScripted(t, capServer("multi-prefix sasl", true, "903"))
	c := New(s.network(store.Network{Nick: "alice", Username: "aliceident", SASLUser: "acct", Password: "pw"}), quiet, Options{})
	run(t, c)
	eventually(t, "registered", func() bool { return c.Status() == StatusConnected })
	want := "AUTHENTICATE " + base64.StdEncoding.EncodeToString([]byte("\x00acct\x00pw"))
	got := s.last().got()
	if !slices.Contains(got, "CAP REQ :sasl") || !slices.Contains(got, want) {
		t.Fatalf("no SASL PLAIN with the account name: %q", got)
	}
	if index(got, "PRIVMSG NickServ") >= 0 || index(got, "PASS ") >= 0 {
		t.Fatalf("SASL worked, yet PASS or NickServ followed: %q", got)
	}
}

func TestSASLPlainDefaultsToTheIdent(t *testing.T) {
	s := startScripted(t, capServer("sasl", true, "903"))
	c := New(s.network(store.Network{Nick: "alice", Username: "ident", Password: "pw"}), quiet, Options{})
	run(t, c)
	eventually(t, "registered", func() bool { return c.Status() == StatusConnected })
	if want := "AUTHENTICATE " + base64.StdEncoding.EncodeToString([]byte("\x00ident\x00pw")); !s.has(want) {
		t.Fatalf("authcid isn't the ident: %q", s.last().got())
	}
}

func TestSASLFailureFallsBackToNickServ(t *testing.T) {
	for _, numeric := range []string{"904", "902", "905", "906", "908"} {
		t.Run(numeric, func(t *testing.T) {
			s := startScripted(t, capServer("sasl", true, numeric))
			c := New(s.network(store.Network{Nick: "alice", Password: "pw"}), quiet, Options{})
			run(t, c)
			eventually(t, "registered anyway", func() bool { return c.Status() == StatusConnected })
			eventually(t, "identified to NickServ", func() bool { return s.has("PRIVMSG NickServ :IDENTIFY pw") })
			got := s.last().got()
			if index(got, "CAP END") < index(got, "AUTHENTICATE ") {
				t.Fatalf("CAP END before the SASL attempt: %q", got)
			}
		})
	}
}

func TestCAPNAKSendsPASS(t *testing.T) {
	s := startScripted(t, capServer("sasl", false, ""))
	c := New(s.network(store.Network{Nick: "alice", Password: "pw"}), quiet, Options{})
	run(t, c)
	eventually(t, "registered", func() bool { return c.Status() == StatusConnected })
	got := s.last().got()
	if p, e := index(got, "PASS pw"), index(got, "CAP END"); p < 0 || e < p {
		t.Fatalf("want PASS then CAP END after a NAK: %q", got)
	}
}

func TestCAPACKWithoutSASLEndsNegotiation(t *testing.T) {
	// A server that ACKs something other than sasl (it's what we asked
	// for that counts).
	s := startScripted(t, func(p *peer, l string) {
		switch {
		case strings.HasPrefix(l, "CAP LS"):
			p.send(":srv CAP * LS :sasl")
		case strings.HasPrefix(l, "CAP REQ"):
			p.send(":srv CAP * ACK :multi-prefix")
		case l == "CAP END":
			p.send(":srv 001 %s :Welcome", p.nick)
		}
	})
	c := New(s.network(store.Network{Nick: "alice", Password: "pw"}), quiet, Options{})
	run(t, c)
	eventually(t, "registered", func() bool { return c.Status() == StatusConnected })
	if index(s.last().got(), "AUTHENTICATE") >= 0 {
		t.Fatalf("authenticated without sasl acknowledged: %q", s.last().got())
	}
}

func TestPasswordWithoutSASLSupport(t *testing.T) {
	s := startScripted(t, capServer("multi-prefix", true, ""))
	c := New(s.network(store.Network{Nick: "alice", Password: "pw"}), quiet, Options{})
	run(t, c)
	eventually(t, "registered", func() bool { return c.Status() == StatusConnected })
	if !s.has("PASS pw") {
		t.Fatalf("no PASS fallback: %q", s.last().got())
	}
}

func TestServerPasswordWinsOverThePASSFallback(t *testing.T) {
	s := startScripted(t, capServer("", true, ""))
	host, port := s.hostPort()
	c := New(store.Network{Nick: "alice", Password: "account",
		Servers: []store.Server{{Host: host, Port: port, Password: "server"}}}, quiet, Options{})
	run(t, c)
	eventually(t, "registered", func() bool { return c.Status() == StatusConnected })
	got := s.last().got()
	if got[0] != "PASS server" || s.count("PASS ") != 1 {
		t.Fatalf("want only the server's PASS, first: %q", got)
	}
}

func TestNickInUseTriesAltNickThenUnderscores(t *testing.T) {
	taken := map[string]bool{"alice": true, "alt": true, "alt_": true}
	s := startScripted(t, func(p *peer, l string) {
		f := strings.Fields(l)
		switch f[0] {
		case "CAP":
			if f[1] == "LS" {
				p.send(":srv CAP * LS :")
			}
		case "NICK":
			if taken[f[1]] {
				p.send(":srv 433 * %s :Nickname is already in use", f[1])
			} else {
				p.send(":srv 001 %s :Welcome", f[1])
			}
		}
	})
	c := New(s.network(store.Network{Nick: "alice"}), quiet, Options{AltNick: "alt"})
	run(t, c)
	eventually(t, "registered", func() bool { return c.Status() == StatusConnected })
	var nicks []string
	for _, l := range s.last().got() {
		if strings.HasPrefix(l, "NICK ") {
			nicks = append(nicks, strings.TrimPrefix(l, "NICK "))
		}
	}
	if !slices.Equal(nicks, []string{"alice", "alt", "alt_", "alt__"}) {
		t.Fatalf("nicks tried: %q", nicks)
	}
	if c.Nick() != "alt__" {
		t.Fatalf("Nick() = %q", c.Nick())
	}

	// Once registered, a 433 answers a client's NICK and changes nothing.
	s.send(":srv 433 alt__ bob :Nickname is already in use")
	s.send(":srv PING :after")
	eventually(t, "the PING answered", func() bool { return s.has("PONG :after") })
	if c.Nick() != "alt__" || s.count("NICK ") != 4 {
		t.Fatalf("a 433 after registration changed the nick: %q", s.last().got())
	}
}

func TestNetworkAltNickWinsAndSameAltIsSkipped(t *testing.T) {
	c := New(store.Network{Nick: "alice", AltNick: " netalt "}, quiet, Options{AltNick: "useralt"})
	if c.altNick != "netalt" {
		t.Fatalf("altNick %q", c.altNick)
	}
	// An alternate equal to the nick isn't tried: straight to underscores.
	s := startScripted(t, func(p *peer, l string) {
		f := strings.Fields(l)
		switch {
		case f[0] == "CAP" && f[1] == "LS":
			p.send(":srv CAP * LS :")
		case f[0] == "NICK" && f[1] == "alice":
			p.send(":srv 433 * alice :in use")
		case f[0] == "NICK":
			p.send(":srv 001 %s :Welcome", f[1])
		}
	})
	c = New(s.network(store.Network{Nick: "alice"}), quiet, Options{AltNick: "ALICE"})
	run(t, c)
	eventually(t, "registered", func() bool { return c.Status() == StatusConnected })
	if c.Nick() != "alice_" {
		t.Fatalf("nick %q", c.Nick())
	}
}

func TestWelcomeSetsTheNickAndUsesDefaults(t *testing.T) {
	s := startScripted(t, func(p *peer, l string) {
		switch {
		case strings.HasPrefix(l, "CAP LS"):
			p.send(":srv CAP * LS :")
		case strings.HasPrefix(l, "USER "):
			p.send(":srv 001 Alice :Welcome") // the server's spelling
		}
	})
	var events eventRec
	c := New(s.network(store.Network{Name: "Net", Nick: "alice"}), quiet, Options{OnEvent: events.add})
	run(t, c)
	eventually(t, "registered", func() bool { return c.Status() == StatusConnected })
	if c.Nick() != "Alice" {
		t.Fatalf("nick %q", c.Nick())
	}
	if !s.has("USER alice 0 * :alice") {
		t.Fatalf("USER without ident or realname: %q", s.last().got())
	}
	eventually(t, "connected event", func() bool { return events.has("connected", "Connected to Net") })
	h := c.Health()
	if h.ConnectedAt.IsZero() || h.Attempts != 0 || h.Lag != -1 {
		t.Fatalf("health: %+v", h)
	}
}

func TestPendingLinesWaitForRegistration(t *testing.T) {
	release := make(chan struct{})
	s := startScripted(t, func(p *peer, l string) {
		switch {
		case strings.HasPrefix(l, "CAP LS"):
			p.send(":srv CAP * LS :")
		case strings.HasPrefix(l, "USER "):
			go func() {
				<-release
				p.send(":srv 001 %s :Welcome", p.nick)
			}()
		}
	})
	c := New(s.network(store.Network{Nick: "alice", Channels: []string{"#a"}}), quiet, Options{})
	c.SendRaw("MODE alice +i") // before Run: kept
	run(t, c)
	eventually(t, "registering", func() bool { return s.has("USER alice 0 * :alice") })
	c.SendMessage("bob", "hello")
	for i := range maxPending + 10 {
		c.SendRaw("PRIVMSG bob :" + strconv.Itoa(i))
	}
	time.Sleep(50 * time.Millisecond)
	if s.count("PRIVMSG") != 0 || s.count("MODE") != 0 {
		t.Fatalf("lines sent before 001: %q", s.last().got())
	}
	close(release)
	eventually(t, "pending lines flushed", func() bool { return s.count("PRIVMSG bob") == maxPending-1 }) // MODE + hello + 98 of the rest: capped at maxPending
	got := s.last().got()
	if j, m := index(got, "JOIN #a"), index(got, "MODE alice +i"); j < 0 || m < j {
		t.Fatalf("saved channels are joined before held lines go out: %q", got)
	}
	if index(got, "PRIVMSG bob :hello") < 0 {
		t.Fatalf("the held message: %q", got)
	}
	time.Sleep(50 * time.Millisecond)
	if n := s.count("PRIVMSG bob"); n != maxPending-1 {
		t.Fatalf("%d lines sent; past the cap they're dropped", n)
	}
}

// eventRec records events.
type eventRec struct {
	mu sync.Mutex
	e  []Event
}

func (r *eventRec) add(e Event) { r.mu.Lock(); r.e = append(r.e, e); r.mu.Unlock() }
func (r *eventRec) has(kind, title string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, e := range r.e {
		if e.Kind == kind && strings.Contains(e.Title, title) && !e.At.IsZero() {
			return true
		}
	}
	return false
}
func (r *eventRec) count(kind string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, e := range r.e {
		if e.Kind == kind {
			n++
		}
	}
	return n
}
