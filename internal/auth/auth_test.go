package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/4evrd/evrd/internal/accounts"
	"github.com/4evrd/evrd/internal/config"
	"github.com/4evrd/evrd/internal/store"
)

const testPassword = "correct horse"

func sessionCookie(t *testing.T, rec *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	for _, c := range rec.Result().Cookies() {
		if c.Name == cookieName {
			return c
		}
	}
	t.Fatalf("no session cookie (status %d: %s)", rec.Code, rec.Body)
	return nil
}

// passwordLogin posts a sign-in and returns the response.
func passwordLogin(m *Manager, user, pw string) *httptest.ResponseRecorder {
	body, _ := json.Marshal(map[string]string{"username": user, "password": pw})
	r := httptest.NewRequest(http.MethodPost, "/auth/password", strings.NewReader(string(body)))
	r.RemoteAddr = "203.0.113.9:5555"
	r.Header.Set("User-Agent", strings.Repeat("u", 300))
	rec := httptest.NewRecorder()
	m.PasswordLogin(rec, r)
	return rec
}

func errorOf(rec *httptest.ResponseRecorder) string {
	var body struct{ Error string }
	json.NewDecoder(rec.Body).Decode(&body)
	return body.Error
}

func withCookie(r *http.Request, c *http.Cookie) *http.Request {
	r.AddCookie(c)
	return r
}

func TestHash(t *testing.T) {
	h := Hash("token")
	if len(h) != 64 || h != Hash("token") || h == Hash("token2") || strings.Contains(h, "token") {
		t.Fatalf("hash: %q", h)
	}
	if randToken() == randToken() {
		t.Fatal("tokens repeat")
	}
}

func TestPasswordLogin(t *testing.T) {
	m, st, acc := newManager(t, config.Auth{PasswordLogin: true, SessionTTL: time.Hour})
	start := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	m.now = func() time.Time { return start }
	u, _ := acc.Create(accounts.CreateInput{Username: "alice", Password: testPassword})

	rec := passwordLogin(m, " Alice ", testPassword)
	if rec.Code != http.StatusOK {
		t.Fatalf("sign-in: %d %s", rec.Code, rec.Body)
	}
	c := sessionCookie(t, rec)
	if !c.HttpOnly || c.Secure || c.SameSite != http.SameSiteLaxMode || c.Path != "/" || !c.Expires.Equal(start.Add(time.Hour)) {
		t.Fatalf("cookie: %+v", c)
	}
	sess, err := st.SessionByID(t.Context(), Hash(c.Value))
	if err != nil {
		t.Fatalf("the session is stored by the token's hash: %v", err)
	}
	if _, err := st.SessionByID(t.Context(), c.Value); err == nil {
		t.Fatal("the raw token is stored")
	}
	if sess.UserID != u.ID || sess.Method != "password" || sess.IP != "203.0.113.9" || len(sess.UserAgent) != 255 || !sess.ExpiresAt.Equal(start.Add(time.Hour)) {
		t.Fatalf("session: %+v", sess)
	}
	if got, _ := st.UserByID(t.Context(), u.ID); got.LastLoginAt == nil || !got.LastLoginAt.Equal(start) {
		t.Fatalf("last login: %v", got.LastLoginAt)
	}

	for _, tc := range []struct {
		name, user, pw string
		code           int
		msg            string
	}{
		{"wrong password", "alice", "wrong horse!", http.StatusUnauthorized, "wrong username or password"},
		{"no such user", "nobody", testPassword, http.StatusUnauthorized, "wrong username or password"},
	} {
		if rec := passwordLogin(m, tc.user, tc.pw); rec.Code != tc.code || errorOf(rec) != tc.msg {
			t.Errorf("%s: %d %q", tc.name, rec.Code, rec.Body)
		}
	}

	r := httptest.NewRequest(http.MethodPost, "/auth/password", strings.NewReader("{not json"))
	rec = httptest.NewRecorder()
	m.PasswordLogin(rec, r)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad body: %d", rec.Code)
	}

	u, _ = st.UserByID(t.Context(), u.ID)
	u.AllowedHosts = []string{"10.0.0.0/8"}
	st.UpdateUser(t.Context(), u)
	if rec := passwordLogin(m, "alice", testPassword); rec.Code != http.StatusForbidden || len(rec.Result().Cookies()) != 0 {
		t.Fatalf("from a denied address: %d %s", rec.Code, rec.Body)
	}

	u.AllowedHosts, u.Suspended = nil, true
	st.UpdateUser(t.Context(), u)
	if rec := passwordLogin(m, "alice", testPassword); rec.Code != http.StatusForbidden || errorOf(rec) != "this account is suspended" {
		t.Fatalf("suspended: %d %s", rec.Code, rec.Body)
	}
}

