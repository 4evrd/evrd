package bouncer

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/4evrd/evrd/internal/identd"
	"github.com/4evrd/evrd/internal/store"
	"github.com/4evrd/evrd/internal/upstream"
)

// addrConn is one end of a pipe that claims to be a TCP socket.
type addrConn struct {
	net.Conn
	local, remote net.Addr
}

func (c addrConn) LocalAddr() net.Addr  { return c.local }
func (c addrConn) RemoteAddr() net.Addr { return c.remote }

func tcpAddr(s string) net.Addr { return net.TCPAddrFromAddrPort(netip.MustParseAddrPort(s)) }

// fakeSocket is a network's connection as dialed: its addresses, and
// whether the server welcomes it (else it stays registering).
type fakeSocket struct {
	local, remote string
	welcome       bool
}

// identRig connects networks over pipes with the addresses given, keyed by
// the network's host.
func identRig(t *testing.T, sockets map[string]fakeSocket) *liveRig {
	t.Helper()
	r := newLiveRig(t)
	r.b.SetDialer(func(ctx context.Context, userID int64, bind, host string, port int) (net.Conn, error) {
		s, ok := sockets[host]
		if !ok {
			return nil, fmt.Errorf("no socket for %s", host)
		}
		ours, theirs := net.Pipe()
		// a pipe doesn't buffer: replies go out while the client writes on
		out := make(chan string, 16)
		go func() {
			for l := range out {
				io.WriteString(theirs, l)
			}
		}()
		go func() {
			defer theirs.Close()
			defer close(out)
			sc := bufio.NewScanner(theirs)
			nick := "x"
			for sc.Scan() {
				l := sc.Text()
				if n, ok := strings.CutPrefix(l, "NICK "); ok {
					nick = n
				}
				if strings.HasPrefix(l, "CAP LS") {
					out <- ":srv CAP * LS :\r\n"
				}
				if strings.HasPrefix(l, "USER ") && s.welcome {
					out <- ":srv 001 " + nick + " :Welcome\r\n"
				}
			}
		}()
		return addrConn{Conn: ours, local: tcpAddr(s.local), remote: tcpAddr(s.remote)}, nil
	})
	r.b.Start(r.ctx)
	return r
}

// identNet starts a network of its own user, whose ident is ident.
func (r *liveRig) identNet(host, ident string) store.Network {
	r.t.Helper()
	u := r.user(ident, nil)
	n, err := r.st.CreateNetwork(context.Background(), store.Network{UserID: u.ID, Name: host, Host: host, Port: 6667, Nick: ident + "_", Username: ident})
	if err != nil {
		r.t.Fatal(err)
	}
	if err := r.b.Connect(n.ID); err != nil {
		r.t.Fatal(err)
	}
	return n
}

// dialed waits for a network's socket to be up (and welcomed, if want).
func (r *liveRig) dialed(id int64, want upstream.Status) {
	r.t.Helper()
	eventually(r.t, fmt.Sprintf("network %d dialed and %s", id, want), func() bool {
		c, ok := r.b.ConnFor(id)
		if !ok {
			return false
		}
		_, _, up := c.Addrs()
		return up && c.Status() == want
	})
}

func ip(s string) netip.Addr { return netip.MustParseAddr(s) }

func TestIdentExactMatch(t *testing.T) {
	r := identRig(t, map[string]fakeSocket{
		"a": {"10.0.0.2:40001", "192.0.2.1:6667", true},
		"b": {"10.0.0.2:40002", "192.0.2.1:6667", true},
	})
	a, b := r.identNet("a", "alice"), r.identNet("b", "bob")
	r.dialed(a.ID, upstream.StatusConnected)
	r.dialed(b.ID, upstream.StatusConnected)

	for _, q := range []struct {
		from         string
		lport, fport int
		want         string
	}{
		{"192.0.2.1", 40001, 6667, "alice"},
		{"192.0.2.1", 40002, 6667, "bob"},
		{"::ffff:192.0.2.1", 40002, 6667, "bob"}, // a v4-mapped asker
		{"192.0.2.1", 40003, 6667, ""},           // both registered: no guess
		{"192.0.2.1", 40001, 6697, ""},           // not that server port
	} {
		got, ok := r.b.Ident(ip(q.from), q.lport, q.fport)
		if got != q.want || ok != (q.want != "") {
			t.Errorf("Ident(%s, %d, %d) = %q, %v; want %q", q.from, q.lport, q.fport, got, ok, q.want)
		}
	}
}

func TestIdentNATRewrotePort(t *testing.T) {
	r := identRig(t, map[string]fakeSocket{
		"a": {"172.18.0.5:40001", "192.0.2.1:6667", true},  // registered
		"b": {"172.18.0.5:40002", "192.0.2.1:6667", false}, // registering
	})
	a, b := r.identNet("a", "alice"), r.identNet("b", "bob")
	r.dialed(a.ID, upstream.StatusConnected)
	r.dialed(b.ID, upstream.StatusConnecting)
	// NAT gave bob's connection port 61000: the one registering answers
	if got, ok := r.b.Ident(ip("192.0.2.1"), 61000, 6667); got != "bob" || !ok {
		t.Errorf("NAT-rewritten port: %q, %v", got, ok)
	}
}

