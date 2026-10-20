package client

import (
	"net"
	"strings"
	"testing"
	"time"

	"github.com/4evrd/evrd/internal/store"
)

// The network's capabilities while registering: CAP LS waits for PASS to
// say which network, and REQ takes them before CAP END.
func TestNetworkCapsAtRegistration(t *testing.T) {
	r := newRigCaps(t, everyLinkCap)
	r.attached()
	r.linked()

	ic := r.dialRaw(r.addr)
	ic.send("CAP LS 302")
	time.Sleep(100 * time.Millisecond) // less than the wait
	ic.send("PASS alice/one:alice-password")
	ic.send("NICK alice")
	ic.send("USER alice 0 * :alice")
	if l := ic.expect(" LS "); !strings.Contains(l, " away-notify ") || !strings.Contains(l, " extended-join ") {
		t.Fatalf("LS without the network's: %q", l)
	}
	ic.send("CAP REQ :away-notify cap-notify")
	ic.expect("CAP alice ACK :away-notify cap-notify")
	ic.send("CAP END")
	ic.expect(" 001 ")
	ic.send("PRIVMSG *status :version")
	if seen := ic.until("PRIVMSG alice_one :evrd "); anyHas(seen, " NEW ") {
		t.Fatalf("offered again after attaching: %q", seen)
	}
	ic.send("SIMULATE :bob!u@h AWAY :gone")
	ic.expect(":bob!u@h AWAY :gone")
}

// On a node that forwards the client, the owner answers the waiting CAP LS,
// with its link's capabilities.
func TestNetworkCapsAtRegistrationThroughAForwardingNode(t *testing.T) {
	r := newRigCaps(t, everyLinkCap)
	r.attached()
	r.linked()
	const secret = "cluster-secret"
	r.srv.SetCluster(&fakeCluster{self: "owner"}, secret)
	internal := freeAddr(t)
	go r.srv.ListenInternal(r.ctx, internal)
	dialUntil(t, func() (net.Conn, error) { return net.Dial("tcp", internal) })
	front := r.frontNode(&fakeCluster{self: "front", owner: store.Node{ID: "owner", IRCAddr: internal}}, secret)
	dialUntil(t, func() (net.Conn, error) { return net.Dial("tcp", front) }).Close()

	ic := r.dialRaw(front)
	ic.send("CAP LS 302")
	ic.send("PASS alice/one:alice-password")
	ic.send("NICK alice")
	ic.send("USER alice 0 * :alice")
	if l := ic.expect(" LS "); !strings.Contains(l, " away-notify ") {
		t.Fatalf("LS: %q", l)
	}
	ic.send("CAP REQ :away-notify")
	ic.expect("ACK :away-notify")
	ic.send("CAP END")
	ic.expect(" 001 alice_one ")
}

func TestCapLSWaitsThenAnswers(t *testing.T) {
	r := newRigCaps(t, everyLinkCap)
	r.attached()
	r.linked()

	// nothing but CAP LS: answered after the wait, with evrd's own
	ic := r.dialRaw(r.addr)
	start := time.Now()
	ic.send("CAP LS 302")
	l := ic.expect(" LS ")
	if waited := time.Since(start); waited < 150*time.Millisecond || strings.Contains(l, "away-notify") {
		t.Fatalf("after %v: %q", waited, l)
	}
	// the network's can't be had yet: all or nothing, as the spec has it
	ic.send("CAP REQ :away-notify cap-notify")
	ic.expect("CAP * NAK :away-notify cap-notify")
	// SASL names the network: then they can
	ic.send("CAP REQ :sasl")
	ic.send("AUTHENTICATE PLAIN")
	ic.send("AUTHENTICATE " + plain("", "alice/one", "alice-password"))
	ic.expect(" 903 ")
	ic.send("CAP REQ :away-notify")
	ic.expect("ACK :away-notify")

	// a REQ answers a waiting LS first
	ic = r.dialRaw(r.addr)
	ic.send("CAP LS 302")
	ic.send("CAP REQ :server-time")
	ic.expect(" LS ")
	ic.expect("ACK :server-time")
}
