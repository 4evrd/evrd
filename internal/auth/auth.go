// Package auth signs people in to the evrd web app and keeps them signed in.
//
// Sessions live in the database (store.Session) behind an HttpOnly cookie, so
// they survive restarts and can be listed and revoked. Two ways in:
//
//   - a local username + password (when password_login is on), and
//   - OpenID Connect (auth-code + PKCE) against Logto or any OIDC provider.
//     SAML, social and enterprise logins are connectors in the provider, so
//     evrd only ever speaks OIDC.
//
// Accounts are provisioned on first SSO sign-in and linked by the ID token's
// subject, never by a name the user can change.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/4evrd/evrd/internal/realip"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"github.com/4evrd/evrd/internal/accounts"
	"github.com/4evrd/evrd/internal/config"
	"github.com/4evrd/evrd/internal/store"
	"github.com/4evrd/evrd/internal/telemetry"
)

const cookieName = "evrd_session"

// touchEvery bounds how often a session's last-seen time is written.
const touchEvery = time.Minute

// AuthServiceImpl is web sign-in: the sign-in and sign-out handlers, the
// middleware that finds who's asking, and their sessions.
type AuthServiceImpl interface {
	SessionServiceImpl

	SSOPasswordServiceImpl

	SetConfig(a config.Auth)
	// Failures is the failed sign-in count per username, shared by the web
	// and IRC sign-ins (10 in 15 minutes locks the account out for a while).
	Failures() *FailureLimiter
	Current(r *http.Request) (store.User, store.Session, bool)
	Forwarded(secret string, next http.Handler) http.Handler
	RequireUser(next http.HandlerFunc) http.HandlerFunc
	RequireAdmin(next http.HandlerFunc) http.HandlerFunc
	PasswordLogin(w http.ResponseWriter, r *http.Request)
	OIDCLogin(w http.ResponseWriter, r *http.Request)
	Callback(w http.ResponseWriter, r *http.Request)
	Logout(w http.ResponseWriter, r *http.Request)
	ClientIP(r *http.Request) string
}

// Repository is what sign-in keeps its records in.
type Repository interface {
	store.UserRepositoryImpl
	store.SessionRepositoryImpl
}

// Manager owns sign-in, sessions and the request middleware.
type Manager struct {
	SessionServiceImpl // listing sessions and signing out

	users         store.UserRepositoryImpl
	sess          store.SessionRepositoryImpl
	acc           accounts.AccountServiceImpl
	cmu           sync.Mutex // guards cfg (SetConfig)
	cfg           config.Auth
	pub           string // public URL
	log           *slog.Logger
	now           func() time.Time
	fails         *FailureLimiter
	*ssoPasswords // IRC sign-in with the SSO password

	oidc       *oidc.Provider
	verifier   *oidc.IDTokenVerifier
	oauth      oauth2.Config
	endSession string

	mu      sync.Mutex
	pending map[string]pendingLogin // OIDC state -> flow data
}

// SetConfig applies sign-in settings changed at runtime (password sign-in,
// session length, cookies, the SSO label and account rules). The provider
// itself (issuer, client) is set up once, at startup.
func (m *Manager) SetConfig(a config.Auth) {
	m.cmu.Lock()
	m.cfg = a
	m.cmu.Unlock()
	m.ssoPasswords.apply(a)
}

func (m *Manager) Failures() *FailureLimiter { return m.fails }

// conf is the current sign-in config.
func (m *Manager) conf() config.Auth {
	m.cmu.Lock()
	defer m.cmu.Unlock()
	return m.cfg
}

type pendingLogin struct {
	nonce, verifier string
	next            string // where to go once signed in (a path of the app)
	expires         time.Time
}

// SafeNext is next if it's a path of this app ("/irc/approve/…"), else "/":
// never another site's address (//evil.example, /\evil.example).
func SafeNext(next string) string {
	if !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") || strings.HasPrefix(next, "/\\") || strings.ContainsAny(next, "\r\n\\") {
		return "/"
	}
	return next
}

