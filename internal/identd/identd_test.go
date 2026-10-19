package identd

import (
	"bufio"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"strings"
	"testing"
	"time"
)

// alice is the only connection: from 192.0.2.7, our port 50000 to its 6667.
func alice(from netip.Addr, lport, fport int) (string, bool) {
	if from == netip.MustParseAddr("192.0.2.7") && lport == 50000 && fport == 6667 {
		return "alice", true
	}
	return "", false
}

func TestAnswer(t *testing.T) {
	s := New(alice, slog.New(slog.NewTextHandler(io.Discard, nil)))
	s.lookup = func(from netip.Addr, lport, fport int) (string, bool) {
		if lport == 1 {
			return "b@d ünïcode name", true // cleaned, and cut at 10
		}
		if lport == 2 {
			return "@@@", true // nothing left: no user
		}
		return alice(from, lport, fport)
	}
	from := netip.MustParseAddr("192.0.2.7")
	for line, want := range map[string]string{
		"50000,6667":       "50000 , 6667 : USERID : UNIX : alice\r\n",
		"  50000 ,  6667 ": "50000 , 6667 : USERID : UNIX : alice\r\n",
		"50001, 6667":      "50001 , 6667 : ERROR : NO-USER\r\n",
		"1, 6667":          "1 , 6667 : USERID : UNIX : bdncodenam\r\n",
		"2, 6667":          "2 , 6667 : ERROR : NO-USER\r\n",
		"0, 6667":          "0 , 6667 : ERROR : INVALID-PORT\r\n",
		"50000, 70000":     "50000 , 70000 : ERROR : INVALID-PORT\r\n",
		"hello":            "0 , 0 : ERROR : INVALID-PORT\r\n",
		"a, b":             "0 , 0 : ERROR : INVALID-PORT\r\n",
		"":                 "0 , 0 : ERROR : INVALID-PORT\r\n",
	} {
		if got := s.Answer(from, line); got != want {
			t.Errorf("Answer(%q) = %q, want %q", line, got, want)
		}
	}
	if got := s.Answer(netip.MustParseAddr("198.51.100.1"), "50000,6667"); !strings.Contains(got, "NO-USER") {
		t.Errorf("another server: %q", got)
	}
}

func TestReadLine(t *testing.T) {
	for in, want := range map[string]string{
		"1,2\r\n":   "1,2",
		"1,2\n":     "1,2",
		"1,2":       "1,2", // no newline before the stream ends
		"1,2\nmore": "1,2",
	} {
		if got, err := readLine(strings.NewReader(in)); err != nil || got != want {
			t.Errorf("readLine(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"", strings.Repeat("1", 200), strings.Repeat(" ", 100) + "\n"} {
		if got, err := readLine(strings.NewReader(in)); err == nil {
			t.Errorf("readLine(%d bytes) = %q, want an error", len(in), got)
		}
	}
}

// serve starts s on a loopback port; the lookup sees the client as 192.0.2.7.
func serve(t *testing.T, s *Server) (string, chan error) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx, ln) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("Serve didn't return")
		}
	})
	return ln.Addr().String(), done
}

func loopbackIsAlice(s *Server) {
	s.lookup = func(from netip.Addr, lport, fport int) (string, bool) {
		if from != netip.MustParseAddr("127.0.0.1") {
			return "", false
		}
		return alice(netip.MustParseAddr("192.0.2.7"), lport, fport)
	}
}

// query sends one line and reads the reply ("" if the server just closed).
func query(t *testing.T, addr, line string) string {
	t.Helper()
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(5 * time.Second))
	io.WriteString(c, line)
	reply, _ := bufio.NewReader(c).ReadString('\n')
	return reply
}

func TestServe(t *testing.T) {
	s := New(nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	loopbackIsAlice(s)
	addr, _ := serve(t, s)
	if got := query(t, addr, "50000 , 6667\r\n"); got != "50000 , 6667 : USERID : UNIX : alice\r\n" {
		t.Errorf("good query: %q", got)
	}
	if got := query(t, addr, "garbage\n"); got != "0 , 0 : ERROR : INVALID-PORT\r\n" {
		t.Errorf("garbage: %q", got)
	}
	if got := query(t, addr, strings.Repeat("9", 300)+"\r\n"); got != "" {
		t.Errorf("oversized: %q", got)
	}
}

func TestSlowClientTimesOutAndDoesNotBlock(t *testing.T) {
	s := New(nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	loopbackIsAlice(s)
	s.Timeout = 300 * time.Millisecond
	addr, _ := serve(t, s)

	slow, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer slow.Close()
	io.WriteString(slow, "50000,") // never finishes
	// others are answered meanwhile
	if got := query(t, addr, "50000,6667\n"); !strings.Contains(got, "USERID") {
		t.Errorf("while one is slow: %q", got)
	}
	// and the slow one is dropped after the timeout
	slow.SetReadDeadline(time.Now().Add(5 * time.Second))
	start := time.Now()
	n, err := slow.Read(make([]byte, 64))
	var ne net.Error
	if n != 0 || err == nil || errors.As(err, &ne) && ne.Timeout() {
		t.Errorf("slow client: read %d, %v", n, err)
	}
	if time.Since(start) > 3*time.Second {
		t.Errorf("slow client held %v", time.Since(start))
	}
}

func TestTooManyConnections(t *testing.T) {
	s := New(nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	loopbackIsAlice(s)
	s.MaxConns = 1
	addr, _ := serve(t, s)

	hold, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer hold.Close()
	// the held one takes the only slot once accepted; the next is closed
	deadline := time.Now().Add(5 * time.Second)
	for query(t, addr, "50000,6667\n") != "" {
		if time.Now().After(deadline) {
			t.Fatal("a second connection was answered while the slot was taken")
		}
		time.Sleep(10 * time.Millisecond)
	}
	hold.Close()
	for query(t, addr, "50000,6667\n") == "" {
		if time.Now().After(deadline) {
			t.Fatal("not answered once the slot was free")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestServeAcceptError(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ln.Close()
	s := New(alice, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := s.Serve(context.Background(), ln); err == nil {
		t.Error("Serve on a closed listener: no error")
	}
}
