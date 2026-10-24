package client

import (
	"testing"
)

// whoKnown attaches a first client and waits until evrd can answer WHO
// about its channel.
func (r *rig) whoKnown(network, ch string) {
	r.t.Helper()
	r.known(network, ch)
	n := r.network(network)
	eventually(r.t, ch+" WHO known", func() bool {
		conn, ok := r.b.ConnFor(n.ID)
		if !ok {
			return false
		}
		_, ok, _ = conn.WhoState(ch)
		return ok
	})
}

func TestWhoComesFromEvrdsState(t *testing.T) {
	r := newRigCaps(t, "away-notify account-notify extended-join evrd.test/whox")
	r.whoKnown("one", "#one")
	asked := r.asked("WHO ")

	ic := r.attach("alice/one:alice-password")
	ic.until(":evrd 366 alice_one #one ")

	ic.send("WHO #one")
	ic.expect(":evrd 352 alice_one #one u h fake alice_one H :0 Real alice_one")
	ic.expect(":evrd 315 alice_one #one :End of /WHO list")
	ic.send("WHO #one %tcnfar,42")
	ic.expect(":evrd 354 alice_one 42 #one alice_one H 0 :Real alice_one")
	ic.expect(":evrd 315 alice_one #one :End of /WHO list")
	ic.send("WHO alice_one")
	ic.expect(":evrd 352 alice_one #one u h fake alice_one H :0 Real alice_one")
	ic.expect(":evrd 315 alice_one alice_one :End of /WHO list")
	if r.asked("WHO ") != asked {
		t.Fatalf("the network was asked: %q", r.irc.Lines())
	}

	// away-notify keeps the flags current
	ic.send("SIMULATE :alice_one!u@h AWAY :brb")
	conn, _ := r.b.ConnFor(r.network("one").ID)
	eventually(t, "away noted", func() bool { w, _, _, _ := conn.WhoNick("alice_one"); return w.Away })
	ic.send("WHO #one %nf")
	ic.expect(":evrd 354 alice_one alice_one G")

	// masks, filters and channels evrd isn't in are the network's
	ic.send("WHO *")
	ic.send("WHO #one o")
	ic.send("WHO #elsewhere")
	eventually(t, "passed on", func() bool {
		return r.irc.Has("WHO *") && r.irc.Has("WHO #one o") && r.irc.Has("WHO #elsewhere")
	})
}

func TestWhoGoesToTheNetworkWithoutAwayNotify(t *testing.T) {
	r := newRigCaps(t, "extended-join")
	r.known("one", "#one")
	eventually(t, "evrd's own WHO", func() bool { return r.asked("WHO #one") >= 1 })
	ic := r.attach("alice/one:alice-password")
	ic.until(":evrd 366 alice_one #one ")
	before := r.asked("WHO #one")
	ic.send("WHO #one")
	// the network's answer, passed through
	ic.expect(":fake 352 alice_one #one u h fake alice_one H :0 Real alice_one")
	if r.asked("WHO #one") != before+1 {
		t.Fatalf("not passed on: %q", r.irc.Lines())
	}
}
