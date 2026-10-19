package upstream

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/4evrd/evrd/internal/irc"
	"github.com/4evrd/evrd/internal/store"
)

func TestPingTimeoutReconnects(t *testing.T) {
	// The server registers us, then never says anything again (and doesn't
	// answer our PINGs).
	s := startScripted(t, func(p *peer, l string) {
		switch {
		case strings.HasPrefix(l, "CAP LS"):
			p.send(":srv CAP * LS :")
		case strings.HasPrefix(l, "USER "):
			p.send(":srv 001 %s :Welcome", p.nick)
		}
	})
	var events eventRec
	var rec logRec
	c := New(s.network(store.Network{Name: "Net", Nick: "me"}), quiet, Options{
		OnEvent: events.add, OnLog: rec.add,
		PingInterval: 50 * time.Millisecond, ReadTimeout: 300 * time.Millisecond,
	})
	run(t, c)
	eventually(t, "registered", func() bool { return c.Status() == StatusConnected })
	eventually(t, "lag measured with PINGs", func() bool { return s.count("PING :evrd-lag-") > 0 })
	eventually(t, "ping timeout noticed", func() bool { return c.Health().LastError == "ping timeout" })
	h := c.Health()
	if h.Attempts != 1 || h.Hint == "" || h.LastErrorAt.IsZero() {
		t.Fatalf("health after the timeout: %+v", h)
	}
	if !events.has("disconnected", "Net ping timeout") {
		t.Fatal("no disconnected event")
	}
	if !rec.has("evrd", "Disconnected: ping timeout", true) {
		t.Fatalf("log: %+v", rec.all())
	}
	// A working link that dropped retries within about a second.
	eventually(t, "reconnected", func() bool { return s.conns() == 2 && c.Status() == StatusConnected })
}

func TestLagIsMeasured(t *testing.T) {
	_, c := connected(t, store.Network{Nick: "me"}, Options{PingInterval: 30 * time.Millisecond})
	eventually(t, "lag known", func() bool { return c.Health().Lag >= 0 })
}

func TestPongWithAnotherTokenIsIgnored(t *testing.T) {
	s, c := connected(t, store.Network{Nick: "me"}, Options{})
	c.mu.Lock()
	c.lagToken, c.lagSent = "mine", time.Now()
	c.mu.Unlock()
	s.send(":srv PONG srv :other")
	settle(t, s)
	if c.Health().Lag != -1 {
		t.Fatal("a PONG to someone else's PING set the lag")
	}
	s.send(":srv PONG srv :mine")
	settle(t, s)
	if c.Health().Lag < 0 {
		t.Fatal("our PONG didn't set the lag")
	}
}

func TestRetryBacksOffAndStops(t *testing.T) {
	dead, _ := net.Listen("tcp", "127.0.0.1:0")
	port := dead.Addr().(*net.TCPAddr).Port
	dead.Close()
	var rec logRec
	var events eventRec
	c := New(store.Network{Name: "Net", Nick: "me", Host: "127.0.0.1", Port: port}, quiet, Options{OnLog: rec.add, OnEvent: events.add})
	if c.Status() != StatusDisconnected {
		t.Fatal("a new connection is disconnected")
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { c.Run(ctx); close(done) }()
	eventually(t, "the failure recorded", func() bool { return c.Health().Attempts > 0 })
	h := c.Health()
	if h.Status != StatusReconnecting || h.LastError != "connection refused" || h.RetryIn < 500*time.Millisecond || h.RetryIn > 1500*time.Millisecond {
		t.Fatalf("health: %+v", h)
	}
	if !rec.has("evrd", "Couldn't connect: connection refused", true) || !rec.has("evrd", "nothing listens on that port", false) {
		t.Fatalf("log: %+v", rec.all())
	}
	// A failed attempt is an event, with the hint, once per error rather
	// than at every retry.
	if !events.has("failed", "Net: couldn't connect: connection refused") {
		t.Fatal("no failed event")
	}
	eventually(t, "a second attempt", func() bool { return c.Health().Attempts > 1 })
	if events.count("failed") != 1 || events.count("disconnected") != 0 {
		t.Fatalf("events: %d failed, %d disconnected", events.count("failed"), events.count("disconnected"))
	}
	c.SendRaw("PRIVMSG x :held")
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run didn't stop while waiting to retry")
	}
	if h := c.Health(); h.Status != StatusDisconnected || h.RetryIn != 0 || len(c.pending) != 0 {
		t.Fatalf("after stopping: %+v, %d pending", h, len(c.pending))
	}
}