// New builds the manager, discovering the OIDC provider when one is
// configured.
func New(ctx context.Context, repo Repository, acc accounts.AccountServiceImpl, cfg config.Auth, publicURL string, log *slog.Logger) (*Manager, error) {
	m := &Manager{
		SessionServiceImpl: NewSessionService(repo),
		users:              repo,
		sess:               repo,
		acc:                acc,
		cfg:                cfg,
		pub:                publicURL,
		log:                log,
		now:                time.Now,
		fails:              NewFailureLimiter(10, 15*time.Minute),
		ssoPasswords:       newSSOPasswords(cfg, nil),
		pending:            map[string]pendingLogin{},
	}
	if !cfg.OIDCEnabled() {
		return m, nil
	}
	if publicURL == "" {
		return nil, errors.New("oidc needs public_url (the callback is public_url/auth/callback)")
	}
	p, err := oidc.NewProvider(ctx, cfg.OIDCIssuer)
	if err != nil {
		return nil, err
	}
	var meta struct {
		EndSession string `json:"end_session_endpoint"`
	}
	_ = p.Claims(&meta)
	scopes := Scopes(cfg)
	m.oidc = p
	m.ssoPasswords.setProvider(p, cfg) // for sso_irc_password password_grant
	m.verifier = p.Verifier(&oidc.Config{ClientID: cfg.OIDCClientID})
	m.endSession = meta.EndSession
	m.oauth = oauth2.Config{
		ClientID:     cfg.OIDCClientID,
		ClientSecret: cfg.OIDCClientSecret,
		Endpoint:     p.Endpoint(),
		RedirectURL:  publicURL + "/auth/callback",
		Scopes:       scopes,
	}
	return m, nil
}

// --- sessions ------------------------------------------------------------

// Hash is how a session token is stored: SHA-256, hex.
func Hash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func randToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func (m *Manager) startSession(w http.ResponseWriter, r *http.Request, u store.User, method, idToken string) error {
	token := randToken()
	now := m.now()
	err := m.sess.CreateSession(r.Context(), store.Session{
		ID: Hash(token), UserID: u.ID, Method: method, IDToken: idToken,
		IP: m.ClientIP(r), UserAgent: truncate(r.UserAgent(), 255),
		CreatedAt: now, LastSeenAt: now, ExpiresAt: now.Add(m.conf().SessionTTL),
	})
	if err != nil {
		return err
	}
	u.LastLoginAt = &now
	_ = m.users.UpdateUser(r.Context(), u)
	http.SetCookie(w, &http.Cookie{
		Name: cookieName, Value: token, Path: "/",
		HttpOnly: true, Secure: m.conf().CookieSecure, SameSite: http.SameSiteLaxMode,
		Expires: now.Add(m.conf().SessionTTL),
	})
	return nil
}

func (m *Manager) clearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: cookieName, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: m.conf().CookieSecure, SameSite: http.SameSiteLaxMode,
	})
}

// Current returns the signed-in user and their session, if the request has a
// live session for an account that isn't suspended.
func (m *Manager) Current(r *http.Request) (store.User, store.Session, bool) {
	c, err := r.Cookie(cookieName)
	if err != nil || c.Value == "" {
		return store.User{}, store.Session{}, false
	}
	sess, err := m.sess.SessionByID(r.Context(), Hash(c.Value))
	now := m.now()
	if err != nil || now.After(sess.ExpiresAt) {
		return store.User{}, store.Session{}, false
	}
	u, err := m.users.UserByID(r.Context(), sess.UserID)
	if err != nil || u.Suspended {
		return store.User{}, store.Session{}, false
	}
	if now.Sub(sess.LastSeenAt) > touchEvery {
		_ = m.sess.TouchSession(r.Context(), sess.ID, now)
		sess.LastSeenAt = now
	}
	return u, sess, true
}

type ctxKey int

