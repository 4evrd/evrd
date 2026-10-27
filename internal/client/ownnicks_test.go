package client

import (
	"strings"
	"testing"
)

// Asking about *status (mIRC's whois on query, a notify list) is answered
// by evrd, never passed to the network, whose "No such nick/channel" would
// land in the *status query.
func TestOwnNicksStayOffTheNetwork(t *testing.T) {
	r := newRig(t)
	ic := r.attached()

	ic.send("WHOIS *status")
	ic.expect(" 311 alice_one *status status evrd ")
	ic.expect(" 318 alice_one *status ")
	ic.send("WHOIS *nosuch")
	ic.expect(" 401 alice_one *nosuch ")
	ic.send("WHO *status")
	ic.expect(" 315 alice_one *status ")
	ic.send("MODE *status")
	ic.expect(" 502 alice_one ")
	ic.send("ISON *status *nosuch bob")
	ic.expect(" 303 alice_one :*status")
	ic.send("USERHOST *status")
	ic.expect(" 302 alice_one :*status=+status@evrd")
	ic.send("PRIVMSG *controlpanel :help")
	ic.expect(" 401 alice_one *controlpanel ")
	ic.send("NOTICE *buffextras :hi")
	ic.send("WHOIS alice_one")
	eventually(t, "the network asked about real nicks", func() bool { return r.irc.Has("WHOIS alice_one") && r.irc.Has("ISON bob") })
	for _, l := range r.irc.Lines() {
		if strings.Contains(l, "*") && !strings.HasPrefix(l, "CAP") && !strings.HasPrefix(l, "USER") {
			t.Errorf("sent to the network: %q", l)
		}
	}
}

// mIRC sends typing notifications in the *status query too: they stay here,
// or the network's "No such nick" fills the window as you type (#137).
func TestTypingToStatusStaysOffTheNetwork(t *testing.T) {
	r := newRigCaps(t, "message-tags")
	ic := r.attach("alice/one:alice-password", "message-tags")
	ic.expect("JOIN #one")
	eventually(t, "the network takes tags", func() bool {
		c, ok := r.b.ConnFor(r.network("one").ID)
		return ok && c.HasCap("message-tags")
	})
	ic.send("@+typing=active TAGMSG *status")
	ic.send("@+typing=active TAGMSG #one")
	eventually(t, "typing in #one reached the network", func() bool { return r.irc.Has("TAGMSG #one") })
	if r.irc.Has("TAGMSG *status") {
		t.Fatal("typing to *status went to the network")
	}
}
