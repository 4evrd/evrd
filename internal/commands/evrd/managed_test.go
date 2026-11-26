package evrd

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/4evrd/evrd/internal/managed/managedtest"
	"github.com/4evrd/evrd/internal/testirc"
)

// managedEvrd is evrd running in managed mode.
type managedEvrd struct {
	t        *testing.T
	api, irc string
	stderr   *syncBuffer
	issuer   *managedtest.Issuer
}

// startManaged runs evrd in managed mode in front of the IRC server at
// server (host:port), with WEBIRC, until the test ends.
func startManaged(t *testing.T, server string) *managedEvrd {
	t.Helper()
	noEnv(t)
	is := managedtest.New(t)
	host, port, _ := net.SplitHostPort(server)
	conf, api, ircAddr := testConfig(t, shortDir(t),
		"managed yes",
		"managed_network ExampleNet",
		"managed_server "+host+" "+port,
		"managed_server_name irc.example.org",
		"managed_token_issuer "+is.URL,
		"managed_token_audience "+managedtest.Audience,
		"managed_webirc_password webirc-secret",
		"managed_webirc_resolve no",
		"managed_away Away",
	)
	t.Setenv("EVRD_MANAGED_TOKEN", "api-token")
	ctx, cancel := context.WithCancel(context.Background())
	m := &managedEvrd{t: t, api: api, irc: ircAddr, stderr: &syncBuffer{}, issuer: is}
	done := make(chan int, 1)
	go func() { done <- Execute(ctx, []string{"evrd", "-config", conf}, io.Discard, m.stderr) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(15 * time.Second):
			t.Error("evrd didn't stop")
		}
	})
	deadline := time.Now().Add(15 * time.Second)
	for {
		if code, _ := m.call("GET", "/api/managed/health", nil); code == 200 {
			return m
		}
		select {
		case code := <-done:
			t.Fatalf("evrd exited (%d):\n%s", code, m.stderr)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("the managed API never answered:\n%s", m.stderr)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// call is a managed API request.
func (m *managedEvrd) call(method, path string, body any) (int, map[string]any) {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, "http://"+m.api+path, rd)
	req.Header.Set("Authorization", "Bearer api-token")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, nil
	}
	defer res.Body.Close()
	var out map[string]any
	json.NewDecoder(res.Body).Decode(&out)
	return res.StatusCode, out
}

// lineConn is a raw IRC connection that keeps what it read.
type lineConn struct {
	t    *testing.T
	c    net.Conn
	sc   *bufio.Scanner
	seen []string
}

func dialIRC(t *testing.T, addr string) *lineConn {
	t.Helper()
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return &lineConn{t: t, c: c, sc: bufio.NewScanner(c)}
}

func (l *lineConn) send(f string, a ...any) { fmt.Fprintf(l.c, f+"\r\n", a...) }

// expect reads until a line contains want, answering PINGs.
func (l *lineConn) expect(want string) string {
	l.t.Helper()
	l.c.SetReadDeadline(time.Now().Add(15 * time.Second))
	for l.sc.Scan() {
		line := l.sc.Text()
		l.seen = append(l.seen, line)
		if p, ok := strings.CutPrefix(line, "PING "); ok {
			l.send("PONG %s", p)
		}
		if strings.Contains(line, want) {
			return line
		}
	}
	l.t.Fatalf("never got %q; saw:\n%s", want, strings.Join(l.seen, "\n"))
	return ""
}

// noEvrd fails for any line naming evrd.
func (l *lineConn) noEvrd() {
	l.t.Helper()
	for _, line := range l.seen {
		if strings.Contains(strings.ToLower(line), "evrd") {
			l.t.Errorf("sent %q", line)
		}
	}
}

