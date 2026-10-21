package api

import (
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/4evrd/evrd/internal/auth"
	"github.com/4evrd/evrd/internal/store"
)

// stream opens a network's chat socket as c, with extra request headers.
func (c *client) stream(path string, hdr http.Header) (*websocket.Conn, *http.Response, error) {
	d := websocket.Dialer{Jar: c.c.Jar, HandshakeTimeout: 5 * time.Second}
	return d.Dial("ws"+strings.TrimPrefix(c.e.srv.URL, "http")+path+"/stream", hdr)
}

// next reads the next frame from the socket as an object.
func next(t *testing.T, ws *websocket.Conn) map[string]any {
	t.Helper()
	ws.SetReadDeadline(time.Now().Add(5 * time.Second))
	var m map[string]any
	if err := ws.ReadJSON(&m); err != nil {
		t.Fatalf("read from the stream: %v", err)
	}
	return m
}

// nextWith skips frames until one has field == want.
func nextWith(t *testing.T, ws *websocket.Conn, field, want string) map[string]any {
	t.Helper()
	for {
		if m := next(t, ws); m[field] == want {
			return m
		}
	}
}

type fakeStatus struct{ got chan string }

func (fakeStatus) StatusCommands() []string { return []string{"clearbuffer"} }

func (f fakeStatus) WebStatus(userID int64, n store.Network, text string, out func(string)) {
	f.got <- text
	out("you said " + text + " on " + n.Name)
}

func TestStream(t *testing.T) {
	e := newEnv(t)
	got := make(chan string, 1)
	e.api.status = fakeStatus{got} // before any request is served
	e.user("alice", store.RoleUser)
	c := e.login("alice")
	path, irc, id := e.liveNetwork(c, "fake")

	ws, _, err := c.stream(path, http.Header{"User-Agent": {"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) Chrome/120.0 Safari/537.36"}})
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	if m := next(t, ws); m["type"] != "ready" {
		t.Fatalf("first frame: %v", m)
	}
	// The socket counts as a web client on the dashboard.
	_, cl := c.do("GET", "/api/me/clients", nil)
	clients := cl["clients"].([]any)
	if len(clients) != 1 || clients[0].(map[string]any)["software"] != "Chrome on macOS" || clients[0].(map[string]any)["ip"] != "127.0.0.1" {
		t.Fatalf("clients: %v", cl)
	}

	// Live messages are pushed.
	irc.send(":carol!c@h PRIVMSG #evrd :hi there")
	if m := nextWith(t, ws, "text", "hi there"); m["sender"] != "carol" || m["target"] != "#evrd" || m["kind"] != "message" {
		t.Fatalf("pushed message: %v", m)
	}

	// Frames from the page go upstream (and echo back).
	ws.WriteJSON(map[string]string{"target": "#evrd", "text": "hello"})
	irc.expect("PRIVMSG #evrd :hello")
	if m := nextWith(t, ws, "text", "hello"); m["sender"] != "alice" {
		t.Fatalf("echo: %v", m)
	}
	ws.WriteJSON(map[string]string{"target": "#evrd", "text": "waves", "kind": "action"})
	irc.expect("PRIVMSG #evrd :\x01ACTION waves\x01")
	if m := nextWith(t, ws, "text", "waves"); m["kind"] != "action" {
		t.Fatalf("action echo: %v", m)
	}
	ws.WriteJSON(map[string]string{"target": "#evrd", "text": "psst", "kind": "notice"})
	irc.expect("NOTICE #evrd :psst")

	// Line breaks never reach the server; empty frames are ignored.
	ws.WriteJSON(map[string]string{"target": "#evrd", "text": "a\r\nQUIT :pwned"})
	ws.WriteJSON(map[string]string{"raw": "PRIVMSG #x :one\r\nQUIT"})
	ws.WriteJSON(map[string]string{"target": "", "text": "nowhere"})
	ws.WriteJSON(map[string]string{"raw": "  TOPIC #evrd :new topic  "})
	if l := irc.expect("TOPIC "); l != "TOPIC #evrd :new topic" {
		t.Fatalf("raw line: %q", l)
	}
	// Frames are handled in order, so the ones before it were dropped.
	for _, bad := range []string{"pwned", "QUIT", "PRIVMSG #x", "nowhere"} {
		if irc.log.Has(bad) {
			t.Errorf("the server got %q: %q", bad, irc.log.Lines())
		}
	}

	// A *status command sees the network as it is now.
	if err := e.st.UpdateNetwork(func() store.Network { n, _ := e.st.NetworkByID(id); n.Name = "renamed"; return n }()); err != nil {
		t.Fatal(err)
	}
	ws.WriteJSON(map[string]string{"status": "listnetworks"})
	if m := nextWith(t, ws, "sender", "*status"); m["text"] != "you said listnetworks on renamed" || m["target"] != "*status" {
		t.Fatalf("status reply: %v", m)
	}
	if cmd := <-got; cmd != "listnetworks" {
		t.Fatalf("command: %q", cmd)
	}

	// Closing the socket lets the client go.
	ws.Close()
	eventually(t, "the web client to detach", func() bool {
		_, cl := c.do("GET", "/api/me/clients", nil)
		return len(cl["clients"].([]any)) == 0
	})
}

