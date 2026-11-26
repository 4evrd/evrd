package upstream

import (
	"context"
	"testing"

	"github.com/4evrd/evrd/internal/store"
)

// Managed mode: WEBIRC opens each connection, ahead of PASS, and the
// connection answers CTCP VERSION as told.
func TestWebIRCOpensTheConnection(t *testing.T) {
	s := startScripted(t, nil)
	host, port := s.hostPort()
	n := store.Network{Nick: "me", Servers: []store.Server{{Host: host, Port: port, Password: "serverpw"}}}
	opts := Options{WebIRC: func(ctx context.Context) string { return "WEBIRC pw gateway host.example 192.0.2.1" },
		Idle: func() bool { return true }, VersionReply: "Boson 1.0"}
	c := New(n, quiet, opts)
	run(t, c)
	eventually(t, "registered", func() bool { return c.Status() == StatusConnected })
	got := s.last().got()
	if len(got) < 2 || got[0] != "WEBIRC pw gateway host.example 192.0.2.1" || got[1] != "PASS serverpw" {
		t.Fatalf("opened with %q", got)
	}
	s.send(":bob!b@h PRIVMSG me :\x01VERSION\x01")
	eventually(t, "the managed VERSION", func() bool { return s.has("NOTICE bob :\x01VERSION Boson 1.0\x01") })
}

func TestNoWebIRCLineNoWebIRC(t *testing.T) {
	s := startScripted(t, nil)
	c := New(s.network(store.Network{Nick: "me"}), quiet, Options{WebIRC: func(context.Context) string { return "" }})
	run(t, c)
	eventually(t, "registered", func() bool { return c.Status() == StatusConnected })
	if s.count("WEBIRC") != 0 {
		t.Fatal("sent WEBIRC")
	}
	if c.replyFrom != "*evrd!evrd@evrd" {
		t.Errorf("modules reply from %q", c.replyFrom)
	}
	if c := New(store.Network{}, quiet, Options{ReplyFrom: "irc.example"}); c.replyFrom != "irc.example" {
		t.Errorf("ReplyFrom: %q", c.replyFrom)
	}
}
