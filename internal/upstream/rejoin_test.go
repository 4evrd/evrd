package upstream

import (
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/4evrd/evrd/internal/irc"
	"github.com/4evrd/evrd/internal/store"
)

// regNet is a network whose services are a server of their own: NickServ
// answers IDENTIFY after delay (never if silent), and only then does the
// server let the user into #reg… channels (+R: 477 before). #opers… are
// for opers only (520), and OPER is answered after operDelay. lag refuses
// a #reg… join that many more times after identifying, as a server that
// hasn't caught up with services yet does.
type regNet struct {
	pw, operPW string
	delay      time.Duration
	silent     bool
	operDelay  time.Duration
	lag        int

	mu         sync.Mutex
	identified map[int]bool // by connection
	oper       map[int]bool
	refused    []string // "477 #reg", "520 #opers"
}

func (r *regNet) reply(p *peer, l string) {
	m := irc.Parse(l)
	switch {
	case m.Command == "JOIN" && len(m.Params) > 0:
		for _, ch := range strings.Split(m.Params[0], ",") {
			r.mu.Lock()
			code := ""
			switch {
			case strings.HasPrefix(ch, "#reg") && (!r.identified[p.n] || r.lag > 0):
				code = "477"
				if r.identified[p.n] {
					r.lag--
				}
			case strings.HasPrefix(ch, "#opers") && !r.oper[p.n]:
				code = "520"
			}
			if code != "" {
				r.refused = append(r.refused, code+" "+ch)
			}
			r.mu.Unlock()
			switch code {
			case "477":
				p.send(":srv 477 %s %s :You need to be identified to a registered account to join this channel", p.me(), ch)
			case "520":
				p.send(":srv 520 %s %s :Only IRC operators may join %s (+O is set)", p.me(), ch, ch)
			default:
				p.send(":%s!u@h JOIN %s", p.me(), ch)
			}
		}
	case m.Command == "OPER" && len(m.Params) == 2:
		ok := m.Params[1] == r.operPW
		time.AfterFunc(r.operDelay, func() {
			if !ok {
				p.send(":srv 491 %s :Invalid oper credentials", p.me())
				return
			}
			r.mu.Lock()
			r.oper[p.n] = true
			r.mu.Unlock()
			p.send(":srv 381 %s :You are now an IRC operator", p.me())
		})
	default:
		services{answer: func(p *peer, to, text string) {
			if r.silent || !strings.EqualFold(to, "NickServ") || text != "IDENTIFY "+r.pw {
				return
			}
			time.AfterFunc(r.delay, func() { r.login(p) })
		}}.reply(p, l)
	}
}

// login is services saying yes (as Anope does: a notice, then 900).
func (r *regNet) login(p *peer) {
	r.mu.Lock()
	r.identified[p.n] = true
	r.mu.Unlock()
	p.send(":NickServ!services@services. NOTICE %s :Password accepted - you are now recognized.", p.me())
	p.send(":srv 900 %s %s!u@h %s :You are now logged in as %s", p.me(), p.me(), p.me(), p.me())
}

func (r *regNet) refusals() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.refused...)
}

func startRegNet(t *testing.T, r *regNet) *scripted {
	r.identified, r.oper = map[int]bool{}, map[int]bool{}
	return startScripted(t, r.reply)
}

// joins is how many JOINs naming ch the connection sent.
func joins(p *peer, ch string) int {
	n := 0
	for _, l := range p.got() {
		if m := irc.Parse(l); m.Command == "JOIN" && len(m.Params) > 0 && slices.Contains(strings.Split(m.Params[0], ","), ch) {
			n++
		}
	}
	return n
}

