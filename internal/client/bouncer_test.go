package client

import (
	"net"
	"strconv"
	"strings"
	"testing"

	"github.com/4evrd/evrd/internal/irc"
	"github.com/4evrd/evrd/internal/store"
)

// bouncerConn signs in with SASL as authcid, asking for caps, sends the
// lines before CAP END, and waits for the MOTD's end.
func (r *rig) bouncerConn(addr, authcid string, caps []string, before ...string) *ircClient {
	r.t.Helper()
	ic := r.dialRaw(addr)
	ic.send("CAP LS 302")
	ic.send("CAP REQ :" + strings.Join(append([]string{"sasl"}, caps...), " "))
	ic.send("AUTHENTICATE PLAIN")
	ic.send("AUTHENTICATE " + plain("", authcid, "alice-password"))
	ic.expect(" 903 ")
	for _, l := range before {
		ic.send(l)
	}
	ic.send("NICK alice")
	ic.send("USER alice 0 * :alice")
	ic.send("CAP END")
	return ic
}

func (r *rig) netID(name string) string { return strconv.FormatInt(r.network(name).ID, 10) }

var networksCaps = []string{"soju.im/bouncer-networks", "soju.im/bouncer-networks-notify", "batch"}

func TestBouncerConnection(t *testing.T) {
	r := newRig(t)
	one, two := r.netID("one"), r.netID("two")
	ic := r.bouncerConn(r.addr, "alice", networksCaps)
	ic.expect(" 001 alice :Welcome to evrd - a bouncer connection, on no network")
	ic.expect(" 005 alice BOUNCER_NETID :are supported")
	// the networks, at once (notify)
	ic.expect("BATCH +1 soju.im/bouncer-networks")
	l := ic.expect("BOUNCER NETWORK " + one + " ")
	if !strings.HasPrefix(l, "@batch=1 ") || !strings.Contains(l, "name=one;nickname=alice_one;port=") || !strings.Contains(l, "state=disconnected;tls=0") {
		t.Fatalf("network: %q", l)
	}
	ic.expect("BOUNCER NETWORK " + two + " ")
	ic.expect("BATCH -1")
	ic.send("PRIVMSG #one :hi")
	ic.expect("NOTICE alice :This is a bouncer connection, on no network")

	ic.send("BOUNCER LISTNETWORKS")
	ic.expect("BATCH +2 soju.im/bouncer-networks")
	ic.expect("BATCH -2")

	// a bound connection: the network's state changes, and the bouncer
	// connection hears it
	bound := r.bouncerConn(r.addr, "alice", []string{"soju.im/bouncer-networks"}, "BOUNCER BIND "+two)
	bound.expect(" 001 alice_two :Welcome to evrd - attached to two")
	bound.expect(" 005 alice_two BOUNCER_NETID=" + two + " :are supported")
	bound.expect("JOIN #two")
	ic.expect("BOUNCER NETWORK " + two + " state=connected")
	bound.send("BOUNCER BIND " + one)
	bound.expect("FAIL BOUNCER REGISTRATION_IS_COMPLETED BIND :")

	// add, change, remove
	ic.send("BOUNCER ADDNETWORK host=irc.example.org;name=three;tls=0;realname=Al\\sIce")
	added := irc.Parse(ic.expect("BOUNCER ADDNETWORK "))
	three := added.Params[1]
	// (it may already say it can't connect)
	if l := ic.expect("BOUNCER NETWORK " + three + " "); !strings.Contains(l, "host=irc.example.org;name=three;nickname=alice;port=6667;realname=Al\\sIce;") {
		t.Fatalf("new network: %q", l)
	}
	if n := r.network("three"); n.Port != 6667 || n.TLS || n.Realname != "Al Ice" {
		t.Fatalf("added %+v", n)
	}
	ic.send("BOUNCER CHANGENETWORK " + three + " nickname=al;pass=sekrit")
	ic.expect("BOUNCER CHANGENETWORK " + three)
	for {
		attrs := parseAttrs(irc.Parse(ic.expect("BOUNCER NETWORK " + three + " ")).Params[2])
		if attrs["nickname"] == "al" {
			break
		}
	}
	if n := r.network("three"); n.Nick != "al" || n.ServerList()[0].Password != "sekrit" {
		t.Fatalf("changed %+v", n)
	}
	ic.send("BOUNCER DELNETWORK " + three)
	ic.expect("BOUNCER DELNETWORK " + three)
	ic.expect("BOUNCER NETWORK " + three + " *")
}

