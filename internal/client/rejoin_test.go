package client

import (
	"bufio"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/4evrd/evrd/internal/upstream"
)

// ownJoins counts the JOINs of ours for ch among lines: each one makes a
// client open (and mIRC bring forward) the channel's window.
func ownJoins(lines []string, ch string) int {
	n := 0
	for _, l := range lines {
		if f := strings.Fields(l); len(f) >= 3 && strings.HasPrefix(f[0], ":alice_one!") && f[1] == "JOIN" && strings.EqualFold(f[2], ch) {
			n++
		}
	}
	return n
}

// settle reads what a client has been sent up to now: the answer to a
// PING sent last marks the end.
func (ic *ircClient) settle() []string {
	ic.t.Helper()
	settled++
	token := fmt.Sprintf("settled-%d", settled)
	ic.send("PING :" + token)
	return ic.until(token)
}

var settled int

// quiet reads what a client is sent for d while nobody does anything:
// nothing should come but evrd asking whether it's still there.
func (ic *ircClient) quiet(d time.Duration) (sent []string) {
	ic.t.Helper()
	ic.c.SetReadDeadline(time.Now().Add(d))
	for ic.sc.Scan() {
		if l := ic.sc.Text(); !strings.HasPrefix(l, "PING ") {
			sent = append(sent, l)
		} else {
			ic.send("PONG " + strings.TrimPrefix(l, "PING "))
		}
	}
	ic.sc = bufio.NewScanner(ic.c) // a timed-out scanner stays failed
	return sent
}

// A client is sent a channel's JOIN once, when it attaches: not again as
// other clients come and go, as it reconnects, or as it joins a channel
// it's in already (mIRC's own autojoin) — each would pop the channel's
// window up over the one the user is typing in.
func TestChannelsAreJoinedOncePerClient(t *testing.T) {
	r := newRig(t)
	first := r.attached()
	eventually(t, "joined #one", func() bool {
		m, _ := r.hist.Recent(r.network("one").ID, "#one", 10)
		return len(m) > 0 && m[0].Kind == "join"
	})
	if n := ownJoins(first.settle(), "#one"); n != 1 {
		t.Fatalf("attaching: %d JOINs of #one, want 1", n)
	}

	// a second client comes, joins what it's in already, and goes
	second := r.attach("alice/one:alice-password")
	if n := ownJoins(second.settle(), "#one"); n != 1 {
		t.Fatalf("second client: %d JOINs of #one, want 1", n)
	}
	second.send("JOIN #one")
	second.settle()
	second.send("QUIT :bye")
	if seen := first.settle(); ownJoins(seen, "#one") != 0 {
		t.Fatalf("the first client was sent JOINs as the second came and went:\n%s", strings.Join(seen, "\n"))
	}

	// the first one joins what it's in already, as mIRC does on connect
	first.send("JOIN #one")
	if seen := first.settle(); ownJoins(seen, "#one") != 0 {
		t.Fatalf("a JOIN of a channel the client is in came back:\n%s", strings.Join(seen, "\n"))
	}

	// it disconnects and reconnects: one JOIN, and the other client none
	other := r.attach("alice/one:alice-password")
	other.settle()
	first.send("QUIT :reconnecting")
	again := r.attach("alice/one:alice-password")
	if n := ownJoins(again.settle(), "#one"); n != 1 {
		t.Fatalf("reconnecting: %d JOINs of #one, want 1", n)
	}
	again.send("JOIN #one")
	if seen := again.settle(); ownJoins(seen, "#one") != 0 {
		t.Fatalf("after reconnecting, a JOIN of #one came back:\n%s", strings.Join(seen, "\n"))
	}
	if seen := other.settle(); ownJoins(seen, "#one") != 0 {
		t.Fatalf("a client was sent JOINs as another reconnected:\n%s", strings.Join(seen, "\n"))
	}

	// left alone, the clients are sent nothing: no loop going round
	for name, ic := range map[string]*ircClient{"other": other, "again": again} {
		if sent := ic.quiet(3 * time.Second); len(sent) > 0 {
			t.Fatalf("%s was sent lines while nothing happened:\n%s", name, strings.Join(sent, "\n"))
		}
	}

	// the network's link drops and comes back: each channel is left (a
	// KICK, as ZNC does) and joined again, once
	again.send("PRIVMSG *status :disconnect")
	seen := again.until("Disconnected from one")
	again.send("PRIVMSG *status :connect")
	n := r.network("one")
	eventually(t, "one connected", func() bool { return r.b.Status(n.ID) == upstream.StatusConnected })
	seen = append(seen, again.until(" JOIN #one")...)
	seen = append(seen, again.settle()...)
	kicks := 0
	for _, l := range seen {
		if strings.Contains(l, " KICK #one alice_one ") {
			kicks++
		}
	}
	if kicks != 1 || ownJoins(seen, "#one") != 1 {
		t.Fatalf("network reconnect: %d KICKs and %d JOINs of #one, want 1 each:\n%s", kicks, ownJoins(seen, "#one"), strings.Join(seen, "\n"))
	}
	if sent := again.quiet(3 * time.Second); len(sent) > 0 {
		t.Fatalf("sent lines after the reconnect settled:\n%s", strings.Join(sent, "\n"))
	}
}

