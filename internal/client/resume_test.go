package client

import (
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/4evrd/evrd/internal/bouncer"
)

// dropped resets a client's connection (no QUIT) and waits for evrd to
// record it.
func (r *rig) dropped(ic *ircClient) {
	r.t.Helper()
	n := len(r.reasons())
	ic.c.(*net.TCPConn).SetLinger(0)
	ic.c.Close()
	eventually(r.t, "dropped", func() bool { return len(r.reasons()) > n })
}

func TestADeviceBackSoonGetsASlimWelcome(t *testing.T) {
	r := newRig(t)
	_, pw, _ := r.acc.NewClientPassword(r.user.ID, "phone")
	phone := "alice/one:" + pw

	first := r.attach(phone)
	first.expect(" 376 ")
	first.expect("\x01VERSION\x01")
	first.send("NOTICE *status :\x01VERSION Goguma 0.7\x01")
	eventually(t, "software known", func() bool {
		cs := r.b.ClientsOf(r.user.ID)
		return len(cs) == 1 && cs[0].Software != ""
	})
	r.dropped(first)

	// back within minutes: no MOTD, no VERSION question, the software kept
	back := r.attach(phone)
	back.send("PRIVMSG *status :uptime")
	seen := back.until("evrd has been running")
	if !anyHas(seen, " 422 ") || anyHas(seen, " 376 ") || anyHas(seen, "VERSION") || !anyHas(seen, " JOIN #one") {
		t.Fatalf("slim welcome:\n%s", strings.Join(seen, "\n"))
	}
	if cs := r.b.ClientsOf(r.user.ID); len(cs) != 1 || !strings.HasPrefix(cs[0].Software, "Goguma") {
		t.Fatalf("clients: %+v", cs)
	}

	// one that quit and came back starts over
	back.send("QUIT :bye")
	back.closed()
	eventually(t, "quit recorded", func() bool { return strings.HasSuffix(strings.Join(r.reasons(), " "), bouncer.DropQuit) })
	again := r.attach(phone)
	again.expect(" 376 ")

	// and a client without a client password can't be told apart
	r.dropped(again)
	plain := r.attach("alice/one:alice-password")
	plain.expect(" 376 ")
}

func TestPlaybackSaysWhatItLeftOut(t *testing.T) {
	r := newRig(t)
	_, pw, _ := r.acc.NewClientPassword(r.user.ID, "phone")
	phone := "alice/one:" + pw
	ic := r.attached()
	ic.status("autoclear #one on", "#one auto-clears")
	ic.status("setbuffer #one 2", "#one plays back 2 lines.")

	p, _ := r.replayedAs(phone)
	p.send("QUIT :bye")
	p.closed()
	time.Sleep(20 * time.Millisecond)
	for i := 1; i <= 5; i++ {
		ic.send(fmt.Sprintf("SIMULATE :bob!u@h PRIVMSG #one :missed %d", i))
	}
	r.said("one", "#one", "missed 5")

	_, seen := r.replayedAs(phone)
	if anyHas(seen, "missed 3") || !anyHas(seen, "missed 5") ||
		!anyHas(seen, "3 more lines in #one from while you were away: /msg *status playback #one 5") {
		t.Fatalf("replay:\n%s", strings.Join(seen, "\n"))
	}
}

func TestTwoClientsSharingAPasswordBothStay(t *testing.T) {
	r := newRig(t)
	r.srv.heardJustNow = time.Nanosecond // always ask
	_, pw, _ := r.acc.NewClientPassword(r.user.ID, "pc")
	pc := "alice/one:" + pw

	first := r.attach(pc)
	first.expect(" 376 ")
	first.expect("\x01VERSION\x01")
	first.send("NOTICE *status :\x01VERSION mIRC v7.77\x01")
	eventually(t, "software known", func() bool {
		cs := r.b.ClientsOf(r.user.ID)
		return len(cs) == 1 && cs[0].Software != ""
	})
	go func() {
		first.expect("PING :evrd") // asked whether it's still there
		first.send("PONG :evrd")
	}()

	// the second client with the same password: told, and both stay
	second := r.attach(pc)
	seen := second.until("so evrd can tell them apart")
	if !anyHas(seen, " 376 ") || !anyHas(seen, `mIRC from 127.0.0.1 is also signed in with the client password "pc"`) {
		t.Fatalf("second:\n%s", strings.Join(seen, "\n"))
	}
	if cs := r.b.ClientsOf(r.user.ID); len(cs) != 2 {
		t.Fatalf("clients: %+v", cs)
	}
	if reasons := r.reasons(); len(reasons) != 0 {
		t.Fatalf("dropped: %v", reasons)
	}

	// a third that comes while the second has gone quiet takes over only
	// that one: it's the second's ghost
	go func() {
		first.expect("PING :evrd")
		first.send("PONG :evrd")
	}()
	third := r.attach(pc)
	if !third.tryExpect(" 422 ", 8*time.Second) { // the slim welcome of a device back, once the ghost didn't answer
		t.Fatal("the third client wasn't welcomed")
	}
	if !second.closed() {
		t.Fatal("the ghost is still attached")
	}
	eventually(t, "replaced", func() bool { return strings.Join(r.reasons(), " ") == bouncer.DropReplaced })
	if cs := r.b.ClientsOf(r.user.ID); len(cs) != 2 {
		t.Fatalf("clients: %+v", cs)
	}
}