func TestNoServers(t *testing.T) {
	c := New(store.Network{Nick: "me"}, quiet, Options{})
	ok, err := c.dialAndServe(context.Background())
	if ok || err == nil || err.Error() != "no servers configured" {
		t.Fatalf("%v %v", ok, err)
	}
}

func TestCustomDialer(t *testing.T) {
	s := startScripted(t, nil)
	host, port := s.hostPort()
	var dialed string
	c := New(store.Network{Nick: "me", Host: "irc.invalid", Port: 6667}, quiet, Options{
		Dial: func(ctx context.Context, h string, p int) (net.Conn, error) {
			dialed = fmt.Sprintf("%s:%d", h, p)
			return (&net.Dialer{}).DialContext(ctx, "tcp", fmt.Sprintf("%s:%d", host, port))
		},
	})
	run(t, c)
	eventually(t, "registered through the dialer", func() bool { return c.Status() == StatusConnected })
	if dialed != "irc.invalid:6667" {
		t.Fatalf("dialed %q", dialed)
	}
}

// startTLS runs a scripted server behind TLS with cert.
func startTLS(t *testing.T, cert tls.Certificate) *scripted {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &scripted{t: t, ln: ln, reply: welcome}
	t.Cleanup(func() { ln.Close() })
	go s.serve(func(c net.Conn) net.Conn {
		return tls.Server(c, &tls.Config{Certificates: []tls.Certificate{cert}})
	})
	return s
}

func TestTLSWithATrustedFingerprint(t *testing.T) {
	cert, leaf := selfSigned(t)
	s := startTLS(t, cert)
	_, port := s.hostPort()
	var rec logRec
	n := store.Network{Nick: "me", TrustPKI: true, Fingerprints: []string{Fingerprint(leaf)},
		Servers: []store.Server{{Host: "localhost", Port: port, TLS: true}}}
	c := New(n, quiet, Options{OnLog: rec.add})
	run(t, c)
	eventually(t, "registered over TLS", func() bool { return c.Status() == StatusConnected })
	if !rec.has("evrd", "with TLS", false) || !rec.has("evrd", "TLS handshake done", false) {
		t.Fatalf("log: %+v", rec.all())
	}
	if c.Health().UntrustedFingerprint != "" {
		t.Fatal("a trusted certificate reported as untrusted")
	}
}

func TestTLSWithAnUntrustedCertificate(t *testing.T) {
	cert, leaf := selfSigned(t)
	s := startTLS(t, cert)
	_, port := s.hostPort()
	n := store.Network{Nick: "me", TrustPKI: true, Servers: []store.Server{{Host: "localhost", Port: port, TLS: true}}}
	c := New(n, quiet, Options{})
	run(t, c)
	eventually(t, "refused", func() bool { return c.Health().Attempts > 0 })
	h := c.Health()
	if h.UntrustedFingerprint != Fingerprint(leaf) || h.Status == StatusConnected {
		t.Fatalf("health: %+v", h)
	}
	if !strings.Contains(h.Hint, "certificate") {
		t.Fatalf("hint: %q (error %q)", h.Hint, h.LastError)
	}
}

