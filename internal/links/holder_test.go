package links

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/4evrd/evrd/internal/irctls"
)

// sockPath is a short path for a unix socket (their length is limited).
func sockPath(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "links")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return filepath.Join(dir, "l.sock")
}

// serveHolder runs h until the returned stop is called (or the test ends).
func serveHolder(t *testing.T, h *Holder) (*Client, func()) {
	t.Helper()
	path := sockPath(t)
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- h.Serve(ctx, ln) }()
	stop := func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("Serve: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("Serve didn't return")
		}
	}
	t.Cleanup(cancel)
	return NewClient(path), stop
}

func quietHolder() *Holder { return NewHolder(slog.New(slog.NewTextHandler(io.Discard, nil))) }

// openRegistered opens link id to srv through cl and registers it as nick.
func openRegistered(t *testing.T, srv *fakeServer, cl *Client, id, nick string) (net.Conn, *bufio.Reader) {
	t.Helper()
	tcp, err := net.Dial("tcp", srv.ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	st, err := cl.Open(context.Background(), id, tcp, false, "irc.test", irctls.Trust{})
	if err != nil {
		t.Fatal(err)
	}
	r := bufio.NewReader(st)
	io.WriteString(st, "NICK "+nick+"\r\n")
	srv.expect(t, "NICK "+nick)
	srv.send(t, ":srv 001 "+nick+" :Welcome")
	if got := readLine(t, r); !strings.Contains(got, " 001 ") {
		t.Fatalf("got %q", got)
	}
	return st, r
}

// eventually polls cond for up to 5s.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		if cond() {
			return
		}
	}
	t.Fatalf("never: %s", what)
}

func listLinks(t *testing.T, cl *Client) []Info {
	t.Helper()
	ls, err := cl.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return ls
}

