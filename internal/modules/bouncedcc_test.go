package modules

import (
	"strings"
	"testing"
)

// Without dcc_public, bouncedcc relays nothing and says so (internal/dcc
// has the relaying's tests).
func TestBounceDCCNeedsAnAddress(t *testing.T) {
	f := newFake()
	m := start(t, f, "bouncedcc", nil)
	r, ok := m.(CTCPRewriter)
	if !ok {
		t.Fatal("bouncedcc doesn't rewrite CTCPs")
	}
	in := "\x01DCC SEND f 2130706433 5000 10\x01"
	if got := r.ServerCTCP("bob!b@h", in); got != in {
		t.Fatalf("to the clients: %q", got)
	}
	if got := r.ClientCTCP("bob", in, "127.0.0.1"); got != "" {
		t.Fatalf("sent without relaying: %q", got)
	}
	if len(f.logs) != 2 || !strings.Contains(f.logs[0], "dcc_public") {
		t.Fatalf("logged %q", f.logs)
	}
}