// With QuietRejoin, a network's link dropping leaves the client's channels
// open: a notice instead of a KICK, and back in, no JOIN of ours — only
// what changed (bob left meanwhile; the op is still there).
func TestQuietRejoinKeepsChannelsOpen(t *testing.T) {
	r := newRig(t)
	u := r.user
	u.QuietRejoin = true
	r.st.UpdateUser(r.ctx, u)
	ic := r.attached()
	ic.until(" JOIN #one")
	ic.send("SIMULATE :bob!b@bobhost JOIN #one")
	ic.until("bob!b@bobhost JOIN #one")
	ic.settle()

	ic.send("PRIVMSG *status :disconnect")
	seen := ic.until(":evrd NOTICE #one :Disconnected from one - this channel stays open")
	if seen = append(seen, ic.settle()...); anyHas(seen, " KICK ") {
		t.Fatalf("disconnect:\n%s", strings.Join(seen, "\n"))
	}
	ic.send("PRIVMSG *status :connect")
	seen = ic.until(":evrd NOTICE #one :Back on one")
	if ownJoins(seen, "#one") != 0 || anyHas(seen, " 353 ") || anyHas(seen, "JOIN #one") {
		t.Fatalf("rejoined with a JOIN or names:\n%s", strings.Join(seen, "\n"))
	}
	if !anyHas(seen, ":bob!b@bobhost PART #one :Left while evrd was away") || anyHas(seen, "op!") || anyHas(seen, " TOPIC ") {
		t.Fatalf("what changed:\n%s", strings.Join(seen, "\n"))
	}
	if sent := ic.quiet(2 * time.Second); len(sent) > 0 {
		t.Fatalf("sent lines after the rejoin:\n%s", strings.Join(sent, "\n"))
	}
	// still in it as far as evrd's concerned: a message goes through
	ic.send("PRIVMSG #one :still here")
	eventually(t, "sent", func() bool { return r.irc.Has("PRIVMSG #one :still here") })
}

// The link dropping on its own (not from *status): the connection evrd
// reconnects is the one the client follows already.
func TestQuietRejoinAfterTheLinkDrops(t *testing.T) {
	r := newRig(t)
	u := r.user
	u.QuietRejoin = true
	r.st.UpdateUser(r.ctx, u)
	ic := r.attached()
	ic.until(" JOIN #one")
	ic.settle()

	r.irc.Drop()
	seen := ic.until(":evrd NOTICE #one :Disconnected from one - this channel stays open")
	seen = append(seen, ic.until(":evrd NOTICE #one :Back on one")...)
	if anyHas(seen, " KICK ") || ownJoins(seen, "#one") != 0 || anyHas(seen, " 353 ") {
		t.Fatalf("the link dropped and came back:\n%s", strings.Join(seen, "\n"))
	}
	if sent := ic.quiet(2 * time.Second); len(sent) > 0 {
		t.Fatalf("sent lines after the rejoin:\n%s", strings.Join(sent, "\n"))
	}
}
