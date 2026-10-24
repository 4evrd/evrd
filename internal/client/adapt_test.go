package client

import (
	"strings"
	"testing"

	"github.com/4evrd/evrd/internal/irc"
)

// everyLinkCap is a network offering every capability evrd passes through.
const everyLinkCap = "message-tags batch multi-prefix userhost-in-names extended-join account-notify account-tag " +
	"away-notify chghost invite-notify setname draft/channel-rename draft/message-redaction extended-monitor standard-replies labeled-response draft/multiline=max-bytes=40,max-lines=3"

// full attaches a client that takes every capability: evrd's at
// registration, the network's once offered (CAP NEW).
func (r *rig) full() *ircClient {
	r.t.Helper()
	ic := r.attached("batch", "message-tags", "server-time", "standard-replies")
	ic.expect("CAP alice_one NEW :")
	ic.send("CAP REQ :" + strings.Join(linkCaps, " "))
	ic.expect("CAP alice_one ACK :multi-prefix")
	return ic
}

func TestLinkCapsComeAndGo(t *testing.T) {
	r := newRigCaps(t, everyLinkCap)
	// at registration the network isn't known: only evrd's own
	ic := r.attach("alice/one:alice-password", "chghost")
	ic.expect("NAK :chghost")
	ic.expect(" 001 ")
	r.linked()
	// then CAP NEW (CAP 302 implies cap-notify)
	l := ic.expect("CAP alice_one NEW :")
	for _, c := range linkCaps {
		if !strings.Contains(l, c) {
			t.Fatalf("%s not offered: %q", c, l)
		}
	}
	ic.send("CAP REQ :chghost away-notify")
	ic.expect("ACK :chghost away-notify")
	// the network drops one: so does the client
	ic.send("SIMULATE :fake CAP alice_one DEL :chghost")
	ic.expect("CAP alice_one DEL :chghost")
	ic.send("CAP LIST")
	if l := ic.expect(" LIST :"); strings.Contains(l, "chghost") || !strings.Contains(l, "away-notify") {
		t.Fatalf("after DEL: %q", l)
	}
	ic.send("CAP REQ :chghost")
	ic.expect("NAK :chghost")
	// and offers it again
	ic.send("SIMULATE :fake CAP alice_one NEW :chghost")
	ic.expect("CAP alice_one NEW :chghost")

	// a client without cap-notify isn't told
	quiet := r.attach("alice/one:alice-password")
	quiet.send("CAP LS")
	if l := quiet.expect(" LS :"); !strings.Contains(l, "chghost") || strings.Contains(l, "LS * :") {
		t.Fatalf("LS after attach: %q", l)
	}
	quiet.send("PRIVMSG *status :version")
	if seen := quiet.until("PRIVMSG alice_one :evrd "); anyHas(seen, " NEW ") {
		t.Fatalf("CAP NEW without cap-notify: %q", seen)
	}
}

