package client

import (
	"strings"
	"testing"

	"github.com/4evrd/evrd/internal/irc"
)

// linked waits until network one's link has negotiated message-tags.
func (r *rig) linked() {
	r.t.Helper()
	eventually(r.t, "message-tags acknowledged", func() bool {
		c, ok := r.b.ConnFor(r.network("one").ID)
		return ok && c.HasCap("message-tags")
	})
}

func TestTypingBothWays(t *testing.T) {
	r := newRigCaps(t, "message-tags")
	ic := r.attached("message-tags")
	other := r.attached("message-tags", "echo-message")
	plain := r.attached()
	r.linked()
	n := r.network("one")
	before := len(chat(r.hist, n.ID, "#one"))

	// from the network: client-only tags pass, the server's don't
	ic.send("SIMULATE @+typing=active;account=bob;msgid=srv1 :bob!u@h TAGMSG #one")
	if got := ic.expect("TAGMSG #one"); got != "@+typing=active :bob!u@h TAGMSG #one" {
		t.Fatalf("relayed TAGMSG: %q", got)
	}
	ic.send("SIMULATE @+draft/reply=abc;account=bob :bob!u@h PRIVMSG #one :re")
	l := irc.Parse(ic.expect("PRIVMSG #one :re"))
	if l.Tags["+draft/reply"] != "abc" || l.Tags["account"] != "" || l.Tags["msgid"] == "" {
		t.Fatalf("PRIVMSG tags: %+v", l.Tags)
	}
	// a client without message-tags: no TAGMSG, no tags
	seen := plain.until("PRIVMSG #one :re")
	if anyHas(seen, "TAGMSG") || strings.HasPrefix(seen[len(seen)-1], "@") {
		t.Fatalf("plain client: %q", seen)
	}

	// to the network: only client-only tags go, and typing isn't saved
	ic.send("@+typing=active;msgid=fake;time=2020-01-01T00:00:00.000Z TAGMSG #one")
	eventually(t, "TAGMSG sent upstream", func() bool { return r.irc.Has("@+typing=active TAGMSG #one") })
	// the user's other clients see it (soju does the same); the sender only
	// with echo-message
	if got := other.expect(":alice_one TAGMSG #one"); got != "@+typing=active :alice_one TAGMSG #one" {
		t.Fatalf("other session: %q", got)
	}
	other.send("@+typing=paused TAGMSG #one")
	if got := other.expect(":alice_one TAGMSG #one"); got != "@+typing=paused :alice_one TAGMSG #one" {
		t.Fatalf("echo: %q", got)
	}
	if seen := ic.until("+typing=paused"); anyHas(seen, "+typing=active :alice_one") {
		t.Fatalf("the sender got its own TAGMSG back: %q", seen)
	}

	ic.send("@+draft/reply=xyz;account=evil;+=x PRIVMSG #one :answer")
	eventually(t, "tags sent upstream", func() bool { return r.irc.Has("@+draft/reply=xyz PRIVMSG #one :answer") })
	if r.irc.Has("account=evil") || r.irc.Has("msgid=fake") {
		t.Fatalf("server tags injected: %q", r.irc.Lines())
	}
	got := irc.Parse(other.expect("PRIVMSG #one :answer"))
	if got.Tags["+draft/reply"] != "xyz" || got.Tags["msgid"] == "" || got.Tags["account"] != "" {
		t.Fatalf("other session's copy: %+v", got.Tags)
	}
	eventually(t, "the message saved", func() bool { return len(chat(r.hist, n.ID, "#one")) == before+2 })
	for _, m := range chat(r.hist, n.ID, "#one") {
		if m.Text == "" {
			t.Fatalf("a TAGMSG was saved: %+v", m)
		}
	}
}

func TestTypingWithoutUpstreamTags(t *testing.T) {
	r := newRig(t)
	ic := r.attached("message-tags")
	other := r.attached("message-tags")
	ic.send("@+typing=active TAGMSG #one")
	ic.send("@+draft/reply=xyz PRIVMSG #one :plain")
	eventually(t, "message sent", func() bool { return r.irc.Has("PRIVMSG #one :plain") })
	if r.irc.Has("TAGMSG") || r.irc.Has("+draft/reply") {
		t.Fatalf("tags sent to a network without message-tags: %q", r.irc.Lines())
	}
	if seen := other.until("PRIVMSG #one :plain"); anyHas(seen, "TAGMSG") {
		t.Fatalf("a dropped TAGMSG reached another client: %q", seen)
	}
}

func TestClientTags(t *testing.T) {
	for in, want := range map[string]string{
		"@+b=2;+a;msgid=x;time=y X": "+a;+b=2",
		"@account=z X":              "",
		"@+=v X":                    "",
		"@+long=" + strings.Repeat("v", maxClientTags) + " X": "",
	} {
		if got := clientTags(irc.Parse(in).Tags); got != want {
			t.Errorf("%.40q: got %q, want %q", in, got, want)
		}
	}
	// a client without message-tags sends none
	se := &session{caps: map[string]bool{}}
	if got := se.clientTags(irc.Parse("@+a=1 TAGMSG #c")); got != "" {
		t.Fatalf("without the cap: %q", got)
	}
}