// The joins wait for services by default, on every connection: a +R
// channel lets evrd in after a reconnect too, and clients see it come back.
func TestRegisteredOnlyChannelAfterReconnect(t *testing.T) {
	r := &regNet{pw: "pw-pw", delay: 50 * time.Millisecond}
	s := startRegNet(t, r)
	c, rec := connect(t, s, store.Network{AuthMethod: store.AuthNickServ, Password: "pw-pw", Channels: []string{"#reg"}})
	raw, stop := c.SubscribeRaw()
	defer stop()
	eventually(t, "in #reg", func() bool { return c.Joined("#reg") })
	first := s.last()
	if i, j := index(first.got(), "PRIVMSG NickServ :IDENTIFY pw-pw"), index(first.got(), "JOIN #reg"); i < 0 || j < i {
		t.Fatalf("want IDENTIFY, then JOIN: %q", first.got())
	}
	if !rec.has("evrd", "Joining channels once services confirm the login", false) {
		t.Fatalf("log: %+v", rec.all())
	}

	first.c.Close() // the link drops
	eventually(t, "reconnected", func() bool { return s.conns() == 2 && c.Status() == StatusConnected })
	eventually(t, "back in #reg", func() bool { return c.Joined("#reg") })
	if got := r.refusals(); len(got) != 0 {
		t.Fatalf("refused: %q", got)
	}
	if joins(s.last(), "#reg") != 1 || !slices.Contains(c.Channels(), "#reg") {
		t.Fatalf("sent %q, channels %q", s.last().got(), c.Channels())
	}
	// a client attached all along was taken out of #reg and sees it again
	var left, rejoined bool
	for !rejoined {
		select {
		case l := <-raw:
			left = left || slices.Contains(l.Left, "#reg")
			rejoined = left && strings.Contains(l.Text, " JOIN #reg")
		case <-time.After(2 * time.Second):
			t.Fatalf("left %v, no JOIN for clients", left)
		}
	}
}

// Joins sent before the login went through (here, opted out of waiting)
// and refused with 477 are sent once more when services say yes, with
// their keys; the channels stay saved meanwhile.
func TestRefusedBeforeLoginIsJoinedAfter(t *testing.T) {
	r := &regNet{pw: "pw-pw", delay: 150 * time.Millisecond}
	s := startRegNet(t, r)
	c, rec := connect(t, s, store.Network{AuthMethod: store.AuthNickServ, Password: "pw-pw", JoinBeforeAuth: true,
		Channels:        []string{"#reg", "#regkeyed", "#open"},
		ChannelSettings: map[string]store.ChannelSettings{"#regkeyed": {Key: "sekrit"}}})
	eventually(t, "refused", func() bool { return len(r.refusals()) == 2 })
	if !slices.Contains(c.Channels(), "#reg") || !slices.Contains(c.Channels(), "#regkeyed") || c.Joined("#reg") {
		t.Fatalf("channels %q", c.Channels())
	}
	eventually(t, "in both", func() bool { return c.Joined("#reg") && c.Joined("#regkeyed") })
	p := s.last()
	if !s.has("JOIN #regkeyed sekrit") || joins(p, "#reg") != 2 || joins(p, "#regkeyed") != 2 || joins(p, "#open") != 1 {
		t.Fatalf("sent %q", p.got())
	}
	if !rec.has("evrd", "again: the server refused them before the login or OPER went through", false) {
		t.Fatalf("log: %+v", rec.all())
	}
	time.Sleep(100 * time.Millisecond)
	if joins(p, "#reg") != 2 {
		t.Fatalf("sent %q", p.got())
	}
}

// A 477 after services said yes (the server hasn't caught up) is tried
// again shortly — once: a channel that keeps refusing isn't tried forever.
func TestRefusedAfterLoginIsTriedOnceMore(t *testing.T) {
	shorten(t)
	r := &regNet{pw: "pw-pw", lag: 1}
	s := startRegNet(t, r)
	c, _ := connect(t, s, store.Network{AuthMethod: store.AuthNickServ, Password: "pw-pw", Channels: []string{"#reg"}})
	eventually(t, "in #reg", func() bool { return c.Joined("#reg") })
	if joins(s.last(), "#reg") != 2 {
		t.Fatalf("sent %q", s.last().got())
	}

	r.mu.Lock()
	r.lag = 5 // refuses for good
	r.mu.Unlock()
	s.last().c.Close()
	eventually(t, "reconnected", func() bool { return s.conns() == 2 && c.Status() == StatusConnected })
	eventually(t, "tried twice", func() bool { return joins(s.last(), "#reg") == 2 })
	time.Sleep(200 * time.Millisecond)
	if joins(s.last(), "#reg") != 2 || c.Joined("#reg") || !slices.Contains(c.Channels(), "#reg") {
		t.Fatalf("sent %q, channels %q", s.last().got(), c.Channels())
	}
}