// raw sends one request line (with fd, if >= 0) to the holder at cl's
// socket and returns its answer.
func raw(t *testing.T, cl *Client, line string, fd int) response {
	t.Helper()
	conn, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: cl.Path(), Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	var oob []byte
	if fd >= 0 {
		oob = unix.UnixRights(fd)
	}
	if _, _, err := conn.WriteMsgUnix([]byte(line+"\n"), oob, nil); err != nil {
		t.Fatal(err)
	}
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	b, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		t.Fatalf("no answer: %v", err)
	}
	var resp response
	if err := json.Unmarshal(b, &resp); err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestBadRequests(t *testing.T) {
	cl, _ := serveHolder(t, quietHolder())
	if cl.Path() == "" {
		t.Fatal("no path")
	}
	for _, c := range []struct{ line, want string }{
		{`{"version":2,"op":"list"}`, "bad request or protocol version"},
		{`{"op":"list"}`, "bad request or protocol version"},
		{`not json`, "bad request or protocol version"},
		{`{"version":1,"op":"dance"}`, "unknown op"},
		{`{"version":1,"op":"open","link":"n1"}`, "open needs a socket"},
	} {
		if resp := raw(t, cl, c.line, -1); resp.OK || resp.Error != c.want {
			t.Errorf("%s: %+v", c.line, resp)
		}
	}

	// A socket sent with a request in the wrong version is closed, not kept.
	srv := newFakeServer(t)
	tcp, err := net.Dial("tcp", srv.ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	f, _ := tcp.(*net.TCPConn).File()
	tcp.Close()
	resp := raw(t, cl, `{"version":99,"op":"open","link":"n1"}`, int(f.Fd()))
	f.Close()
	if resp.Error != "bad request or protocol version" {
		t.Fatalf("got %+v", resp)
	}
	// with every copy closed, the server sees the connection end
	eventually(t, "the server sees the socket closed", func() bool {
		srv.mu.Lock()
		defer srv.mu.Unlock()
		if len(srv.conns) == 0 {
			return false
		}
		srv.conns[0].SetReadDeadline(time.Now().Add(10 * time.Millisecond))
		_, err := srv.conns[0].Read(make([]byte, 1))
		return errors.Is(err, io.EOF)
	})
	if ls := listLinks(t, cl); len(ls) != 0 {
		t.Fatalf("links %+v", ls)
	}
}

func TestOpenWithSomethingThatIsNotASocket(t *testing.T) {
	cl, _ := serveHolder(t, quietHolder())
	f, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	resp := raw(t, cl, `{"version":1,"op":"open","link":"n1"}`, int(f.Fd()))
	if resp.OK || !strings.HasPrefix(resp.Error, "socket: ") {
		t.Fatalf("got %+v", resp)
	}
}

func TestRequestCutShort(t *testing.T) {
	cl, _ := serveHolder(t, quietHolder())
	for _, send := range [][]byte{
		[]byte(`{"version":1,"op":"li`), // then closes
		[]byte(strings.Repeat("x", 70<<10)),
	} {
		conn, err := net.Dial("unix", cl.Path())
		if err != nil {
			t.Fatal(err)
		}
		conn.Write(send)
		conn.(*net.UnixConn).CloseWrite()
		conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		b, _ := io.ReadAll(conn)
		conn.Close()
		if len(b) != 0 {
			t.Fatalf("answered %q", b)
		}
	}
	// still serving
	listLinks(t, cl)
}

func TestCloseSendsQuit(t *testing.T) {
	srv := newFakeServer(t)
	cl, _ := serveHolder(t, quietHolder())
	ctx := context.Background()
	st, r := openRegistered(t, srv, cl, "n1", "alice")
	if err := cl.Close(ctx, "n1", "see you"); err != nil {
		t.Fatal(err)
	}
	srv.expect(t, "QUIT :see you")
	// evrd's stream ends
	st.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := r.ReadString('\n'); err == nil {
		t.Fatal("stream still open")
	}
	if ls := listLinks(t, cl); len(ls) != 0 {
		t.Fatalf("links %+v", ls)
	}
	// closing what isn't there is fine
	if err := cl.Close(ctx, "n1", ""); err != nil {
		t.Fatal(err)
	}
}

func TestCloseWithoutQuit(t *testing.T) {
	srv := newFakeServer(t)
	cl, _ := serveHolder(t, quietHolder())
	openRegistered(t, srv, cl, "n1", "alice")
	if err := cl.Close(context.Background(), "n1", ""); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the link is gone", func() bool { return len(listLinks(t, cl)) == 0 })
	for _, l := range srv.received() {
		if strings.HasPrefix(l, "QUIT") {
			t.Fatalf("server got %q", l)
		}
	}
}

func TestListAndAttachTakeover(t *testing.T) {
	srv := newFakeServer(t)
	cl, _ := serveHolder(t, quietHolder())
	ctx := context.Background()
	st1, r1 := openRegistered(t, srv, cl, "n1", "alice")
	ls := listLinks(t, cl)
	if len(ls) != 1 || ls[0] != (Info{Link: "n1", Nick: "alice", Registered: true, Attached: true}) {
		t.Fatalf("links %+v", ls)
	}

	// A second evrd attaches while the first still is: it takes over, and
	// the first's stream ends without ending the link.
	st2, res, err := cl.Resume(ctx, "n1")
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(res.ConnectedAt) > time.Minute || res.Dropped != 0 {
		t.Fatalf("resumed %+v", res)
	}
	st1.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := r1.ReadString('\n'); err == nil {
		t.Fatal("old stream still open")
	}
	srv.send(t, ":bob!b@h PRIVMSG alice :hi")
	if got := readLine(t, bufio.NewReader(st2)); got != ":bob!b@h PRIVMSG alice :hi" {
		t.Fatalf("got %q", got)
	}
	// the old stream closing doesn't detach the new one
	st1.Close()
	io.WriteString(st2, "PRIVMSG bob :yo\r\n")
	srv.expect(t, "PRIVMSG bob :yo")
	if ls := listLinks(t, cl); len(ls) != 1 || !ls[0].Attached {
		t.Fatalf("links %+v", ls)
	}
}

func TestAttachBeforeRegistration(t *testing.T) {
	srv := newFakeServer(t)
	cl, _ := serveHolder(t, quietHolder())
	ctx := context.Background()
	if _, _, err := cl.Resume(ctx, "nope"); !errors.Is(err, ErrNoLink) {
		t.Fatalf("unknown link: %v", err)
	}
	tcp, _ := net.Dial("tcp", srv.ln.Addr().String())
	st, err := cl.Open(ctx, "n1", tcp, false, "irc.test", irctls.Trust{})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := cl.Resume(ctx, "n1"); !errors.Is(err, ErrNoLink) {
		t.Fatalf("unregistered link: %v", err)
	}
	// Lines before 001 aren't buffered, and evrd going away before
	// registration ends the link.
	io.WriteString(st, "NICK a\r\n")
	srv.expect(t, "NICK a")
	srv.send(t, ":srv NOTICE * :Looking up your hostname")
	readLine(t, bufio.NewReader(st))
	st.Close()
	eventually(t, "the unregistered link is dropped", func() bool { return len(listLinks(t, cl)) == 0 })
}

func TestOpenReplacesALinkWithTheSameID(t *testing.T) {
	srv := newFakeServer(t)
	cl, _ := serveHolder(t, quietHolder())
	_, r1 := openRegistered(t, srv, cl, "n1", "alice")
	openRegistered(t, srv, cl, "n1", "alice")
	// the first one is gone: its stream ends
	if _, err := r1.ReadString('\n'); err == nil {
		t.Fatal("first stream still open")
	}
	if ls := listLinks(t, cl); len(ls) != 1 {
		t.Fatalf("links %+v", ls)
	}
}

func TestKeepAliveWhileDetached(t *testing.T) {
	srv := newFakeServer(t)
	h := quietHolder()
	h.KeepAlive = 20 * time.Millisecond
	cl, _ := serveHolder(t, h)
	st, _ := openRegistered(t, srv, cl, "n1", "alice")
	st.Close()
	eventually(t, "detached", func() bool { ls := listLinks(t, cl); return len(ls) == 1 && !ls[0].Attached })
	srv.expect(t, "PING :evrd-links")
	// the answer to our own ping isn't kept for evrd; another PONG is
	srv.send(t, ":srv PONG srv :evrd-links")
	srv.send(t, ":srv PONG srv :something-else")
	eventually(t, "one line buffered", func() bool { return listLinks(t, cl)[0].Buffered == 1 })
	st2, _, err := cl.Resume(context.Background(), "n1")
	if err != nil {
		t.Fatal(err)
	}
	if got := readLine(t, bufio.NewReader(st2)); got != ":srv PONG srv :something-else" {
		t.Fatalf("got %q", got)
	}
}

func TestKeepAliveStopsWithTheLink(t *testing.T) {
	srv := newFakeServer(t)
	h := quietHolder()
	h.KeepAlive = 10 * time.Millisecond
	cl, _ := serveHolder(t, h)
	openRegistered(t, srv, cl, "n1", "alice")
	h.mu.Lock()
	l := h.links["n1"]
	h.mu.Unlock()
	done := make(chan struct{})
	go func() { h.keepAlive(l); close(done) }()
	if err := cl.Close(context.Background(), "n1", ""); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("keepAlive still running after the link closed")
	}
}

