package client

import (
	"bufio"
	"context"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/4evrd/evrd/internal/links"
)

// holder runs a connection holder for the test; its client and a free
// address for an IRC listener.
func holder(t *testing.T) (*links.Client, string) {
	t.Helper()
	dir, err := os.MkdirTemp("", "hold") // short: unix socket paths are limited
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	path := filepath.Join(dir, "l.sock")
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go links.NewHolder(slog.New(slog.NewTextHandler(io.Discard, nil))).Serve(ctx, ln)
	free, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := free.Addr().String()
	free.Close()
	return links.NewClient(path), addr
}

// serveThrough starts an IRC server on r's bouncer that serves addr through
// the holder, as an evrd would; cancel stops it, as evrd stopping.
func (r *rig) serveThrough(h *links.Client, addr string) (*Server, context.CancelFunc) {
	r.t.Helper()
	srv := NewServer(r.b, r.acc, slog.New(slog.NewTextHandler(r.logs, nil)), "", "")
	srv.followEvery = 100 * time.Millisecond
	srv.lsWait = 200 * time.Millisecond
	srv.SetHolder(h)
	ctx, cancel := context.WithCancel(r.ctx)
	r.t.Cleanup(cancel)
	go srv.ListenAndServe(ctx, []string{"irc+insecure://" + addr})
	select {
	case <-srv.Ready():
	case <-time.After(5 * time.Second):
		r.t.Fatal("never ready")
	}
	return srv, cancel
}

func TestClientsStayConnectedWhileEvrdRestarts(t *testing.T) {
	r := newRig(t)
	h, addr := holder(t)
	_, stop := r.serveThrough(h, addr)

	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	ic := &ircClient{t: t, c: c, sc: bufio.NewScanner(c)}
	ic.send("CAP LS 302")
	ic.send("CAP REQ :server-time echo-message")
	ic.send("CAP END")
	ic.send("PASS alice/one:alice-password")
	ic.send("NICK alice")
	ic.send("USER alice 0 * :alice")
	ic.expect(" 001 alice_one ")
	ic.until(" JOIN #one")
	ic.send("PRIVMSG #one :before")
	ic.expect("PRIVMSG #one :before")

	// evrd stops; the holder keeps the client, answering it meanwhile
	// (one bouncer stands in for both evrds here: the first one's
	// attachment stays, as it would in an evrd that never stopped)
	stop()
	time.Sleep(100 * time.Millisecond)
	ic.send("PING :gap")
	ic.expect(":evrd PONG evrd :gap")
	ic.send("PRIVMSG #one :during")

	// the next evrd picks it up where it was: no welcome, same nick, caps
	r.serveThrough(h, addr)
	eventually(t, "the line sent meanwhile reaches the network", func() bool { return r.irc.Has("PRIVMSG #one :during") })
	ic.send("SIMULATE :bob!u@h PRIVMSG #one :after")
	seen := ic.until(":bob!u@h PRIVMSG #one :after")
	if anyHas(seen, " 001 ") || anyHas(seen, " JOIN ") || anyHas(seen, " 376 ") {
		t.Fatalf("a second welcome: %q", seen)
	}
	if last := seen[len(seen)-1]; !strings.HasPrefix(last, "@time=") {
		t.Fatalf("server-time lost: %q", last)
	}
	ic.send("PRIVMSG *status :version")
	ic.expect("PRIVMSG alice_one :evrd ")
	cs := r.b.ClientsOf(r.user.ID)
	if len(cs) != 2 || cs[1].Networks[0] != "one" || !cs[1].Since.Equal(cs[0].Since) {
		t.Fatalf("clients after: %+v", cs)
	}

	// QUIT closes it for good
	ic.send("QUIT :bye")
	eventually(t, "client gone", func() bool { return len(r.b.ClientsOf(r.user.ID)) == 1 })
	c.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, err := io.ReadAll(c); err != nil {
		t.Fatalf("not closed: %v", err)
	}
}

func TestAClientMidRegistrationIsNotHeld(t *testing.T) {
	r := newRig(t)
	h, addr := holder(t)
	_, stop := r.serveThrough(h, addr)
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	io.WriteString(c, "NICK alice\r\n")
	time.Sleep(100 * time.Millisecond)
	stop()
	c.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, err := io.ReadAll(c); err != nil {
		t.Fatalf("not closed: %v", err)
	}
}

func TestClientsArriveWhileEvrdIsAway(t *testing.T) {
	r := newRig(t)
	h, addr := holder(t)
	_, stop := r.serveThrough(h, addr)
	stop()
	time.Sleep(50 * time.Millisecond)
	// evrd is away: the holder still accepts, and the next evrd serves it
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	ic := &ircClient{t: t, c: c, sc: bufio.NewScanner(c)}
	ic.send("PASS alice/one:alice-password")
	ic.send("NICK alice")
	ic.send("USER alice 0 * :alice")
	r.serveThrough(h, addr)
	ic.expect(" 001 alice_one ")
}

func TestUnreadableStateIsNotResumed(t *testing.T) {
	r := newRig(t)
	srv := NewServer(r.b, r.acc, slog.New(slog.NewTextHandler(io.Discard, nil)), "", "")
	se := newSession(srv, nil)
	for _, st := range []string{`nope`, `{"v":99}`, `{"v":1,"user":999}`, `{"v":1,"user":1,"network":999}`} {
		if err := se.restore([]byte(st)); err == nil {
			t.Errorf("restored %s", st)
		}
	}
}
