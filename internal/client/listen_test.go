package client

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/4evrd/evrd/internal/cluster"
	"github.com/4evrd/evrd/internal/history"
	"github.com/4evrd/evrd/internal/store"
)

// freeAddr is a local address nothing listens on (yet).
func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	return addr
}

// dialUntil dials addr until a listener is up.
func dialUntil(t *testing.T, dial func() (net.Conn, error)) net.Conn {
	t.Helper()
	var c net.Conn
	eventually(t, "listener up", func() bool {
		var err error
		c, err = dial()
		return err == nil
	})
	t.Cleanup(func() { c.Close() })
	return c
}

func (r *rig) register(c net.Conn, pass string) *ircClient {
	ic := &ircClient{t: r.t, c: c, sc: bufio.NewScanner(c)}
	ic.send("PASS " + pass)
	ic.send("NICK alice")
	ic.send("USER alice 0 * :alice")
	return ic
}

func TestTLSListener(t *testing.T) {
	r := newRig(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx, cancel := context.WithCancel(r.ctx)
	t.Cleanup(cancel)

	// no certificate configured: a self-signed one
	addr := freeAddr(t)
	go NewServer(r.b, r.acc, log, "", "").ListenAndServe(ctx, []string{"ircs://" + addr, "ws+insecure://" + freeAddr(t)})
	c := dialUntil(t, func() (net.Conn, error) {
		return tls.Dial("tcp", addr, &tls.Config{InsecureSkipVerify: true})
	})
	cert := c.(*tls.Conn).ConnectionState().PeerCertificates[0]
	if cert.Subject.CommonName != "evrd" {
		t.Fatalf("certificate: %v", cert.Subject)
	}
	r.register(c, "alice/one:alice-password").expect(" 001 alice_one ")

	// a certificate given by SetTLSConfig
	self, err := selfSignedCert()
	if err != nil {
		t.Fatal(err)
	}
	srv := NewServer(r.b, r.acc, log, "", "")
	srv.SetTLSConfig(&tls.Config{Certificates: []tls.Certificate{self}})
	addr2 := freeAddr(t)
	go srv.listen(ctx, "tls://"+addr2)
	c2 := dialUntil(t, func() (net.Conn, error) {
		return tls.Dial("tcp", addr2, &tls.Config{InsecureSkipVerify: true})
	})
	if got := c2.(*tls.Conn).ConnectionState().PeerCertificates[0].Raw; string(got) != string(self.Certificate[0]) {
		t.Fatal("SetTLSConfig's certificate wasn't used")
	}

	// a certificate file that doesn't exist: the listener isn't started
	bad := NewServer(r.b, r.acc, log, filepath.Join(t.TempDir(), "no.crt"), filepath.Join(t.TempDir(), "no.key"))
	done := make(chan struct{})
	go func() { bad.listen(ctx, "ircs://"+freeAddr(t)); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("listen with a missing certificate didn't give up")
	}
	if _, err := bad.tlsConfig(); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("tls error: %v", err)
	}
}

type fakeCluster struct {
	self  string
	owner store.Node
	err   error
}

func (f *fakeCluster) Self() store.Node       { return store.Node{ID: f.self} }
func (f *fakeCluster) Owns(userID int64) bool { return false }
func (f *fakeCluster) WaitOwner(ctx context.Context, userID int64, max time.Duration) (store.Node, error) {
	return f.owner, f.err
}

// frontNode starts a listener that believes another node owns every user.
func (r *rig) frontNode(cl Cluster, secret string) string {
	r.t.Helper()
	srv := NewServer(r.b, r.acc, slog.New(slog.NewTextHandler(io.Discard, nil)), "", "")
	srv.SetCluster(cl, secret)
	addr := freeAddr(r.t)
	go srv.listen(r.ctx, "irc://"+addr)
	return addr
}

