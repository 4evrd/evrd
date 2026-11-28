package client

import (
	"strings"
	"testing"
	"time"
)

// autoattach brings a detached channel back to the attached client the way
// *status attach does: its JOIN, then what was said there, once.
func TestAutoAttachBringsTheChannelBack(t *testing.T) {
	r := newRig(t)
	ic := r.attached()
	ic.status("loadmod autoattach", "autoattach is on for one")
	ic.status("modset autoattach rules #one *ping*", "Set autoattach's")
	eventually(t, "joined #one", func() bool { c, ok := r.b.ConnFor(r.network("one").ID); return ok && c.Joined("#one") })
	ic.send("DETACH #one")
	ic.expect("PART #one :Detached")
	eventually(t, "detached saved", func() bool { return r.network("one").Channel("#one").Detached })

	ic.send("SIMULATE :bob!u@h PRIVMSG #one :nothing to see")
	ic.send("PRIVMSG *status :version")
	if seen := ic.until("evrd "); anyHas(seen, "JOIN #one") || anyHas(seen, "nothing to see") {
		t.Fatalf("attached on a line that doesn't match: %q", seen)
	}

	ic.send("SIMULATE :bob!u@h PRIVMSG #one :ping alice")
	seen := ic.until("ping alice")
	if !anyHas(seen, "JOIN #one") {
		t.Fatalf("no JOIN: %q", seen)
	}
	eventually(t, "attached saved", func() bool { return !r.network("one").Channel("#one").Detached })
	ic.send("PRIVMSG *status :version")
	if seen := ic.until("evrd "); anyHas(seen, "ping alice") {
		t.Fatalf("the line came twice: %q", seen)
	}
	ic.send("SIMULATE :bob!u@h PRIVMSG #one :and now live")
	ic.expect("and now live")
}

// autoreply answers private messages while nothing is attached.
func TestAutoReplyWhileAway(t *testing.T) {
	r := newRig(t)
	ic := r.attached()
	ic.status("loadmod autoreply", "autoreply is on for one")
	ic.status("modset autoreply reply gone fishing, %sender%", "Set autoreply's")

	// attached: no reply
	ic.send("SIMULATE :bob!u@h PRIVMSG alice_one :here?")
	ic.expect("here?")
	conn, _ := r.b.ConnFor(r.network("one").ID)
	r.left(ic, "one")
	time.Sleep(200 * time.Millisecond)
	if r.irc.Has("NOTICE bob :") {
		t.Fatal("answered with a client attached")
	}

	conn.SendRaw("SIMULATE :bob!u@h PRIVMSG alice_one :now?")
	eventually(t, "the reply", func() bool { return r.irc.Has("NOTICE bob :gone fishing, bob") })
	conn.SendRaw("SIMULATE :bob!u@h PRIVMSG alice_one :hello??")
	r.said("one", "bob", "hello??")
	n := 0
	for _, l := range r.irc.Lines() {
		if strings.Contains(l, "NOTICE bob :") {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("%d replies within the interval", n)
	}
}
