package auth

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/4evrd/evrd/internal/accounts"
	"github.com/4evrd/evrd/internal/config"
	"github.com/4evrd/evrd/internal/store"
)

const (
	testClientID = "evrd-client"
	testPublic   = "https://evrd.example.org"
)

// fakeIdP is an in-process OpenID provider: discovery, JWKS, and a token
// endpoint that checks PKCE and hands out RS256-signed ID tokens.
type fakeIdP struct {
	t   *testing.T
	srv *httptest.Server
	key *rsa.PrivateKey

	mu     sync.Mutex
	grants map[string]grant // code -> what the token endpoint returns
	// passwords answer the password grant: username -> its user
	passwords map[string]pwUser
}

// pwUser is a provider user for the password grant: their password and
// subject, or the error the token endpoint answers with.
type pwUser struct {
	password, sub   string
	error, describe string
}

type grant struct {
	challenge string // PKCE S256 challenge from the authorize request
	claims    map[string]any
	key       *rsa.PrivateKey // signs the ID token; nil = the published key
}

func newFakeIdP(t *testing.T) *fakeIdP {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	p := &fakeIdP{t: t, key: key, grants: map[string]grant{}, passwords: map[string]pwUser{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"issuer":                                p.srv.URL,
			"authorization_endpoint":                p.srv.URL + "/authorize",
			"token_endpoint":                        p.srv.URL + "/token",
			"jwks_uri":                              p.srv.URL + "/jwks",
			"end_session_endpoint":                  p.srv.URL + "/session/end",
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{
			"kty": "RSA", "alg": "RS256", "use": "sig", "kid": "k1",
			"n": b64(key.N.Bytes()), "e": b64(big.NewInt(int64(key.E)).Bytes()),
		}}})
	})
	mux.HandleFunc("/token", p.token)
	p.srv = httptest.NewServer(mux)
	t.Cleanup(p.srv.Close)
	return p
}

// issue makes code redeemable at the token endpoint (once).
func (p *fakeIdP) issue(code string, g grant) {
	p.mu.Lock()
	p.grants[code] = g
	p.mu.Unlock()
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func (p *fakeIdP) token(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	if r.Form.Get("grant_type") == "password" {
		p.passwordGrant(w, r)
		return
	}
	p.mu.Lock()
	g, ok := p.grants[r.Form.Get("code")]
	delete(p.grants, r.Form.Get("code"))
	p.mu.Unlock()
	sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
	if !ok || r.Form.Get("grant_type") != "authorization_code" || b64(sum[:]) != g.challenge {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		io.WriteString(w, `{"error":"invalid_grant"}`)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"access_token": "at", "token_type": "Bearer", "expires_in": 3600,
		"id_token": p.sign(g.key, g.claims),
	})
}

// passwordGrant answers grant_type=password for the sign-in client.
func (p *fakeIdP) passwordGrant(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	fail := func(code int, e, d string) {
		w.WriteHeader(code)
		json.NewEncoder(w).Encode(map[string]string{"error": e, "error_description": d})
	}
	if r.Form.Get("client_id") != testClientID || r.Form.Get("client_secret") != "s3cret" {
		fail(http.StatusUnauthorized, "invalid_client", "bad client")
		return
	}
	if r.Form.Get("scope") != "openid" {
		fail(http.StatusBadRequest, "invalid_scope", "")
		return
	}
	p.mu.Lock()
	u, ok := p.passwords[r.Form.Get("username")]
	p.mu.Unlock()
	switch {
	case ok && u.error != "":
		fail(http.StatusBadRequest, u.error, u.describe)
	case !ok || u.password != r.Form.Get("password"):
		fail(http.StatusBadRequest, "invalid_grant", "Invalid user credentials")
	default:
		json.NewEncoder(w).Encode(map[string]any{"access_token": "at", "token_type": "Bearer", "expires_in": 300,
			"id_token": p.sign(nil, p.idClaims("", map[string]any{"sub": u.sub}))})
	}
}

// sign makes a compact RS256 JWT.
func (p *fakeIdP) sign(key *rsa.PrivateKey, claims map[string]any) string {
	if key == nil {
		key = p.key
	}
	head, _ := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT", "kid": "k1"})
	body, _ := json.Marshal(claims)
	in := b64(head) + "." + b64(body)
	sum := sha256.Sum256([]byte(in))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	if err != nil {
		p.t.Fatal(err)
	}
	return in + "." + b64(sig)
}

