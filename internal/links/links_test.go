package links

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/4evrd/evrd/internal/irctls"
)

// fakeServer is an IRC server the test drives by hand.
type fakeServer struct {
	ln    net.Listener
	mu    sync.Mutex
	conns []net.Conn
	got   []string
	seen  chan string
}

func newFakeServer(t *testing.T) *fakeServer {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &fakeServer{ln: ln, seen: make(chan string, 100)}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			s.mu.Lock()
			s.conns = append(s.conns, c)
			s.mu.Unlock()
			go func() {
				sc := bufio.NewScanner(c)
				for sc.Scan() {
					s.mu.Lock()
					s.got = append(s.got, sc.Text())
					s.mu.Unlock()
					s.seen <- sc.Text()
				}
			}()
		}
	}()
	return s
}

func (s *fakeServer) send(t *testing.T, line string) {
	t.Helper()
	s.mu.Lock()
	c := s.conns[len(s.conns)-1]
	s.mu.Unlock()
	if _, err := io.WriteString(c, line+"\r\n"); err != nil {
		t.Fatal(err)
	}
}

func (s *fakeServer) expect(t *testing.T, prefix string) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case l := <-s.seen:
			if strings.HasPrefix(l, prefix) {
				return
			}
		case <-deadline:
			t.Fatalf("server never got %q", prefix)
		}
	}
}

func (s *fakeServer) received() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.got...)
}

