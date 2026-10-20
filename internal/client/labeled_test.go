package client

import (
	"strings"
	"testing"

	"github.com/4evrd/evrd/internal/irc"
)

// labeledClient attaches a client with labeled-response (and batch,
// echo-message, message-tags).
func (r *rig) labeledClient() *ircClient {
	r.t.Helper()
	ic := r.attached("batch", "echo-message", "message-tags")
	ic.expect("CAP alice_one NEW :")
	ic.send("CAP REQ :labeled-response")
	ic.expect("ACK :labeled-response")
	return ic
}

func TestLabeledResponses(t *testing.T) {
	r := newRigCaps(t, everyLinkCap)
	ic := r.labeledClient()
	other := r.labeledClient()

	// evrd's own answers: one line labeled, several in a batch, none an ACK
	ic.send("@label=a1 PING :x")
	ic.expect("@label=a1 :evrd PONG evrd :x")
	ic.send("@label=a2 PRIVMSG #one :hi")
	if l := ic.expect("PRIVMSG #one :hi"); !strings.HasPrefix(l, "@label=a2;msgid=") {
		t.Fatalf("echo: %q", l)
	}
	ic.send("@label=a3 PRIVMSG *status :help")
	open := irc.Parse(ic.expect(" BATCH +"))
	if open.Tags["label"] != "a3" || open.Params[1] != "labeled-response" {
		t.Fatalf("batch: %+v", open)
	}
	ref := strings.TrimPrefix(open.Params[0], "+")
	if l := ic.expect("evrd commands"); !strings.HasPrefix(l, "@batch="+ref+" ") {
		t.Fatalf("in the batch: %q", l)
	}
	ic.expect("BATCH -" + ref)
	ic.send("@label=a4 TAGMSG #one")
	ic.expect("@label=a4 :evrd ACK")

	// passed to the network with the label, which comes back to this
	// client only
	ic.send("@label=a5 WHOIS bob")
	var sent string
	eventually(t, "labeled WHOIS upstream", func() bool {
		for _, l := range r.irc.Lines() {
			if strings.HasSuffix(l, "~a5 WHOIS bob") {
				sent = l
				return true
			}
		}
		return false
	})
	theirs := irc.Parse(sent).Tags["label"]
	ic.send("SIMULATE @label=" + theirs + " :fake 311 alice_one bob u h * :Bob")
	ic.expect("@label=a5 :fake 311 alice_one bob u h * :Bob")
	if l := other.expect(" 311 alice_one bob"); strings.HasPrefix(l, "@") {
		t.Fatalf("another client got the label: %q", l)
	}
	ic.send("SIMULATE @label=" + theirs + " BATCH +lb labeled-response")
	ic.send("SIMULATE @batch=lb :fake 318 alice_one bob :End of WHOIS")
	ic.send("SIMULATE BATCH -lb")
	ic.expect("@label=a5 BATCH +lb labeled-response")
	ic.expect("@batch=lb :fake 318 alice_one bob :End of WHOIS")
	ic.expect("BATCH -lb")
	ic.send("SIMULATE @label=" + theirs + " :fake ACK")
	ic.expect("@label=a5 :fake ACK")
	seen := other.until(":fake 318 alice_one bob")
	other.send("PING :mark")
	seen = append(seen, other.until("PONG")...)
	if anyHas(seen, "BATCH") || anyHas(seen, "ACK") || anyHas(seen, "label=") || strings.HasPrefix(seen[0], "@") {
		t.Fatalf("another client: %q", seen)
	}
}

func TestLabeledResponseNeedsBatch(t *testing.T) {
	r := newRigCaps(t, everyLinkCap)
	ic := r.attached("message-tags")
	ic.expect("CAP alice_one NEW :")
	ic.send("CAP REQ :labeled-response")
	ic.expect("NAK :labeled-response")
	ic.send("CAP REQ :batch labeled-response")
	ic.expect("ACK :batch labeled-response")
	// a network without it: not offered
	plain := newRig(t).attached("batch")
	plain.send("CAP REQ :labeled-response")
	plain.expect("NAK :labeled-response")
}
