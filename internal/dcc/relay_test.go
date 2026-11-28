package dcc

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/4evrd/evrd/internal/config"
	"github.com/4evrd/evrd/internal/messages"
)

// testRelay is a relay on loopback: evrd's public address is 127.0.0.1,
// private addresses are allowed (or everything would be refused), and the
// user's clients attach from 127.0.0.1.
type testRelay struct {
	*Relay
	mu   sync.Mutex
	sent []string
	logs []string
	cfg  config.DCC
}

func newRelay(t *testing.T) *testRelay {
	t.Helper()
	tr := &testRelay{cfg: config.DCC{Public: []netip.Addr{netip.MustParseAddr("127.0.0.1")}, Listen: "127.0.0.1",
		Timeout: 3 * time.Second, Private: true}}
	tr.Relay = &Relay{
		Config:    func() config.DCC { tr.mu.Lock(); defer tr.mu.Unlock(); return tr.cfg },
		Send:      func(l string) { tr.mu.Lock(); tr.sent = append(tr.sent, l); tr.mu.Unlock() },
		ClientIPs: func() []string { return []string{"127.0.0.1"} },
		Log:       func(m messages.Text) { tr.mu.Lock(); tr.logs = append(tr.logs, m.English()); tr.mu.Unlock() },
	}
	return tr
}

func (tr *testRelay) set(f func(*config.DCC)) {
	tr.mu.Lock()
	f(&tr.cfg)
	tr.mu.Unlock()
}

