package handlers_test

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/4evrd/evrd/internal/auth"
	"github.com/4evrd/evrd/internal/config"
	"github.com/4evrd/evrd/internal/store"
)

const chatSite = "https://chat.example"

func webChatEnv(t *testing.T) *env {
	return newEnv(t, func(c *config.Config) { c.Auth.WebChatOrigins = []string{chatSite} })
}

// webChatToken runs the sign-in hand-off as c and returns the fragment it
// sent the browser back with.
func (c *client) webChatToken(network string) url.Values {
	c.e.t.Helper()
	q := url.Values{"return": {chatSite + "/chat/"}, "state": {"state-0123456789abcdef"}}
	if network != "" {
		q.Set("network", network)
	}
	res, err := c.c.Get(c.e.srv.URL + "/auth/web-chat?" + q.Encode())
	if err != nil {
		c.e.t.Fatal(err)
	}
	res.Body.Close()
	loc, _ := url.Parse(res.Header.Get("Location"))
	if res.StatusCode != http.StatusFound || loc == nil || !strings.HasPrefix(loc.String(), chatSite+"/chat/#") {
		c.e.t.Fatalf("hand-off: %d %q", res.StatusCode, res.Header.Get("Location"))
	}
	f, _ := url.ParseQuery(loc.Fragment)
	return f
}

// chatReq is a request from the web chat's page: its origin, its token
// (none for ""), no cookies.
func (e *env) chatReq(method, path, token, origin string) *http.Response {
	e.t.Helper()
	req, _ := http.NewRequest(method, e.srv.URL+path, nil)
	if origin != "" {
		req.Header.Set("Origin", origin)
		req.Header.Set("Sec-Fetch-Site", "cross-site")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := http.DefaultTransport.RoundTrip(req)
	if err != nil {
		e.t.Fatal(err)
	}
	res.Body.Close()
	return res
}

// chatStream opens the chat socket as the web chat does: its origin, the
// token as a subprotocol, no cookies.
func (e *env) chatStream(path, token, origin string, hdr http.Header) (*websocket.Conn, *http.Response, error) {
	d := websocket.Dialer{HandshakeTimeout: 5 * time.Second, Subprotocols: []string{auth.WebSocketProtocol, auth.TokenProtocol + token}}
	if hdr == nil {
		hdr = http.Header{}
	}
	hdr.Set("Origin", origin)
	hdr.Set("Sec-Fetch-Site", "cross-site")
	return d.Dial("ws"+strings.TrimPrefix(e.srv.URL, "http")+path, hdr)
}

func TestWebChatHandOff(t *testing.T) {
	e := webChatEnv(t)
	e.user("alice", store.RoleUser)
	c := e.login("alice")
	_, _, id := e.liveNetwork(c, "IRCForever")
	f := c.webChatToken("")
	if f.Get("token") == "" || f.Get("user") != "alice" || f.Get("network") != strconv.FormatInt(id, 10) || f.Get("api") != e.srv.URL || f.Get("state") != "state-0123456789abcdef" {
		t.Fatalf("fragment: %v", f)
	}
	if exp, _ := strconv.ParseInt(f.Get("expires"), 10, 64); time.Until(time.Unix(exp, 0)) < 29*24*time.Hour {
		t.Fatalf("expires %v", f.Get("expires"))
	}
	// It's one of the account's sessions, signed out like the others.
	code, list := c.doAny("GET", "/api/me/sessions", nil)
	var chat map[string]any
	for _, s := range list.([]any) {
		if s := s.(map[string]any); s["method"] == "webchat" {
			chat = s
		}
	}
	if code != 200 || chat == nil || chat["site"] != "chat.example" {
		t.Fatalf("sessions: %v", list)
	}
	if code, _ := c.do("DELETE", "/api/me/sessions/"+chat["id"].(string), nil); code != http.StatusNoContent {
		t.Fatalf("sign it out: %d", code)
	}
	if res := e.chatReq("GET", "/api/networks", f.Get("token"), chatSite); res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("signed-out token: %d", res.StatusCode)
	}
	// Not signed in: the web app's page signs in first. Elsewhere: refused.
	res, _ := e.anon().c.Get(e.srv.URL + "/auth/web-chat?return=" + url.QueryEscape(chatSite+"/") + "&state=state-0123456789abcdef")
	if res.StatusCode != http.StatusFound || !strings.HasPrefix(res.Header.Get("Location"), "/web-chat?") {
		t.Fatalf("not signed in: %d %q", res.StatusCode, res.Header.Get("Location"))
	}
	res, _ = c.c.Get(e.srv.URL + "/auth/web-chat?return=" + url.QueryEscape("https://evil.example/") + "&state=state-0123456789abcdef")
	if res.StatusCode != http.StatusBadRequest || res.Header.Get("Location") != "" {
		t.Fatalf("another site: %d %q", res.StatusCode, res.Header.Get("Location"))
	}
}

