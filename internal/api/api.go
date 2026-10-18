// Package api exposes evrd's HTTP + WebSocket API, consumed by the web app:
// sign-in, the signed-in user's own account, their networks and message
// history, a live per-network message stream, and user administration.
//
// Every route except /api/config and the sign-in endpoints needs a session
// cookie (see internal/auth); /api/admin/* also needs the admin role. A user's
// own resources are always looked up through the session, never a request
// field, so one user can't reach another's.
//
// In a cluster, a request about a user is served by the node that owns that
// user (see cluster.go): any other node forwards it there.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"

	"code.ircforever.org/ircforever/evrd/internal/accounts"
	"code.ircforever.org/ircforever/evrd/internal/auth"
	"code.ircforever.org/ircforever/evrd/internal/bouncer"
	"code.ircforever.org/ircforever/evrd/internal/config"
	"code.ircforever.org/ircforever/evrd/internal/store"
	"code.ircforever.org/ircforever/evrd/internal/version"
)

// StatusRunner runs a *status command for the web chat (the IRC listener
// implements it, so both share one set of commands).
type StatusRunner interface {
	WebStatus(userID int64, n store.Network, text string, out func(string))
}

type Server struct {
	b      *bouncer.Bouncer
	status StatusRunner // nil: *status isn't available in the web chat
	cl     Cluster      // nil: a single node that serves every user
	secret string       // the cluster secret
	acc    *accounts.Service
	auth   *auth.Manager
	cfg    *config.Config
	log    *slog.Logger
	up     websocket.Upgrader
}

func NewServer(b *bouncer.Bouncer, acc *accounts.Service, am *auth.Manager, cfg *config.Config, status StatusRunner, log *slog.Logger) *Server {
	return &Server{
		b: b, acc: acc, auth: am, cfg: cfg, status: status, log: log,
		up: websocket.Upgrader{CheckOrigin: auth.CheckWebSocketOrigin},
	}
}

func (s *Server) st() store.Store { return s.b.Store() }

// ListenAndServe runs the API until ctx is cancelled.
func (s *Server) ListenAndServe(ctx context.Context, addr string) error {
	srv := &http.Server{Addr: addr, Handler: s.Handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		srv.Shutdown(context.Background())
	}()
	s.log.Info("api listening", "addr", addr)
	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		return err
	}
	return nil
}

// Handler is the public API, behind the load balancer.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// sign-in
	mux.HandleFunc("GET /api/config", s.handleConfig)
	mux.HandleFunc("POST /auth/password", s.auth.PasswordLogin)
	mux.HandleFunc("GET /auth/login", s.auth.OIDCLogin)
	mux.HandleFunc("GET /auth/callback", s.auth.Callback)
	mux.HandleFunc("POST /auth/logout", s.auth.Logout)

	s.routes(mux)
	return auth.SameOrigin(mux)
}

