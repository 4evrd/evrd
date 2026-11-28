package upstream

import (
	"net"
	"net/netip"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/4evrd/evrd/internal/config"
	"github.com/4evrd/evrd/internal/dcc"
	"github.com/4evrd/evrd/internal/store"
	"github.com/4evrd/evrd/internal/testirc"
)

// bouncedcc on a connection: a DCC offer from the server reaches the
// clients rewritten to point at evrd, and one from a client goes to the
// server the same way.
func TestCTCPRewriting(t *testing.T) {
	dcc.Configure(config.DCC{Public: []netip.Addr{netip.MustParseAddr("127.0.0.1")}, Listen: "127.0.0.1", Timeout: time.Second, Private: true})
	t.Cleanup(func() { dcc.Configure(config.DCC{Timeout: config.DefaultDCCTimeout}) })
	var log testirc.Log
	addr := testirc.StartLogged(t, &log)
	host, port, _ := net.SplitHostPort(addr)
	p, _ := strconv.Atoi(port)
	n := store.Network{ID: 44, Nick: "alice", Host: host, Port: p,
		Modules: map[string]store.ModuleConfig{"bouncedcc": {Enabled: true}}}
	c := New(n, quiet, Options{ClientIPs: func() []string { return []string{"127.0.0.1"} }})
	raw, stop := c.SubscribeRaw()
	defer stop()
	run(t, c)
	eventually(t, "registered", func() bool { return c.Status() == StatusConnected })

	log.Send("@time=2026-01-01T00:00:00.000Z :bob!b@h PRIVMSG alice :\x01DCC SEND f.txt 2130706433 5555 10\x01")
	var got string
	for got == "" {
		select {
		case l := <-raw:
			if strings.Contains(l.Text, "DCC SEND") {
				got = l.Text
			}
		case <-time.After(5 * time.Second):
			t.Fatal("no offer reached the clients")
		}
	}
	if !strings.HasPrefix(got, "@time=2026-01-01T00:00:00.000Z :bob!b@h PRIVMSG alice :\x01DCC SEND f.txt 2130706433 ") || strings.Contains(got, " 5555 ") {
		t.Fatalf("the clients got %q", got)
	}

	out := c.ClientCTCP("bob", "\x01DCC CHAT chat 3232235777 6000\x01", "127.0.0.1")
	if o, ok := dcc.Parse(out); !ok || o.Port == 6000 || o.Addr != netip.MustParseAddr("127.0.0.1") {
		t.Fatalf("to the server: %q", out)
	}
	if out := c.ClientCTCP("bob", "\x01VERSION\x01", "127.0.0.1"); out != "\x01VERSION\x01" {
		t.Fatalf("another CTCP changed: %q", out)
	}
	if out := c.ClientCTCP("bob", "hello", "127.0.0.1"); out != "hello" {
		t.Fatalf("a message changed: %q", out)
	}
}
