package client

// A phone on a bad connection (issue #92): connections that freeze, drop
// and come back, many times over. Timers are scaled down: evrd's 90 s of
// quiet and 60 s ping timeout become a fraction of a second.

import (
	"bufio"
	"fmt"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/4evrd/evrd/internal/bouncer"
	"github.com/4evrd/evrd/internal/irc"
)

// shaper is a TCP proxy in front of evrd that can freeze a connection:
// nothing passes either way, and nobody is told — a phone out of signal.
type shaper struct {
	ln     net.Listener
	to     string
	frozen atomic.Bool
}

func newShaper(t *testing.T, to string) *shaper {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &shaper{ln: ln, to: to}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			up, err := net.Dial("tcp", to)
			if err != nil {
				c.Close()
				continue
			}
			go s.pipe(c, up)
			go s.pipe(up, c)
		}
	}()
	return s
}

// pipe copies from a to b while the shaper isn't frozen; frozen, it holds
// what it read (a link that went silent).
func (s *shaper) pipe(a, b net.Conn) {
	defer a.Close()
	defer b.Close()
	buf := make([]byte, 4096)
	for {
		n, err := a.Read(buf)
		for s.frozen.Load() {
			time.Sleep(10 * time.Millisecond)
		}
		if n > 0 {
			if _, werr := b.Write(buf[:n]); werr != nil {
				return
			}
		}
		if err != nil {
			return
		}
	}
}

// dial connects a client through the shaper.
func (s *shaper) dial(t *testing.T, pass string) *ircClient {
	t.Helper()
	c, err := net.Dial("tcp", s.ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	ic := &ircClient{t: t, c: c, sc: bufio.NewScanner(c)}
	ic.send("PASS " + pass)
	ic.send("NICK alice")
	ic.send("USER alice 0 * :Alice")
	return ic
}

func TestAFrozenLinkLeavesNoGhost(t *testing.T) {
	r := newRig(t)
	r.srv.pingQuiet = 200 * time.Millisecond
	r.srv.SetClientPingTimeout(300 * time.Millisecond)
	sh := newShaper(t, r.addr)
	_, pw, _ := r.acc.NewClientPassword(r.user.ID, "phone")
	ic := sh.dial(t, "alice/one:"+pw)
	ic.expect(" 376 ")
	eventually(t, "attached", func() bool { return r.b.Clients(r.user.ID) == 1 })

	sh.frozen.Store(true) // out of signal: no FIN, no RST, nothing
	eventually(t, "the ghost is gone", func() bool { return r.b.Clients(r.user.ID) == 0 })
	if got := strings.Join(r.reasons(), " "); got != bouncer.DropPingTimeout {
		t.Fatalf("recorded: %q", got)
	}
	sh.frozen.Store(false)
}

func TestTwentyDropsLoseNothingAndRepeatNothing(t *testing.T) {
	r := newRig(t)
	u := r.user
	u.AutoAway, u.AutoAwayDelay = true, new(5) // longer than any gap below
	r.st.UpdateUser(r.ctx, u)
	_, pw, _ := r.acc.NewClientPassword(r.user.ID, "phone")
	phone := "alice/one:" + pw

	setup := r.attached()
	setup.status("autoclear #one on", "#one auto-clears")
	setup.status("setbuffer #one 50", "#one plays back 50 lines.")
	setup.send("QUIT :bye")
	setup.closed()

	var mu sync.Mutex
	got := map[string]int{}
	connect := func() *ircClient {
		t.Helper()
		ic := r.attach(phone)
		ic.expect(" 004 ")
		ic.send("PRIVMSG *status :uptime")
		for _, l := range ic.until("evrd has been running") {
			m := irc.Parse(l)
			// "[12:34:56] missed 7": playback without server-time is stamped
			if m.Command == "PRIVMSG" && len(m.Params) == 2 && strings.Contains(m.Params[1], "missed ") {
				mu.Lock()
				got[m.Params[1][strings.Index(m.Params[1], "missed "):]]++
				mu.Unlock()
			}
		}
		return ic
	}
	sent := 0
	ic := connect()
	for cycle := 0; cycle < 20; cycle++ {
		if cycle%2 == 0 {
			ic.c.(*net.TCPConn).SetLinger(0) // dropped: reset
		}
		n := len(r.reasons())
		ic.c.Close() // or hung up
		eventually(t, "drop recorded", func() bool { return len(r.reasons()) > n })
		for i := 0; i < 3; i++ { // the network, while the phone is away
			sent++
			r.irc.Send(fmt.Sprintf(":bob!u@h PRIVMSG #one :missed %d", sent))
		}
		r.said("one", "#one", fmt.Sprintf("missed %d", sent))
		ic = connect()
	}

	var lost, twice []string
	for i := 1; i <= sent; i++ {
		switch got[fmt.Sprintf("missed %d", i)] {
		case 0:
			lost = append(lost, fmt.Sprint(i))
		case 1:
		default:
			twice = append(twice, fmt.Sprint(i))
		}
	}
	if len(lost) > 0 || len(twice) > 0 {
		t.Fatalf("over 20 drops: lost %v, got twice %v", lost, twice)
	}
	// the phone was the only client, and every drop was shorter than the
	// auto-away delay: the network never heard AWAY
	if r.irc.Has("AWAY") {
		t.Fatalf("away flapped: %v", r.irc.Lines())
	}
}

func TestALineSentAsTheLinkDiesComesBackWithItsMsgid(t *testing.T) {
	r := newRig(t)
	_, pw, _ := r.acc.NewClientPassword(r.user.ID, "phone")
	phone := "alice/one:" + pw
	ic := r.attached()
	ic.status("autoclear #one on", "#one auto-clears")

	p := r.attach(phone, "message-tags", "server-time", "echo-message")
	p.expect(" 376 ")
	p.send("PRIVMSG #one :sent as the link died")
	echo := irc.Parse(p.expect("PRIVMSG #one :sent as the link died"))
	p.c.(*net.TCPConn).SetLinger(0)
	p.c.Close() // gone before the client knew whether it went through

	time.Sleep(200 * time.Millisecond)
	back := r.attach(phone, "message-tags", "server-time")
	back.expect(" 004 ")
	again := irc.Parse(back.expect("PRIVMSG #one :sent as the link died"))
	if again.Tags["msgid"] == "" || again.Tags["msgid"] != echo.Tags["msgid"] {
		t.Fatalf("replayed with msgid %q, echoed with %q", again.Tags["msgid"], echo.Tags["msgid"])
	}
}
