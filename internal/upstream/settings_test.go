package upstream

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"io"
	"log/slog"
	"math/big"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/4evrd/evrd/internal/store"
	"github.com/4evrd/evrd/internal/testirc"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func eventually(t *testing.T, what string, f func() bool) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if f() {
			return
		}
	}
	t.Fatalf("never: %s", what)
}

func TestJoinLines(t *testing.T) {
	got := joinLines([]string{"#a", "#b", "#c", "#d"}, map[string]store.ChannelSettings{
		"#b": {Key: "kb"}, "#c": {Disabled: true}, "#d": {Key: "kd"},
	})
	want := []string{"JOIN #b,#d kb,kd", "JOIN #a"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestCodec(t *testing.T) {
	latin1 := []byte("caf\xe9") // "café" in ISO-8859-1
	if got := newCodec("").decode(latin1); got != string(latin1) {
		t.Fatalf("no encoding changed the line: %q", got)
	}
	fb := newCodec("^ISO-8859-1")
	if got := fb.decode(latin1); got != "café" {
		t.Fatalf("fallback decode: %q", got)
	}
	if got := fb.decode([]byte("naïve")); got != "naïve" {
		t.Fatalf("fallback left UTF-8 alone: %q", got)
	}
	if got := fb.encode("café"); got != "café" {
		t.Fatalf("fallback sends UTF-8: %q", got)
	}
	force := newCodec("ISO-8859-1")
	if got := force.encode("café"); got != string(latin1) {
		t.Fatalf("forced encode: %q", got)
	}
}

func TestFloodPacesAfterTheBurst(t *testing.T) {
	var f flood
	f.set(20, 2) // 2 at once, then one every 50ms
	start := time.Now()
	for range 5 {
		f.wait()
	}
	if d := time.Since(start); d < 140*time.Millisecond || d > time.Second {
		t.Fatalf("5 lines at 20/s with burst 2 took %v, want ~150ms", d)
	}
	f.set(0, 9)
	start = time.Now()
	for range 100 {
		f.wait()
	}
	if time.Since(start) > 50*time.Millisecond {
		t.Fatal("rate 0 should turn flood protection off")
	}
	for _, l := range []string{"PONG :x", "CAP END", "QUIT :bye"} {
		if !exempt(l) {
			t.Errorf("%q should skip flood protection", l)
		}
	}
	if exempt("PRIVMSG #a :hi") {
		t.Error("PRIVMSG should be paced")
	}
}

// selfSigned makes a certificate for localhost.
func selfSigned(t *testing.T) (tls.Certificate, *x509.Certificate) {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "localhost"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		DNSNames: []string{"localhost"},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, _ := x509.ParseCertificate(der)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, cert
}

// handshake runs a TLS handshake against a server with cert and reports the
// client side's error.
func handshake(t *testing.T, c *Conn, cert tls.Certificate) error {
	t.Helper()
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	go tls.Server(b, &tls.Config{Certificates: []tls.Certificate{cert}}).Handshake()
	return tls.Client(a, c.tlsConfig("localhost")).Handshake()
}

func TestCertificateTrust(t *testing.T) {
	cert, leaf := selfSigned(t)
	fp := Fingerprint(leaf)

	c := New(store.Network{TrustPKI: true}, quiet, Options{})
	err := handshake(t, c, cert)
	var ue *UntrustedCertError
	if !errors.As(err, &ue) || ue.Fingerprint != fp {
		t.Fatalf("a self-signed cert should be refused with its fingerprint: %v", err)
	}
	if c.Health().UntrustedFingerprint != fp {
		t.Fatal("the refused fingerprint isn't reported")
	}
	if !strings.Contains(err.Error(), ColonHex(fp)) {
		t.Fatalf("the error should show the fingerprint: %v", err)
	}

	c = New(store.Network{TrustPKI: true, Fingerprints: []string{fp}}, quiet, Options{})
	if err := handshake(t, c, cert); err != nil {
		t.Fatalf("a listed fingerprint is trusted: %v", err)
	}
	c = New(store.Network{TrustAllCerts: true}, quiet, Options{})
	if err := handshake(t, c, cert); err != nil {
		t.Fatalf("trust-all accepts anything: %v", err)
	}
	c = New(store.Network{}, quiet, Options{})
	if err := handshake(t, c, cert); err == nil {
		t.Fatal("with PKI off and no fingerprint, nothing is trusted")
	}
}

// run starts c and stops it when the test ends.
func run(t *testing.T, c *Conn) context.CancelFunc {
	ctx, cancel := context.WithCancel(context.Background())
	go c.Run(ctx)
	t.Cleanup(cancel)
	return cancel
}

func TestFailoverMovesToTheNextServer(t *testing.T) {
	dead, _ := net.Listen("tcp", "127.0.0.1:0")
	deadAddr := dead.Addr().(*net.TCPAddr)
	dead.Close() // nothing listens here any more
	live := testirc.Start(t)
	host, port, _ := net.SplitHostPort(live)
	var p int
	for _, ch := range port {
		p = p*10 + int(ch-'0')
	}
	n := store.Network{Nick: "alice", Servers: []store.Server{
		{Host: "127.0.0.1", Port: deadAddr.Port}, {Host: host, Port: p},
	}}
	c := New(n, quiet, Options{})
	run(t, c)
	eventually(t, "connected to the second server", func() bool {
		return c.Status() == StatusConnected && c.Health().Server == live
	})
}

func TestCTCPRepliesAndQuitMessage(t *testing.T) {
	var log testirc.Log
	addr := testirc.StartLogged(t, &log)
	host, port, _ := net.SplitHostPort(addr)
	var p int
	for _, ch := range port {
		p = p*10 + int(ch-'0')
	}
	n := store.Network{Nick: "alice", Host: host, Port: p, QuitMsg: "see you", Channels: []string{"#k"},
		ChannelSettings: map[string]store.ChannelSettings{"#k": {Key: "sekrit"}}}
	idle := true
	c := New(n, quiet, Options{CTCPReplies: map[string]string{"time": "", "finger": "no"}, Idle: func() bool { return idle }, Version: "test"})
	raw, unsub := c.SubscribeRaw()
	defer unsub()
	cancel := run(t, c)
	eventually(t, "registered", func() bool { return c.Status() == StatusConnected })
	eventually(t, "joined with the key", func() bool { return log.Has("JOIN #k sekrit") })

	for _, req := range []string{"VERSION", "FINGER", "TIME"} {
		c.SendRaw("SIMULATE :bob!u@h PRIVMSG alice :\x01" + req + "\x01")
	}
	eventually(t, "VERSION answered while idle", func() bool { return log.Has("NOTICE bob :\x01VERSION evrd test\x01") })
	eventually(t, "custom FINGER reply", func() bool { return log.Has("NOTICE bob :\x01FINGER no\x01") })
	time.Sleep(100 * time.Millisecond)
	if log.Has("NOTICE bob :\x01TIME") {
		t.Fatal("an empty reply blocks the request")
	}
	// handled requests aren't passed on to clients
	for len(raw) > 0 {
		if l := <-raw; strings.Contains(l, "\x01") {
			t.Fatalf("CTCP leaked to clients: %q", l)
		}
	}

	cancel()
	eventually(t, "quit message sent", func() bool { return log.Has("QUIT :see you") })
}
