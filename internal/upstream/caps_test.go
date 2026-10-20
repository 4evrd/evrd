package upstream

import (
	"net"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/4evrd/evrd/internal/store"
	"github.com/4evrd/evrd/internal/testirc"
)

func TestMessageTagsAreRequestedWhenOffered(t *testing.T) {
	// with SASL: each cap asked for on its own, SASL after both answers
	s := startScripted(t, capServer("sasl message-tags=x multi-prefix", true, "903"))
	c := New(s.network(store.Network{Nick: "alice", Password: "pw"}), quiet, Options{})
	run(t, c)
	eventually(t, "registered", func() bool { return c.Status() == StatusConnected })
	got := s.last().got()
	sasl, tags, auth, end := index(got, "CAP REQ :sasl"), index(got, "CAP REQ :message-tags"), index(got, "AUTHENTICATE PLAIN"), index(got, "CAP END")
	if sasl < 0 || tags < 0 || auth < tags || end < auth || s.count("CAP REQ") != 2 {
		t.Fatalf("negotiation: %q", got)
	}
	if !c.HasCap("message-tags") || !c.HasCap("sasl") || c.HasCap("multi-prefix") {
		t.Fatal("acknowledged caps")
	}

	// without a password: just message-tags, then CAP END
	s = startScripted(t, capServer("message-tags", true, ""))
	c = New(s.network(store.Network{Nick: "bob"}), quiet, Options{})
	run(t, c)
	eventually(t, "registered", func() bool { return c.Status() == StatusConnected })
	if got := s.last().got(); index(got, "CAP REQ :message-tags") < 0 || index(got, "CAP END") < index(got, "CAP REQ") || index(got, "AUTHENTICATE") >= 0 {
		t.Fatalf("without SASL: %q", got)
	}
	if !c.HasCap("message-tags") {
		t.Fatal("message-tags not acknowledged")
	}

	// not offered: not asked for
	s = startScripted(t, capServer("multi-prefix", true, ""))
	c = New(s.network(store.Network{Nick: "carol"}), quiet, Options{})
	run(t, c)
	eventually(t, "registered", func() bool { return c.Status() == StatusConnected })
	if s.count("CAP REQ") != 0 || c.HasCap("message-tags") {
		t.Fatalf("asked for what isn't offered: %q", s.last().got())
	}
}

// A refused message-tags doesn't cost SASL; a CAP LS over several lines is
// read whole.
func TestMessageTagsRefusedAndMultilineLS(t *testing.T) {
	s := startScripted(t, func(p *peer, l string) {
		switch {
		case strings.HasPrefix(l, "CAP LS"):
			p.send(":srv CAP * LS * :sasl")
			p.send(":srv CAP * LS :message-tags")
		case l == "CAP REQ :message-tags":
			p.send(":srv CAP * NAK :message-tags")
		case l == "CAP REQ :sasl":
			p.send(":srv CAP * ACK :sasl")
		default:
			capServer("", true, "903")(p, l)
		}
	})
	c := New(s.network(store.Network{Nick: "alice", Password: "pw"}), quiet, Options{})
	run(t, c)
	eventually(t, "registered", func() bool { return c.Status() == StatusConnected })
	if !s.has("AUTHENTICATE PLAIN") || c.HasCap("message-tags") || s.count("CAP REQ") != 2 {
		t.Fatalf("got %q", s.last().got())
	}
}

