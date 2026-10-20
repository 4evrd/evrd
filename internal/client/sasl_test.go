package client

import (
	"bufio"
	"encoding/base64"
	"net"
	"strings"
	"testing"

	"github.com/4evrd/evrd/internal/store"
)

// dialRaw connects to addr without registering.
func (r *rig) dialRaw(addr string) *ircClient {
	r.t.Helper()
	c, err := net.Dial("tcp", addr)
	if err != nil {
		r.t.Fatal(err)
	}
	r.t.Cleanup(func() { c.Close() })
	return &ircClient{t: r.t, c: c, sc: bufio.NewScanner(c)}
}

func plain(authzid, authcid, password string) string {
	return base64.StdEncoding.EncodeToString([]byte(authzid + "\x00" + authcid + "\x00" + password))
}

// sasl negotiates SASL PLAIN and returns the client, before CAP END.
func (r *rig) sasl(addr, authcid, password string) *ircClient {
	r.t.Helper()
	ic := r.dialRaw(addr)
	ic.send("CAP LS 302")
	ic.expect("sasl=PLAIN")
	ic.send("CAP REQ :sasl server-time")
	ic.expect("ACK :sasl server-time")
	ic.send("NICK alice")
	ic.send("USER alice 0 * :alice")
	ic.send("AUTHENTICATE PLAIN")
	ic.expect("AUTHENTICATE +")
	ic.send("AUTHENTICATE " + plain("", authcid, password))
	return ic
}

func TestSASLPlainSignIn(t *testing.T) {
	r := newRig(t)
	ic := r.sasl(r.addr, "alice/two", "alice-password")
	ic.expect(":evrd 900 alice alice!alice@evrd alice :You are now logged in as alice")
	ic.expect(":evrd 903 alice :SASL authentication successful")
	ic.send("AUTHENTICATE PLAIN")
	ic.expect(" 907 alice ")
	ic.send("CAP END")
	ic.expect(" 001 alice_two :Welcome to evrd — attached to two")
	ic.send("AUTHENTICATE PLAIN")
	ic.expect(" 907 alice_two ")

	// an IRC client password, and the authorization identity is ignored
	_, secret, err := r.acc.NewClientPassword(r.user.ID, "phone")
	if err != nil {
		t.Fatal(err)
	}
	ic = r.dialRaw(r.addr)
	ic.send("CAP REQ :sasl")
	ic.send("AUTHENTICATE plain")
	ic.expect("AUTHENTICATE +")
	ic.send("AUTHENTICATE " + plain("someone", "alice", secret))
	ic.expect(" 903 ")
	ic.send("NICK alice")
	ic.send("USER alice 0 * :alice")
	ic.send("CAP END")
	ic.expect(" 001 alice_one ")
	eventually(t, "attached with the device", func() bool {
		for _, c := range r.b.ClientsOf(r.user.ID) {
			if c.Device == "phone" {
				return true
			}
		}
		return false
	})
}

func TestSASLRefusals(t *testing.T) {
	r := newRig(t)
	// a wrong password: 904, logged like PASS; without a PASS, no way in
	ic := r.sasl(r.addr, "alice", "wrong")
	ic.expect(":evrd 904 alice :Invalid credentials")
	if !strings.Contains(r.logs.String(), "irc sign-in failed") {
		t.Fatalf("not logged: %s", r.logs.String())
	}
	ic.send("CAP END")
	ic.expect(" 464 alice :Password required")

	ic = r.dialRaw(r.addr)
	ic.send("CAP LS")
	ic.send("AUTHENTICATE PLAIN") // sasl not asked for
	ic.expect(":evrd 904 * :SASL authentication failed")
	ic.send("CAP REQ :sasl")
	ic.send("AUTHENTICATE EXTERNAL")
	ic.expect(":evrd 908 * PLAIN :are available SASL mechanisms")
	ic.expect(" 904 ")
	ic.send("AUTHENTICATE PLAIN")
	ic.expect("AUTHENTICATE +")
	ic.send("AUTHENTICATE *")
	ic.expect(" 906 * :SASL authentication aborted")
	ic.send("AUTHENTICATE PLAIN")
	ic.expect("AUTHENTICATE +")
	ic.send("AUTHENTICATE !!!notbase64")
	ic.expect(" 904 * :SASL authentication failed")
	// a payload over several 400-byte lines
	long := plain("", "alice", strings.Repeat("x", 400))
	ic.send("AUTHENTICATE PLAIN")
	ic.expect("AUTHENTICATE +")
	ic.send("AUTHENTICATE " + long[:400])
	ic.send("AUTHENTICATE " + long[400:])
	ic.expect(" 904 * :Invalid credentials")
	// a suspended account
	u := r.user
	u.Suspended = true
	r.st.UpdateUser(u)
	ic.send("AUTHENTICATE PLAIN")
	ic.expect("AUTHENTICATE +")
	ic.send("AUTHENTICATE " + plain("", "alice", "alice-password"))
	ic.expect(" 904 * :This account is suspended")
}

// SASL on a node that forwards the client: the owner signs it in.
func TestSASLThroughAForwardingNode(t *testing.T) {
	r := newRig(t)
	const secret = "cluster-secret"
	r.srv.SetCluster(&fakeCluster{self: "owner"}, secret)
	internal := freeAddr(t)
	go r.srv.ListenInternal(r.ctx, internal)
	dialUntil(t, func() (net.Conn, error) { return net.Dial("tcp", internal) })
	front := r.frontNode(&fakeCluster{self: "front", owner: store.Node{ID: "owner", IRCAddr: internal}}, secret)
	dialUntil(t, func() (net.Conn, error) { return net.Dial("tcp", front) }).Close()
	ic := r.sasl(front, "alice/two", "alice-password")
	ic.expect(" 903 ")
	ic.send("CAP END")
	ic.expect(" 001 alice_two ")
}
