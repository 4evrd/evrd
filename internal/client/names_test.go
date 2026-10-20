package client

import (
	"testing"
)

func TestNoImplicitNames(t *testing.T) {
	r := newRig(t)
	ic := r.attach("alice/one:alice-password", "no-implicit-names")
	// the NAMES evrd asks for when attaching aren't for it
	ic.send("JOIN #new")
	ic.expect("JOIN #new")
	ic.send("PRIVMSG *status :version")
	if seen := ic.until("PRIVMSG alice_one :evrd "); anyHas(seen, " 353 ") || anyHas(seen, " 366 ") {
		t.Fatalf("implicit NAMES: %q", seen)
	}
	// the ones it asks for are
	ic.send("NAMES #new")
	ic.send("NAMES #one")
	ic.expect(" 353 alice_one = #new ")
	ic.expect(" 366 alice_one #new ")
	ic.expect(" 366 alice_one #one ")
	// and only those
	ic.send("SIMULATE :fake 353 alice_one = #new :x")
	ic.send("PRIVMSG *status :version")
	if seen := ic.until("PRIVMSG alice_one :evrd "); anyHas(seen, " 353 ") {
		t.Fatalf("NAMES after the asked-for ones: %q", seen)
	}
}

func TestPreAway(t *testing.T) {
	r := newRig(t)
	ic := r.dialRaw(r.addr)
	ic.send("CAP REQ :draft/pre-away")
	ic.send("AWAY :on the train")
	ic.send("PASS alice/one:alice-password")
	ic.send("NICK alice")
	ic.send("USER alice 0 * :alice")
	ic.send("CAP END")
	ic.expect(" 001 ")
	eventually(t, "away set upstream", func() bool { return r.irc.Has("AWAY :on the train") })

	// "*" (present, not reading) is left to auto-away
	r2 := newRig(t)
	ic = r2.dialRaw(r2.addr)
	ic.send("CAP REQ :draft/pre-away")
	ic.send("AWAY *")
	ic.send("PASS alice/one:alice-password")
	ic.send("NICK alice")
	ic.send("USER alice 0 * :alice")
	ic.send("CAP END")
	ic.expect(" 376 ")
	ic.status("version", "evrd ")
	if r2.irc.Has("AWAY") {
		t.Fatalf("AWAY * passed on: %q", r2.irc.Lines())
	}
}