const (
	userKey ctxKey = iota
	sessionKey
	forwardedKey
	clientIPKey
)

// Headers a node sets when it forwards a signed-in request to the node that
// owns the user it's about.
const (
	HeaderSecret  = "X-Evrd-Cluster-Secret"
	HeaderCaller  = "X-Evrd-Caller"  // the signed-in user's ID
	HeaderSession = "X-Evrd-Session" // their session's ID
	// HeaderClient is the browser's address as the forwarding node worked
	// it out (believed only with the cluster secret).
	HeaderClient = "X-Evrd-Client-IP"
)

// Forwarded authenticates requests arriving on the internal endpoint from
// another node: the cluster secret proves the sender, and the caller is
// loaded fresh from the database (so a suspension made meanwhile still
// counts). Handlers then see the caller exactly as on the node the browser
// talked to.
func (m *Manager) Forwarded(secret string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if secret == "" || subtle.ConstantTimeCompare([]byte(r.Header.Get(HeaderSecret)), []byte(secret)) != 1 {
			writeErr(w, http.StatusUnauthorized, "not a cluster node")
			return
		}
		ctx := context.WithValue(r.Context(), forwardedKey, true)
		if ip := r.Header.Get(HeaderClient); ip != "" {
			ctx = context.WithValue(ctx, clientIPKey, ip)
		}
		if id, err := strconv.ParseInt(r.Header.Get(HeaderCaller), 10, 64); err == nil {
			u, err := m.users.UserByID(ctx, id)
			if err != nil || u.Suspended {
				writeErr(w, http.StatusUnauthorized, "sign in first")
				return
			}
			ctx = context.WithValue(ctx, userKey, u)
			if sess, err := m.sess.SessionByID(ctx, r.Header.Get(HeaderSession)); err == nil && sess.UserID == u.ID {
				ctx = context.WithValue(ctx, sessionKey, sess)
			}
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// IsForwarded reports whether a request came from another node.
func IsForwarded(r *http.Request) bool {
	v, _ := r.Context().Value(forwardedKey).(bool)
	return v
}

// User is the signed-in user of a request that passed RequireUser.
func User(r *http.Request) store.User {
	u, _ := r.Context().Value(userKey).(store.User)
	return u
}

// SessionOf is the session of a request that passed RequireUser.
func SessionOf(r *http.Request) store.Session {
	s, _ := r.Context().Value(sessionKey).(store.Session)
	return s
}

// RequireUser gates a handler on a signed-in user (401 JSON otherwise).
func (m *Manager) RequireUser(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if IsForwarded(r) {
			if _, ok := r.Context().Value(userKey).(store.User); ok {
				next(w, r) // authenticated by Forwarded
			} else {
				writeErr(w, http.StatusUnauthorized, "sign in first")
			}
			return
		}
		u, sess, ok := m.Current(r)
		if !ok {
			writeErr(w, http.StatusUnauthorized, "sign in first")
			return
		}
		ctx := context.WithValue(r.Context(), userKey, u)
		ctx = context.WithValue(ctx, sessionKey, sess)
		next(w, r.WithContext(ctx))
	}
}

// RequireAdmin gates a handler on a signed-in admin (403 for everyone else).
func (m *Manager) RequireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return m.RequireUser(func(w http.ResponseWriter, r *http.Request) {
		if !User(r).IsAdmin() {
			writeErr(w, http.StatusForbidden, "admins only")
			return
		}
		next(w, r)
	})
}

// --- password sign-in ----------------------------------------------------

