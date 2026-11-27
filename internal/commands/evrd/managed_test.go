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
func startManaged(t *testing.T, server string, extra ...string) *managedEvrd {
	t.Helper()
	noEnv(t)
	is := managedtest.New(t)
	host, port, _ := net.SplitHostPort(server)
	conf, api, ircAddr := testConfig(t, shortDir(t), append([]string{
		"managed yes",
		"managed_network ExampleNet",
		"managed_server " + host + " " + port,
		"managed_server_name irc.example.org",
		"managed_token_issuer " + is.URL,
		"managed_token_audience " + managedtest.Audience,
		"managed_webirc_password webirc-secret",
		"managed_webirc_resolve no",
		"managed_away Away",
	}, extra...)...)
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
	// ngircd takes WEBIRC's gateway as the user name: send the ident there
	m := startManaged(t, server, "managed_webirc_gateway {ident}")
	if code, out := m.call("PUT", "/api/managed/users/alice", map[string]any{
		"subject": "usr_1", "nick": "alice", "ident": "alice", "realname": "Alice from Boson", "channels": []string{"#boson"},
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
	b.expect(" JOIN :#boson") // ngircd's form

	// a classic client, straight to ngircd
	c := dialIRC(t, server)
	c.send("NICK classic")
	c.send("USER classic 0 * :Classic")
	c.expect(" 001 classic ")
	c.send("JOIN #boson")
	c.expect("366 classic #boson")
	c.send("WHOIS alice")
	whois := c.expect(" 311 classic alice ")
	if f := strings.Fields(whois); len(f) < 6 || f[4] != "alice" || (f[5] != "127.0.0.1" && f[5] != "localhost") {
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

// registerNick registers nick with NickServ on server (a classic client),
// with Anope's quick kill protection (20 seconds). Its password is
// owner-secret-<nick>.
func registerNick(t *testing.T, server, nick string) {
	t.Helper()
	c := dialIRC(t, server)
	c.send("NICK %s", nick)
	c.send("USER %s 0 * :Owner", nick)
	c.expect(" 001 " + nick + " ")
	c.send("PRIVMSG NickServ :REGISTER owner-secret-%s %s@example.org", nick, nick)
	c.expect("registered.")
	c.send("PRIVMSG NickServ :SET KILL QUICK")
	c.expect("reduced delay")
	c.send("QUIT")
	c.c.Close()
}

// Against ngircd with Anope (dev/e2e-managed.sh, EVRD_E2E_NGIRCD_ANOPE):
// an account that doesn't log in to services, provisioned with nicks
// someone else registered with NickServ, moves to a nick that isn't
// registered before it joins anything, is never identified, and isn't
// renamed or killed when NickServ's protection would have hit.
func TestManagedNickGuard(t *testing.T) {
	server := os.Getenv("EVRD_E2E_NGIRCD_ANOPE")
	if server == "" {
		t.Skip("set EVRD_E2E_NGIRCD_ANOPE=host:port (dev/e2e-managed.sh)")
	}
	registerNick(t, server, "alice")
	registerNick(t, server, "alice-home")

	// a classic user in the channel, watching
	c := dialIRC(t, server)
	c.send("NICK watcher")
	c.send("USER watcher 0 * :Watcher")
	c.expect(" 001 watcher ")
	c.send("JOIN #boson")
	c.expect("366 watcher #boson")

	// no grace: off the nick at once, as it always was
	m := startManaged(t, server, "managed_webirc_gateway {ident}", "managed_nick_guard_grace 0")
	if code, out := m.call("PUT", "/api/managed/users/alice-home", map[string]any{
		"subject": "usr_fed", "nick": "alice-home", "altNick": "alice-home_", "ident": "alicehome",
		"auth": map[string]any{"method": "none"}, "channels": []string{"#boson"},
	}); code != 201 {
		t.Fatalf("create: %d %v", code, out)
	}
	b := dialIRC(t, m.irc)
	b.send("PASS %s", m.issuer.Token("usr_fed", "alice-home"))
	b.send("NICK alice-home")
	b.send("USER alice-home 0 * :Alice")
	b.expect(" 001 ")
	b.expect("NICK :alice-home_") // her own client follows
	b.expect(" JOIN :#boson")

	joined := c.expect(" JOIN :#boson")
	if !strings.HasPrefix(joined, ":alice-home_!") {
		t.Fatalf("joined as %q", joined)
	}
	b.send("PRIVMSG #boson :hello from another server")
	c.expect(":alice-home_!alicehome@")
	c.send("WHOIS alice-home_")
	for {
		l := c.expect("alice-home_")
		if strings.Contains(l, " 307 ") || strings.Contains(l, " 330 ") {
			t.Fatalf("identified to NickServ: %q", l)
		}
		if strings.Contains(l, " 318 ") {
			break
		}
	}
	// past NickServ's quick protection (20s): no rename, no kill
	time.Sleep(25 * time.Second)
	c.send("PING :still-there")
	c.expect("still-there")
	for _, l := range append(c.seen, b.seen...) {
		if strings.Contains(l, "Guest") || strings.Contains(l, " KILL ") || strings.Contains(l, ":alice-home!") && strings.Contains(l, " JOIN ") {
			t.Errorf("enforced or seen on the registered nick: %q", l)
		}
	}
	if code, out := m.call("GET", "/api/managed/users/alice-home", nil); code != 200 || out["currentNick"] != "alice-home_" || out["status"] != "connected" {
		t.Errorf("account: %d %v", code, out)
	}
	if strings.Count(m.stderr.String(), "connecting network") > 1 {
		t.Errorf("reconnected:\n%s", m.stderr)
	}
	b.noEvrd()
}

// Against ngircd with Anope (EVRD_E2E_NGIRCD_ANOPE; NICKLEN 16, like
// IRCForever): a Boson user whose handle is longer than NICKLEN gets it
// cut by parts; on a registered nick it's warned, can identify with
// NickServ within the grace and keeps the nick (remembered for next
// time); on one it doesn't identify for it's moved off once the grace
// ends.
func TestManagedNickGuardGrace(t *testing.T) {
	server := os.Getenv("EVRD_E2E_NGIRCD_ANOPE")
	if server == "" {
		t.Skip("set EVRD_E2E_NGIRCD_ANOPE=host:port (dev/e2e-managed.sh)")
	}
	registerNick(t, server, "lion")
	registerNick(t, server, "gale")

	m := startManaged(t, server, "managed_webirc_gateway {ident}", "managed_nick_guard_grace 8")
	if code, out := m.call("PUT", "/api/managed/users/lion-windows-bosonchat", map[string]any{
		"subject": "usr_lion", "nick": "lion-windows-bosonchat", "ident": "lion",
		"auth": map[string]any{"method": "none"}, "channels": []string{"#den"},
	}); code != 201 {
		t.Fatalf("create: %d %v", code, out)
	}
	b := dialIRC(t, m.irc)
	b.send("PASS %s", m.issuer.Token("usr_lion", "lion-windows-bosonchat"))
	b.send("NICK lion")
	b.send("USER lion 0 * :Lion")
	b.expect(" 001 ")
	b.expect("NICK :lion-windows") // cut by parts to NICKLEN 16, not lion-windows-bos
	b.expect(" JOIN :#den")
	for _, l := range b.seen {
		if strings.Contains(l, "NICK :lion-windows-") || strings.Contains(l, "NICK :lion-windows_") {
			t.Errorf("on a nick cut badly: %q", l)
		}
	}

	// the nick they own: warned, identifies, keeps it
	b.send("NICK lion")
	b.expect("NICK :lion")
	warn := b.expect("is registered with NickServ")
	if !strings.HasPrefix(warn, ":irc.example.org NOTICE lion :lion is registered with NickServ. Identify within 8 seconds") ||
		!strings.Contains(warn, "moved to lion-windows") {
		t.Errorf("warning: %q", warn)
	}
	b.send("PRIVMSG NickServ :IDENTIFY owner-secret-lion")
	b.expect("Password accepted")
	time.Sleep(12 * time.Second) // past the grace
	c := dialIRC(t, server)
	c.send("NICK watcher2")
	c.send("USER watcher 0 * :Watcher")
	c.expect(" 001 watcher2 ")
	c.send("WHOIS lion")
	c.expect(" 311 watcher2 lion ")
	c.expect(" 307 watcher2 lion ") // identified
	if code, out := m.call("GET", "/api/managed/users/lion-windows-bosonchat", nil); code != 200 || out["currentNick"] != "lion" || out["identifiedNick"] != "lion" {
		t.Errorf("account: %d %v", code, out)
	}

	// someone else's: warned, doesn't identify, moved off after the grace,
	// to the nick they identified for (services know them on it now)
	b.send("NICK gale")
	b.expect("NICK :gale")
	b.expect("gale is registered with NickServ. Identify within 8 seconds (/msg NickServ IDENTIFY password) or you'll be moved to lion")
	start := time.Now()
	b.expect("NICK :lion")
	if d := time.Since(start); d < 6*time.Second {
		t.Errorf("moved after %s, before the grace", d)
	}
	for _, l := range b.seen {
		if strings.Contains(l, " 311 ") || strings.Contains(l, " 318 ") {
			t.Errorf("the guard's WHOIS reached the client: %q", l)
		}
		if strings.Contains(l, "Guest") || strings.Contains(l, " KILL ") {
			t.Errorf("enforced: %q", l)
		}
	}
	b.noEvrd()
}

// Against ngircd with Anope (EVRD_E2E_NGIRCD_ANOPE): guests sign in with
// nothing, through the web gateway's WEBIRC, show on the server with
// their own address, never keep a registered nick, and quit once gone
// past the linger.
func TestManagedGuestsNgircd(t *testing.T) {
	server := os.Getenv("EVRD_E2E_NGIRCD_ANOPE")
	if server == "" {
		t.Skip("set EVRD_E2E_NGIRCD_ANOPE=host:port (dev/e2e-managed.sh)")
	}
	registerNick(t, server, "rex")
	c := dialIRC(t, server)
	c.send("NICK keeper")
	c.send("USER keeper 0 * :Keeper")
	c.expect(" 001 keeper ")
	c.send("JOIN #lobby")
	c.expect("366 keeper #lobby")

	m := startManaged(t, server, "managed_webirc_gateway {ident}", "managed_guests yes", "managed_guest_linger 2",
		"managed_nick_guard_grace 3", "managed_client_webirc_password gw-secret")
	g := dialIRC(t, m.irc)
	g.send("CAP LS 302")
	g.send("WEBIRC gw-secret webircgateway 192.0.2.77 192.0.2.77")
	g.send("NICK rex")
	g.send("USER visitor 0 * :A visitor")
	g.send("CAP REQ :message-tags server-time echo-message")
	g.send("CAP END")
	g.expect(":irc.example.org 001 rex ")
	g.expect("rex is registered with NickServ. Identify within 3 seconds")
	g.expect("NICK :rex_") // a guest never keeps a registered nick
	g.send("JOIN #lobby")
	g.expect(" JOIN :#lobby")
	joined := c.expect(" JOIN :#lobby")
	if !strings.HasPrefix(joined, ":rex_!visitor@192.0.2.77 ") {
		t.Errorf("the guest joined as %q (want its own address, through WEBIRC)", joined)
	}
	g.send("PRIVMSG #lobby :hello, I'm a guest")
	if echo := g.expect("hello, I'm a guest"); !strings.Contains(echo, "msgid=") {
		t.Errorf("echo-message: %q", echo)
	}
	c.expect("hello, I'm a guest")
	if code, out := m.call("GET", "/api/managed/health", nil); code != 200 || out["guests"].(map[string]any)["active"] != float64(1) {
		t.Errorf("health: %d %v", code, out)
	}
	g.noEvrd()
	g.c.Close()
	start := time.Now()
	c.expect(":rex_!visitor@192.0.2.77 QUIT")
	if d := time.Since(start); d < 1500*time.Millisecond {
		t.Errorf("quit after %s, before the linger", d)
	}
	if code, out := m.call("GET", "/api/managed/users", nil); code != 200 || len(out["users"].([]any)) != 0 {
		t.Errorf("users: %d %v", code, out)
	}
}