// waitLog waits for a log line containing s.
func (tr *testRelay) waitLog(t *testing.T, s string) {
	t.Helper()
	end := time.Now().Add(5 * time.Second)
	for time.Now().Before(end) {
		tr.mu.Lock()
		for _, l := range tr.logs {
			if strings.Contains(l, s) {
				tr.mu.Unlock()
				return
			}
		}
		tr.mu.Unlock()
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("no log with %q: %q", s, tr.logs)
}

// waitSent waits for a line sent to the server.
func (tr *testRelay) waitSent(t *testing.T) string {
	t.Helper()
	end := time.Now().Add(5 * time.Second)
	for time.Now().Before(end) {
		tr.mu.Lock()
		if len(tr.sent) > 0 {
			l := tr.sent[0]
			tr.mu.Unlock()
			return l
		}
		tr.mu.Unlock()
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("nothing sent to the server")
	return ""
}

// peer listens on loopback, as the other side (or the user's client) of a
// DCC, and hands over the one connection it gets.
func peer(t *testing.T) (port int, conns <-chan net.Conn) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	ch := make(chan net.Conn, 1)
	go func() {
		c, err := l.Accept()
		if err == nil {
			ch <- c
		}
	}()
	return l.Addr().(*net.TCPAddr).Port, ch
}

func got(t *testing.T, ch <-chan net.Conn) net.Conn {
	t.Helper()
	select {
	case c := <-ch:
		t.Cleanup(func() { c.Close() })
		return c
	case <-time.After(5 * time.Second):
		t.Fatal("nobody connected")
		return nil
	}
}

// connect connects to an offer the way a DCC client does.
func connect(t *testing.T, text string) (net.Conn, Offer) {
	t.Helper()
	o, ok := Parse(text)
	if !ok {
		t.Fatalf("not an offer: %q", text)
	}
	if o.Addr != netip.MustParseAddr("127.0.0.1") {
		t.Fatalf("the offer doesn't point at evrd: %q", text)
	}
	c, err := net.Dial("tcp", netip.AddrPortFrom(o.Addr, uint16(o.Port)).String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c, o
}

func offer(typ, name string, port int, size int64, token string) string {
	return Offer{Type: typ, Name: name, Addr: netip.MustParseAddr("127.0.0.1"), Port: port, Size: size, Token: token}.String()
}

// sendFile writes data, as a DCC SEND's sender, and closes.
func sendFile(c net.Conn, data []byte) { c.Write(data); c.(*net.TCPConn).CloseWrite() }

func readAll(t *testing.T, c net.Conn) []byte {
	t.Helper()
	c.SetReadDeadline(time.Now().Add(5 * time.Second))
	b, err := io.ReadAll(c)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// Someone offers the user a file: the client is given evrd's address,
// connects, and evrd fetches the file from them.
func TestIncomingSend(t *testing.T) {
	tr := newRelay(t)
	port, theirs := peer(t)
	data := bytes.Repeat([]byte("evrd"), 50000)
	out := tr.FromServer("bob!b@host", offer("SEND", "big file.bin", port, int64(len(data)), ""))
	client, o := connect(t, out)
	if o.Port == port || o.Name != "big file.bin" || o.Size != int64(len(data)) {
		t.Fatalf("offer %+v", o)
	}
	go sendFile(got(t, theirs), data)
	if b := readAll(t, client); !bytes.Equal(b, data) {
		t.Fatalf("got %d bytes, want %d", len(b), len(data))
	}
	client.Close()
	tr.waitLog(t, "DCC SEND big file.bin with bob done")
}

// The user's client offers a chat: the other side is given evrd's address
// and evrd connects to the client, where it attached from.
func TestOutgoingChat(t *testing.T) {
	tr := newRelay(t)
	port, mine := peer(t)
	// the client's offer names another address: evrd goes where it attached from
	text := Offer{Type: "CHAT", Name: "chat", Addr: netip.MustParseAddr("192.0.2.7"), Port: port, Size: -1}.String()
	out := tr.FromClient("bob", text, "127.0.0.1")
	them, o := connect(t, out)
	if o.Port == port || o.Type != "CHAT" {
		t.Fatalf("offer %+v", o)
	}
	client := got(t, mine)
	io.WriteString(them, "hi from bob\n")
	if l, _ := bufio.NewReader(client).ReadString('\n'); l != "hi from bob\n" {
		t.Fatalf("client read %q", l)
	}
	io.WriteString(client, "hi from alice\n")
	if l, _ := bufio.NewReader(them).ReadString('\n'); l != "hi from alice\n" {
		t.Fatalf("bob read %q", l)
	}
	them.Close()
	tr.waitLog(t, "DCC CHAT chat with bob done")
}

// The user sends a file: evrd connects to the client and passes on what
// it sends.
func TestOutgoingSend(t *testing.T) {
	tr := newRelay(t)
	port, mine := peer(t)
	out := tr.FromClient("bob", offer("SEND", "f.txt", port, 5, ""), "::ffff:127.0.0.1")
	them, _ := connect(t, out)
	go sendFile(got(t, mine), []byte("hello"))
	if b := readAll(t, them); string(b) != "hello" {
		t.Fatalf("bob got %q", b)
	}
}

// Someone offers a passive file (they can't listen): the client gets an
// ordinary offer; when it connects, evrd listens for them and answers with
// its address and their token.
func TestIncomingPassive(t *testing.T) {
	tr := newRelay(t)
	out := tr.FromServer("bob!b@host", offer("SEND", "p.txt", 0, 4, "42"))
	client, o := connect(t, out)
	if o.Port == 0 || o.Token != "" {
		t.Fatalf("the client should get an ordinary offer: %+v", o)
	}
	line := tr.waitSent(t)
	text, ok := strings.CutPrefix(line, "PRIVMSG bob :")
	if !ok {
		t.Fatalf("answer %q", line)
	}
	them, a := connect(t, text)
	if a.Token != "42" || a.Port == 0 || a.Name != "p.txt" {
		t.Fatalf("answer %+v", a)
	}
	sendFile(them, []byte("data"))
	if b := readAll(t, client); string(b) != "data" {
		t.Fatalf("client got %q", b)
	}
}

// The user's client offers a passive file: it goes on (with evrd's
// address); the answer, with where they listen, comes back pointing at
// evrd, and evrd connects to them.
func TestOutgoingPassive(t *testing.T) {
	tr := newRelay(t)
	out := tr.FromClient("bob", Offer{Type: "SEND", Name: "q.txt", Addr: netip.MustParseAddr("10.1.1.1"), Port: 0, Size: 3, Token: "7"}.String(), "127.0.0.1")
	o, _ := Parse(out)
	if o.Port != 0 || o.Token != "7" || o.Addr != netip.MustParseAddr("127.0.0.1") {
		t.Fatalf("passed on %+v", o)
	}
	port, theirs := peer(t)
	// an answer with another token isn't this one's
	if a := tr.FromServer("bob!b@h", offer("SEND", "q.txt", port, 3, "8")); a == "" {
		t.Fatal("dropped")
	} else if ao, _ := Parse(a); ao.Port == port {
		t.Fatal("an unrelated offer went to the client as it was")
	}
	answer := tr.FromServer("bob!b@h", offer("SEND", "q.txt", port, 3, "7"))
	client, a := connect(t, answer)
	if a.Port == port || a.Token != "7" {
		t.Fatalf("answer %+v", a)
	}
	sendFile(client, []byte("abc"))
	if b := readAll(t, got(t, theirs)); string(b) != "abc" {
		t.Fatalf("bob got %q", b)
	}
}

// RESUME and ACCEPT name the port each side knows.
func TestResume(t *testing.T) {
	tr := newRelay(t)
	port, _ := peer(t)
	out := tr.FromServer("bob!b@host", offer("SEND", "f", port, 100, ""))
	o, _ := Parse(out)
	resume := tr.FromClient("bob", Offer{Type: "RESUME", Name: "f", Port: o.Port, Size: 50}.String(), "127.0.0.1")
	if r, _ := Parse(resume); r.Port != port || r.Size != 50 {
		t.Fatalf("resume to bob: %q", resume)
	}
	accept := tr.FromServer("bob!b@host", Offer{Type: "ACCEPT", Name: "f", Port: port, Size: 50}.String())
	if a, _ := Parse(accept); a.Port != o.Port {
		t.Fatalf("accept to the client: %q", accept)
	}
	// someone else's RESUME isn't touched
	other := Offer{Type: "RESUME", Name: "f", Port: o.Port, Size: 50}.String()
	if got := tr.FromClient("carol", other, "127.0.0.1"); got != other {
		t.Fatalf("changed %q", got)
	}

	// a passive offer's RESUME goes to them with port 0 and the token
	out = tr.FromServer("bob!b@host", offer("SEND", "g", 0, 100, "5"))
	o, _ = Parse(out)
	resume = tr.FromClient("bob", Offer{Type: "RESUME", Name: "g", Port: o.Port, Size: 10}.String(), "127.0.0.1")
	if r, _ := Parse(resume); r.Port != 0 || r.Token != "5" {
		t.Fatalf("passive resume: %q", resume)
	}
}

// Offers pointing inside evrd's network aren't followed unless allowed,
// nor ones at privileged ports.
func TestPrivateAddresses(t *testing.T) {
	tr := newRelay(t)
	tr.set(func(c *config.DCC) { c.Private = false })
	if out := tr.FromServer("bob!b@h", offer("SEND", "f", 5000, 1, "")); out != "" {
		t.Fatalf("relayed to loopback: %q", out)
	}
	tr.waitLog(t, "it points inside a private network (127.0.0.1:5000)")
	tr.set(func(c *config.DCC) { c.Private = true })
	if out := tr.FromServer("bob!b@h", offer("SEND", "f", 25, 1, "")); out != "" {
		t.Fatalf("relayed to port 25: %q", out)
	}
	for _, a := range []string{"10.0.0.1:5000", "192.168.0.1:5000", "fe80::1:5000", "[fd00::1]:5000", "0.0.0.0:5000", "224.0.0.1:5000"} {
		ap, err := netip.ParseAddrPort(a)
		if err != nil {
			continue // fe80::1:5000 isn't one; never mind
		}
		if reachable(config.DCC{}, ap) {
			t.Errorf("%s is reachable", a)
		}
	}
	if !reachable(config.DCC{}, netip.MustParseAddrPort("203.0.113.5:5000")) {
		t.Error("a public address isn't reachable")
	}
}

// A listener meant for the user's client takes only the client's address,
// and gives up after the timeout.
func TestClientOnlyAndTimeout(t *testing.T) {
	tr := newRelay(t)
	tr.ClientIPs = func() []string { return []string{"192.0.2.1"} }
	tr.set(func(c *config.DCC) { c.Timeout = 500 * time.Millisecond })
	port, _ := peer(t)
	out := tr.FromServer("bob!b@h", offer("CHAT", "chat", port, -1, ""))
	c, _ := connect(t, out)
	c.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := c.Read(make([]byte, 1)); err == nil {
		t.Fatal("a stranger's connection was kept")
	}
	tr.waitLog(t, "nobody connected to the DCC CHAT chat with bob in time")
	tr.mu.Lock()
	defer tr.mu.Unlock()
	if len(tr.Relay.transfers) != 0 {
		t.Fatalf("still waiting: %d", len(tr.Relay.transfers))
	}
}

// A file larger than the limit isn't relayed: by its offer, or when it
// turns out larger than it said.
func TestSizeLimit(t *testing.T) {
	tr := newRelay(t)
	tr.set(func(c *config.DCC) { c.MaxMB = 1 })
	if out := tr.FromServer("bob!b@h", offer("SEND", "huge", 5000, 2<<20, "")); out != "" {
		t.Fatalf("relayed a 2 MB offer: %q", out)
	}
	tr.waitLog(t, "it's bigger than this server relays (1 MB)")
	if out := tr.FromClient("bob", offer("SEND", "huge", 5000, 2<<20, ""), "127.0.0.1"); out != "" {
		t.Fatalf("sent a 2 MB offer: %q", out)
	}

	port, theirs := peer(t)
	out := tr.FromServer("bob!b@h", offer("SEND", "liar", port, 10, ""))
	client, _ := connect(t, out)
	go func() {
		c := got(t, theirs)
		chunk := make([]byte, 64<<10)
		for range 40 { // 2.5 MB
			if _, err := c.Write(chunk); err != nil {
				return
			}
		}
	}()
	client.SetReadDeadline(time.Now().Add(5 * time.Second))
	n, _ := io.Copy(io.Discard, client)
	if n > 1<<20 {
		t.Fatalf("relayed %d bytes", n)
	}
	tr.waitLog(t, "not relaying the DCC SEND liar with bob")
}

// Without a public address nothing is relayed: offers to the user pass
// as they are, the client's own aren't sent (they'd give its address
// away), and the user is told once.
func TestNoPublicAddress(t *testing.T) {
	tr := newRelay(t)
	tr.set(func(c *config.DCC) { c.Public = nil })
	var replies []string
	tr.Reply = func(m messages.Text) { replies = append(replies, m.English()) }
	in := offer("SEND", "f", 5000, 1, "")
	if out := tr.FromServer("bob!b@h", in); out != in {
		t.Fatalf("changed %q", out)
	}
	if out := tr.FromClient("bob", in, "127.0.0.1"); out != "" {
		t.Fatalf("sent %q", out)
	}
	if out := tr.FromServer("bob!b@h", in); out != in {
		t.Fatalf("changed %q", out)
	}
	// told once of offers to them, each time their own is dropped
	if len(replies) != 2 || !strings.Contains(replies[0], "dcc_public") {
		t.Fatalf("replies %q", replies)
	}
	// and other CTCPs, or messages to channels, aren't touched
	if out := tr.FromClient("#chan", in, "127.0.0.1"); out != in {
		t.Fatalf("a channel's %q", out)
	}
	if out := tr.FromServer("bob!b@h", "\x01VERSION\x01"); out != "\x01VERSION\x01" {
		t.Fatal(out)
	}
}

// One connection relays only so many DCCs at once.
func TestBusy(t *testing.T) {
	tr := newRelay(t)
	port, _ := peer(t)
	for i := range MaxWaiting {
		if out := tr.FromServer("bob!b@h", offer("SEND", "f"+strconv.Itoa(i), port, 1, "")); out == "" {
			t.Fatalf("offer %d refused", i)
		}
	}
	if out := tr.FromServer("bob!b@h", offer("SEND", "one more", port, 1, "")); out != "" {
		t.Fatal("no limit")
	}
	tr.waitLog(t, "too many DCCs at once")
}

func TestListenPortRange(t *testing.T) {
	base := freePort(t)
	cfg := config.DCC{Listen: "127.0.0.1", Ports: []config.PortRange{{Lo: base, Hi: base + 1}}}
	a, err := listen(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := listen(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if pa, pb := portOf(a), portOf(b); pa == pb || pa < base || pa > base+1 || pb < base || pb > base+1 {
		t.Fatalf("ports %d %d of %d-%d", pa, pb, base, base+1)
	}
	if _, err := listen(cfg); !errors.Is(err, errNoPort) {
		t.Fatalf("a third: %v", err)
	}
}

// freePort finds a port whose next one is free too.
func freePort(t *testing.T) int {
	for range 50 {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		p := l.Addr().(*net.TCPAddr).Port
		l2, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(p+1))
		l.Close()
		if err == nil {
			l2.Close()
			return p
		}
	}
	t.Fatal("no two free ports")
	return 0
}

// A file transfer that stalls is cut off; a chat isn't.
func TestIdle(t *testing.T) {
	a1, a2 := tcpPair(t)
	b1, b2 := tcpPair(t)
	done := make(chan error, 1)
	go func() { _, err := pipe(a2, b1, false, 300*time.Millisecond, 0); done <- err }()
	a1.Write([]byte("x"))
	select {
	case err := <-done:
		if !errors.Is(err, errIdle) {
			t.Fatalf("ended with %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("a stalled transfer went on")
	}
	b2.SetReadDeadline(time.Now().Add(time.Second))
	if b, _ := io.ReadAll(b2); string(b) != "x" {
		t.Fatalf("got %q", b)
	}
}

func tcpPair(t *testing.T) (net.Conn, net.Conn) {
	t.Helper()
	port, ch := peer(t)
	c, err := net.Dial("tcp", "127.0.0.1:"+strconv.Itoa(port))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c, got(t, ch)
}