// idClaims are the standard claims of a good ID token for this client.
func (p *fakeIdP) idClaims(nonce string, extra map[string]any) map[string]any {
	now := time.Now()
	c := map[string]any{
		"iss": p.srv.URL, "aud": testClientID, "nonce": nonce,
		"iat": now.Unix(), "exp": now.Add(time.Hour).Unix(),
	}
	for k, v := range extra {
		c[k] = v
	}
	return c
}

func newOIDCManager(t *testing.T, p *fakeIdP, a config.Auth) (*Manager, store.Store, *accounts.Service) {
	t.Helper()
	st, err := store.Open("sqlite:" + filepath.Join(t.TempDir(), "evrd.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	acc := accounts.New(st, config.Limits{})
	a.OIDCIssuer, a.OIDCClientID, a.OIDCClientSecret = p.srv.URL, testClientID, "s3cret"
	if a.SessionTTL == 0 {
		a.SessionTTL = time.Hour
	}
	m, err := New(context.Background(), st, acc, a, testPublic, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return m, st, acc
}

// authorize runs GET /auth/login and returns the provider redirect's query.
func authorize(t *testing.T, m *Manager) url.Values {
	t.Helper()
	rec := httptest.NewRecorder()
	m.OIDCLogin(rec, httptest.NewRequest(http.MethodGet, "/auth/login", nil))
	if rec.Code != http.StatusFound {
		t.Fatalf("login: %d", rec.Code)
	}
	loc, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	return loc.Query()
}

// callback returns from the provider with state and code.
func callback(m *Manager, state, code string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/auth/callback?"+url.Values{"state": {state}, "code": {code}}.Encode(), nil)
	r.RemoteAddr = "198.51.100.7:4242"
	m.Callback(rec, r)
	return rec
}

// signIn runs a whole SSO sign-in for an identity with the given claims.
func signIn(t *testing.T, m *Manager, p *fakeIdP, extra map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	q := authorize(t, m)
	p.issue("code-1", grant{challenge: q.Get("code_challenge"), claims: p.idClaims(q.Get("nonce"), extra)})
	return callback(m, q.Get("state"), "code-1")
}

func loginError(rec *httptest.ResponseRecorder) string {
	loc, _ := url.Parse(rec.Header().Get("Location"))
	return loc.Query().Get("login_error")
}

func TestNewOIDC(t *testing.T) {
	p := newFakeIdP(t)
	m, _, _ := newOIDCManager(t, p, config.Auth{OIDCAdminRole: "evrd-admin"})
	if m.oauth.RedirectURL != testPublic+"/auth/callback" || m.endSession != p.srv.URL+"/session/end" {
		t.Fatalf("redirect %q, end session %q", m.oauth.RedirectURL, m.endSession)
	}
	if got := strings.Join(m.oauth.Scopes, " "); got != "openid profile email roles" {
		t.Fatalf("scopes with an admin role: %q", got)
	}
	m, _, _ = newOIDCManager(t, p, config.Auth{})
	if got := strings.Join(m.oauth.Scopes, " "); got != "openid profile email" {
		t.Fatalf("scopes: %q", got)
	}

	st, _ := store.Open("sqlite:" + filepath.Join(t.TempDir(), "evrd.db"))
	defer st.Close()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	if _, err := New(context.Background(), st, nil, config.Auth{OIDCIssuer: p.srv.URL}, "", log); err == nil {
		t.Fatal("OIDC without a public URL has no callback")
	}
	if _, err := New(context.Background(), st, nil, config.Auth{OIDCIssuer: p.srv.URL + "/nope"}, testPublic, log); err == nil {
		t.Fatal("an issuer without discovery")
	}
}

func TestOIDCLoginRedirect(t *testing.T) {
	p := newFakeIdP(t)
	m, _, _ := newOIDCManager(t, p, config.Auth{})
	q := authorize(t, m)
	if q.Get("client_id") != testClientID || q.Get("redirect_uri") != testPublic+"/auth/callback" || q.Get("response_type") != "code" {
		t.Fatalf("authorize request: %v", q)
	}
	if q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" || q.Get("nonce") == "" || q.Get("state") == "" {
		t.Fatalf("PKCE, nonce and state: %v", q)
	}
	if again := authorize(t, m); again.Get("state") == q.Get("state") || again.Get("nonce") == q.Get("nonce") {
		t.Fatal("state and nonce must be fresh per sign-in")
	}

	// Abandoned flows are dropped once they expire.
	start := time.Now()
	m.now = func() time.Time { return start.Add(11 * time.Minute) }
	authorize(t, m)
	if len(m.pending) != 1 {
		t.Fatalf("pending flows: %d", len(m.pending))
	}
}

func TestOIDCSignIn(t *testing.T) {
	p := newFakeIdP(t)
	m, st, _ := newOIDCManager(t, p, config.Auth{OIDCAutoCreate: true, CookieSecure: true})
	rec := signIn(t, m, p, map[string]any{"sub": "logto-1", "preferred_username": "Alice", "email": "alice@example.org"})
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/" {
		t.Fatalf("callback: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	u, err := st.UserBySubject(t.Context(), "logto-1")
	if err != nil || u.Username != "alice" || u.Email != "alice@example.org" || u.LastLoginAt == nil {
		t.Fatalf("provisioned: %v %+v", err, u)
	}
	c := sessionCookie(t, rec)
	if !c.Secure || !c.HttpOnly {
		t.Fatalf("cookie: %+v", c)
	}
	sess, err := st.SessionByID(t.Context(), Hash(c.Value))
	if err != nil || sess.UserID != u.ID || sess.Method != "oidc" || sess.IDToken == "" || sess.IP != "198.51.100.7" {
		t.Fatalf("session: %v %+v", err, sess)
	}

	// Signing out of an SSO session goes through the provider's end-session
	// page, with the ID token as the hint.
	out := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/auth/logout", nil)
	r.AddCookie(c)
	m.Logout(out, r)
	var body struct{ Redirect string }
	json.NewDecoder(out.Body).Decode(&body)
	next, _ := url.Parse(body.Redirect)
	if !strings.HasPrefix(body.Redirect, p.srv.URL+"/session/end?") || next.Query().Get("id_token_hint") != sess.IDToken ||
		next.Query().Get("client_id") != testClientID || next.Query().Get("post_logout_redirect_uri") != testPublic+"/" {
		t.Fatalf("logout redirect: %q", body.Redirect)
	}
	if _, err := st.SessionByID(t.Context(), sess.ID); err == nil {
		t.Fatal("the session outlived sign-out")
	}
}

func TestOIDCAdminRoleFromToken(t *testing.T) {
	p := newFakeIdP(t)
	m, st, _ := newOIDCManager(t, p, config.Auth{OIDCAutoCreate: true, OIDCAdminRole: "evrd-admin"})
	signIn(t, m, p, map[string]any{"sub": "a", "username": "alice", "roles": []string{"evrd-admin"}})
	signIn(t, m, p, map[string]any{"sub": "b", "username": "bob", "roles": []string{"other"}})
	a, _ := st.UserBySubject(t.Context(), "a")
	b, _ := st.UserBySubject(t.Context(), "b")
	if !a.IsAdmin() || b.IsAdmin() {
		t.Fatalf("roles: alice %s, bob %s", a.Role, b.Role)
	}
}

func TestOIDCRolesClaim(t *testing.T) {
	p := newFakeIdP(t)
	// authentik: groups; Keycloak: realm_access.roles
	m, st, _ := newOIDCManager(t, p, config.Auth{OIDCAutoCreate: true, OIDCAdminRole: "evrd-admin", OIDCRolesClaim: "groups"})
	signIn(t, m, p, map[string]any{"sub": "a", "username": "alice", "groups": []string{"staff", "evrd-admin"}, "roles": []string{}})
	k, sk, _ := newOIDCManager(t, p, config.Auth{OIDCAutoCreate: true, OIDCAdminRole: "evrd-admin", OIDCRolesClaim: "realm_access.roles"})
	signIn(t, k, p, map[string]any{"sub": "k", "username": "kim", "realm_access": map[string]any{"roles": []string{"evrd-admin"}}})
	a, _ := st.UserBySubject(t.Context(), "a")
	kim, _ := sk.UserBySubject(t.Context(), "k")
	if !a.IsAdmin() || !kim.IsAdmin() {
		t.Fatalf("groups: %s, realm_access.roles: %s", a.Role, kim.Role)
	}
}

func TestOIDCSignInReturnsToItsPage(t *testing.T) {
	p := newFakeIdP(t)
	m, _, _ := newOIDCManager(t, p, config.Auth{OIDCAutoCreate: true})
	rec := httptest.NewRecorder()
	m.OIDCLogin(rec, httptest.NewRequest(http.MethodGet, "/auth/login?next="+url.QueryEscape("/irc/approve/tok-1"), nil))
	loc, _ := url.Parse(rec.Header().Get("Location"))
	q := loc.Query()
	p.issue("code-1", grant{challenge: q.Get("code_challenge"), claims: p.idClaims(q.Get("nonce"), map[string]any{"sub": "n1", "username": "nina"})})
	if got := callback(m, q.Get("state"), "code-1").Header().Get("Location"); got != "/irc/approve/tok-1" {
		t.Fatalf("back to %q", got)
	}
}

func TestSafeNext(t *testing.T) {
	for in, want := range map[string]string{
		"/irc/approve/x": "/irc/approve/x", "/account#security": "/account#security", "": "/",
		"//evil.example": "/", "/\\evil.example": "/", "https://evil.example": "/", "evil": "/", "/a\r\nSet-Cookie: x": "/",
	} {
		if got := SafeNext(in); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}

func TestRolesIn(t *testing.T) {
	claims := map[string]any{
		"roles":         []any{"a", "b", 3},
		"groups":        "x y",
		"realm_access":  map[string]any{"roles": []any{"r"}},
		"not_an_object": "z",
	}
	for path, want := range map[string]string{
		"": "a b", "roles": "a b", "groups": "x y", "realm_access.roles": "r",
		"not_an_object.roles": "", "missing": "", "realm_access": "",
	} {
		if got := strings.Join(RolesIn(claims, path), " "); got != want {
			t.Errorf("%q: %q, want %q", path, got, want)
		}
	}
}

func TestScopes(t *testing.T) {
	for _, c := range []struct {
		a    config.Auth
		want string
	}{
		{config.Auth{}, "openid profile email"},
		{config.Auth{OIDCAdminRole: "admin"}, "openid profile email roles"}, // as Logto needs
		{config.Auth{OIDCAdminRole: "admin", OIDCScopes: "groups openid"}, "openid profile email groups"},
		{config.Auth{OIDCScopes: "offline_access"}, "openid profile email offline_access"},
	} {
		if got := strings.Join(Scopes(c.a), " "); got != c.want {
			t.Errorf("%+v: %q, want %q", c.a, got, c.want)
		}
	}
}

func TestOIDCCallbackRefusals(t *testing.T) {
	p := newFakeIdP(t)
	other, _ := rsa.GenerateKey(rand.Reader, 2048)
	good := map[string]any{"sub": "s1", "username": "alice"}

	for _, tc := range []struct {
		name string
		cfg  config.Auth
		run  func(m *Manager, st store.Store) *httptest.ResponseRecorder
		want string
	}{
		{"provider error", config.Auth{OIDCAutoCreate: true}, func(m *Manager, _ store.Store) *httptest.ResponseRecorder {
			q := authorize(t, m)
			rec := httptest.NewRecorder()
			m.Callback(rec, httptest.NewRequest(http.MethodGet, "/auth/callback?error=access_denied&state="+q.Get("state"), nil))
			return rec
		}, "cancelled"},
		{"unknown state", config.Auth{OIDCAutoCreate: true}, func(m *Manager, _ store.Store) *httptest.ResponseRecorder {
			authorize(t, m)
			return callback(m, "forged", "code-1")
		}, "expired"},
		{"replayed state", config.Auth{OIDCAutoCreate: true}, func(m *Manager, _ store.Store) *httptest.ResponseRecorder {
			q := authorize(t, m)
			p.issue("code-1", grant{challenge: q.Get("code_challenge"), claims: p.idClaims(q.Get("nonce"), good)})
			if rec := callback(m, q.Get("state"), "code-1"); loginError(rec) != "" {
				t.Fatalf("first use: %q", loginError(rec))
			}
			p.issue("code-2", grant{challenge: q.Get("code_challenge"), claims: p.idClaims(q.Get("nonce"), good)})
			return callback(m, q.Get("state"), "code-2")
		}, "expired"},
		{"slow return", config.Auth{OIDCAutoCreate: true}, func(m *Manager, _ store.Store) *httptest.ResponseRecorder {
			q := authorize(t, m)
			p.issue("code-1", grant{challenge: q.Get("code_challenge"), claims: p.idClaims(q.Get("nonce"), good)})
			start := time.Now()
			m.now = func() time.Time { return start.Add(11 * time.Minute) }
			return callback(m, q.Get("state"), "code-1")
		}, "expired"},
		{"code exchange refused", config.Auth{OIDCAutoCreate: true}, func(m *Manager, _ store.Store) *httptest.ResponseRecorder {
			q := authorize(t, m)
			return callback(m, q.Get("state"), "no-such-code")
		}, "provider"},
		{"PKCE verifier mismatch", config.Auth{OIDCAutoCreate: true}, func(m *Manager, _ store.Store) *httptest.ResponseRecorder {
			q := authorize(t, m)
			// The code was issued to another sign-in's challenge.
			p.issue("code-1", grant{challenge: authorize(t, m).Get("code_challenge"), claims: p.idClaims(q.Get("nonce"), good)})
			return callback(m, q.Get("state"), "code-1")
		}, "provider"},
		{"nonce mismatch", config.Auth{OIDCAutoCreate: true}, func(m *Manager, _ store.Store) *httptest.ResponseRecorder {
			q := authorize(t, m)
			p.issue("code-1", grant{challenge: q.Get("code_challenge"), claims: p.idClaims("replayed-nonce", good)})
			return callback(m, q.Get("state"), "code-1")
		}, "provider"},
		{"forged signature", config.Auth{OIDCAutoCreate: true}, func(m *Manager, _ store.Store) *httptest.ResponseRecorder {
			q := authorize(t, m)
			p.issue("code-1", grant{challenge: q.Get("code_challenge"), claims: p.idClaims(q.Get("nonce"), good), key: other})
			return callback(m, q.Get("state"), "code-1")
		}, "provider"},
		{"token for another client", config.Auth{OIDCAutoCreate: true}, func(m *Manager, _ store.Store) *httptest.ResponseRecorder {
			q := authorize(t, m)
			c := p.idClaims(q.Get("nonce"), good)
			c["aud"] = "someone-else"
			p.issue("code-1", grant{challenge: q.Get("code_challenge"), claims: c})
			return callback(m, q.Get("state"), "code-1")
		}, "provider"},
		{"expired token", config.Auth{OIDCAutoCreate: true}, func(m *Manager, _ store.Store) *httptest.ResponseRecorder {
			q := authorize(t, m)
			c := p.idClaims(q.Get("nonce"), good)
			c["exp"] = time.Now().Add(-time.Hour).Unix()
			p.issue("code-1", grant{challenge: q.Get("code_challenge"), claims: c})
			return callback(m, q.Get("state"), "code-1")
		}, "provider"},
		{"no account", config.Auth{}, func(m *Manager, _ store.Store) *httptest.ResponseRecorder {
			return signIn(t, m, p, good)
		}, "no_account"},
		{"unusable username", config.Auth{OIDCAutoCreate: true}, func(m *Manager, _ store.Store) *httptest.ResponseRecorder {
			return signIn(t, m, p, map[string]any{"sub": "s1", "email": "42@example.org"})
		}, "bad_username"},
		{"suspended", config.Auth{OIDCAutoCreate: true}, func(m *Manager, st store.Store) *httptest.ResponseRecorder {
			signIn(t, m, p, good)
			u, _ := st.UserBySubject(t.Context(), "s1")
			u.Suspended = true
			st.UpdateUser(t.Context(), u)
			return signIn(t, m, p, good)
		}, "suspended"},
		{"address not allowed", config.Auth{OIDCAutoCreate: true}, func(m *Manager, st store.Store) *httptest.ResponseRecorder {
			signIn(t, m, p, good)
			u, _ := st.UserBySubject(t.Context(), "s1")
			u.AllowedHosts = []string{"10.0.0.0/8"}
			st.UpdateUser(t.Context(), u)
			return signIn(t, m, p, good)
		}, "host_denied"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, st, _ := newOIDCManager(t, p, tc.cfg)
			rec := tc.run(m, st)
			if rec.Code != http.StatusFound || loginError(rec) != tc.want {
				t.Fatalf("got %d %q, want login_error=%s", rec.Code, rec.Header().Get("Location"), tc.want)
			}
			if len(rec.Result().Cookies()) != 0 {
				t.Fatal("a refused sign-in set a cookie")
			}
		})
	}
}

func TestSSOOff(t *testing.T) {
	m, _, _ := newManager(t, config.Auth{})
	for _, h := range []http.HandlerFunc{m.OIDCLogin, m.Callback} {
		rec := httptest.NewRecorder()
		h(rec, httptest.NewRequest(http.MethodGet, "/auth/x?state=a&code=b", nil))
		if rec.Code != http.StatusFound || loginError(rec) != "sso_off" {
			t.Errorf("got %d %q", rec.Code, rec.Header().Get("Location"))
		}
	}
}