// PasswordLogin handles POST /auth/password {username, password}.
func (m *Manager) PasswordLogin(w http.ResponseWriter, r *http.Request) {
	if !m.conf().PasswordLogin {
		writeErr(w, http.StatusForbidden, "password sign-in is turned off — use "+m.conf().OIDCLabel)
		return
	}
	var in struct{ Username, Password string }
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	key := strings.ToLower(strings.TrimSpace(in.Username))
	if m.fails.Blocked(key) {
		writeErr(w, http.StatusTooManyRequests, "too many failed attempts — try again in a few minutes")
		return
	}
	u, err := m.acc.PasswordLogin(in.Username, in.Password)
	switch {
	case errors.Is(err, accounts.ErrSuspended):
		telemetry.WebLogins.Add(r.Context(), 1, telemetry.Attrs("method", "password", "result", "suspended"))
		writeErr(w, http.StatusForbidden, "this account is suspended")
		return
	case err != nil:
		telemetry.WebLogins.Add(r.Context(), 1, telemetry.Attrs("method", "password", "result", "invalid"))
		m.fails.Add(key)
		writeErr(w, http.StatusUnauthorized, "wrong username or password")
		return
	}
	m.fails.Reset(key)
	if !accounts.HostAllowed(u, m.ClientIP(r)) {
		telemetry.WebLogins.Add(r.Context(), 1, telemetry.Attrs("method", "password", "result", "host_denied"))
		writeErr(w, http.StatusForbidden, "signing in from your address isn't allowed for this account")
		return
	}
	if err := m.startSession(w, r, u, "password", ""); err != nil {
		writeErr(w, http.StatusInternalServerError, "couldn't start a session")
		return
	}
	m.log.Info("sign-in", "user", u.Username, "method", "password")
	telemetry.WebLogins.Add(r.Context(), 1, telemetry.Attrs("method", "password", "result", "ok"))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// --- OIDC sign-in --------------------------------------------------------

// OIDCLogin handles GET /auth/login: redirect to the provider.
func (m *Manager) OIDCLogin(w http.ResponseWriter, r *http.Request) {
	if m.oidc == nil {
		http.Redirect(w, r, "/?login_error=sso_off", http.StatusFound)
		return
	}
	state, nonce, verifier := randToken(), randToken(), oauth2.GenerateVerifier()
	m.mu.Lock()
	now := m.now()
	for k, p := range m.pending { // drop abandoned flows
		if now.After(p.expires) {
			delete(m.pending, k)
		}
	}
	m.pending[state] = pendingLogin{nonce: nonce, verifier: verifier, next: SafeNext(r.URL.Query().Get("next")), expires: now.Add(10 * time.Minute)}
	m.mu.Unlock()
	http.Redirect(w, r, m.oauth.AuthCodeURL(state, oidc.Nonce(nonce), oauth2.S256ChallengeOption(verifier)), http.StatusFound)
}

// Claims are the ID-token fields evrd reads.
type Claims struct {
	Subject           string `json:"sub"`
	Email             string `json:"email"`
	Username          string `json:"username"`
	PreferredUsername string `json:"preferred_username"`
	// Roles are the provider's roles or groups, from oidc_roles_claim.
	Roles []string `json:"-"`
}

// Scopes are what evrd asks the provider for: openid, profile and email,
// and oidc_scopes — or, when that isn't set, roles if oidc_admin_role is
// (Logto puts roles in the ID token only when asked for them).
func Scopes(a config.Auth) []string {
	out := []string{oidc.ScopeOpenID, "profile", "email"}
	extra := strings.Fields(a.OIDCScopes)
	if a.OIDCScopes == "" && a.OIDCAdminRole != "" {
		extra = []string{"roles"}
	}
	for _, s := range extra {
		if !contains(out, s) {
			out = append(out, s)
		}
	}
	return out
}

// RolesIn are the roles at path in an ID token's claims: a claim name, or a
// dotted path into nested objects (Keycloak's realm_access.roles), holding a
// list of strings or one (space-separated) string.
func RolesIn(claims map[string]any, path string) []string {
	if path == "" {
		path = "roles"
	}
	var v any = claims
	for _, part := range strings.Split(path, ".") {
		m, ok := v.(map[string]any)
		if !ok {
			return nil
		}
		v = m[part]
	}
	switch v := v.(type) {
	case string:
		return strings.Fields(v)
	case []any:
		var out []string
		for _, e := range v {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// Callback handles GET /auth/callback from the provider.
func (m *Manager) Callback(w http.ResponseWriter, r *http.Request) {
	fail := func(code string) {
		telemetry.WebLogins.Add(r.Context(), 1, telemetry.Attrs("method", "oidc", "result", code))
		http.Redirect(w, r, "/?login_error="+code, http.StatusFound)
	}
	if m.oidc == nil {
		fail("sso_off")
		return
	}
	q := r.URL.Query()
	if q.Get("error") != "" {
		fail("cancelled")
		return
	}
	m.mu.Lock()
	p, ok := m.pending[q.Get("state")]
	delete(m.pending, q.Get("state"))
	m.mu.Unlock()
	if !ok || m.now().After(p.expires) {
		fail("expired")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	tok, err := m.oauth.Exchange(ctx, q.Get("code"), oauth2.VerifierOption(p.verifier))
	if err != nil {
		m.log.Warn("oidc exchange", "err", err)
		fail("provider")
		return
	}
	rawID, _ := tok.Extra("id_token").(string)
	idt, err := m.verifier.Verify(ctx, rawID)
	if err != nil || idt.Nonce != p.nonce {
		m.log.Warn("oidc id_token", "err", err)
		fail("provider")
		return
	}
	var c Claims
	var all map[string]any
	if err := idt.Claims(&c); err != nil || idt.Claims(&all) != nil {
		fail("provider")
		return
	}
	c.Roles = RolesIn(all, m.conf().OIDCRolesClaim)
	u, code := m.provision(c)
	if code != "" {
		fail(code)
		return
	}
	if !accounts.HostAllowed(u, m.ClientIP(r)) {
		fail("host_denied")
		return
	}
	if err := m.startSession(w, r, u, "oidc", rawID); err != nil {
		fail("session")
		return
	}
	m.log.Info("sign-in", "user", u.Username, "method", "oidc")
	telemetry.WebLogins.Add(r.Context(), 1, telemetry.Attrs("method", "oidc", "result", "ok"))
	http.Redirect(w, r, SafeNext(p.next), http.StatusFound)
}

// provision finds or creates the account for an SSO identity. A non-empty
// code is the reason sign-in can't continue (shown on the sign-in page).
func (m *Manager) provision(c Claims) (store.User, string) {
	isAdmin := m.conf().OIDCAdminRole != "" && contains(c.Roles, m.conf().OIDCAdminRole)

	u, err := m.users.UserBySubject(context.Background(), c.Subject)
	switch {
	case err == nil:
		// existing, linked account
	case !errors.Is(err, store.ErrNotFound):
		return store.User{}, "server"
	default:
		name := c.Username
		if name == "" {
			name = c.PreferredUsername
		}
		if name == "" && c.Email != "" {
			name, _, _ = strings.Cut(c.Email, "@")
		}
		name, verr := accounts.NormalizeUsername(name)
		if verr != nil {
			return store.User{}, "bad_username"
		}
		existing, err := m.users.UserByUsername(context.Background(), name)
		switch {
		case err == nil && existing.OIDCSubject == nil && (m.conf().OIDCLinkExisting || claims(existing, c)):
			sub := c.Subject
			existing.OIDCSubject = &sub
			existing.SSOClaimable = false
			if err := m.users.UpdateUser(context.Background(), existing); err != nil {
				return store.User{}, "server"
			}
			m.log.Info("linked sso identity to existing account", "user", name)
			u = existing
		case err == nil:
			return store.User{}, "taken"
		case !m.conf().OIDCAutoCreate:
			return store.User{}, "no_account"
		default:
			role := store.RoleUser
			if isAdmin {
				role = store.RoleAdmin
			}
			u, err = m.acc.Create(accounts.CreateInput{Username: name, Email: c.Email, Role: role, Subject: c.Subject})
			if err != nil {
				m.log.Warn("sso provisioning", "user", name, "err", err)
				return store.User{}, "server"
			}
			m.log.Info("created account on first sso sign-in", "user", name)
		}
	}
	if u.Suspended {
		return store.User{}, "suspended"
	}
	changed := false
	if c.Email != "" && c.Email != u.Email {
		u.Email, changed = c.Email, true
	}
	if m.conf().OIDCAdminRole != "" {
		want := store.RoleUser
		if isAdmin {
			want = store.RoleAdmin
		}
		if want != u.Role && m.acc.GuardAdminChange(u, isAdmin) == nil {
			u.Role, changed = want, true
		}
	}
	if changed {
		_ = m.users.UpdateUser(context.Background(), u)
	}
	return u, ""
}

// claims reports whether an SSO identity may take over an account made for
// it ahead of time (store.User.SSOClaimable): the username already matched,
// and the email must too, so a look-alike registration at the provider can't.
func claims(u store.User, c Claims) bool {
	return u.SSOClaimable && u.Email != "" && strings.EqualFold(strings.TrimSpace(c.Email), u.Email)
}

// --- sign-out ------------------------------------------------------------

// Logout handles POST /auth/logout: ends this session and returns where the
// browser should go next (the provider's end-session page for SSO sessions,
// so the next "sign in" asks again on shared machines).
func (m *Manager) Logout(w http.ResponseWriter, r *http.Request) {
	next := "/"
	if c, err := r.Cookie(cookieName); err == nil && c.Value != "" {
		id := Hash(c.Value)
		if sess, err := m.sess.SessionByID(r.Context(), id); err == nil {
			_ = m.sess.DeleteSession(r.Context(), sess.UserID, id)
			if sess.Method == "oidc" && m.endSession != "" {
				next = m.endSessionURL(sess.IDToken)
			}
		}
	}
	m.clearCookie(w)
	writeJSON(w, http.StatusOK, map[string]string{"redirect": next})
}

func (m *Manager) endSessionURL(idToken string) string {
	q := url.Values{}
	q.Set("client_id", m.conf().OIDCClientID)
	q.Set("post_logout_redirect_uri", m.pub+"/")
	if idToken != "" {
		q.Set("id_token_hint", idToken)
	}
	sep := "?"
	if strings.Contains(m.endSession, "?") {
		sep = "&"
	}
	return m.endSession + sep + q.Encode()
}

// --- request hygiene -----------------------------------------------------

// ClientIP is the browser's address: from the forwarding node on a
// forwarded request, else X-Forwarded-For only from a trusted proxy (see
// realip).
func (m *Manager) ClientIP(r *http.Request) string {
	if ip, ok := r.Context().Value(clientIPKey).(string); ok {
		return ip
	}
	return realip.ClientIP(r, m.conf().TrustedProxies)
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

// --- failure limiter -----------------------------------------------------

// FailureLimiter counts failed sign-ins per key in a sliding window.
type FailureLimiter struct {
	mu     sync.Mutex
	max    int
	window time.Duration
	hits   map[string][]time.Time
	now    func() time.Time
}

// NewFailureLimiter blocks a key after max failures within window.
func NewFailureLimiter(max int, window time.Duration) *FailureLimiter {
	return &FailureLimiter{max: max, window: window, hits: map[string][]time.Time{}, now: time.Now}
}

func (f *FailureLimiter) recent(key string) []time.Time {
	cut := f.now().Add(-f.window)
	kept := f.hits[key][:0]
	for _, t := range f.hits[key] {
		if t.After(cut) {
			kept = append(kept, t)
		}
	}
	if len(kept) == 0 {
		delete(f.hits, key)
		return nil
	}
	f.hits[key] = kept
	return kept
}

// Blocked reports whether key has failed too often lately.
func (f *FailureLimiter) Blocked(key string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.recent(key)) >= f.max
}

// Add counts a failure for key.
func (f *FailureLimiter) Add(key string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.hits[key] = append(f.recent(key), f.now())
}

// Reset forgets key's failures (it signed in).
func (f *FailureLimiter) Reset(key string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.hits, key)
}
