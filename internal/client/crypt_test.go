package client

import (
	"strings"
	"testing"
)

// *crypt from an IRC client: it answers as *crypt, keys set there are
// saved to the network, and the user's messages then go out encrypted
// while the client and history see them as written.
func TestCryptFromAClient(t *testing.T) {
	r := newRig(t)
	ic := r.attached()
	ic.send("PRIVMSG *crypt :setkey #one testkey")
	ic.expect("No such nick") // not switched on yet: no *crypt
	ic.send("PRIVMSG *status :loadmod crypt")
	ic.expect("crypt is on for one.")
	ic.send("PRIVMSG *crypt :setkey #one testkey")
	if l := ic.expect("Key for #one set"); !strings.HasPrefix(l, ":*crypt!crypt@") || !strings.Contains(l, " NOTICE alice_one :") {
		t.Fatalf("answered %q", l)
	}
	eventually(t, "the key saved", func() bool { return r.network("one").Modules["crypt"].Settings["keys"] == "#one testkey" })
	ic.send("PRIVMSG #one :Hello, world!")
	eventually(t, "sent encrypted", func() bool { return r.irc.Has("PRIVMSG #one :+OK 9vjCO08wIY41GHMAo.pCIjl/") })
	r.said("one", "#one", "Hello, world!")
	ic.send("PRIVMSG *crypt :listkeys")
	ic.expect("#one: ECB testkey")
	if r.irc.Has("*crypt") {
		t.Fatal("*crypt's commands went to the network")
	}
}