func TestPasswordLoginTurnedOff(t *testing.T) {
	m, _, acc := newManager(t, config.Auth{PasswordLogin: true, SessionTTL: time.Hour, OIDCLabel: "Logto"})
	acc.Create(accounts.CreateInput{Username: "alice", Password: testPassword})
	m.SetConfig(config.Auth{PasswordLogin: false, OIDCLabel: "Logto"})
	rec := passwordLogin(m, "alice", testPassword)
	if rec.Code != http.StatusForbidden || !strings.Contains(errorOf(rec), "use Logto") || len(rec.Result().Cookies()) != 0 {
		t.Fatalf("turned off at runtime: %d %s", rec.Code, rec.Body)
	}
}

func TestPasswordLoginLockout(t *testing.T) {
	m, _, acc := newManager(t, config.Auth{PasswordLogin: true, SessionTTL: time.Hour})
	start := time.Now()
	m.fails.now = func() time.Time { return start }
	acc.Create(accounts.CreateInput{Username: "alice", Password: testPassword})
	acc.Create(accounts.CreateInput{Username: "bob", Password: testPassword})

	for i := range 10 {
		user := "alice"
		if i%2 == 1 {
			user = "ALICE" // the same account, however it's typed
		}
		if rec := passwordLogin(m, user, "guess-"+strconv.Itoa(i)); rec.Code != http.StatusUnauthorized {
			t.Fatalf("guess %d: %d", i, rec.Code)
		}
	}
	if rec := passwordLogin(m, "alice", testPassword); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("locked out, even with the right password: %d", rec.Code)
	}
	if rec := passwordLogin(m, "bob", testPassword); rec.Code != http.StatusOK {
		t.Fatalf("other accounts aren't locked: %d", rec.Code)
	}
	m.fails.now = func() time.Time { return start.Add(16 * time.Minute) }
	if rec := passwordLogin(m, "alice", testPassword); rec.Code != http.StatusOK {
		t.Fatalf("after the window: %d", rec.Code)
	}
	// Success resets the count: nine more failures don't lock.
	for range 9 {
		passwordLogin(m, "alice", "wrong guess!")
	}
	if rec := passwordLogin(m, "alice", testPassword); rec.Code != http.StatusOK {
		t.Fatalf("after a reset: %d", rec.Code)
	}
}

func TestFailureLimiterWindow(t *testing.T) {
	f := newFailureLimiter(3, time.Minute)
	at := time.Unix(1000, 0)
	f.now = func() time.Time { return at }
	f.add("k")
	at = at.Add(30 * time.Second)
	f.add("k")
	f.add("k")
	if !f.blocked("k") || f.blocked("other") {
		t.Fatal("three hits within the window block")
	}
	at = at.Add(31 * time.Second) // the first hit slides out
	if f.blocked("k") {
		t.Fatal("still blocked after the oldest hit expired")
	}
	at = at.Add(time.Minute)
	f.blocked("k")
	if _, ok := f.hits["k"]; ok {
		t.Fatal("an expired key is kept")
	}
}

func TestCurrentSession(t *testing.T) {
	m, st, acc := newManager(t, config.Auth{PasswordLogin: true, SessionTTL: time.Hour})
	start := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	now := start
	m.now = func() time.Time { return now }
	u, _ := acc.Create(accounts.CreateInput{Username: "alice", Password: testPassword})
	c := sessionCookie(t, passwordLogin(m, "alice", testPassword))
	req := func(c *http.Cookie) *http.Request {
		return withCookie(httptest.NewRequest(http.MethodGet, "/", nil), c)
	}

	got, sess, ok := m.Current(req(c))
	if !ok || got.ID != u.ID || sess.ID != Hash(c.Value) {
		t.Fatalf("current: %v %+v", ok, got)
	}
	if _, _, ok := m.Current(httptest.NewRequest(http.MethodGet, "/", nil)); ok {
		t.Fatal("no cookie")
	}
	for _, v := range []string{"", "forged", Hash(c.Value)} {
		if _, _, ok := m.Current(req(&http.Cookie{Name: cookieName, Value: v})); ok {
			t.Errorf("cookie %q accepted", v)
		}
	}

	// Last seen is written at most once a minute.
	now = start.Add(30 * time.Second)
	m.Current(req(c))
	if s, _ := st.SessionByID(t.Context(), sess.ID); !s.LastSeenAt.Equal(start) {
		t.Fatalf("touched too soon: %v", s.LastSeenAt)
	}
	now = start.Add(2 * time.Minute)
	if _, s, _ := m.Current(req(c)); !s.LastSeenAt.Equal(now) {
		t.Fatalf("returned last seen: %v", s.LastSeenAt)
	}
	if s, _ := st.SessionByID(t.Context(), sess.ID); !s.LastSeenAt.Equal(now) {
		t.Fatalf("stored last seen: %v", s.LastSeenAt)
	}

	// Suspending the account ends its sessions' access at once.
	u.Suspended = true
	st.UpdateUser(t.Context(), u)
	if _, _, ok := m.Current(req(c)); ok {
		t.Fatal("a suspended user's session still works")
	}
	u.Suspended = false
	st.UpdateUser(t.Context(), u)

	now = start.Add(time.Hour + time.Second)
	if _, _, ok := m.Current(req(c)); ok {
		t.Fatal("an expired session still works")
	}
	now = start
	st.DeleteSession(t.Context(), u.ID, sess.ID)
	if _, _, ok := m.Current(req(c)); ok {
		t.Fatal("a revoked session still works")
	}
}