func TestUntrustedCertificateThroughTheHolder(t *testing.T) {
	cert, leaf := selfSigned(t)
	s := startTLS(t, cert)
	_, port := s.hostPort()
	n := store.Network{ID: 40, Nick: "me", TrustPKI: true, Servers: []store.Server{{Host: "localhost", Port: port, TLS: true}}}
	c := New(n, quiet, Options{Links: startTestHolder(t), LinkID: "n40"})
	run(t, c)
	eventually(t, "refused", func() bool { return c.Health().Attempts > 0 })
	if h := c.Health(); h.UntrustedFingerprint != Fingerprint(leaf) {
		t.Fatalf("health: %+v", h)
	}
}

func TestTLSThroughTheHolder(t *testing.T) {
	cert, leaf := selfSigned(t)
	s := startTLS(t, cert)
	_, port := s.hostPort()
	var rec logRec
	n := store.Network{ID: 41, Nick: "me", Fingerprints: []string{Fingerprint(leaf)},
		Servers: []store.Server{{Host: "localhost", Port: port, TLS: true}}}
	c := New(n, quiet, Options{Links: startTestHolder(t), LinkID: "n41", OnLog: rec.add})
	run(t, c)
	eventually(t, "registered through the holder", func() bool { return c.Status() == StatusConnected })
	if !rec.has("evrd", "TLS handshake done", false) {
		t.Fatalf("log: %+v", rec.all())
	}
}

func TestEncodingConvertsBothWays(t *testing.T) {
	var msgs msgRec
	s, c := connected(t, store.Network{Nick: "me", Encoding: "ISO-8859-1"}, Options{Persist: msgs.add})
	s.send(":bob!b@h PRIVMSG #c :caf\xe9")
	settle(t, s)
	if got := msgs.all(); len(got) != 1 || got[0].Text != "café" {
		t.Fatalf("decoded: %+v", got)
	}
	c.SendMessage("#c", "café ☃")
	eventually(t, "sent in latin-1", func() bool { return s.count("PRIVMSG #c :caf\xe9 ") == 1 })
}

func TestFallbackEncodingSendsUTF8(t *testing.T) {
	var msgs msgRec
	s, c := connected(t, store.Network{Nick: "me", Encoding: "^windows-1252"}, Options{Persist: msgs.add})
	s.send(":bob!b@h PRIVMSG #c :\x93quoted\x94")
	s.send(":bob!b@h PRIVMSG #c :naïve")
	settle(t, s)
	got := msgs.all()
	if len(got) != 2 || got[0].Text != "“quoted”" || got[1].Text != "naïve" {
		t.Fatalf("decoded: %+v", got)
	}
	c.SendMessage("#c", "café")
	eventually(t, "sent as UTF-8", func() bool { return s.has("PRIVMSG #c :café") })
}

func TestUnknownEncodingPassesThrough(t *testing.T) {
	k := newCodec("klingon")
	if k.enc != nil || k.decode([]byte("caf\xe9")) != "caf\xe9" || k.encode("café") != "café" {
		t.Fatal("an unknown charset should pass lines through")
	}
}

func TestQuitWithoutMessage(t *testing.T) {
	s := startScripted(t, nil)
	c := New(s.network(store.Network{Nick: "me"}), quiet, Options{})
	cancel := run(t, c)
	eventually(t, "registered", func() bool { return c.Status() == StatusConnected })
	cancel()
	eventually(t, "a plain QUIT", func() bool { return s.has("QUIT") })
	eventually(t, "disconnected", func() bool { return c.Status() == StatusDisconnected })
}

