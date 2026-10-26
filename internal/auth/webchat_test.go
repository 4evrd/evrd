package auth

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/4evrd/evrd/internal/accounts"
	"github.com/4evrd/evrd/internal/config"
	"github.com/4evrd/evrd/internal/store"
)

const (
	chatSite  = "https://chat.example"
	chatState = "0123456789abcdef-state"
)

// webChatSignIn runs GET /auth/web-chat with query, signed in with c (nil:
// not signed in).
func webChatSignIn(m *Manager, query url.Values, c *http.Cookie) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodGet, "/auth/web-chat?"+query.Encode(), nil)
	r.RemoteAddr = "203.0.113.9:5555"
	r.Header.Set("User-Agent", "Mozilla/5.0 Safari/605.1.15")
	if c != nil {
		r.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	m.WebChatSignIn(rec, r)
	return rec
}

// fragment is the hand-off's answer: where it went, and its fragment.
func fragment(t *testing.T, rec *httptest.ResponseRecorder) (*url.URL, url.Values) {
	t.Helper()
	if rec.Code != http.StatusFound {
		t.Fatalf("hand-off: %d %s", rec.Code, rec.Body)
	}
	loc, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	f, err := url.ParseQuery(loc.Fragment)
	if err != nil {
		t.Fatal(err)
	}
	return loc, f
}

// With single sign-on the only way in, the hand-off goes straight to the
// provider and, signed in, comes back to make the token.
func TestWebChatSignInWithSSOOnly(t *testing.T) {
	p := newFakeIdP(t)
	m, st, _ := newOIDCManager(t, p, config.Auth{OIDCAutoCreate: true, WebChatOrigins: []string{chatSite}})
	var logs bytes.Buffer
	m.log = slog.New(slog.NewTextHandler(&logs, nil))

	q := url.Values{"return": {chatSite + "/chat/"}, "state": {chatState}}
	rec := webChatSignIn(m, q, nil)
	loc, _ := url.Parse(rec.Header().Get("Location"))
	if rec.Code != http.StatusFound || loc.Path != "/auth/login" || loc.Query().Get("next") != "/auth/web-chat?"+q.Encode() {
		t.Fatalf("not signed in: %d %s", rec.Code, rec.Header().Get("Location"))
	}

	// the provider's round trip, with that next
	login := httptest.NewRecorder()
	m.OIDCLogin(login, httptest.NewRequest(http.MethodGet, loc.String(), nil))
	pq, _ := url.Parse(login.Header().Get("Location"))
	p.issue("code-1", grant{challenge: pq.Query().Get("code_challenge"), claims: p.idClaims(pq.Query().Get("nonce"), map[string]any{"sub": "s1", "username": "mira"})})
	cb := callback(m, pq.Query().Get("state"), "code-1")
	if got := cb.Header().Get("Location"); got != "/auth/web-chat?"+q.Encode() {
		t.Fatalf("back from the provider to %q", got)
	}

	back, f := fragment(t, webChatSignIn(m, q, sessionCookie(t, cb)))
	if back.Scheme+"://"+back.Host+back.Path != chatSite+"/chat/" || back.RawQuery != "" {
		t.Fatalf("returned to %s", back)
	}
	tok := f.Get("token")
	if tok == "" || f.Get("state") != chatState || f.Get("user") != "mira" || f.Get("network") != "" || f.Get("api") != testPublic {
		t.Fatalf("fragment: %v", f)
	}
	sess, err := st.SessionByID(context.Background(), Hash(tok))
	if err != nil || sess.Method != MethodWebChat || sess.Label != "chat.example" || sess.IP != "203.0.113.9" {
		t.Fatalf("token's session: %+v %v", sess, err)
	}
	if strings.Contains(logs.String(), tok) || !strings.Contains(logs.String(), "web chat sign-in") {
		t.Fatalf("log: %s", logs.String())
	}
}

