package links

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net"
	"strings"
	"testing"
	"time"
)

// handOver gives h a fresh listener on 127.0.0.1 and returns its address.
func handOver(t *testing.T, cl *Client, tlsOn bool) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	if err := cl.Listen(context.Background(), ln, addr, tlsOn); err != nil {
		t.Fatal(err)
	}
	return addr
}

// watch starts evrd's clients stream; the first value is the end of those
// waiting already.
func watch(t *testing.T, cl *Client) (<-chan HeldClient, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	ch, err := cl.Clients(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return ch, cancel
}

func next(t *testing.T, ch <-chan HeldClient) HeldClient {
	t.Helper()
	select {
	case hc, ok := <-ch:
		if !ok {
			t.Fatal("clients stream closed")
		}
		return hc
	case <-time.After(5 * time.Second):
		t.Fatal("no client announced")
	}
	return HeldClient{}
}

// ircClient is a client of the holder's listener.
type ircClient struct {
	net.Conn
	r *bufio.Reader
}

func dialIRC(t *testing.T, addr string) *ircClient {
	t.Helper()
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return &ircClient{c, bufio.NewReader(c)}
}

func (c *ircClient) send(line string) { io.WriteString(c, line+"\r\n") }

func (c *ircClient) expect(t *testing.T, want string) {
	t.Helper()
	c.SetReadDeadline(time.Now().Add(5 * time.Second))
	got, err := c.r.ReadString('\n')
	if err != nil || strings.TrimRight(got, "\r\n") != want {
		t.Fatalf("client got %q (%v), want %q", got, err, want)
	}
}

func (c *ircClient) closed(t *testing.T) string {
	t.Helper()
	c.SetReadDeadline(time.Now().Add(5 * time.Second))
	rest, err := io.ReadAll(c.r)
	if err != nil {
		t.Fatalf("not closed: %v (%q)", err, rest)
	}
	return string(rest)
}

func readFrom(t *testing.T, r *bufio.Reader, conn net.Conn) string {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	l, err := r.ReadString('\n')
	if err != nil {
		t.Fatalf("evrd's stream: %v", err)
	}
	return strings.TrimRight(l, "\r\n")
}

func TestClientHeldOverARestart(t *testing.T) {
	h := quietHolder()
	cl, _ := serveHolder(t, h)
	addr := handOver(t, cl, false)
	if ls, err := cl.Listeners(context.Background()); err != nil || len(ls) != 1 || ls[0].Addr != addr || ls[0].TLS {
		t.Fatalf("listeners %+v, %v", ls, err)
	}

	// evrd #1: told of a new client, which registers
	ch, stop := watch(t, cl)
	if hc := next(t, ch); hc.Client != "" {
		t.Fatalf("waiting before any client: %+v", hc)
	}
	c := dialIRC(t, addr)
	c.send("NICK alice")
	hc := next(t, ch)
	if hc.State != nil || !strings.HasPrefix(hc.Addr, "127.0.0.1:") || hc.Local != addr {
		t.Fatalf("announced %+v", hc)
	}
	cc, err := cl.AttachClient(context.Background(), hc)
	if err != nil {
		t.Fatal(err)
	}
	r := bufio.NewReader(cc)
	if got := readFrom(t, r, cc); got != "NICK alice" {
		t.Fatalf("evrd got %q", got)
	}
	if cc.RemoteAddr().String() != hc.Addr || cc.LocalAddr().String() != addr || cc.Secure() {
		t.Fatalf("addrs %v %v", cc.RemoteAddr(), cc.LocalAddr())
	}
	io.WriteString(cc, ":evrd 001 alice :Welcome\r\n"+SaveState([]byte(`{"v":1}`))+"\n")
	c.expect(t, ":evrd 001 alice :Welcome")

	// evrd stops: the client stays, its PINGs answered, the rest kept
	stop()
	cc.Detach()
	time.Sleep(50 * time.Millisecond)
	c.send("PING :tok")
	c.expect(t, ":evrd PONG evrd :tok")
	c.send("PRIVMSG #chan :while away")

	// evrd #2 gets it back, with its state and what it said
	ch, _ = watch(t, cl)
	hc = next(t, ch)
	if string(hc.State) != `{"v":1}` {
		t.Fatalf("held %+v", hc)
	}
	if end := next(t, ch); end.Client != "" {
		t.Fatalf("after the waiting ones: %+v", end)
	}
	cc, err = cl.AttachClient(context.Background(), hc)
	if err != nil {
		t.Fatal(err)
	}
	r = bufio.NewReader(cc)
	if got := readFrom(t, r, cc); got != "PRIVMSG #chan :while away" {
		t.Fatalf("evrd got %q", got)
	}
	io.WriteString(cc, ":bob PRIVMSG alice :hi\r\n")
	c.expect(t, ":bob PRIVMSG alice :hi")
	c.send("PING :live")
	if got := readFrom(t, r, cc); got != "PING :live" {
		t.Fatalf("attached: PING went to %q", got)
	}

	// evrd closes it: what came first is written, then it hangs up
	io.WriteString(cc, "ERROR :bye\r\n")
	cc.Close()
	if rest := c.closed(t); rest != "ERROR :bye\r\n" {
		t.Fatalf("at close: %q", rest)
	}
	eventually(t, "client gone", func() bool {
		_, err := cl.AttachClient(context.Background(), hc)
		return errors.Is(err, ErrNoClient)
	})
}

func TestUnregisteredClientIsNotHeld(t *testing.T) {
	cl, _ := serveHolder(t, quietHolder())
	addr := handOver(t, cl, false)
	ch, _ := watch(t, cl)
	next(t, ch)
	c := dialIRC(t, addr)
	cc, err := cl.AttachClient(context.Background(), next(t, ch))
	if err != nil {
		t.Fatal(err)
	}
	cc.Detach() // evrd went before it signed in: no state saved
	c.closed(t)
}

func TestClientQuitWhileWaiting(t *testing.T) {
	h := quietHolder()
	cl, _ := serveHolder(t, h)
	addr := handOver(t, cl, false)
	c := dialIRC(t, addr) // no evrd watching yet: it waits
	c.send("QUIT :bye")
	c.closed(t)
}

func TestClientTooChattyWhileWaiting(t *testing.T) {
	h := quietHolder()
	h.MaxClientBuffer = 100
	cl, _ := serveHolder(t, h)
	addr := handOver(t, cl, false)
	c := dialIRC(t, addr)
	for i := 0; i < 10; i++ {
		c.send("PRIVMSG #chan :" + strings.Repeat("x", 20))
	}
	if rest := c.closed(t); rest != reconnectLine {
		t.Fatalf("got %q", rest)
	}
}

func TestClientLineTooLong(t *testing.T) {
	cl, _ := serveHolder(t, quietHolder())
	addr := handOver(t, cl, false)
	c := dialIRC(t, addr)
	c.Write([]byte(strings.Repeat("x", maxClientLine+5000)))
	c.closed(t)
}

func TestHeldClientExpires(t *testing.T) {
	h := quietHolder()
	h.HoldFor = 50 * time.Millisecond
	cl, _ := serveHolder(t, h)
	addr := handOver(t, cl, false)
	c := dialIRC(t, addr)
	if rest := c.closed(t); rest != reconnectLine {
		t.Fatalf("got %q", rest)
	}
}

func TestStoppingTheHolderClosesClients(t *testing.T) {
	cl, stop := serveHolder(t, quietHolder())
	addr := handOver(t, cl, false)
	c := dialIRC(t, addr)
	time.Sleep(50 * time.Millisecond)
	stop()
	if rest := c.closed(t); rest != reconnectLine {
		t.Fatalf("got %q", rest)
	}
	if _, err := net.Dial("tcp", addr); err == nil {
		t.Fatal("still listening")
	}
}

func TestNewerEvrdTakesTheClientOver(t *testing.T) {
	cl, _ := serveHolder(t, quietHolder())
	addr := handOver(t, cl, false)
	ch, _ := watch(t, cl)
	next(t, ch)
	c := dialIRC(t, addr)
	hc := next(t, ch)
	old, err := cl.AttachClient(context.Background(), hc)
	if err != nil {
		t.Fatal(err)
	}
	io.WriteString(old, SaveState([]byte(`{}`))+"\n")
	time.Sleep(20 * time.Millisecond)
	cc, err := cl.AttachClient(context.Background(), hc)
	if err != nil {
		t.Fatal(err)
	}
	old.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := old.Read(make([]byte, 1)); err == nil {
		t.Fatal("the old stream still open")
	}
	c.send("NICK x")
	if got := readFrom(t, bufio.NewReader(cc), cc); got != "NICK x" {
		t.Fatalf("got %q", got)
	}
}

func TestReplacingAListener(t *testing.T) {
	cl, _ := serveHolder(t, quietHolder())
	addr := handOver(t, cl, false)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	if err := cl.Listen(context.Background(), ln, addr, false); err != nil {
		t.Fatal(err)
	}
	if ls, _ := cl.Listeners(context.Background()); len(ls) != 1 {
		t.Fatalf("listeners %+v", ls)
	}
}

func TestTLSClients(t *testing.T) {
	cl, _ := serveHolder(t, quietHolder())
	addr := handOver(t, cl, true)
	// no certificate yet: the handshake fails
	if _, err := tls.Dial("tcp", addr, &tls.Config{InsecureSkipVerify: true}); err == nil {
		t.Fatal("TLS without a certificate")
	}
	if err := cl.SetCert(context.Background(), []byte("junk"), []byte("junk")); err == nil {
		t.Fatal("junk certificate taken")
	}
	certPEM, keyPEM := testCert(t)
	if err := cl.SetCert(context.Background(), certPEM, keyPEM); err != nil {
		t.Fatal(err)
	}
	ch, _ := watch(t, cl)
	next(t, ch)
	tc, err := tls.Dial("tcp", addr, &tls.Config{InsecureSkipVerify: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tc.Close()
	hc := next(t, ch)
	if !hc.TLS {
		t.Fatalf("announced %+v", hc)
	}
	cc, err := cl.AttachClient(context.Background(), hc)
	if err != nil {
		t.Fatal(err)
	}
	if !cc.Secure() {
		t.Fatal("not secure")
	}
	io.WriteString(tc, "NICK tls\r\n")
	if got := readFrom(t, bufio.NewReader(cc), cc); got != "NICK tls" {
		t.Fatalf("got %q", got)
	}
}

func TestHello(t *testing.T) {
	ctx := context.Background()
	h := quietHolder()
	h.Hash = "0123456789ab"
	cl, _ := serveHolder(t, h)
	if hi, err := cl.Hello(ctx); err != nil || hi.Protocol != Version || hi.Hash != h.Hash {
		t.Fatalf("hello: %+v, %v", hi, err)
	}
	// protocol 1, and protocol 2 from before hello: the hash is unknown
	for answer, want := range map[string]int{`{"error":"bad request or protocol version"}`: 1, `{"error":"unknown op"}`: 2} {
		if hi, err := fakeHolder(t, answer+"\n").Hello(ctx); err != nil || hi.Protocol != want || hi.Hash != "" {
			t.Errorf("%s: %+v, %v", answer, hi, err)
		}
	}
	if _, err := fakeHolder(t, `{"error":"nope"}`+"\n").Hello(ctx); err == nil {
		t.Error("an error answer: no error")
	}
	if _, err := NewClient(sockPath(t)).Hello(ctx); !errors.Is(err, ErrUnavailable) {
		t.Errorf("no holder: %v", err)
	}
}

func TestOldHolderHoldsNoClients(t *testing.T) {
	cl := fakeHolder(t, `{"error":"bad request or protocol version"}`+"\n")
	if _, err := cl.Listeners(context.Background()); !errors.Is(err, ErrNoClients) {
		t.Fatalf("Listeners: %v", err)
	}
	if _, err := cl.Clients(context.Background()); !errors.Is(err, ErrNoClients) {
		t.Fatalf("Clients: %v", err)
	}
}

func TestClientOpErrors(t *testing.T) {
	cl := fakeHolder(t, `{"error":"nope"}`+"\n")
	if err := cl.SetCert(context.Background(), nil, nil); err == nil || !strings.Contains(err.Error(), "nope") {
		t.Fatalf("SetCert: %v", err)
	}
	if _, err := cl.Clients(context.Background()); err == nil {
		t.Fatal("Clients: no error")
	}
	if _, err := cl.AttachClient(context.Background(), HeldClient{Client: "c1"}); err == nil {
		t.Fatal("AttachClient: no error")
	}
	gone := NewClient(sockPath(t))
	if _, err := gone.Listeners(context.Background()); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("no holder: %v", err)
	}
	if _, err := gone.AttachClient(context.Background(), HeldClient{Client: "c1"}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("no holder: %v", err)
	}
	if _, err := gone.Clients(context.Background()); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("no holder: %v", err)
	}
}

func TestListenErrors(t *testing.T) {
	cl, _ := serveHolder(t, quietHolder())
	if resp := raw(t, cl, `{"version":2,"op":"listen","addr":":1"}`, -1); resp.Error != "listen needs a socket" {
		t.Fatalf("got %+v", resp)
	}
	if err := cl.Listen(context.Background(), nopListener{}, ":1", false); err == nil {
		t.Fatal("not a socket taken")
	}
	if _, err := cl.AttachClient(context.Background(), HeldClient{Client: "c999"}); !errors.Is(err, ErrNoClient) {
		t.Fatalf("unknown client: %v", err)
	}
}

func TestTCPAddr(t *testing.T) {
	if a := tcpAddr("[::1]:4353").String(); a != "[::1]:4353" {
		t.Fatal(a)
	}
	if a := tcpAddr(":4353").String(); a != ":4353" {
		t.Fatal(a)
	}
}

type nopListener struct{}

func (nopListener) Accept() (net.Conn, error) { return nil, errors.New("no") }
func (nopListener) Close() error              { return nil }
func (nopListener) Addr() net.Addr            { return &net.TCPAddr{} }

func testCert(t *testing.T) (certPEM, keyPEM []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "evrd"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), DNSNames: []string{"evrd"}}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	kder, _ := x509.MarshalPKCS8PrivateKey(key)
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: kder})
}