func TestQuietServerTimesOut(t *testing.T) {
	srv := newFakeServer(t)
	h := quietHolder()
	h.ReadTimeout = 30 * time.Millisecond
	cl, _ := serveHolder(t, h)
	tcp, err := net.Dial("tcp", srv.ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	st, err := cl.Open(context.Background(), "n1", tcp, false, "irc.test", irctls.Trust{})
	if err != nil {
		t.Fatal(err)
	}
	// the server never says a word: the link is dropped and evrd's stream ends
	st.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := bufio.NewReader(st).ReadString('\n'); !errors.Is(err, io.EOF) {
		t.Fatalf("stream: %v", err)
	}
	if ls := listLinks(t, cl); len(ls) != 0 {
		t.Fatalf("links %+v", ls)
	}
}

func TestServerClosingEndsTheStream(t *testing.T) {
	srv := newFakeServer(t)
	cl, _ := serveHolder(t, quietHolder())
	_, r := openRegistered(t, srv, cl, "n1", "alice")
	srv.send(t, "ERROR :Closing link")
	srv.mu.Lock()
	srv.conns[0].Close()
	srv.mu.Unlock()
	if got := readLine(t, r); got != "ERROR :Closing link" {
		t.Fatalf("got %q", got)
	}
	if _, err := r.ReadString('\n'); err == nil {
		t.Fatal("stream still open")
	}
	eventually(t, "the link is gone", func() bool { return len(listLinks(t, cl)) == 0 })
}

func TestStoppingTheHolderQuitsRegisteredLinks(t *testing.T) {
	srv := newFakeServer(t)
	cl, stop := serveHolder(t, quietHolder())
	openRegistered(t, srv, cl, "n1", "alice")
	// and one that never registered: no QUIT for it
	tcp, _ := net.Dial("tcp", srv.ln.Addr().String())
	if _, err := cl.Open(context.Background(), "n2", tcp, false, "irc.test", irctls.Trust{}); err != nil {
		t.Fatal(err)
	}
	stop()
	srv.expect(t, "QUIT :Bouncer restarting")
	n := 0
	for _, l := range srv.received() {
		if strings.HasPrefix(l, "QUIT") {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("%d QUITs", n)
	}
	if _, err := cl.List(context.Background()); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("holder still answering: %v", err)
	}
}

func TestServeReturnsListenerErrors(t *testing.T) {
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: sockPath(t), Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	ln.Close()
	if err := quietHolder().Serve(context.Background(), ln); err == nil {
		t.Fatal("no error from a closed listener")
	}
}

func TestPingAnsweredOnlyWhileDetached(t *testing.T) {
	srv := newFakeServer(t)
	cl, _ := serveHolder(t, quietHolder())
	st, r := openRegistered(t, srv, cl, "n1", "alice")
	// attached: evrd answers PINGs itself, so the holder passes them on
	srv.send(t, "PING :x1")
	if got := readLine(t, r); got != "PING :x1" {
		t.Fatalf("got %q", got)
	}
	st.Close()
	eventually(t, "detached", func() bool { return !listLinks(t, cl)[0].Attached })
	srv.send(t, "PING")
	srv.expect(t, "PONG :")
	srv.send(t, "PING irc.example.org")
	srv.expect(t, "PONG :irc.example.org")
	for _, l := range srv.received() {
		if l == "PONG :x1" {
			t.Fatal("holder answered a PING evrd was there for")
		}
	}
	if b := listLinks(t, cl)[0].Buffered; b != 0 {
		t.Fatalf("%d PINGs buffered", b)
	}
}

func TestNickTracking(t *testing.T) {
	h := quietHolder()
	l := &link{id: "n1", conn: nopConn{}}
	for _, line := range []string{
		":srv 001 alice :Welcome",
		":srv 005 alice CHANTYPES=# :are supported",
		":srv 005 alice NICKLEN=30 :are supported",
		":bob!b@h NICK carol",    // someone else
		":ALICE!a@h NICK alice2", // us, case-insensitively
		"@time=2024-01-01T00:00:00Z :alice2!a@h NICK :alice3",
		":alice3 NICK alice4", // no user@host
		"PRIVMSG",             // no params
		":srv",                // prefix only
	} {
		h.fromServerLine(l, []byte(line+"\r\n"))
	}
	if l.nick != "alice4" || len(l.isupport) != 2 || !l.registered {
		t.Fatalf("nick %q isupport %q registered %v", l.nick, l.isupport, l.registered)
	}
	// a line without its newline still goes out with one
	h.fromServerLine(l, []byte(":srv NOTICE alice4 :cut"))
	if got := string(l.buf[len(l.buf)-1]); got != ":srv NOTICE alice4 :cut\r\n" {
		t.Fatalf("buffered %q", got)
	}
	// a new 001 (reconnect) starts isupport over
	h.fromServerLine(l, []byte(":srv 001 zed :Welcome\r\n"))
	if l.nick != "zed" || len(l.isupport) != 0 {
		t.Fatalf("after 001: %q %q", l.nick, l.isupport)
	}
}

func TestParse(t *testing.T) {
	for _, c := range []struct {
		in, prefix, cmd string
		params          []string
	}{
		{"PING :a b", "", "PING", []string{"a b"}},
		{":n!u@h privmsg #c :hi there", "n!u@h", "PRIVMSG", []string{"#c", "hi there"}},
		{"@a=b;c :srv 005 me X=1 Y :are supported", "srv", "005", []string{"me", "X=1", "Y", "are supported"}},
		{"@a=b PING :x", "", "PING", []string{"x"}},
		{":srv", "srv", "", nil},
		{"CMD  a   b", "", "CMD", []string{"a", "b"}},
		{"", "", "", nil},
	} {
		p, cmd, params := parse(c.in)
		if p != c.prefix || cmd != c.cmd || strings.Join(params, "|") != strings.Join(c.params, "|") {
			t.Errorf("parse(%q) = %q %q %q", c.in, p, cmd, params)
		}
	}
	if nickOf("a!b@c") != "a" || nickOf("srv.example") != "srv.example" {
		t.Error("nickOf")
	}
}

func TestDescribe(t *testing.T) {
	if got := describe(io.EOF); got != "closed" {
		t.Errorf("EOF: %q", got)
	}
	if got := describe(os.ErrDeadlineExceeded); got != "timed out" {
		t.Errorf("timeout: %q", got)
	}
	if got := describe(errors.New("connection reset")); got != "connection reset" {
		t.Errorf("other: %q", got)
	}
}

// nopConn is a server connection that accepts and discards writes.
type nopConn struct{ net.Conn }

func (nopConn) Write(p []byte) (int, error)       { return len(p), nil }
func (nopConn) SetWriteDeadline(time.Time) error  { return nil }
func (nopConn) Close() error                      { return nil }
func (nopConn) SetReadDeadline(t time.Time) error { return nil }
func (nopConn) Read(p []byte) (int, error)        { return 0, io.EOF }
func (nopConn) SetDeadline(t time.Time) error     { return nil }
func (nopConn) LocalAddr() net.Addr               { return nil }
func (nopConn) RemoteAddr() net.Addr              { return nil }

// fakeHolder answers every request with answer (a raw line, or nothing
// when empty: it hangs up).
func fakeHolder(t *testing.T, answer string) *Client {
	t.Helper()
	path := sockPath(t)
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				bufio.NewReader(c).ReadBytes('\n')
				io.WriteString(c, answer)
			}()
		}
	}()
	return NewClient(path)
}