// routes registers the signed-in routes. Each is served on the node that
// owns the user it's about: the caller for /api/me and /api/networks, the
// {id} user for /api/admin/users/{id}.
func (s *Server) routes(mux *http.ServeMux) {
	caller := func(r *http.Request) int64 { return auth.User(r).ID }
	target := pathID
	user := func(h http.HandlerFunc) http.HandlerFunc { return s.auth.RequireUser(s.atOwner(caller, h)) }
	admin := func(h http.HandlerFunc) http.HandlerFunc { return s.auth.RequireAdmin(s.atOwner(target, h)) }
	adminAny := s.auth.RequireAdmin // about every user: served here

	// the signed-in user's own account
	mux.HandleFunc("GET /api/me", user(s.handleMe))
	mux.HandleFunc("PATCH /api/me", user(s.handleUpdateMe))
	mux.HandleFunc("DELETE /api/me", user(s.handleDeleteMe))
	mux.HandleFunc("POST /api/me/password", user(s.handleChangePassword))
	mux.HandleFunc("GET /api/me/sessions", user(s.handleListSessions))
	mux.HandleFunc("DELETE /api/me/sessions/{id}", user(s.handleRevokeSession))
	mux.HandleFunc("POST /api/me/sessions/revoke-others", user(s.handleRevokeOtherSessions))
	mux.HandleFunc("GET /api/me/client-passwords", user(s.handleListClientPasswords))
	mux.HandleFunc("POST /api/me/client-passwords", user(s.handleCreateClientPassword))
	mux.HandleFunc("DELETE /api/me/client-passwords/{id}", user(s.handleDeleteClientPassword))

	// networks
	mux.HandleFunc("GET /api/networks", user(s.handleListNetworks))
	mux.HandleFunc("POST /api/networks", user(s.handleCreateNetwork))
	mux.HandleFunc("PATCH /api/networks/{id}", user(s.handleUpdateNetwork))
	mux.HandleFunc("DELETE /api/networks/{id}", user(s.handleDeleteNetwork))
	mux.HandleFunc("POST /api/networks/{id}/connect", user(s.handleConnect))
	mux.HandleFunc("POST /api/networks/{id}/disconnect", user(s.handleDisconnect))
	mux.HandleFunc("GET /api/networks/{id}/messages", user(s.handleMessages))
	mux.HandleFunc("GET /api/networks/{id}/buffers", user(s.handleBuffers))
	mux.HandleFunc("GET /api/networks/{id}/stream", user(s.handleStream))

	// administration
	mux.HandleFunc("GET /api/admin/overview", adminAny(s.handleAdminOverview))
	mux.HandleFunc("GET /api/admin/users", adminAny(s.handleAdminListUsers))
	mux.HandleFunc("POST /api/admin/users", adminAny(s.handleAdminCreateUser))
	mux.HandleFunc("GET /api/admin/users/{id}", admin(s.handleAdminGetUser))
	mux.HandleFunc("PATCH /api/admin/users/{id}", admin(s.handleAdminUpdateUser))
	mux.HandleFunc("DELETE /api/admin/users/{id}", admin(s.handleAdminDeleteUser))
	mux.HandleFunc("POST /api/admin/users/{id}/password", admin(s.handleAdminSetPassword))
	mux.HandleFunc("POST /api/admin/users/{id}/sessions/revoke", admin(s.handleAdminRevokeSessions))
}

// handleConfig tells the sign-in page which methods exist and the app how
// clients attach. Public.
func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"passwordLogin": s.cfg.Auth.PasswordLogin,
		"sso":           s.cfg.Auth.OIDCEnabled(),
		"ssoLabel":      s.cfg.Auth.OIDCLabel,
		"ircHost":       s.cfg.IRCHost,
		"ircPorts":      ircPorts(s.cfg.Listen),
		"version":       version.Version,
	})
}

type portDTO struct {
	Port int  `json:"port"`
	TLS  bool `json:"tls"`
}

// ircPorts lists the IRC listener ports, TLS first.
func ircPorts(listen []string) []portDTO {
	var tls, plain []portDTO
	for _, l := range listen {
		scheme, addr, ok := strings.Cut(l, "://")
		if !ok {
			scheme, addr = "tcp", l
		}
		i := strings.LastIndex(addr, ":")
		port, err := strconv.Atoi(addr[i+1:])
		if i < 0 || err != nil {
			continue
		}
		switch scheme {
		case "ircs", "tls":
			tls = append(tls, portDTO{port, true})
		case "irc", "irc+insecure", "tcp":
			plain = append(plain, portDTO{port, false})
		}
	}
	return append(tls, plain...)
}

// --- helpers -------------------------------------------------------------

// decode reads a JSON body into v, answering 400 itself on failure.
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(v); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return false
	}
	return true
}

// fail maps an error to a response: validation problems are the caller's
// fault (422) and their message is shown; anything else is logged and hidden.
func (s *Server) fail(w http.ResponseWriter, err error) {
	var ve *accounts.ValidationError
	switch {
	case errors.As(err, &ve):
		writeErr(w, http.StatusUnprocessableEntity, ve.Message)
	case errors.Is(err, accounts.ErrUsernameTaken), errors.Is(err, accounts.ErrLastAdmin):
		writeErr(w, http.StatusConflict, err.Error())
	case errors.Is(err, accounts.ErrInvalidCredentials):
		writeErr(w, http.StatusUnprocessableEntity, "that isn't your current password")
	case errors.Is(err, store.ErrNotFound):
		writeErr(w, http.StatusNotFound, "not found")
	case errors.Is(err, bouncer.ErrSuspended):
		writeErr(w, http.StatusForbidden, "this account is suspended")
	case errors.Is(err, bouncer.ErrNotOwner):
		writeErr(w, http.StatusServiceUnavailable, moving)
	default:
		s.log.Error("api", "err", err)
		writeErr(w, http.StatusInternalServerError, "something went wrong")
	}
}

func pathID(r *http.Request) int64 {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return id
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

func unix(t *time.Time) any {
	if t == nil || t.IsZero() {
		return nil
	}
	return t.Unix()
}