func TestWebChatREST(t *testing.T) {
	e := webChatEnv(t)
	e.user("alice", store.RoleUser)
	c := e.login("alice")
	path, irc, _ := e.liveNetwork(c, "fake")
	irc.send(":carol!c@h PRIVMSG #evrd :earlier")
	eventually(t, "the line saved", func() bool {
		code, msgs := c.doAny("GET", path+"/messages?target=%23evrd", nil)
		return code == 200 && len(msgs.([]any)) == 1
	})
	tok := c.webChatToken("").Get("token")

	// The web chat's routes take the token, from its site, readable there.
	for _, p := range []string{"/api/me", "/api/networks", path, path + "/messages?target=%23evrd", path + "/buffers", path + "/channel?target=%23evrd"} {
		res := e.chatReq("GET", p, tok, chatSite)
		if res.StatusCode != 200 || res.Header.Get("Access-Control-Allow-Origin") != chatSite || res.Header.Get("Access-Control-Allow-Credentials") != "" {
			t.Errorf("GET %s: %d %v", p, res.StatusCode, res.Header)
		}
	}
	// Marking read is a cross-site POST: fine with the token, not the cookie.
	if res := e.chatReq("POST", path+"/read", tok, chatSite); res.StatusCode != http.StatusNoContent {
		t.Errorf("mark read with the token: %d", res.StatusCode)
	}
	req, _ := http.NewRequest("POST", e.srv.URL+path+"/read", nil)
	req.Header.Set("Origin", chatSite)
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	if res, _ := c.c.Do(req); res.StatusCode != http.StatusForbidden {
		t.Errorf("cross-site POST with the cookie: %d", res.StatusCode)
	}
	// Another site gets no CORS; a cross-site POST from it is refused.
	if res := e.chatReq("GET", "/api/networks", tok, "https://evil.example"); res.Header.Get("Access-Control-Allow-Origin") != "" {
		t.Errorf("CORS for another site: %v", res.Header)
	}
	if res := e.chatReq("POST", path+"/read", tok, "https://evil.example"); res.StatusCode != http.StatusForbidden {
		t.Errorf("POST from another site: %d", res.StatusCode)
	}
	// The token is for the web chat's routes only, even from its own page.
	for _, rt := range []string{"GET /api/me/sessions", "GET /api/me/client-passwords", "POST /api/me/client-passwords", "PATCH /api/me", "DELETE /api/me", "POST /api/networks", "PATCH " + path, "DELETE " + path, "GET /api/networks/export"} {
		method, p, _ := strings.Cut(rt, " ")
		if res := e.chatReq(method, p, tok, ""); res.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s with the token: %d", rt, res.StatusCode)
		}
	}
	if res := e.chatReq("GET", "/api/networks", "not-a-token", chatSite); res.StatusCode != http.StatusUnauthorized {
		t.Errorf("a wrong token: %d", res.StatusCode)
	}

	// Preflights: the web chat's routes from its site only.
	pre := func(p, method, origin string) *http.Response {
		req, _ := http.NewRequest("OPTIONS", e.srv.URL+p, nil)
		req.Header.Set("Origin", origin)
		req.Header.Set("Access-Control-Request-Method", method)
		req.Header.Set("Access-Control-Request-Headers", "authorization")
		res, err := http.DefaultTransport.RoundTrip(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res
	}
	if res := pre(path+"/read", "POST", chatSite); res.StatusCode != http.StatusNoContent || res.Header.Get("Access-Control-Allow-Origin") != chatSite || !strings.Contains(res.Header.Get("Access-Control-Allow-Headers"), "Authorization") {
		t.Errorf("preflight: %d %v", res.StatusCode, res.Header)
	}
	if res := pre("/auth/web-chat/revoke", "POST", chatSite); res.StatusCode != http.StatusNoContent {
		t.Errorf("revoke's preflight: %d", res.StatusCode)
	}
	if res := pre("/api/me/password", "POST", chatSite); res.Header.Get("Access-Control-Allow-Origin") != "" {
		t.Errorf("preflight for another route: %d %v", res.StatusCode, res.Header)
	}
	if res := pre(path+"/read", "POST", "https://evil.example"); res.Header.Get("Access-Control-Allow-Origin") != "" {
		t.Errorf("preflight from another site: %v", res.Header)
	}

	// Signing out from the web chat ends the token.
	if res := e.chatReq("POST", "/auth/web-chat/revoke", tok, chatSite); res.StatusCode != http.StatusNoContent || res.Header.Get("Access-Control-Allow-Origin") != chatSite {
		t.Fatalf("revoke: %d %v", res.StatusCode, res.Header)
	}
	if res := e.chatReq("GET", "/api/networks", tok, chatSite); res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("after revoking: %d", res.StatusCode)
	}
	if code, _ := c.do("GET", "/api/me", nil); code != 200 {
		t.Fatal("revoking the web chat signed the browser out")
	}
}