func TestIdentTwoRegisteringIsNoUser(t *testing.T) {
	r := identRig(t, map[string]fakeSocket{
		"a": {"172.18.0.5:40001", "192.0.2.1:6667", false},
		"b": {"172.18.0.5:40002", "192.0.2.1:6667", false},
	})
	a, b := r.identNet("a", "alice"), r.identNet("b", "bob")
	r.dialed(a.ID, upstream.StatusConnecting)
	r.dialed(b.ID, upstream.StatusConnecting)
	if got, ok := r.b.Ident(ip("192.0.2.1"), 61000, 6667); ok {
		t.Errorf("two registering, port rewritten: answered %q", got)
	}
	// the local port still tells them apart
	if got, _ := r.b.Ident(ip("192.0.2.1"), 40002, 6667); got != "bob" {
		t.Errorf("exact port: %q", got)
	}
}

func TestIdentAskedFromAnotherAddress(t *testing.T) {
	r := identRig(t, map[string]fakeSocket{
		"a": {"172.18.0.5:40001", "192.0.2.1:6697", false},
		"b": {"172.18.0.5:40002", "198.51.100.9:6667", false},
	})
	a, b := r.identNet("a", "alice"), r.identNet("b", "bob")
	r.dialed(a.ID, upstream.StatusConnecting)
	r.dialed(b.ID, upstream.StatusConnecting)
	// Docker's port mapping shows the query as from its gateway: the server
	// port still picks the one connection
	if got, ok := r.b.Ident(ip("172.18.0.1"), 40001, 6697); got != "alice" || !ok {
		t.Errorf("from the gateway: %q, %v", got, ok)
	}
	if got, ok := r.b.Ident(ip("172.18.0.1"), 50000, 6667); got != "bob" || !ok {
		t.Errorf("from the gateway, port rewritten: %q, %v", got, ok)
	}
	if got, ok := r.b.Ident(ip("172.18.0.1"), 40001, 7000); ok {
		t.Errorf("no connection to that port: %q", got)
	}
}

func TestIdentClearedWhenTheAttemptEnds(t *testing.T) {
	r := identRig(t, map[string]fakeSocket{"a": {"10.0.0.2:40001", "192.0.2.1:6667", false}})
	a := r.identNet("a", "alice")
	r.dialed(a.ID, upstream.StatusConnecting)
	c, _ := r.b.ConnFor(a.ID)
	if c.Ident() != "alice" {
		t.Fatalf("ident: %q", c.Ident())
	}
	r.b.Disconnect(a.ID)
	eventually(t, "addresses cleared", func() bool { _, _, ok := c.Addrs(); return !ok })
	if got, ok := r.b.Ident(ip("192.0.2.1"), 40001, 6667); ok {
		t.Errorf("after disconnecting: %q", got)
	}
}

// TestIdentdAnswersTheServer is the whole path: an IRC server that, as a
// network connects, asks evrd's identd about it.
func TestIdentdAnswersTheServer(t *testing.T) {
	r := newLiveRig(t)
	r.b.Start(r.ctx)

	idln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go identd.New(r.b.Ident, slog.New(slog.NewTextHandler(io.Discard, nil))).Serve(r.ctx, idln)

	ircln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ircln.Close() })
	replies := make(chan string, 1)
	go func() {
		c, err := ircln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		// asked once the client starts registering, as servers do
		sc := bufio.NewScanner(c)
		for sc.Scan() && !strings.HasPrefix(sc.Text(), "USER ") {
		}
		lport := c.RemoteAddr().(*net.TCPAddr).Port
		fport := c.LocalAddr().(*net.TCPAddr).Port
		q, err := net.Dial("tcp", idln.Addr().String())
		if err != nil {
			replies <- err.Error()
			return
		}
		defer q.Close()
		q.SetDeadline(time.Now().Add(5 * time.Second))
		fmt.Fprintf(q, "%d , %d\r\n", lport, fport)
		reply, _ := bufio.NewReader(q).ReadString('\n')
		replies <- fmt.Sprintf("%d , %d -> %s", lport, fport, reply)
		for sc.Scan() {
		}
	}()

	u := r.user("alice", nil)
	_, port, _ := net.SplitHostPort(ircln.Addr().String())
	var p int
	fmt.Sscan(port, &p)
	n, err := r.st.CreateNetwork(context.Background(), store.Network{UserID: u.ID, Name: "net", Host: "127.0.0.1", Port: p, Nick: "alice", Username: "al!ce~x"})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.b.Connect(n.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-replies:
		lp, fp, _ := strings.Cut(got, " -> ")
		if want := lp + " : USERID : UNIX : alcex\r\n"; fp != want {
			t.Errorf("ident reply: %q, want %q", fp, want)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the server was never answered")
	}
}