func startHolder(t *testing.T) (*Holder, *Client) {
	t.Helper()
	dir, err := os.MkdirTemp("", "links")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	path := filepath.Join(dir, "l.sock") // short: unix socket paths are limited
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	h := NewHolder(slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go h.Serve(ctx, ln)
	return h, NewClient(path)
}

func readLine(t *testing.T, r *bufio.Reader) string {
	t.Helper()
	type res struct {
		s   string
		err error
	}
	ch := make(chan res, 1)
	go func() { s, err := r.ReadString('\n'); ch <- res{s, err} }()
	select {
	case x := <-ch:
		if x.err != nil {
			t.Fatalf("read: %v", x.err)
		}
		return strings.TrimRight(x.s, "\r\n")
	case <-time.After(5 * time.Second):
		t.Fatal("no line")
	}
	return ""
}

func TestLinkSurvivesEvrdGoingAway(t *testing.T) {
	quitGrace = 100 * time.Millisecond
	srv := newFakeServer(t)
	h, cl := startHolder(t)
	ctx := context.Background()

	tcp, err := net.Dial("tcp", srv.ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	st, err := cl.Open(ctx, "n1", tcp, false, "irc.test", irctls.Trust{})
	if err != nil {
		t.Fatal(err)
	}
	r := bufio.NewReader(st)
	io.WriteString(st, "NICK alice\r\nUSER alice 0 * :Alice\r\n")
	srv.expect(t, "USER alice")
	srv.send(t, ":srv 001 alice :Welcome")
	srv.send(t, ":srv 005 alice PREFIX=(ov)@+ NETWORK=Test :are supported")
	readLine(t, r)
	readLine(t, r)
	srv.send(t, ":alice!a@h NICK alice2")
	readLine(t, r)

	// evrd goes away (restart): the link is held.
	st.Close()
	time.Sleep(100 * time.Millisecond)
	if ls, _ := cl.List(ctx); len(ls) != 1 || ls[0].Attached || ls[0].Nick != "alice2" {
		t.Fatalf("held link: %+v", ls)
	}
	srv.send(t, "PING :are-you-there")
	srv.expect(t, "PONG :are-you-there") // the holder answered
	srv.send(t, ":bob!b@h PRIVMSG alice2 :while you were away")

	// The next evrd resumes.
	st2, res, err := cl.Resume(ctx, "n1")
	if err != nil {
		t.Fatal(err)
	}
	if res.Nick != "alice2" || len(res.ISupport) != 1 || !strings.Contains(res.ISupport[0], "NETWORK=Test") {
		t.Fatalf("resumed: %+v", res)
	}
	r2 := bufio.NewReader(st2)
	if got := readLine(t, r2); got != ":bob!b@h PRIVMSG alice2 :while you were away" {
		t.Fatalf("buffered line: %q (the answered PING mustn't be replayed)", got)
	}
	io.WriteString(st2, "PRIVMSG #c :back\r\n")
	srv.expect(t, "PRIVMSG #c :back")

	// The server never saw a second connection or a QUIT.
	srv.mu.Lock()
	n := len(srv.conns)
	srv.mu.Unlock()
	if n != 1 {
		t.Fatalf("%d connections to the server", n)
	}
	for _, l := range srv.received() {
		if strings.HasPrefix(l, "QUIT") || strings.HasPrefix(l, "NICK") && l != "NICK alice" {
			t.Fatalf("server saw %q", l)
		}
	}

	// A real QUIT ends the link.
	io.WriteString(st2, "QUIT :bye\r\n")
	st2.Close()
	srv.expect(t, "QUIT :bye")
	time.Sleep(300 * time.Millisecond)
	if ls, _ := cl.List(ctx); len(ls) != 0 {
		t.Fatalf("link kept after QUIT: %+v", ls)
	}
	if _, _, err := cl.Resume(ctx, "n1"); !errors.Is(err, ErrNoLink) {
		t.Fatalf("resume after quit: %v", err)
	}
	_ = h
}

func TestBufferKeepsTheNewestLines(t *testing.T) {
	srv := newFakeServer(t)
	h, cl := startHolder(t)
	h.MaxBuffer = 200
	ctx := context.Background()
	tcp, _ := net.Dial("tcp", srv.ln.Addr().String())
	st, err := cl.Open(ctx, "n1", tcp, false, "irc.test", irctls.Trust{})
	if err != nil {
		t.Fatal(err)
	}
	io.WriteString(st, "NICK a\r\n")
	srv.expect(t, "NICK a")
	srv.send(t, ":srv 001 a :hi")
	readLine(t, bufio.NewReader(st))
	st.Close()
	time.Sleep(100 * time.Millisecond)
	for i := 0; i < 20; i++ {
		srv.send(t, ":b!b@h PRIVMSG a :line "+strings.Repeat("x", 20)+string(rune('a'+i)))
	}
	time.Sleep(200 * time.Millisecond)
	st2, res, err := cl.Resume(ctx, "n1")
	if err != nil {
		t.Fatal(err)
	}
	if res.Dropped == 0 {
		t.Fatal("nothing dropped past the buffer limit")
	}
	r := bufio.NewReader(st2)
	var last string
	for i := 0; i < 20-res.Dropped; i++ {
		last = readLine(t, r)
	}
	if !strings.HasSuffix(last, "t") { // the 20th line, 'a'+19
		t.Fatalf("last buffered line %q", last)
	}
}

func TestUntrustedCertificateComesBack(t *testing.T) {
	ts := httptest.NewUnstartedServer(nil)
	ts.StartTLS() // a self-signed certificate
	defer ts.Close()
	_, cl := startHolder(t)
	tcp, err := net.Dial("tcp", ts.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	_, err = cl.Open(context.Background(), "n1", tcp, true, "irc.test", irctls.Trust{TrustPKI: true})
	var ue *irctls.UntrustedCertError
	if !errors.As(err, &ue) || ue.Fingerprint == "" {
		t.Fatalf("err: %v", err)
	}
	// Trusting that fingerprint lets it through.
	tcp, _ = net.Dial("tcp", ts.Listener.Addr().String())
	st, err := cl.Open(context.Background(), "n1", tcp, true, "irc.test", irctls.Trust{Fingerprints: []string{ue.Fingerprint}})
	if err != nil {
		t.Fatalf("trusted fingerprint: %v", err)
	}
	st.Close()
	_ = tls.VersionTLS13
}

func TestHolderUnavailable(t *testing.T) {
	cl := NewClient(filepath.Join(t.TempDir(), "nobody.sock"))
	srv := newFakeServer(t)
	tcp, _ := net.Dial("tcp", srv.ln.Addr().String())
	defer tcp.Close()
	if _, err := cl.Open(context.Background(), "n1", tcp, false, "x", irctls.Trust{}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err: %v", err)
	}
	// the socket wasn't given away: still usable for a direct connection
	if _, err := io.WriteString(tcp, "NICK a\r\n"); err != nil {
		t.Fatalf("tcp closed: %v", err)
	}
}

func TestAPlainTextRefusalComesBackThroughTheHolder(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		c, err := ln.Accept()
		if err == nil {
			c.Write([]byte("ERROR :*** Banned: Please use another server.\r\n"))
			time.Sleep(50 * time.Millisecond)
			c.Close()
		}
	}()
	_, cl := startHolder(t)
	tcp, _ := net.Dial("tcp", ln.Addr().String())
	_, err = cl.Open(context.Background(), "n1", tcp, true, "irc.test", irctls.Trust{TrustPKI: true})
	if err == nil || !strings.Contains(err.Error(), "refused the connection: *** Banned: Please use another server.") {
		t.Fatalf("err: %v", err)
	}
}