func TestWebChatStream(t *testing.T) {
	e := webChatEnv(t)
	e.user("alice", store.RoleUser)
	c := e.login("alice")
	path, irc, _ := e.liveNetwork(c, "fake")
	irc.send(":evrd.test 005 alice CHANTYPES=# PREFIX=(ov)@+ :are supported")
	irc.send(":alice!a@h JOIN #evrd")
	tok := c.webChatToken("").Get("token")

	// The real address comes from the trusted proxy in front.
	ws, res, err := e.chatStream(path+"/stream?raw=1", tok, chatSite, http.Header{
		"X-Forwarded-For": {"198.51.100.20"}, "User-Agent": {"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) Version/17.0 Safari/605.1.15"},
	})
	if err != nil {
		t.Fatalf("token socket: %v %v", err, res)
	}
	defer ws.Close()
	if ws.Subprotocol() != auth.WebSocketProtocol {
		t.Fatalf("subprotocol %q", ws.Subprotocol())
	}
	ready := next(t, ws)
	if ready["type"] != "ready" || ready["nick"] != "alice" || ready["connected"] != true || !strings.Contains(strings.Join(anyStrings(ready["isupport"]), " "), "PREFIX=(ov)@+") || !strings.Contains(strings.Join(anyStrings(ready["channels"]), " "), "#evrd") {
		t.Fatalf("ready: %v", ready)
	}
	_, cl := c.do("GET", "/api/me/clients", nil)
	clients := cl["clients"].([]any)
	if len(clients) != 1 || clients[0].(map[string]any)["ip"] != "198.51.100.20" || clients[0].(map[string]any)["software"] != "Safari on macOS via chat.example" {
		t.Fatalf("clients: %v", cl)
	}

	// Every line the network sends, with the id of what evrd saved from it.
	irc.send(":evrd.test 311 alice carol c h * :Carol C")
	if m := nextWith(t, ws, "type", "raw"); m["line"] != ":evrd.test 311 alice carol c h * :Carol C" || m["id"] != nil {
		t.Fatalf("whois reply: %v", m)
	}
	irc.send(":carol!c@h PRIVMSG #evrd :hi there")
	// the two come in either order
	var rawMsg, msg map[string]any
	for rawMsg == nil || msg == nil {
		switch m := next(t, ws); {
		case m["type"] == "raw":
			rawMsg = m
		case m["text"] == "hi there":
			msg = m
		}
	}
	if rawMsg["line"] != ":carol!c@h PRIVMSG #evrd :hi there" || rawMsg["id"] == nil || msg["id"] != rawMsg["id"] || msg["sender"] != "carol" {
		t.Fatalf("message frame %v for raw %v", msg, rawMsg)
	}
	// What the web chat sends goes up as before.
	ws.WriteJSON(map[string]string{"raw": "WHOIS carol"})
	irc.expect("WHOIS carol")
	ws.WriteJSON(map[string]string{"target": "#evrd", "text": "hello"})
	irc.expect("PRIVMSG #evrd :hello")
	if m := nextWith(t, ws, "text", "hello"); m["sender"] != "alice" {
		t.Fatalf("own message: %v", m)
	}
	ws.Close()

	// Without raw=1 there are no raw frames.
	plain, _, err := e.chatStream(path+"/stream", tok, chatSite, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer plain.Close()
	if m := next(t, plain); m["type"] != "ready" || m["nick"] != nil {
		t.Fatalf("ready without raw: %v", m)
	}
	irc.send(":evrd.test 311 alice dave d h * :Dave")
	irc.send(":dave!d@h PRIVMSG #evrd :after")
	if m := next(t, plain); m["text"] != "after" || m["type"] != nil {
		t.Fatalf("without raw: %v", m)
	}

	// Refused: another site with the token, the web chat's site with the
	// cookie (no token), a wrong token, and a signed-out one.
	if _, res, err := e.chatStream(path+"/stream", tok, "https://evil.example", nil); err == nil || res.StatusCode != http.StatusForbidden {
		t.Errorf("another site: %v %v", err, res)
	}
	d := websocket.Dialer{Jar: c.c.Jar, HandshakeTimeout: 5 * time.Second}
	if _, res, err := d.Dial("ws"+strings.TrimPrefix(e.srv.URL, "http")+path+"/stream", http.Header{"Origin": {chatSite}, "Sec-Fetch-Site": {"cross-site"}}); err == nil || res.StatusCode != http.StatusForbidden {
		t.Errorf("the cookie from the web chat's site: %v %v", err, res)
	}
	if _, res, err := e.chatStream(path+"/stream", "nope", chatSite, nil); err == nil || res.StatusCode != http.StatusUnauthorized {
		t.Errorf("a wrong token: %v %v", err, res)
	}
	e.chatReq("POST", "/auth/web-chat/revoke", tok, chatSite)
	if _, res, err := e.chatStream(path+"/stream", tok, chatSite, nil); err == nil || res.StatusCode != http.StatusUnauthorized {
		t.Errorf("a signed-out token: %v %v", err, res)
	}
}

func TestWebChatOriginsSetting(t *testing.T) {
	e := newEnv(t)
	e.user("root", store.RoleAdmin)
	root := e.login("root")
	for _, bad := range []string{"https://chat.example/chat", "chat.example", "ftp://chat.example", "https://chat.example?x=1"} {
		if code, out := root.do("PATCH", "/api/admin/settings", map[string]any{"set": map[string]string{"web_chat_origins": bad}}); code < 400 {
			t.Errorf("web_chat_origins %q: %d %v", bad, code, out)
		}
	}
	// Off by default; on as soon as it's saved.
	res, _ := root.c.Get(e.srv.URL + "/auth/web-chat?return=" + url.QueryEscape(chatSite+"/") + "&state=state-0123456789abcdef")
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("with no sites: %d", res.StatusCode)
	}
	if code, out := root.do("PATCH", "/api/admin/settings", map[string]any{"set": map[string]string{"web_chat_origins": "HTTPS://Chat.Example http://localhost:5180", "web_chat_days": "7"}}); code != 200 {
		t.Fatalf("save: %d %v", code, out)
	}
	f := root.webChatToken("")
	if exp, _ := strconv.ParseInt(f.Get("expires"), 10, 64); time.Until(time.Unix(exp, 0)) > 7*24*time.Hour {
		t.Fatalf("expires %v", f.Get("expires"))
	}
}

