package testirc

import (
	"bufio"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"
)

// client is a raw connection to the fake server.
type client struct {
	t  *testing.T
	c  net.Conn
	sc *bufio.Scanner
}

func dial(t *testing.T, addr string) *client {
	t.Helper()
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return &client{t: t, c: c, sc: bufio.NewScanner(c)}
}

func (c *client) send(l string) { fmt.Fprintf(c.c, "%s\r\n", l) }

func (c *client) expect(want string) {
	c.t.Helper()
	c.c.SetReadDeadline(time.Now().Add(5 * time.Second))
	if !c.sc.Scan() {
		c.t.Fatalf("no line, want %q: %v", want, c.sc.Err())
	}
	if got := c.sc.Text(); got != want {
		c.t.Fatalf("got %q, want %q", got, want)
	}
}

func TestServerNegotiatesCaps(t *testing.T) {
	c := dial(t, StartCaps(t, nil, "message-tags away-notify"))
	c.send("CAP LS 302")
	c.expect(":fake CAP * LS :message-tags away-notify")
	c.send("CAP REQ :message-tags")
	c.expect(":fake CAP * ACK :message-tags")
	c.send("CAP REQ :sasl")
	c.expect(":fake CAP * NAK :sasl")
	c.send("NICK alice")
	c.send("USER alice 0 * :Alice")
	c.expect(":fake 001 alice :welcome")
	c.send("CAP LIST")
	c.expect(":fake CAP alice LIST :message-tags")
}

func TestServerRegistersAndConfirms(t *testing.T) {
	var log Log
	addr := StartLogged(t, &log)
	c := dial(t, addr)
	c.send("CAP LS 302")
	c.expect(":fake CAP * LS :")
	c.send("CAP END") // ignored
	c.send("")        // blank lines are skipped
	c.send("NICK alice")
	c.send("USER alice 0 * :Alice")
	c.expect(":fake 001 alice :welcome")
	c.send("JOIN #c")
	c.expect(":alice!u@h JOIN #c")
	c.send("NAMES #c")
	c.expect(":fake 353 alice = #c :alice @op")
	c.expect(":fake 366 alice #c :End of /NAMES list.")
	c.send("NAMES") // no channel: no reply
	c.send("TOPIC #c")
	c.expect(":fake 332 alice #c :the topic")
	c.send("TOPIC")
	c.send("PART #c")
	c.expect(":alice!u@h PART #c :bye")
	c.send("NICK bob")
	c.expect(":alice!u@h NICK bob")
	c.send("SIMULATE :x!y@z PRIVMSG bob :hi")
	c.expect(":x!y@z PRIVMSG bob :hi")

	log.Send(":srv NOTICE bob :pushed")
	c.expect(":srv NOTICE bob :pushed")
	if log.Connections() != 1 {
		t.Fatalf("connections %d", log.Connections())
	}
	if !log.Has("USER alice") || log.Has("never sent") {
		t.Fatal("Has")
	}
	if l := log.Lines(); len(l) < 10 || l[0] != "CAP LS 302" {
		t.Fatalf("lines %q", l)
	}
	if !strings.HasPrefix(strings.Join(log.Lines(), "|"), "CAP LS 302|CAP END||NICK alice") {
		t.Fatalf("lines %q", log.Lines())
	}
}

func TestStartWithoutALog(t *testing.T) {
	c := dial(t, Start(t))
	c.send("NICK n")
	c.send("USER n 0 * :n")
	c.expect(":fake 001 n :welcome")
}