// evrd in managed mode: the managed API instead of the web app's, accounts
// made through it, clients signing in with an access token, and the
// network reached with WEBIRC.
func TestManagedMode(t *testing.T) {
	ircLog := &testirc.Log{}
	m := startManaged(t, testirc.StartLogged(t, ircLog))
	if !strings.Contains(m.stderr.String(), "msg=\"managed mode\" network=ExampleNet") {
		t.Errorf("not logged:\n%s", m.stderr)
	}
	if code, _ := m.call("GET", "/api/config", nil); code != 404 {
		t.Errorf("the web app's API is there: %d", code)
	}
	if code, out := m.call("PUT", "/api/managed/users/alice", map[string]any{"subject": "usr_1", "channels": []string{"#chat"}}); code != 201 {
		t.Fatalf("create: %d %v", code, out)
	}
	c := dialIRC(t, m.irc)
	c.send("PASS %s", m.issuer.Token("usr_1", "alice"))
	c.send("NICK alice")
	c.send("USER alice 0 * :Alice")
	if l := c.expect(" 001 "); !strings.HasPrefix(l, ":irc.example.org 001 alice ") {
		t.Fatalf("welcome: %q\n%s", l, m.stderr)
	}
	c.expect("JOIN #chat")
	c.noEvrd()
	if !ircLog.Has("WEBIRC webirc-secret gateway 127.0.0.1 127.0.0.1") {
		t.Errorf("no WEBIRC: %q", ircLog.Lines())
	}
	if code, out := m.call("GET", "/api/managed/health", nil); code != 200 || out["ok"] != true {
		t.Errorf("health: %d %v", code, out)
	}
}

// Against a real ngircd (dev/e2e-managed.sh starts one in Docker and sets
// EVRD_E2E_NGIRCD): a Boson user gets IRCv3 (server-time, msgids, echo)
// from a server without it, shows on it with their own address (WEBIRC),
// and talks with a classic user connected to it directly.
func TestManagedNgircd(t *testing.T) {
	server := os.Getenv("EVRD_E2E_NGIRCD")
	if server == "" {
		t.Skip("set EVRD_E2E_NGIRCD=host:port (dev/e2e-managed.sh)")
	}
	m := startManaged(t, server)
	if code, out := m.call("PUT", "/api/managed/users/alice", map[string]any{
		"subject": "usr_1", "nick": "alice", "realname": "Alice from Boson", "channels": []string{"#boson"},
	}); code != 201 {
		t.Fatalf("create: %d %v", code, out)
	}

	b := dialIRC(t, m.irc)
	b.send("CAP LS 302")
	b.send("NICK alice")
	b.send("USER alice 0 * :Alice")
	b.send("CAP REQ :sasl server-time message-tags echo-message batch draft/chathistory")
	b.expect("ACK")
	b.send("AUTHENTICATE OAUTHBEARER")
	b.expect("AUTHENTICATE +")
	b.send("AUTHENTICATE %s", base64.StdEncoding.EncodeToString([]byte("n,,\x01auth=Bearer "+m.issuer.Token("usr_1", "alice")+"\x01\x01")))
	b.expect(" 903 ")
	b.send("CAP END")
	b.expect(":irc.example.org 001 alice ")
	b.expect("JOIN #boson")

	// a classic client, straight to ngircd
	c := dialIRC(t, server)
	c.send("NICK classic")
	c.send("USER classic 0 * :Classic")
	c.expect(" 001 classic ")
	c.send("JOIN #boson")
	c.expect("366 classic #boson")
	c.send("WHOIS alice")
	whois := c.expect(" 311 classic alice ")
	if f := strings.Fields(whois); len(f) < 6 || (f[5] != "127.0.0.1" && f[5] != "localhost") {
		t.Errorf("alice doesn't show with her own address (WEBIRC): %q", whois)
	}
	if !strings.Contains(whois, "Alice from Boson") {
		t.Errorf("real name: %q", whois)
	}
	c.send("PRIVMSG #boson :hello from classic")
	in := b.expect("PRIVMSG #boson :hello from classic")
	if !strings.Contains(in, "time=") || !strings.Contains(in, "msgid=") {
		t.Errorf("no server-time or msgid from a server without them: %q", in)
	}
	b.send("PRIVMSG #boson :hello from boson")
	if echo := b.expect("PRIVMSG #boson :hello from boson"); !strings.Contains(echo, "msgid=") {
		t.Errorf("echo-message: %q", echo)
	}
	c.expect(":alice!")
	b.send("CHATHISTORY LATEST #boson * 10")
	b.expect("BATCH +")
	b.expect("hello from classic")
	b.noEvrd()
	if code, out := m.call("GET", "/api/managed/health", nil); code != 200 || out["ok"] != true || out["connections"].(map[string]any)["connected"] != float64(1) {
		t.Errorf("health: %d %v", code, out)
	}
}
