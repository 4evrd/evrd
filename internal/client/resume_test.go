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