func TestStreamRefusals(t *testing.T) {
	e := newEnv(t)
	e.user("alice", store.RoleUser)
	e.user("bob", store.RoleUser)
	alice, bob := e.login("alice"), e.login("bob")
	_, n := alice.do("POST", "/api/networks", map[string]any{"name": "off", "host": "irc.example.org"})
	off := "/api/networks/" + strconv.Itoa(int(n["id"].(float64)))

	if _, res, err := alice.stream(off, nil); err == nil || res == nil || res.StatusCode != http.StatusConflict {
		t.Fatalf("not connected: %v %v", res, err)
	}
	path, _, _ := e.liveNetwork(alice, "fake")
	if _, res, err := bob.stream(path, nil); err == nil || res.StatusCode != http.StatusNotFound {
		t.Fatalf("someone else's network: %v %v", res, err)
	}
	if _, res, err := e.anon().stream(path, nil); err == nil || res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no session: %v %v", res, err)
	}
	// Another site's page can't ride the cookie.
	if _, res, err := alice.stream(path, http.Header{"Origin": {"https://evil.example"}}); err == nil || res.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-site socket: %v %v", res, err)
	}
	ws, _, err := alice.stream(path, http.Header{"Origin": {e.srv.URL}})
	if err != nil {
		t.Fatalf("same-origin socket: %v", err)
	}
	defer ws.Close()
	next(t, ws) // ready
	// Without a *status runner, the web chat says so.
	ws.WriteJSON(map[string]string{"status": "help"})
	if m := nextWith(t, ws, "sender", "*status"); m["text"] != "*status isn't available here." {
		t.Fatalf("status reply: %v", m)
	}
}

// Suspending a user closes their open chat sockets.
func TestStreamClosesOnSuspend(t *testing.T) {
	e := newEnv(t)
	e.user("root", store.RoleAdmin)
	alice := e.user("alice", store.RoleUser)
	c := e.login("alice")
	path, _, _ := e.liveNetwork(c, "fake")
	ws, _, err := c.stream(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	next(t, ws) // ready
	if code, _ := e.login("root").do("PATCH", "/api/admin/users/"+strconv.FormatInt(alice.ID, 10), map[string]any{"suspended": true}); code != 200 {
		t.Fatalf("suspend: %d", code)
	}
	ws.SetReadDeadline(time.Now().Add(5 * time.Second))
	for {
		var m map[string]any
		if err := ws.ReadJSON(&m); err != nil {
			if strings.Contains(err.Error(), "timeout") {
				t.Fatal("the socket stayed open after suspension")
			}
			return // closed by the server
		}
	}
}

func TestBrowserNameAndClientAddr(t *testing.T) {
	for ua, want := range map[string]string{
		"Mozilla/5.0 (Windows NT 10.0) AppleWebKit Chrome/120 Safari/537 Edg/120": "Edge on Windows",
		"Mozilla/5.0 (X11; Linux x86_64; rv:120.0) Gecko Firefox/120.0":           "Firefox on Linux",
		"Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) Safari/604.1":     "Safari on iOS",
		"Mozilla/5.0 (Linux; Android 14) Chrome/120 Mobile Safari/537":            "Chrome on Android",
		"curl/8.0": "Web",
	} {
		if got := browserName(ua); got != want {
			t.Errorf("browserName(%q) = %q, want %q", ua, got, want)
		}
	}
	s := &Server{auth: &auth.Manager{}}
	r, _ := http.NewRequest("GET", "/", nil)
	r.RemoteAddr = "192.0.2.1:5555"
	if got := s.clientAddr(r); got != "192.0.2.1" {
		t.Errorf("clientAddr: %q", got)
	}
	r.RemoteAddr = "pipe"
	if got := s.clientAddr(r); got != "pipe" {
		t.Errorf("clientAddr without a port: %q", got)
	}
	// X-Forwarded-For counts only from a trusted proxy (loopback by default).
	r.Header.Set("X-Forwarded-For", " 198.51.100.7 , 10.0.0.1")
	r.RemoteAddr = "192.0.2.1:5555"
	if got := s.clientAddr(r); got != "192.0.2.1" {
		t.Errorf("clientAddr with a spoofed X-Forwarded-For: %q", got)
	}
	r.Header.Set("X-Forwarded-For", " 198.51.100.7 , 127.0.0.1")
	r.RemoteAddr = "127.0.0.1:5555"
	if got := s.clientAddr(r); got != "198.51.100.7" {
		t.Errorf("clientAddr behind a proxy: %q", got)
	}
}
