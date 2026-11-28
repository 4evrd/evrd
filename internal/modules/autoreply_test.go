package modules

import (
	"slices"
	"testing"
	"time"
)

func TestAutoReply(t *testing.T) {
	f := newFake()
	m := start(t, f, "autoreply", map[string]string{"reply": "%nick% is out; back soon, %sender%", "interval": "5"}).(*autoreply)
	now := time.Unix(1000, 0)
	m.now = func() time.Time { return now }

	m.Line(line(":bob!b@h PRIVMSG alice :hi"))
	m.Line(line(":bob!b@h PRIVMSG alice :are you there?")) // within the interval: once
	want := []string{"NOTICE bob :alice is out; back soon, bob"}
	if !slices.Equal(f.sent, want) {
		t.Fatalf("sent %q", f.sent)
	}
	if len(f.logs) != 1 || f.logs[0] != "autoreply: told bob you're away" {
		t.Fatalf("logged %q", f.logs)
	}

	// not channels, CTCP, services, servers, ourselves
	f.sent = nil
	for _, l := range []string{
		":carol!c@h PRIVMSG #home :hi alice",
		":carol!c@h PRIVMSG alice :\x01VERSION\x01",
		":carol!c@h PRIVMSG alice :\x01ACTION waves\x01",
		":NickServ!s@services. PRIVMSG alice :This nick is registered",
		":Q!TheQBot@CServe.quakenet.org PRIVMSG alice :hello",
		":irc.example.org PRIVMSG alice :server says",
		":alice!a@h PRIVMSG alice :note to self",
		":carol!c@h NOTICE alice :a notice",
	} {
		m.Line(line(l))
	}
	if len(f.sent) != 0 {
		t.Fatalf("sent %q", f.sent)
	}

	// not while a client is attached
	f.attached = true
	m.Line(line(":dave!d@h PRIVMSG alice :hi"))
	if len(f.sent) != 0 {
		t.Fatalf("answered with a client attached: %q", f.sent)
	}
	f.attached = false

	// once the interval is over, again
	now = now.Add(5 * time.Minute)
	m.Line(line(":BOB!b@h PRIVMSG alice :still away?"))
	if !slices.Equal(f.sent, []string{"NOTICE BOB :alice is out; back soon, BOB"}) {
		t.Fatalf("sent %q", f.sent)
	}
}

func TestAutoReplyDefault(t *testing.T) {
	f := newFake()
	m := start(t, f, "autoreply", nil)
	m.Line(line(":bob!b@h PRIVMSG alice :hi"))
	if !slices.Equal(f.sent, []string{"NOTICE bob :I am currently away, try again later"}) {
		t.Fatalf("sent %q", f.sent)
	}
	f.sent = nil
	m.Line(line(":bob!b@h PRIVMSG alice :hi")) // two minutes by default
	if len(f.sent) != 0 {
		t.Fatalf("sent %q", f.sent)
	}
}
