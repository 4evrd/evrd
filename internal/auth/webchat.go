package auth

// Another site's web chat using the bouncer (web_chat_origins). Third-party
// cookies don't reach evrd from another site's page, so the web chat gets a
// token of its own instead:
//
//  1. It opens GET /auth/web-chat?return=<its page>&state=<random> in the
//     browser. return must be on one of web_chat_origins.
//  2. evrd signs the user in as usual (the sign-in page, or straight to the
//     identity provider when that's the only way in) and comes back.
//  3. evrd makes a token — a session with method webchat, one per device,
//     listed and revocable under Account → Sessions, lasting web_chat_days
//     from its last use — and sends the browser back to return with the
//     token in the fragment (never the query: it isn't sent to the server or
//     in Referer headers).
//  4. The web chat calls the API with Authorization: Bearer <token>, and
//     opens the chat socket with the subprotocols "evrd" and
//     "evrd.token.<token>" (a browser can't set headers on a WebSocket).
//
// A token only works on the web chat's routes (internal/http picks them:
// reading networks and history, the chat socket, marking read), never for
// the account itself, and it never works as a cookie. POST
// /auth/web-chat/revoke with the token signs it out.

import (
	"context"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/4evrd/evrd/internal/store"
)

// MethodWebChat is the session method of a web chat's token.
const MethodWebChat = "webchat"

// WebSocketProtocol is the chat socket's subprotocol for a web chat that
// signs in with a token: it offers this and TokenProtocol+<token>, and evrd
// answers with this one (never the token).
const (
	WebSocketProtocol = "evrd"
	TokenProtocol     = "evrd.token."
)

// HeaderViaToken marks a request a node forwards as authenticated by a web
// chat's token (believed only with the cluster secret).
const HeaderViaToken = "X-Evrd-Via-Token"

// maxWebChats is how many web chat tokens an account keeps; making another
// signs out the one used least recently.
const maxWebChats = 10

// validState is what a web chat's state may look like: what it generated
// to check the answer is one it asked for.
var validState = regexp.MustCompile(`^[A-Za-z0-9._~-]{16,128}$`)

// BearerToken is the web chat token a request carries: Authorization:
// Bearer <token>, or on a WebSocket handshake the TokenProtocol
// subprotocol.
func BearerToken(r *http.Request) (string, bool) {
	if h := r.Header.Get("Authorization"); h != "" {
		scheme, tok, _ := strings.Cut(h, " ")
		tok = strings.TrimSpace(tok)
		if strings.EqualFold(scheme, "Bearer") && tok != "" {
			return tok, true
		}
		return "", false
	}
	if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		return "", false
	}
	for _, h := range r.Header.Values("Sec-WebSocket-Protocol") {
		for _, p := range strings.Split(h, ",") {
			if tok, ok := strings.CutPrefix(strings.TrimSpace(p), TokenProtocol); ok && tok != "" {
				return tok, true
			}
		}
	}
	return "", false
}

// ViaToken reports whether a request was signed in by a web chat's token
// (not the session cookie).
func ViaToken(r *http.Request) bool {
	v, _ := r.Context().Value(viaTokenKey).(bool)
	return v
}

// RequireWebChat is RequireUser for the routes a web chat may use: a request
// with a token is signed in by the token alone (a cookie beside it doesn't
// count), anything else as RequireUser does.
func (m *Manager) RequireWebChat(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tok, ok := BearerToken(r)
		if !ok || IsForwarded(r) {
			m.RequireUser(next)(w, r)
			return
		}
		u, sess, ok := m.tokenUser(r.Context(), tok)
		if !ok {
			writeErr(w, http.StatusUnauthorized, "sign in first")
			return
		}
		ctx := context.WithValue(r.Context(), userKey, u)
		ctx = context.WithValue(ctx, sessionKey, sess)
		ctx = context.WithValue(ctx, viaTokenKey, true)
		next(w, r.WithContext(ctx))
	}
}

// tokenUser is the account a live web chat token is for. Using it moves its
// end (at most once a minute).
func (m *Manager) tokenUser(ctx context.Context, tok string) (store.User, store.Session, bool) {
	sess, err := m.sess.SessionByID(ctx, Hash(tok))
	now := m.now()
	if err != nil || sess.Method != MethodWebChat || now.After(sess.ExpiresAt) {
		return store.User{}, store.Session{}, false
	}
	u, err := m.users.UserByID(ctx, sess.UserID)
	if err != nil || u.Suspended {
		return store.User{}, store.Session{}, false
	}
	if now.Sub(sess.LastSeenAt) > touchEvery {
		exp := now.Add(m.webChatTTL())
		_ = m.sess.RenewSession(ctx, sess.ID, now, exp)
		sess.LastSeenAt, sess.ExpiresAt = now, exp
	}
	return u, sess, true
}

func (m *Manager) webChatTTL() time.Duration {
	days := m.conf().WebChatDays
	if days <= 0 {
		days = 30
	}
	return time.Duration(days) * 24 * time.Hour
}