// Services that never answer: the joins go out after the wait, once. A
// 477 then is kept for a late yes, and with none never tried again.
func TestServicesSilentJoinsOnceAfterTheWait(t *testing.T) {
	shorten(t)
	r := &regNet{pw: "pw-pw", silent: true}
	s := startRegNet(t, r)
	c, rec := connect(t, s, store.Network{AuthMethod: store.AuthNickServ, Password: "pw-pw", AuthTimeout: 5, Channels: []string{"#reg", "#open"}})
	eventually(t, "in #open", func() bool { return c.Joined("#open") })
	if c.Health().Auth != AuthUnconfirmed || !rec.has("evrd", "Services didn't answer the login within", true) {
		t.Fatalf("auth %q, log %+v", c.Health().Auth, rec.all())
	}
	time.Sleep(300 * time.Millisecond)
	p := s.last()
	if joins(p, "#reg") != 1 || joins(p, "#open") != 1 || len(r.refusals()) != 1 {
		t.Fatalf("sent %q", p.got())
	}
	// services answer after all: #reg once more, and only once
	r.login(p)
	eventually(t, "in #reg", func() bool { return c.Joined("#reg") })
	if c.Health().Auth != AuthIdentified {
		t.Fatalf("auth %q", c.Health().Auth)
	}
	r.login(p)
	time.Sleep(100 * time.Millisecond)
	if joins(p, "#reg") != 2 || joins(p, "#open") != 1 {
		t.Fatalf("sent %q", p.got())
	}
}

func operNet(pw string) store.Network {
	return store.Network{Channels: []string{"#opers", "#open"},
		Modules: map[string]store.ModuleConfig{"perform": {Enabled: true, Settings: map[string]string{"commands": "OPER me " + pw}}}}
}

// An OPER in perform goes out before the joins, which wait for its
// answer: oper-only channels let evrd in, on every connection.
func TestPerformOperBeforeTheJoins(t *testing.T) {
	r := &regNet{operPW: "op-secret", operDelay: 50 * time.Millisecond}
	s := startRegNet(t, r)
	c, rec := connect(t, s, operNet("op-secret"))
	eventually(t, "in #opers", func() bool { return c.Joined("#opers") && c.Joined("#open") })
	if i, j := index(s.last().got(), "OPER me op-secret"), index(s.last().got(), "JOIN #opers,#open"); i < 0 || j < i {
		t.Fatalf("want OPER, then JOIN: %q", s.last().got())
	}
	s.last().c.Close()
	eventually(t, "reconnected", func() bool { return s.conns() == 2 && c.Status() == StatusConnected })
	eventually(t, "back in #opers", func() bool { return c.Joined("#opers") })
	if got := r.refusals(); len(got) != 0 {
		t.Fatalf("refused: %q", got)
	}
	if !rec.has("evrd", "Joining channels once the server answers OPER", false) {
		t.Fatalf("log: %+v", rec.all())
	}
	noPassword(t, rec, "op-secret")
}

// OPER answered too late: the joins go out after operWait, and the
// oper-only channel is joined again once 381 comes.
func TestOperOnlyRefusalIsJoinedAfter381(t *testing.T) {
	shorten(t)
	r := &regNet{operPW: "op-secret", operDelay: 300 * time.Millisecond}
	s := startRegNet(t, r)
	c, rec := connect(t, s, operNet("op-secret"))
	eventually(t, "refused", func() bool { return len(r.refusals()) == 1 })
	if !c.Joined("#open") || c.Joined("#opers") {
		t.Fatalf("channels %q", c.Channels())
	}
	eventually(t, "in #opers", func() bool { return c.Joined("#opers") })
	time.Sleep(100 * time.Millisecond)
	if joins(s.last(), "#opers") != 2 || joins(s.last(), "#open") != 1 {
		t.Fatalf("sent %q", s.last().got())
	}
	noPassword(t, rec, "op-secret")
}

// OPER refused: the joins go out at once, and nothing is tried again.
func TestOperRefusedReleasesTheJoins(t *testing.T) {
	r := &regNet{operPW: "op-secret"}
	s := startRegNet(t, r)
	c, _ := connect(t, s, operNet("wrong"))
	eventually(t, "in #open", func() bool { return c.Joined("#open") }) // well before operWait
	time.Sleep(100 * time.Millisecond)
	if joins(s.last(), "#opers") != 1 || c.Joined("#opers") || !slices.Contains(c.Channels(), "#opers") {
		t.Fatalf("sent %q", s.last().got())
	}
}

// Logged in and opered: the joins wait for both answers.
func TestJoinsWaitForLoginAndOper(t *testing.T) {
	r := &regNet{pw: "pw-pw", delay: 150 * time.Millisecond, operPW: "op-secret", operDelay: 20 * time.Millisecond}
	s := startRegNet(t, r)
	n := operNet("op-secret")
	n.AuthMethod, n.Password, n.Channels = store.AuthNickServ, "pw-pw", []string{"#reg", "#opers"}
	c, _ := connect(t, s, n)
	eventually(t, "in both", func() bool { return c.Joined("#reg") && c.Joined("#opers") })
	if got := r.refusals(); len(got) != 0 {
		t.Fatalf("refused: %q", got)
	}
}