func TestWebChatToken(t *testing.T) {
	m, st, acc := newManager(t, config.Auth{PasswordLogin: true, SessionTTL: time.Hour, WebChatOrigins: []string{chatSite}, WebChatDays: 2})
	if _, err := acc.Create(accounts.CreateInput{Username: "alice", Password: testPassword}); err != nil {
		t.Fatal(err)
	}
	q := url.Values{"return": {chatSite + "/"}, "state": {chatState}}
	// Not signed in, with passwords on: the web app's page signs in.
	if rec := webChatSignIn(m, q, nil); rec.Code != http.StatusFound || rec.Header().Get("Location") != "/web-chat?"+q.Encode() {
		t.Fatalf("not signed in: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	cookie := sessionCookie(t, passwordLogin(m, "alice", testPassword))
	_, f := fragment(t, webChatSignIn(m, q, cookie))
	tok := f.Get("token")
	start := m.now()

	seen := func(r *http.Request) (store.User, bool, bool) {
		var u store.User
		var via bool
		ok := false
		m.RequireWebChat(func(w http.ResponseWriter, r *http.Request) { u, via, ok = User(r), ViaToken(r), true })(httptest.NewRecorder(), r)
		return u, via, ok
	}
	bearer := httptest.NewRequest(http.MethodGet, "/api/networks", nil)
	bearer.Header.Set("Authorization", "Bearer "+tok)
	if u, via, ok := seen(bearer); !ok || !via || u.Username != "alice" {
		t.Fatalf("bearer: %v %v %v", u, via, ok)
	}
	// On a socket it comes as a subprotocol.
	ws := httptest.NewRequest(http.MethodGet, "/api/networks/1/stream", nil)
	ws.Header.Set("Upgrade", "websocket")
	ws.Header.Set("Sec-WebSocket-Protocol", "evrd, "+TokenProtocol+tok)
	if _, via, ok := seen(ws); !ok || !via {
		t.Fatal("subprotocol token refused")
	}
	// A bad token isn't helped by a cookie beside it.
	bad := httptest.NewRequest(http.MethodGet, "/api/networks", nil)
	bad.Header.Set("Authorization", "Bearer nope")
	bad.AddCookie(cookie)
	if _, _, ok := seen(bad); ok {
		t.Fatal("a bad token with a cookie got in")
	}
	// The cookie alone still works there, and the token is never a cookie.
	if _, via, ok := seen(withCookie(httptest.NewRequest(http.MethodGet, "/", nil), cookie)); !ok || via {
		t.Fatal("cookie on a web chat route")
	}
	if _, _, ok := m.Current(withCookie(httptest.NewRequest(http.MethodGet, "/", nil), &http.Cookie{Name: cookieName, Value: tok})); ok {
		t.Fatal("the token worked as a cookie")
	}

	// Using it moves its end.
	m.now = func() time.Time { return start.Add(36 * time.Hour) }
	if _, _, ok := seen(bearer); !ok {
		t.Fatal("refused within its days")
	}
	if s, _ := st.SessionByID(context.Background(), Hash(tok)); !s.ExpiresAt.After(start.Add(83 * time.Hour)) {
		t.Fatalf("not renewed: ends %v", s.ExpiresAt)
	}
	m.now = func() time.Time { return start.Add(36*time.Hour + 49*time.Hour) }
	if _, _, ok := seen(bearer); ok {
		t.Fatal("accepted after its days unused")
	}
	m.now = time.Now

	// Revoking signs it out; a cookie can't revoke a session.
	_, f = fragment(t, webChatSignIn(m, q, cookie))
	tok2 := f.Get("token")
	rv := httptest.NewRequest(http.MethodPost, "/auth/web-chat/revoke", nil)
	rv.Header.Set("Authorization", "Bearer "+tok2)
	rec := httptest.NewRecorder()
	m.WebChatRevoke(rec, rv)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("revoke: %d", rec.Code)
	}
	if _, err := st.SessionByID(context.Background(), Hash(tok2)); err == nil {
		t.Fatal("still there after revoking")
	}
	rv = httptest.NewRequest(http.MethodPost, "/auth/web-chat/revoke", nil)
	rv.Header.Set("Authorization", "Bearer "+cookie.Value)
	m.WebChatRevoke(httptest.NewRecorder(), rv)
	if _, _, ok := m.Current(withCookie(httptest.NewRequest(http.MethodGet, "/", nil), cookie)); !ok {
		t.Fatal("a web session was revoked as a web chat's")
	}

	// An account keeps its newest maxWebChats.
	for range maxWebChats + 3 {
		fragment(t, webChatSignIn(m, q, cookie))
	}
	list, _ := st.ListSessions(context.Background(), 1)
	chats := 0
	for _, s := range list {
		if s.Method == MethodWebChat {
			chats++
		}
	}
	if chats != maxWebChats {
		t.Fatalf("%d web chat tokens", chats)
	}
}

// Only a return on web_chat_origins, with a state, gets anything back.
func TestWebChatSignInRefusals(t *testing.T) {
	m, _, acc := newManager(t, config.Auth{PasswordLogin: true, SessionTTL: time.Hour, WebChatOrigins: []string{chatSite, "http://localhost:5180"}})
	acc.Create(accounts.CreateInput{Username: "alice", Password: testPassword})
	cookie := sessionCookie(t, passwordLogin(m, "alice", testPassword))
	for _, ret := range []string{
		"", "/chat/", "//chat.example/", "https://evil.example/", "http://chat.example/", "https://chat.example.evil.example/",
		"https://chat.example@evil.example/", "https://user@chat.example/", "javascript:alert(1)", "https:chat.example",
		"https://chat.example:8443/", "https://chat.example\\@evil.example/", "http://localhost:5181/",
	} {
		rec := webChatSignIn(m, url.Values{"return": {ret}, "state": {chatState}}, cookie)
		if rec.Code != http.StatusBadRequest || rec.Header().Get("Location") != "" {
			t.Errorf("return %q: %d %q", ret, rec.Code, rec.Header().Get("Location"))
		}
	}
	for _, state := range []string{"", "short", strings.Repeat("x", 129), "has space in it here", "0123456789abcdef#"} {
		if rec := webChatSignIn(m, url.Values{"return": {chatSite + "/"}, "state": {state}}, cookie); rec.Code != http.StatusBadRequest {
			t.Errorf("state %q: %d", state, rec.Code)
		}
	}
	// Allowed: another path, a port given in the setting, a fragment dropped.
	for ret, want := range map[string]string{
		"https://CHAT.example/x/y?z=1#old": "https://CHAT.example/x/y?z=1#",
		"http://localhost:5180/chat/":      "http://localhost:5180/chat/#",
	} {
		rec := webChatSignIn(m, url.Values{"return": {ret}, "state": {chatState}}, cookie)
		if got := rec.Header().Get("Location"); rec.Code != http.StatusFound || !strings.HasPrefix(got, want) || strings.Contains(got, "old") {
			t.Errorf("return %q: %d %q", ret, rec.Code, got)
		}
	}
	// No sites allowed: nothing is.
	m.SetConfig(config.Auth{PasswordLogin: true, SessionTTL: time.Hour})
	if rec := webChatSignIn(m, url.Values{"return": {chatSite + "/"}, "state": {chatState}}, cookie); rec.Code != http.StatusBadRequest {
		t.Errorf("with no web chat sites: %d", rec.Code)
	}
}

// The network: the one asked for, else web_chat_network, else the only one.
func TestWebChatNetwork(t *testing.T) {
	m, _, acc := newManager(t, config.Auth{PasswordLogin: true, SessionTTL: time.Hour, WebChatOrigins: []string{chatSite}})
	u, _ := acc.Create(accounts.CreateInput{Username: "alice", Password: testPassword})
	cookie := sessionCookie(t, passwordLogin(m, "alice", testPassword))
	net := func(asked string) string {
		q := url.Values{"return": {chatSite + "/"}, "state": {chatState}}
		if asked != "" {
			q.Set("network", asked)
		}
		_, f := fragment(t, webChatSignIn(m, q, cookie))
		return f.Get("network")
	}
	if got := net(""); got != "" {
		t.Fatalf("no networks: %q", got)
	}
	a, err := acc.CreateNetwork(context.Background(), store.Network{UserID: u.ID, Name: "IRCForever", Host: "irc.example", Port: 6697, Nick: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	id := func(n store.Network) string { return strconv.FormatInt(n.ID, 10) }
	if got := net(""); got != id(a) {
		t.Fatalf("the only network: %q", got)
	}
	b, _ := acc.CreateNetwork(context.Background(), store.Network{UserID: u.ID, Name: "libera", Host: "irc.libera.chat", Port: 6697, Nick: "alice"})
	if got := net(""); got != "" {
		t.Fatalf("two networks, no default: %q", got)
	}
	if got := net("LIBERA"); got != id(b) {
		t.Fatalf("asked for libera: %q", got)
	}
	m.SetConfig(config.Auth{PasswordLogin: true, SessionTTL: time.Hour, WebChatOrigins: []string{chatSite}, WebChatNetwork: "ircforever"})
	if got := net(""); got != id(a) {
		t.Fatalf("the default: %q", got)
	}
	if got := net("nope"); got != id(a) {
		t.Fatalf("asked for one they don't have: %q", got)
	}
}