func TestRequireUserAndAdmin(t *testing.T) {
	m, st, acc := newManager(t, config.Auth{PasswordLogin: true, SessionTTL: time.Hour})
	acc.Create(accounts.CreateInput{Username: "admin", Password: testPassword, Role: store.RoleAdmin})
	acc.Create(accounts.CreateInput{Username: "user", Password: testPassword})
	adminC := sessionCookie(t, passwordLogin(m, "admin", testPassword))
	userC := sessionCookie(t, passwordLogin(m, "user", testPassword))

	var seen store.User
	var seenSess store.Session
	ok := func(w http.ResponseWriter, r *http.Request) {
		seen, seenSess = User(r), SessionOf(r)
		w.WriteHeader(http.StatusNoContent)
	}
	for _, tc := range []struct {
		name   string
		h      http.HandlerFunc
		cookie *http.Cookie
		code   int
		who    string
	}{
		{"user, anonymous", m.RequireUser(ok), nil, http.StatusUnauthorized, ""},
		{"user, signed in", m.RequireUser(ok), userC, http.StatusNoContent, "user"},
		{"admin, anonymous", m.RequireAdmin(ok), nil, http.StatusUnauthorized, ""},
		{"admin, as a user", m.RequireAdmin(ok), userC, http.StatusForbidden, ""},
		{"admin, as an admin", m.RequireAdmin(ok), adminC, http.StatusNoContent, "admin"},
	} {
		seen, seenSess = store.User{}, store.Session{}
		r := httptest.NewRequest(http.MethodGet, "/api/x", nil)
		if tc.cookie != nil {
			r.AddCookie(tc.cookie)
		}
		rec := httptest.NewRecorder()
		tc.h(rec, r)
		if rec.Code != tc.code || seen.Username != tc.who {
			t.Errorf("%s: %d, handler saw %q", tc.name, rec.Code, seen.Username)
		}
		if tc.who != "" && seenSess.ID != Hash(tc.cookie.Value) {
			t.Errorf("%s: session %q", tc.name, seenSess.ID)
		}
	}
	if u, _ := st.UserByUsername(t.Context(), "user"); u.IsAdmin() {
		t.Fatal("setup")
	}
}

