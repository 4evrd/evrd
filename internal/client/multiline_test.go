package client

import (
	"strings"
	"testing"

	"github.com/4evrd/evrd/internal/irc"
)

func TestMultiline(t *testing.T) {
	r := newRigCaps(t, everyLinkCap)
	ic := r.attached("batch", "message-tags", "echo-message")
	if l := ic.expect("CAP alice_one NEW :"); !strings.Contains(l, "draft/multiline=max-bytes=40,max-lines=3") {
		t.Fatalf("offered: %q", l)
	}
	ic.send("CAP REQ :draft/multiline")
	ic.expect("ACK :draft/multiline")
	plain := r.attached()
	n := r.network("one")

	// a client's batch: one batch upstream, saved a line at a time
	ic.send("@+x=1 BATCH +m1 draft/multiline #one")
	ic.send("@batch=m1 PRIVMSG #one :hello")
	ic.send("@batch=m1;draft/multiline-concat PRIVMSG #one : world")
	ic.send("@batch=m1 PRIVMSG #one :line2")
	ic.send("BATCH -m1")
	open := irc.Parse(ic.expect(" BATCH +"))
	if open.Prefix != "alice_one!alice_one@evrd" || open.Tags["msgid"] == "" || open.Tags["+x"] != "1" || open.Params[1] != "draft/multiline" {
		t.Fatalf("echo: %+v", open)
	}
	ic.expect(";draft/multiline-concat :alice_one!alice_one@evrd PRIVMSG #one : world")
	ic.expect("BATCH -")
	eventually(t, "sent as a batch", func() bool {
		return r.irc.Has("@batch=ml") && r.irc.Has(";draft/multiline-concat PRIVMSG #one : world")
	})
	if !r.irc.Has("@+x=1 BATCH +ml") {
		t.Fatalf("upstream: %q", r.irc.Lines())
	}
	eventually(t, "saved", func() bool {
		var got []string
		for _, m := range chat(r.hist, n.ID, "#one") {
			got = append(got, m.Text)
		}
		return strings.Join(got, "|") == "hello world|line2"
	})
	// the plain client sees the lines
	plain.expect("PRIVMSG #one :hello world")
	plain.expect("PRIVMSG #one :line2")

	for _, c := range []struct {
		lines []string
		want  string
	}{
		{[]string{"a", "b", "c", "d"}, "FAIL BATCH MULTILINE_MAX_LINES 3 :"},
		{[]string{strings.Repeat("x", 41)}, "FAIL BATCH MULTILINE_MAX_BYTES 40 :"},
	} {
		ic.send("BATCH +m2 draft/multiline #one")
		for _, l := range c.lines {
			ic.send("@batch=m2 PRIVMSG #one :" + l)
		}
		ic.send("BATCH -m2")
		ic.expect(c.want)
	}
	ic.send("BATCH +m3 draft/multiline #one")
	ic.send("@batch=m3 PRIVMSG #other :x")
	ic.send("BATCH -m3")
	ic.expect("FAIL BATCH MULTILINE_INVALID_TARGET #one #other :")
	ic.send("BATCH +m4 draft/multiline #one")
	ic.send("@batch=m4;draft/multiline-concat PRIVMSG #one :x")
	ic.send("BATCH -m4")
	ic.expect("FAIL BATCH MULTILINE_INVALID :")
	ic.send("BATCH +m5 draft/multiline #one")
	ic.send("@batch=m5 PRIVMSG #one :x")
	ic.send("@batch=m5 NOTICE #one :y")
	ic.send("BATCH -m5")
	ic.expect("FAIL BATCH MULTILINE_INVALID :")

	// the network's batch: whole for the client with the cap, lines for
	// the others
	ic.send("SIMULATE @msgid=nb :bob!u@h BATCH +nb draft/multiline #one")
	ic.send("SIMULATE @batch=nb :bob!u@h PRIVMSG #one :a")
	ic.send("SIMULATE @batch=nb;draft/multiline-concat :bob!u@h PRIVMSG #one :b")
	ic.send("SIMULATE BATCH -nb")
	ic.expect(":bob!u@h BATCH +nb draft/multiline #one")
	ic.expect("batch=nb;draft/multiline-concat :bob!u@h PRIVMSG #one :b")
	ic.expect("BATCH -nb")
	seen := plain.until("PRIVMSG #one :b")
	if anyHas(seen, "BATCH") || anyHas(seen, "@batch") || anyHas(seen, "@msgid") {
		t.Fatalf("plain: %q", seen)
	}
}

func TestMultilineNeedsBatch(t *testing.T) {
	r := newRigCaps(t, everyLinkCap)
	ic := r.attached("message-tags")
	ic.expect("CAP alice_one NEW :")
	ic.send("CAP REQ :draft/multiline")
	ic.expect("NAK :draft/multiline")
	// BATCH from a client without it goes to the network, as before
	ic.send("BATCH +x draft/multiline #one")
	eventually(t, "passed on", func() bool { return r.irc.Has("BATCH +x draft/multiline #one") })
}