func TestClientErrors(t *testing.T) {
	ctx := context.Background()
	srv := newFakeServer(t)
	dialTCP := func() net.Conn {
		c, err := net.Dial("tcp", srv.ln.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		return c
	}

	refuse := fakeHolder(t, `{"error":"nope"}`+"\n")
	if _, err := refuse.List(ctx); err == nil || err.Error() != "links: nope" {
		t.Errorf("List: %v", err)
	}
	if err := refuse.Close(ctx, "n1", ""); err == nil || err.Error() != "links: nope" {
		t.Errorf("Close: %v", err)
	}
	if _, _, err := refuse.Resume(ctx, "n1"); err == nil || err.Error() != "links: nope" || errors.Is(err, ErrNoLink) {
		t.Errorf("Resume: %v", err)
	}
	// Open's failure is the server's, passed on: no "links:" prefix
	if _, err := refuse.Open(ctx, "n1", dialTCP(), false, "h", irctls.Trust{}); err == nil || err.Error() != "nope" {
		t.Errorf("Open: %v", err)
	}
	// an older holder's bare "EOF" from a TLS handshake is the server hanging up
	if _, err := fakeHolder(t, `{"error":"EOF"}`+"\n").Open(ctx, "n1", dialTCP(), true, "h", irctls.Trust{}); !errors.Is(err, irctls.ErrHungUp) {
		t.Errorf("Open EOF: %v", err)
	}

	garbled := fakeHolder(t, "<html>\n")
	if _, err := garbled.List(ctx); err == nil || !strings.Contains(err.Error(), "bad answer") {
		t.Errorf("garbled: %v", err)
	}
	silent := fakeHolder(t, "")
	if _, err := silent.List(ctx); err == nil || !errors.Is(err, io.EOF) {
		t.Errorf("silent: %v", err)
	}
	if _, err := silent.Open(ctx, "n1", dialTCP(), false, "h", irctls.Trust{}); err == nil {
		t.Error("Open with a silent holder: no error")
	}

	nobody := NewClient(filepath.Join(t.TempDir(), "x.sock"))
	if _, err := nobody.List(ctx); !errors.Is(err, ErrUnavailable) {
		t.Errorf("List: %v", err)
	}
	if err := nobody.Close(ctx, "n1", ""); !errors.Is(err, ErrUnavailable) {
		t.Errorf("Close: %v", err)
	}
	if _, _, err := nobody.Resume(ctx, "n1"); !errors.Is(err, ErrUnavailable) {
		t.Errorf("Resume: %v", err)
	}

	// Open needs a real socket to hand over.
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	if _, err := refuse.Open(ctx, "n1", a, false, "h", irctls.Trust{}); err == nil || !strings.Contains(err.Error(), "not a socket") {
		t.Errorf("pipe: %v", err)
	}
	// ... that's still open.
	closed := dialTCP()
	closed.Close()
	if _, err := refuse.Open(ctx, "n1", closed, false, "h", irctls.Trust{}); err == nil {
		t.Error("closed socket: no error")
	}
}

func TestClientHonoursTheContextDeadline(t *testing.T) {
	path := sockPath(t)
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		c, err := ln.Accept()
		if err == nil {
			defer c.Close()
			io.Copy(io.Discard, c) // never answers
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := NewClient(path).List(ctx); err == nil {
		t.Fatal("no error")
	}
	if time.Since(start) > time.Second {
		t.Fatalf("took %v", time.Since(start))
	}
}

func TestWriteJSON(t *testing.T) {
	var sb strings.Builder
	w := bufio.NewWriter(&sb)
	if err := writeJSON(w, response{OK: true}); err != nil || sb.String() != `{"ok":true}`+"\n" {
		t.Fatalf("%q %v", sb.String(), err)
	}
	if err := writeJSON(w, func() {}); err == nil {
		t.Fatal("marshalled a func")
	}
	if err := writeJSON(bufio.NewWriterSize(failWriter{}, 16), response{Error: strings.Repeat("x", 100)}); err == nil {
		t.Fatal("no write error")
	}
}

type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, errors.New("broken") }