func TestForwarded(t *testing.T) {
	m, st, acc := newManager(t, config.Auth{PasswordLogin: true, SessionTTL: time.Hour})
	alice, _ := acc.Create(accounts.CreateInput{Username: "alice", Password: testPassword})
	bob, _ := acc.Create(accounts.CreateInput{Username: "bob", Password: testPassword})
	aliceC := sessionCookie(t, passwordLogin(m, "alice", testPassword))
	bobC := sessionCookie(t, passwordLogin(m, "bob", testPassword))
	const secret = "cluster-secret"

	var seen store.User
	var seenSess store.Session
	var forwarded bool
	h := m.Forwarded(secret, m.RequireUser(func(w http.ResponseWriter, r *http.Request) {
		seen, seenSess, forwarded = User(r), SessionOf(r), IsForwarded(r)
	}))
	send := func(h http.Handler, hdr map[string]string) int {
		seen, seenSess, forwarded = store.User{}, store.Session{}, false
		r := httptest.NewRequest(http.MethodGet, "/internal/x", nil)
		for k, v := range hdr {
			r.Header.Set(k, v)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		return rec.Code
	}
	id := func(u store.User) string { return strconv.FormatInt(u.ID, 10) }

	if code := send(h, map[string]string{HeaderSecret: secret, HeaderCaller: id(alice), HeaderSession: Hash(aliceC.Value)}); code != http.StatusOK ||
		seen.ID != alice.ID || seenSess.ID != Hash(aliceC.Value) || !forwarded {
		t.Fatalf("forwarded: %d %+v %+v", code, seen, seenSess)
	}
	// Someone else's session ID isn't attached to the caller.
	if code := send(h, map[string]string{HeaderSecret: secret, HeaderCaller: id(alice), HeaderSession: Hash(bobC.Value)}); code != http.StatusOK || seenSess.ID != "" {
		t.Fatalf("mismatched session: %d %+v", code, seenSess)
	}

	for _, tc := range []struct {
		name   string
		secret string
		hdr    map[string]string
	}{
		{"no secret", secret, map[string]string{HeaderCaller: id(alice)}},
		{"wrong secret", secret, map[string]string{HeaderSecret: "guess", HeaderCaller: id(alice)}},
		{"clustering off", "", map[string]string{HeaderSecret: "", HeaderCaller: id(alice)}},
		{"unknown caller", secret, map[string]string{HeaderSecret: secret, HeaderCaller: "99999"}},
		{"no caller", secret, map[string]string{HeaderSecret: secret}},
	} {
		h := m.Forwarded(tc.secret, m.RequireUser(func(http.ResponseWriter, *http.Request) { t.Errorf("%s: reached the handler", tc.name) }))
		if code := send(h, tc.hdr); code != http.StatusUnauthorized {
			t.Errorf("%s: %d", tc.name, code)
		}
	}

	// A suspension on another node counts here too.
	bob.Suspended = true
	st.UpdateUser(t.Context(), bob)
	if code := send(h, map[string]string{HeaderSecret: secret, HeaderCaller: id(bob)}); code != http.StatusUnauthorized {
		t.Fatalf("suspended caller: %d", code)
	}

	// Without Forwarded, the headers mean nothing.
	direct := m.RequireUser(func(http.ResponseWriter, *http.Request) { t.Error("headers alone signed in") })
	if code := send(direct, map[string]string{HeaderSecret: secret, HeaderCaller: id(alice)}); code != http.StatusUnauthorized {
		t.Fatalf("direct: %d", code)
	}
}

func TestLogout(t *testing.T) {
	m, st, acc := newManager(t, config.Auth{PasswordLogin: true, SessionTTL: time.Hour, CookieSecure: true})
	acc.Create(accounts.CreateInput{Username: "alice", Password: testPassword})
	c := sessionCookie(t, passwordLogin(m, "alice", testPassword))
	other := sessionCookie(t, passwordLogin(m, "alice", testPassword))
	m.endSession = "https://id.example.org/end" // a password session never goes there

	logout := func(c *http.Cookie) (string, *http.Cookie) {
		r := httptest.NewRequest(http.MethodPost, "/auth/logout", nil)
		if c != nil {
			r.AddCookie(c)
		}
		rec := httptest.NewRecorder()
		m.Logout(rec, r)
		var body struct{ Redirect string }
		json.NewDecoder(rec.Body).Decode(&body)
		return body.Redirect, sessionCookie(t, rec)
	}
	next, cleared := logout(c)
	if next != "/" || cleared.Value != "" || cleared.MaxAge >= 0 || !cleared.Secure {
		t.Fatalf("logout: %q %+v", next, cleared)
	}
	if _, err := st.SessionByID(t.Context(), Hash(c.Value)); err == nil {
		t.Fatal("the session wasn't deleted")
	}
	if _, _, ok := m.Current(withCookie(httptest.NewRequest(http.MethodGet, "/", nil), other)); !ok {
		t.Fatal("signing out ended other sessions")
	}
	if next, _ := logout(nil); next != "/" {
		t.Fatalf("anonymous: %q", next)
	}
}

func TestEndSessionURL(t *testing.T) {
	m, _, _ := newManager(t, config.Auth{OIDCClientID: "cid"})
	m.pub = "https://evrd.example.org"
	m.endSession = "https://id.example.org/end?ui=1"
	if got := m.endSessionURL(""); got != "https://id.example.org/end?ui=1&client_id=cid&post_logout_redirect_uri=https%3A%2F%2Fevrd.example.org%2F" {
		t.Fatalf("got %q", got)
	}
}

func TestClientIP(t *testing.T) {
	for addr, want := range map[string]string{
		"192.0.2.1:1234":   "192.0.2.1",
		"[2001:db8::1]:80": "2001:db8::1",
		"unix-socket":      "unix-socket",
	} {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.RemoteAddr = addr
		if got := (&Manager{}).ClientIP(r); got != want {
			t.Errorf("%s: %q", addr, got)
		}
	}
}