func TestForwardingToTheOwner(t *testing.T) {
	r := newRig(t)
	// the rig's server is the owner, taking forwarded clients internally
	const secret = "cluster-secret"
	r.srv.SetCluster(&fakeCluster{self: "owner"}, secret)
	internal := freeAddr(t)
	go r.srv.ListenInternal(r.ctx, internal)
	dialUntil(t, func() (net.Conn, error) { return net.Dial("tcp", internal) })

	// the client's caps and address travel with it
	u := r.user
	u.AllowedHosts = []string{"127.0.0.1"}
	r.st.UpdateUser(u)
	front := r.frontNode(&fakeCluster{self: "front", owner: store.Node{ID: "owner", IRCAddr: internal}}, secret)
	c := dialUntil(t, func() (net.Conn, error) { return net.Dial("tcp", front) })
	ic := &ircClient{t: t, c: c, sc: bufio.NewScanner(c)}
	ic.send("CAP LS 302")
	ic.send("CAP REQ :server-time")
	ic.send("PASS alice/one:alice-password")
	ic.send("NICK alice")
	ic.send("USER alice 0 * :alice")
	ic.send("CAP END")
	ic.send("PRIVMSG *status :version") // sent before the splice: passed on
	ic.expect(" 001 alice_one ")
	ic.expect("PRIVMSG alice_one :evrd ")
	eventually(t, "attached on the owner", func() bool {
		cs := r.b.ClientsOf(r.user.ID)
		return len(cs) == 1 && cs[0].IP == "127.0.0.1"
	})
	// server-time was negotiated on the front node
	r.hist.Add(historyLine(r.network("one").ID, "#one", "tagged"))
	ic.send("PRIVMSG *status :playback #one 1")
	ic.expect("@time=")
}

func TestForwardedAddressIsChecked(t *testing.T) {
	r := newRig(t)
	const secret = "cluster-secret"
	r.srv.SetCluster(&fakeCluster{self: "owner"}, secret)
	internal := freeAddr(t)
	go r.srv.ListenInternal(r.ctx, internal)
	u := r.user
	u.AllowedHosts = []string{"10.0.0.0/8"}
	r.st.UpdateUser(u)

	// the original address counts, not the forwarding node's
	c := dialUntil(t, func() (net.Conn, error) { return net.Dial("tcp", internal) })
	io.WriteString(c, "EVRD-PROXY "+secret+" 10.1.2.3 echo-message,bogus\r\n")
	ic := r.register(c, "alice/one:alice-password")
	ic.expect(" 001 alice_one ")
	ic.send("CAP LIST")
	ic.expect("LIST :echo-message")

	denied := dialUntil(t, func() (net.Conn, error) { return net.Dial("tcp", internal) })
	io.WriteString(denied, "EVRD-PROXY "+secret+" 192.0.2.1 -\r\n")
	r.register(denied, "alice/one:alice-password").expect("Signing in from 192.0.2.1 isn't allowed")

	// a wrong secret or no introduction: hung up on
	for _, intro := range []string{"EVRD-PROXY wrong 10.1.2.3 -", "NICK alice"} {
		c := dialUntil(t, func() (net.Conn, error) { return net.Dial("tcp", internal) })
		io.WriteString(c, intro+"\r\n")
		ic := r.register(c, "alice/one:alice-password")
		if !ic.closed() {
			t.Fatalf("%q: not hung up on", intro)
		}
	}
}

func TestForwardingFallbacks(t *testing.T) {
	r := newRig(t)
	for _, c := range []struct {
		name string
		cl   *fakeCluster
		want string
	}{
		// nobody owns the user (suspended, say): this node says why
		{"no owner", &fakeCluster{self: "front", err: cluster.ErrNoOwner}, " 001 alice_one "},
		// ownership came here meanwhile
		{"now ours", &fakeCluster{self: "front", owner: store.Node{ID: "front"}}, " 001 alice_one "},
		{"lookup failed", &fakeCluster{self: "front", err: errors.New("db down")}, movingMsg},
		{"owner without IRC", &fakeCluster{self: "front", owner: store.Node{ID: "owner"}}, movingMsg},
		{"owner unreachable", &fakeCluster{self: "front", owner: store.Node{ID: "owner", IRCAddr: freeAddr(t)}}, movingMsg},
	} {
		front := r.frontNode(c.cl, "s")
		conn := dialUntil(t, func() (net.Conn, error) { return net.Dial("tcp", front) })
		ic := r.register(conn, "alice/one:alice-password")
		ic.expect(c.want)
		conn.Close()
		eventually(t, c.name+": detached", func() bool { return r.b.Clients(r.user.ID) == 0 })
	}
	// unknown users aren't forwarded; attaching refuses them
	front := r.frontNode(&fakeCluster{self: "front"}, "s")
	conn := dialUntil(t, func() (net.Conn, error) { return net.Dial("tcp", front) })
	r.register(conn, "nobody:x").expect("Invalid credentials")
}

func historyLine(networkID int64, target, text string) history.Message {
	return history.Message{NetworkID: networkID, Target: target, Sender: "bob", Kind: "message", Text: text, Time: time.Now()}
}
