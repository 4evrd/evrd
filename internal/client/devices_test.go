package client

import (
	"net"
	"strings"
	"testing"
	"time"
)

// replayedAs is replayed for a client signing in with pass.
func (r *rig) replayedAs(pass string) (*ircClient, []string) {
	r.t.Helper()
	ic := r.attach(pass)
	ic.expect(" 004 ") // then the MOTD, or 422 when it's back within minutes
	ic.send("PRIVMSG *status :uptime")
	return ic, ic.until("evrd has been running")
}

func TestEachDeviceGetsWhatItMissed(t *testing.T) {
	r := newRig(t)
	_, phonePw, _ := r.acc.NewClientPassword(r.user.ID, "phone")
	_, laptopPw, _ := r.acc.NewClientPassword(r.user.ID, "laptop")
	phone := "alice/one:" + phonePw
	laptop := "alice/one:" + laptopPw

	ic := r.attached()
	ic.status("autoclear #one on", "#one auto-clears")
	ic.send("QUIT :bye")
	ic.closed()

	// the phone comes and goes: its first visit gets the backlog
	p, _ := r.replayedAs(phone)
	p.send("QUIT :bye")
	p.closed()
	time.Sleep(20 * time.Millisecond)

	// the laptop is on while lines arrive the phone wasn't there for
	lap, _ := r.replayedAs(laptop)
	lap.send("SIMULATE :bob!u@h PRIVMSG #one :while the phone was away")
	lap.expect("while the phone was away")

	// the phone gets them, though the laptop saw them and is still on
	p, seen := r.replayedAs(phone)
	if !anyHas(seen, "while the phone was away") {
		t.Fatalf("the phone missed what it wasn't there for:\n%s", strings.Join(seen, "\n"))
	}
	p.send("QUIT :bye")
	p.closed()
	time.Sleep(20 * time.Millisecond)

	// and not again: it saw them
	p, seen = r.replayedAs(phone)
	if anyHas(seen, "while the phone was away") {
		t.Fatalf("the phone got them twice:\n%s", strings.Join(seen, "\n"))
	}

	// the laptop, back after it left, gets only what's new to it
	lap.send("QUIT :bye")
	lap.closed()
	time.Sleep(20 * time.Millisecond)
	p.send("SIMULATE :bob!u@h PRIVMSG #one :after the laptop left")
	p.expect("after the laptop left")
	_, seen = r.replayedAs(laptop)
	if anyHas(seen, "while the phone was away") || !anyHas(seen, "after the laptop left") {
		t.Fatalf("the laptop's replay:\n%s", strings.Join(seen, "\n"))
	}
}

func TestADroppedDeviceGetsAgainWhatItMayHaveMissed(t *testing.T) {
	r := newRig(t)
	_, phonePw, _ := r.acc.NewClientPassword(r.user.ID, "phone")
	phone := "alice/one:" + phonePw
	ic := r.attached()
	ic.status("autoclear #one on", "#one auto-clears")

	p, _ := r.replayedAs(phone)
	p.send("PING :alive") // heard from now
	p.expect("PONG")
	time.Sleep(20 * time.Millisecond)
	ic.send("SIMULATE :bob!u@h PRIVMSG #one :sent as the phone lost signal")
	p.expect("sent as the phone lost signal")
	p.c.(*net.TCPConn).SetLinger(0) // a dropped connection (reset), not QUIT or
	p.c.Close()                     // hanging up: evrd can't tell what arrived

	time.Sleep(200 * time.Millisecond)
	_, seen := r.replayedAs(phone)
	if !anyHas(seen, "sent as the phone lost signal") {
		t.Fatalf("a dropped device lost a line:\n%s", strings.Join(seen, "\n"))
	}
}