func TestCTCPStockAnswers(t *testing.T) {
	idle := true
	s, c := connected(t, store.Network{Nick: "me"}, Options{Idle: func() bool { return idle }, Version: "v9"})
	raw, unsub := c.SubscribeRaw()
	defer unsub()
	s.send(":bob!b@h PRIVMSG me :\x01PING 12345\x01")
	s.send(":bob!b@h PRIVMSG me :\x01TIME\x01")
	s.send(":bob!b@h PRIVMSG me :\x01CLIENTINFO\x01")
	s.send(":bob!b@h PRIVMSG me :\x01DCC SEND x\x01") // not ours to answer
	s.send(":bob!b@h PRIVMSG #chan :\x01VERSION\x01") // to a channel: not answered
	settle(t, s)
	eventually(t, "PING echoed", func() bool { return s.has("NOTICE bob :\x01PING 12345\x01") })
	eventually(t, "CLIENTINFO", func() bool { return s.has("NOTICE bob :\x01CLIENTINFO ACTION CLIENTINFO PING TIME VERSION\x01") })
	if s.count("NOTICE bob :\x01TIME ") != 1 || s.count("NOTICE bob :\x01DCC") != 0 || s.count("NOTICE bob :\x01VERSION") != 0 {
		t.Fatalf("answers: %q", s.last().got())
	}
	passed := map[string]bool{}
	for len(raw) > 0 {
		if m := irc.Parse(<-raw); m.Command == "PRIVMSG" {
			passed[m.Params[1]] = true
		}
	}
	if !passed["\x01DCC SEND x\x01"] || !passed["\x01VERSION\x01"] || passed["\x01PING 12345\x01"] {
		t.Fatalf("passed to clients: %v", passed)
	}

	// With a client attached, it answers itself.
	idle = false
	s.send(":bob!b@h PRIVMSG me :\x01VERSION\x01")
	settle(t, s)
	if s.count("NOTICE bob :\x01VERSION") != 0 {
		t.Fatal("answered VERSION with a client attached")
	}
	if c.ctcp("bob!b@h", "me", "\x01ACTION waves\x01") || c.ctcp("bob", "me", "x") {
		t.Fatal("an ACTION or plain text is handled as a CTCP request")
	}
}

func TestDescribeErr(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want string
	}{
		{nil, "disconnected"},
		{ErrPingTimeout, "ping timeout"},
		{fmt.Errorf("read: %w", io.EOF), "connection closed by server"},
		{&net.DNSError{Name: "irc.invalid", Err: "no such host"}, "can't resolve irc.invalid"},
		{context.DeadlineExceeded, "connection timed out"},
		{&net.OpError{Op: "dial", Err: errors.New("connect: connection refused")}, "connection refused"},
		{&net.OpError{Op: "dial", Err: errors.New("no route to host")}, "can't connect"},
		{&tls.CertificateVerificationError{Err: x509.UnknownAuthorityError{}}, "TLS certificate rejected"},
		{errors.New("something else"), "something else"},
	} {
		if got := describeErr(tc.err); got != tc.want {
			t.Errorf("describeErr(%v) = %q, want %q", tc.err, got, tc.want)
		}
	}
	if roundDur(10*time.Millisecond) != "a moment" || roundDur(1600*time.Millisecond) != "2s" {
		t.Error("roundDur")
	}
}

func TestLogged(t *testing.T) {
	for _, tc := range []struct {
		registered  bool
		cmd, prefix string
		want        bool
	}{
		{false, "PING", "", false},
		{true, "PONG", "", false},
		{true, "ERROR", "", true},
		{true, "KILL", "", true},
		{false, "001", "srv", true},
		{true, "433", "srv", true},
		{true, "904", "srv", true},
		{true, "NOTICE", "srv", true},
		{true, "NOTICE", "bob!b@h", false},
		{true, "PRIVMSG", "bob!b@h", false},
	} {
		if got := logged(tc.registered, tc.cmd, tc.prefix, nil); got != tc.want {
			t.Errorf("logged(%v, %s, %s) = %v", tc.registered, tc.cmd, tc.prefix, got)
		}
	}
}

func TestHelpers(t *testing.T) {
	m := irc.Parse(":a PART #c :why")
	if lastParamIf(m, 2) != "why" || lastParamIf(m, 3) != "" || lastParam(irc.Message{}) != "" {
		t.Error("lastParam")
	}
	if !containsFold([]string{"#A"}, "#a") || containsFold(nil, "#a") {
		t.Error("containsFold")
	}
	if !isChannel("&local") || isChannel("") || isChannel("nick") {
		t.Error("isChannel")
	}
	if nickOf("n!u@h") != "n" || nickOf("srv") != "srv" {
		t.Error("nickOf")
	}
}