func TestBouncerErrors(t *testing.T) {
	r := newRig(t)
	one := r.netID("one")
	ic := r.bouncerConn(r.addr, "alice", networksCaps, "BOUNCER LISTNETWORKS")
	ic.expect("FAIL BOUNCER ACCOUNT_REQUIRED LISTNETWORKS :Sign in first")
	ic.expect(" 001 ")
	for cmd, want := range map[string]string{
		"BOUNCER":                                   "FAIL BOUNCER UNKNOWN_COMMAND * :Unknown subcommand",
		"BOUNCER FROB":                              "FAIL BOUNCER UNKNOWN_COMMAND FROB :Unknown subcommand",
		"BOUNCER ADDNETWORK":                        "FAIL BOUNCER NEED_ATTRIBUTE ADDNETWORK * host :",
		"BOUNCER ADDNETWORK name=x":                 "FAIL BOUNCER NEED_ATTRIBUTE ADDNETWORK * host :A host is needed",
		"BOUNCER ADDNETWORK host=h;port=abc":        "FAIL BOUNCER INVALID_ATTRIBUTE ADDNETWORK * port :Invalid port",
		"BOUNCER ADDNETWORK host=h;tls=2":           "FAIL BOUNCER INVALID_ATTRIBUTE ADDNETWORK * tls :",
		"BOUNCER ADDNETWORK host=h;state=connected": "FAIL BOUNCER READ_ONLY_ATTRIBUTE ADDNETWORK * state :",
		"BOUNCER ADDNETWORK host=h;colour=red":      "FAIL BOUNCER UNKNOWN_ATTRIBUTE ADDNETWORK * colour :",
		"BOUNCER ADDNETWORK host=h;name=one":        "FAIL BOUNCER INVALID_ATTRIBUTE ADDNETWORK * name :You already have a network called one",
		"BOUNCER ADDNETWORK host=h;name=a/b":        "FAIL BOUNCER INVALID_ATTRIBUTE ADDNETWORK * * :a network needs a name",
		"BOUNCER CHANGENETWORK 999 name=x":          "FAIL BOUNCER INVALID_NETID CHANGENETWORK 999 :Unknown network ID",
		"BOUNCER CHANGENETWORK " + one:              "FAIL BOUNCER NEED_ATTRIBUTE CHANGENETWORK * :",
		"BOUNCER CHANGENETWORK " + one + " error=x": "FAIL BOUNCER READ_ONLY_ATTRIBUTE CHANGENETWORK " + one + " error :",
		"BOUNCER DELNETWORK":                        "FAIL BOUNCER INVALID_NETID DELNETWORK * :",
		"BOUNCER DELNETWORK 999":                    "FAIL BOUNCER INVALID_NETID DELNETWORK 999 :",
		"BOUNCER BIND " + one:                       "FAIL BOUNCER REGISTRATION_IS_COMPLETED BIND :",
	} {
		ic.send(cmd)
		ic.expect(want)
	}
	// the network limit (3 in the rig)
	ic.send("BOUNCER ADDNETWORK host=h;name=three")
	ic.expect("BOUNCER ADDNETWORK ")
	ic.send("BOUNCER ADDNETWORK host=h;name=four")
	ic.expect("FAIL BOUNCER UNKNOWN_ERROR ADDNETWORK * :")

	// BIND to a network that isn't there
	bad := r.bouncerConn(r.addr, "alice", networksCaps, "BOUNCER BIND 999")
	bad.expect("FAIL BOUNCER INVALID_NETID BIND 999 :Unknown network ID")
	bad.expect(" 464 ")

	// without the capability, BOUNCER is unknown
	plainC := r.attached()
	plainC.send("BOUNCER LISTNETWORKS")
	plainC.expect("FAIL BOUNCER UNKNOWN_COMMAND LISTNETWORKS :Unknown subcommand")
}

func TestBouncerBindThroughAForwardingNode(t *testing.T) {
	r := newRig(t)
	const secret = "cluster-secret"
	r.srv.SetCluster(&fakeCluster{self: "owner"}, secret)
	internal := freeAddr(t)
	go r.srv.ListenInternal(r.ctx, internal)
	dialUntil(t, func() (net.Conn, error) { return net.Dial("tcp", internal) })
	front := r.frontNode(&fakeCluster{self: "front", owner: store.Node{ID: "owner", IRCAddr: internal}}, secret)
	dialUntil(t, func() (net.Conn, error) { return net.Dial("tcp", front) }).Close()
	ic := r.bouncerConn(front, "alice", networksCaps, "BOUNCER BIND "+r.netID("two"))
	ic.expect(" 001 alice_two ")
	ic.expect("BOUNCER_NETID=" + r.netID("two"))
}

func TestAttrs(t *testing.T) {
	in := map[string]string{"a": "x y;z\\w", "b": "line\r\nnext", "c": ""}
	enc := encodeAttrs(in)
	if enc != `a=x\sy\:z\\w;b=line\r\nnext;c=` {
		t.Fatalf("encoded %q", enc)
	}
	back := parseAttrs(enc + ";;d")
	if back["a"] != in["a"] || back["b"] != in["b"] || back["c"] != "" || len(back) != 4 {
		t.Fatalf("parsed %q", back)
	}
	if got := unescapeTag(`trail\`); got != "trail" {
		t.Fatalf("trailing backslash: %q", got)
	}
}
