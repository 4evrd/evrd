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

// Manager owns sign-in, sessions and the request middleware.
type Manager struct {
	SessionServiceImpl // listing sessions and signing out

	st    store.Store
	acc   *accounts.Service
	cmu   sync.Mutex // guards cfg (SetConfig)
	cfg   config.Auth
	pub   string // public URL
	log   *slog.Logger
	now   func() time.Time
	fails *failureLimiter

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
}

// conf is the current sign-in config.
func (m *Manager) conf() config.Auth {
	m.cmu.Lock()
	defer m.cmu.Unlock()
	return m.cfg
}

type pendingLogin struct {
	nonce, verifier string
	expires         time.Time
}

// New builds the manager, discovering the OIDC provider when one is
// configured.
func New(ctx context.Context, st store.Store, acc *accounts.Service, cfg config.Auth, publicURL string, log *slog.Logger) (*Manager, error) {
	m := &Manager{
		SessionServiceImpl: NewSessionService(st),
		st:                 st,
		acc:                acc,
		cfg:                cfg,
		pub:                publicURL,
		log:                log,
		now:                time.Now,
		fails:              newFailureLimiter(10, 15*time.Minute),
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
	scopes := []string{oidc.ScopeOpenID, "profile", "email"}
	if cfg.OIDCAdminRole != "" {
		scopes = append(scopes, "roles")
	}
	m.oidc = p
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
	err := m.st.CreateSession(r.Context(), store.Session{
		ID: Hash(token), UserID: u.ID, Method: method, IDToken: idToken,
		IP: m.ClientIP(r), UserAgent: truncate(r.UserAgent(), 255),
		CreatedAt: now, LastSeenAt: now, ExpiresAt: now.Add(m.conf().SessionTTL),
	})
	if err != nil {
		return err
	}
	u.LastLoginAt = &now
	_ = m.st.UpdateUser(r.Context(), u)
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
	sess, err := m.st.SessionByID(r.Context(), Hash(c.Value))
	now := m.now()
	if err != nil || now.After(sess.ExpiresAt) {
		return store.User{}, store.Session{}, false
	}
	u, err := m.st.UserByID(r.Context(), sess.UserID)
	if err != nil || u.Suspended {
		return store.User{}, store.Session{}, false
	}
	if now.Sub(sess.LastSeenAt) > touchEvery {
		_ = m.st.TouchSession(r.Context(), sess.ID, now)
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
			u, err := m.st.UserByID(ctx, id)
			if err != nil || u.Suspended {
				writeErr(w, http.StatusUnauthorized, "sign in first")
				return
			}
			ctx = context.WithValue(ctx, userKey, u)
			if sess, err := m.st.SessionByID(ctx, r.Header.Get(HeaderSession)); err == nil && sess.UserID == u.ID {
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
	if m.fails.blocked(key) {
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
		m.fails.add(key)
		writeErr(w, http.StatusUnauthorized, "wrong username or password")
		return
	}
	m.fails.reset(key)
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
	m.pending[state] = pendingLogin{nonce: nonce, verifier: verifier, expires: now.Add(10 * time.Minute)}
	m.mu.Unlock()
	http.Redirect(w, r, m.oauth.AuthCodeURL(state, oidc.Nonce(nonce), oauth2.S256ChallengeOption(verifier)), http.StatusFound)
}

// Claims are the ID-token fields evrd reads.
type Claims struct {
	Subject           string   `json:"sub"`
	Email             string   `json:"email"`
	Username          string   `json:"username"`
	PreferredUsername string   `json:"preferred_username"`
	Roles             []string `json:"roles"`
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
	if err := idt.Claims(&c); err != nil {
		fail("provider")
		return
	}
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
	http.Redirect(w, r, "/", http.StatusFound)
}

// provision finds or creates the account for an SSO identity. A non-empty
// code is the reason sign-in can't continue (shown on the sign-in page).
func (m *Manager) provision(c Claims) (store.User, string) {
	isAdmin := m.conf().OIDCAdminRole != "" && contains(c.Roles, m.conf().OIDCAdminRole)

	u, err := m.st.UserBySubject(context.Background(), c.Subject)
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
		existing, err := m.st.UserByUsername(context.Background(), name)
		switch {
		case err == nil && existing.OIDCSubject == nil && (m.conf().OIDCLinkExisting || claims(existing, c)):
			sub := c.Subject
			existing.OIDCSubject = &sub
			existing.SSOClaimable = false
			if err := m.st.UpdateUser(context.Background(), existing); err != nil {
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
		_ = m.st.UpdateUser(context.Background(), u)
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
		if sess, err := m.st.SessionByID(r.Context(), id); err == nil {
			_ = m.st.DeleteSession(r.Context(), sess.UserID, id)
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

// SameOrigin refuses cross-site state-changing requests (CSRF), on top of the
// SameSite=Lax cookie.
func SameOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
		default:
			if !sameOrigin(r) {
				writeErr(w, http.StatusForbidden, "cross-site request refused")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// CheckWebSocketOrigin is the upgrader's origin check: a browser opening a
// socket from another site must not ride the session cookie.
func CheckWebSocketOrigin(r *http.Request) bool { return sameOrigin(r) }

func sameOrigin(r *http.Request) bool {
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" {
		return site == "same-origin" || site == "none"
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true // not a browser cross-site request
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	host := r.Header.Get("X-Forwarded-Host")
	if host == "" {
		host = r.Host
	}
	return strings.EqualFold(u.Host, host)
}

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

// failureLimiter counts failed sign-ins per key in a sliding window.
type failureLimiter struct {
	mu     sync.Mutex
	max    int
	window time.Duration
	hits   map[string][]time.Time
	now    func() time.Time
}

func newFailureLimiter(max int, window time.Duration) *failureLimiter {
	return &failureLimiter{max: max, window: window, hits: map[string][]time.Time{}, now: time.Now}
}

func (f *failureLimiter) recent(key string) []time.Time {
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

func (f *failureLimiter) blocked(key string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.recent(key)) >= f.max
}

func (f *failureLimiter) add(key string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.hits[key] = append(f.recent(key), f.now())
}

func (f *failureLimiter) reset(key string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.hits, key)
}