func TestCapsChangeAfterRegistration(t *testing.T) {
	s, c := connected(t, store.Network{Nick: "me"}, Options{})
	s.send(":srv CAP me NEW :message-tags away-notify")
	eventually(t, "asked for", func() bool { return s.has("CAP REQ :message-tags") })
	if s.has("CAP REQ :away-notify") || s.count("CAP END") != 1 {
		t.Fatalf("got %q", s.last().got())
	}
	s.send(":srv CAP me ACK :message-tags")
	eventually(t, "acknowledged", func() bool { return c.HasCap("message-tags") })
	s.send(":srv CAP me DEL :message-tags")
	eventually(t, "gone", func() bool { return !c.HasCap("message-tags") })
	s.send(":srv CAP me ACK :message-tags")
	s.send(":srv CAP me ACK :-message-tags")
	settle(t, s)
	if c.HasCap("message-tags") {
		t.Fatal("-cap kept it")
	}
	// LIST replaces the set, across several lines
	s.send(":srv CAP me LIST * :message-tags")
	s.send(":srv CAP me LIST :sasl")
	s.send(":srv CAP me LIST :multi-prefix")
	settle(t, s)
	if c.HasCap("message-tags") || c.HasCap("sasl") || !c.HasCap("multi-prefix") {
		t.Fatal("LIST")
	}
}

func TestClientTagsGoUpstream(t *testing.T) {
	s := startScripted(t, capServer("message-tags", true, ""))
	c := New(s.network(store.Network{Nick: "me"}), quiet, Options{})
	run(t, c)
	eventually(t, "registered", func() bool { return c.Status() == StatusConnected && c.HasCap("message-tags") })
	raw, unsub := c.SubscribeRaw()
	defer unsub()
	c.SendTagMsg("s1", "+typing=active", "#c")
	c.SendTagMsg("s1", "", "#c") // nothing to say
	c.SendTagged("s1", "+draft/reply=1", "PRIVMSG", "#c", "hi")
	eventually(t, "sent", func() bool { return s.has("@+draft/reply=1 PRIVMSG #c :hi") })
	if !s.has("@+typing=active TAGMSG #c") || s.count("@") != 2 {
		t.Fatalf("got %q", s.last().got())
	}
	select {
	case l := <-raw:
		if l.Text != "@+typing=active :me TAGMSG #c" || l.Origin != "s1" || !l.ID.IsZero() {
			t.Fatalf("to other sessions: %+v", l)
		}
	case <-time.After(time.Second):
		t.Fatal("other sessions didn't get the TAGMSG")
	}

	// a network without message-tags gets neither
	s2, c2 := connected(t, store.Network{Nick: "me"}, Options{})
	c2.SendTagMsg("s1", "+typing=active", "#c")
	c2.SendTagged("s1", "+draft/reply=1", "PRIVMSG", "#c", "plain")
	eventually(t, "sent", func() bool { return s2.has("PRIVMSG #c :plain") })
	if s2.count("@") != 0 || s2.has("TAGMSG") {
		t.Fatalf("got %q", s2.last().got())
	}
}

// A link the holder kept through a restart still knows its caps: evrd asks
// with CAP LIST when it resumes.
func TestResumedLinkKeepsItsCaps(t *testing.T) {
	var log testirc.Log
	addr := testirc.StartCaps(t, &log, "message-tags")
	host, port, _ := net.SplitHostPort(addr)
	p, _ := strconv.Atoi(port)
	n := store.Network{ID: 7, Nick: "alice", Host: host, Port: p}
	holder := startTestHolder(t)
	var stopping atomic.Bool
	opts := Options{Links: holder, LinkID: "n7", Detaching: stopping.Load}

	c1 := New(n, quiet, opts)
	cancel1 := run(t, c1)
	eventually(t, "acknowledged", func() bool { return c1.Status() == StatusConnected && c1.HasCap("message-tags") })
	stopping.Store(true)
	cancel1()
	time.Sleep(200 * time.Millisecond)

	stopping.Store(false)
	c2 := New(n, quiet, opts)
	run(t, c2)
	eventually(t, "resumed", func() bool { return c2.Status() == StatusConnected })
	eventually(t, "caps known again", func() bool { return c2.HasCap("message-tags") })
	if !log.Has("CAP LIST") || log.Connections() != 1 {
		t.Fatalf("got %q", log.Lines())
	}
}