func anyStrings(v any) []string {
	var out []string
	l, _ := v.([]any)
	for _, s := range l {
		if s, ok := s.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// In a cluster, the web chat's requests and socket reach the user's node
// still signed in by the token, and still from the web chat's site.
func TestWebChatThroughTheFront(t *testing.T) {
	cl := newTwoNodes(t, func(c *config.Config) { c.Auth.WebChatOrigins = []string{chatSite} })
	e := cl.front
	alice := e.user("alice", store.RoleUser)
	cl.p.place(alice.ID, cl.backN)
	ac := e.login("alice")
	irc := newFakeIRC(t)
	_, n := ac.do("POST", "/api/networks", map[string]any{"name": "fake", "host": "127.0.0.1", "port": irc.port(), "tls": false})
	id := int64(n["id"].(float64))
	t.Cleanup(func() { cl.back.b.Disconnect(id) })
	path := "/api/networks/" + strconv.FormatInt(id, 10)
	ac.do("POST", path+"/connect", nil)
	irc.expect("USER ")
	eventually(t, "registration", func() bool { _, n := ac.do("GET", path, nil); return n["status"] == "connected" })

	tok := ac.webChatToken("").Get("token")
	if res := e.chatReq("GET", path+"/buffers", tok, chatSite); res.StatusCode != 200 || res.Header.Get("Access-Control-Allow-Origin") != chatSite {
		t.Fatalf("buffers through the front: %d", res.StatusCode)
	}
	ws, res, err := e.chatStream(path+"/stream?raw=1", tok, chatSite, nil)
	if err != nil {
		t.Fatalf("socket through the front: %v %v", err, res)
	}
	defer ws.Close()
	next(t, ws) // ready
	irc.send(":carol!c@h PRIVMSG #x :via the back")
	if m := nextWith(t, ws, "type", "raw"); m["line"] != ":carol!c@h PRIVMSG #x :via the back" {
		t.Fatalf("raw: %v", m)
	}
	// A browser can't claim a token it doesn't have by the forwarding header.
	d := websocket.Dialer{Jar: ac.c.Jar, HandshakeTimeout: 5 * time.Second}
	if _, res, err := d.Dial("ws"+strings.TrimPrefix(e.srv.URL, "http")+path+"/stream", http.Header{"Origin": {chatSite}, "Sec-Fetch-Site": {"cross-site"}, auth.HeaderViaToken: {"1"}}); err == nil || res.StatusCode != http.StatusForbidden {
		t.Fatalf("cookie with the forwarding header: %v %v", err, res)
	}
}