// WebChatSignIn handles GET /auth/web-chat?return=<url>&state=<state>
// [&network=<name>]: signs the user in if need be, makes a token for the
// web chat and sends the browser back to return with it in the fragment:
//
//	#token=…&state=…&user=<username>&network=<id or empty>&api=<evrd's URL>&expires=<unix>
//
// or, when no token could be made, #error=server&state=…. A return that
// isn't on one of web_chat_origins, or a missing state, is refused here and
// goes nowhere.
func (m *Manager) WebChatSignIn(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	ret, origin, ok := m.webChatReturn(q.Get("return"))
	if !ok {
		refuse(w, "This web chat isn't allowed to sign in here: its address isn't one of this server's web chat sites (web_chat_origins).")
		return
	}
	state := q.Get("state")
	if !validState.MatchString(state) {
		refuse(w, "The web chat sent no state (16 to 128 letters, digits, '.', '_', '~' or '-').")
		return
	}
	// only what the hand-off needs, so nothing else rides along
	again := url.Values{"return": {q.Get("return")}, "state": {state}}
	if n := q.Get("network"); n != "" {
		again.Set("network", n)
	}

	u, _, signedIn := m.Current(r)
	if !signedIn {
		if m.oidc != nil && !m.conf().PasswordLogin {
			// the provider is the only way in: no need for the sign-in page
			http.Redirect(w, r, "/auth/login?next="+url.QueryEscape("/auth/web-chat?"+again.Encode()), http.StatusFound)
			return
		}
		// the web app's page signs in (password or SSO) and comes back here
		http.Redirect(w, r, "/web-chat?"+again.Encode(), http.StatusFound)
		return
	}

	back := url.Values{"state": {state}}
	tok := randToken()
	now := m.now()
	exp := now.Add(m.webChatTTL())
	site := strings.TrimPrefix(strings.TrimPrefix(origin, "https://"), "http://")
	err := m.sess.CreateSession(r.Context(), store.Session{
		ID: Hash(tok), UserID: u.ID, Method: MethodWebChat, Label: truncate(site, 255),
		IP: m.ClientIP(r), UserAgent: truncate(r.UserAgent(), 255),
		CreatedAt: now, LastSeenAt: now, ExpiresAt: exp,
	})
	if err != nil {
		m.log.Error("web chat sign-in", "user", u.Username, "err", err)
		back.Set("error", "server")
		http.Redirect(w, r, ret+"#"+back.Encode(), http.StatusFound)
		return
	}
	m.pruneWebChats(r.Context(), u.ID)
	back.Set("token", tok)
	back.Set("user", u.Username)
	back.Set("network", m.webChatNetwork(r.Context(), u.ID, q.Get("network")))
	back.Set("api", m.baseURL(r))
	back.Set("expires", strconv.FormatInt(exp.Unix(), 10))
	m.log.Info("web chat sign-in", "user", u.Username, "site", site, "ip", m.ClientIP(r)) // never the token
	w.Header().Set("Referrer-Policy", "no-referrer")
	http.Redirect(w, r, ret+"#"+back.Encode(), http.StatusFound)
}

// WebChatRevoke handles POST /auth/web-chat/revoke: the token it carries
// (Authorization: Bearer) is signed out. It answers 204 either way.
func (m *Manager) WebChatRevoke(w http.ResponseWriter, r *http.Request) {
	if tok, ok := BearerToken(r); ok {
		if sess, err := m.sess.SessionByID(r.Context(), Hash(tok)); err == nil && sess.Method == MethodWebChat {
			_ = m.sess.DeleteSession(r.Context(), sess.UserID, sess.ID)
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

// webChatReturn checks a web chat's return address: an absolute http(s)
// URL on one of web_chat_origins. It comes back without any fragment, with
// its origin.
func (m *Manager) webChatReturn(raw string) (string, string, bool) {
	if raw == "" || strings.ContainsAny(raw, "\\\r\n\t ") {
		return "", "", false
	}
	u, err := url.Parse(raw)
	if err != nil || !u.IsAbs() || u.User != nil || u.Host == "" || u.Opaque != "" {
		return "", "", false
	}
	a := m.conf()
	origin := strings.ToLower(u.Scheme + "://" + u.Host)
	if !a.WebChatAllowed(origin) {
		return "", "", false
	}
	u.Fragment, u.RawFragment = "", ""
	return u.String(), origin, true
}

// webChatNetwork is the network the web chat should use, by ID ("" for
// none): the one it asked for by name, else web_chat_network, else the
// user's only network.
func (m *Manager) webChatNetwork(ctx context.Context, userID int64, asked string) string {
	nets, err := m.acc.ListNetworks(ctx, userID)
	if err != nil {
		return ""
	}
	for _, name := range []string{asked, m.conf().WebChatNetwork} {
		if name == "" {
			continue
		}
		for _, n := range nets {
			if strings.EqualFold(n.Name, name) {
				return strconv.FormatInt(n.ID, 10)
			}
		}
	}
	if len(nets) == 1 {
		return strconv.FormatInt(nets[0].ID, 10)
	}
	return ""
}

// pruneWebChats keeps an account's newest maxWebChats web chat tokens.
func (m *Manager) pruneWebChats(ctx context.Context, userID int64) {
	list, err := m.sess.ListSessions(ctx, userID)
	if err != nil {
		return
	}
	var chats []store.Session
	for _, s := range list {
		if s.Method == MethodWebChat {
			chats = append(chats, s)
		}
	}
	sort.SliceStable(chats, func(i, j int) bool { return chats[i].LastSeenAt.After(chats[j].LastSeenAt) })
	for i := maxWebChats; i < len(chats); i++ {
		_ = m.sess.DeleteSession(ctx, userID, chats[i].ID)
	}
}

// baseURL is where evrd's API is: public_url, or the address this request
// came to.
func (m *Manager) baseURL(r *http.Request) string {
	if m.pub != "" {
		return strings.TrimRight(m.pub, "/")
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

// refuse answers a hand-off that can't go back where it came from.
func refuse(w http.ResponseWriter, why string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusBadRequest)
	_, _ = w.Write([]byte(why + "\n"))
}