func TestPassThroughAndTranslation(t *testing.T) {
	r := newRigCaps(t, everyLinkCap)
	full := r.full()
	// the attach burst answers from evrd's state: wait until it has the topic
	eventually(t, "topic known", func() bool {
		c, ok := r.b.ConnFor(r.network("one").ID)
		return ok && c.Topic("#one") != ""
	})
	plain := r.attached()
	// its channel's names and topic, asked for after 376, before reading on
	plain.until(" 332 ")
	sim := func(l string) { full.send("SIMULATE " + l) }
	both := func(l, wantFull, wantPlain string) {
		t.Helper()
		sim(l)
		sim(":fake NOTICE alice_one :mark") // what both see, to read up to
		f, p := full.until(":mark"), plain.until(":mark")
		if wantFull != "" && !anyHas(f, wantFull) {
			t.Errorf("%s: full client got %q", l, f)
		}
		if wantPlain != "" && !anyHas(p, wantPlain) {
			t.Errorf("%s: plain client got %q", l, p)
		}
		if wantPlain == "" && len(p) != 1 {
			t.Errorf("%s: plain client got %q", l, p)
		}
	}
	both(":bob!u@h JOIN #one bobacct :Bob Real", ":bob!u@h JOIN #one bobacct :Bob Real", ":bob!u@h JOIN #one")
	both(":bob!u@h ACCOUNT bobacct", ":bob!u@h ACCOUNT bobacct", "")
	both(":bob!u@h AWAY :gone", ":bob!u@h AWAY :gone", "")
	both(":bob!u@h SETNAME :New Name", ":bob!u@h SETNAME :New Name", "")
	both(":carol!u@h INVITE dave #one", ":carol!u@h INVITE dave #one", "")
	both(":carol!u@h INVITE alice_one #two", "", ":carol!u@h INVITE alice_one #two")
	both("@account=bob;+x=1 :bob!u@h PRIVMSG #one :hi", ";account=bob", ":bob!u@h PRIVMSG #one :hi")
	both(":fake 353 alice_one = #one :@+bob!u@h carol!c@h", ":fake 353 alice_one = #one :@+bob!u@h carol!c@h", ":fake 353 alice_one = #one :@bob carol")
	both(":fake 352 alice_one #one u h fake bob H@+ :0 Bob", "H@+ :0 Bob", "bob H@ :0 Bob")
	both(":fake FAIL JOIN CHANNEL_RENAMED #x #y :Renamed", ":fake FAIL JOIN CHANNEL_RENAMED #x #y :Renamed", ":fake NOTICE alice_one :FAIL JOIN CHANNEL_RENAMED #x #y: Renamed")
	both(":fake WARN REHASH CERTS :odd", "WARN REHASH", "NOTICE alice_one :WARN REHASH CERTS: odd")

	// a host change: QUIT and JOIN (with the modes) for clients without chghost
	sim(":bob!u@h CHGHOST newu newh")
	full.expect(":bob!u@h CHGHOST newu newh")
	seen := plain.until(":evrd MODE #one +o bob")
	if !anyHas(seen, ":bob!u@h QUIT :Changing host") || !anyHas(seen, ":bob!newu@newh JOIN #one") {
		t.Fatalf("chghost: %q", seen)
	}

	// a network batch, and lines in it, for clients with batch
	sim("BATCH +b1 netsplit a.net b.net")
	sim("@batch=b1 :dave!u@h QUIT :a.net b.net")
	sim("BATCH -b1")
	full.expect("BATCH +b1 netsplit")
	full.expect("@batch=b1 :dave!u@h QUIT")
	full.expect("BATCH -b1")
	seen = plain.until("QUIT :a.net b.net")
	if anyHas(seen, "BATCH") || strings.HasPrefix(seen[len(seen)-1], "@") {
		t.Fatalf("batch for a client without it: %q", seen)
	}

	// replies, reactions and redactions name lines by evrd's msgids
	sim("@msgid=up1 :bob!u@h PRIVMSG #one :oops")
	ours := irc.Parse(full.expect("PRIVMSG #one :oops")).Tags["msgid"]
	if ours == "" || ours == "up1" {
		t.Fatalf("msgid %q", ours)
	}
	sim("@+draft/reply=up1 :carol!u@h PRIVMSG #one :lol")
	if l := full.expect("PRIVMSG #one :lol"); !strings.Contains(l, "+draft/reply="+ours) {
		t.Fatalf("reply: %q", l)
	}
	sim(":bob!u@h REDACT #one up1 :typo")
	full.expect(":bob!u@h REDACT #one " + ours + " typo")
	full.send("@+draft/reply=" + ours + " PRIVMSG #one :answer")
	eventually(t, "reply sent with the network's msgid", func() bool { return r.irc.Has("@+draft/reply=up1 PRIVMSG #one :answer") })
	full.send("REDACT #one " + ours + " :my bad")
	eventually(t, "redaction sent with the network's msgid", func() bool { return r.irc.Has("REDACT #one up1 :my bad") })
	if seen := plain.until("PRIVMSG #one :lol"); anyHas(seen, "REDACT") {
		t.Fatalf("REDACT for a client without it: %q", seen)
	}

	// a channel rename: PART and JOIN for clients without it
	sim(":fake RENAME #one #uno :moving")
	full.expect(":fake RENAME #one #uno :moving")
	plain.expect(":alice_one!alice_one@evrd PART #one :Renamed to #uno: moving")
	plain.expect(":alice_one!alice_one@evrd JOIN #uno")
	eventually(t, "saved under the new name", func() bool { return strings.Join(r.network("one").Channels, " ") == "#uno" })
}

// A client with extended-join gets its own JOIN in that form too.
func TestExtendedSelfJoin(t *testing.T) {
	r := newRigCaps(t, everyLinkCap)
	full := r.full()
	full.send("DETACH #one")
	full.expect("PART #one")
	full.send("JOIN #one")
	full.expect(":alice_one!alice_one@evrd JOIN #one * :alice_one")
}
