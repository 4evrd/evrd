// Package api exposes evrd's HTTP + WebSocket API, consumed by the web app:
// sign-in, the signed-in user's own account, their networks and message
// history, a live per-network message stream, and user administration.
//
// Every route except /api/config, /api/languages and the sign-in endpoints
// needs a session cookie (see internal/auth); /api/admin/* also needs the
// admin role. A user's own resources are always looked up through the
// session, never a request field, so one user can't reach another's.
//
// In a cluster, a request about a user is served by the node that owns that
// user (see cluster.go): any other node forwards it there.
package api

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"

	"github.com/4evrd/evrd/internal/accounts"
	"github.com/4evrd/evrd/internal/auth"
	"github.com/4evrd/evrd/internal/bouncer"
	"github.com/4evrd/evrd/internal/certs"
	"github.com/4evrd/evrd/internal/config"
	"github.com/4evrd/evrd/internal/directory"
	"github.com/4evrd/evrd/internal/listen"
	"github.com/4evrd/evrd/internal/metricsq"
	"github.com/4evrd/evrd/internal/settings"
	"github.com/4evrd/evrd/internal/store"
	"github.com/4evrd/evrd/internal/version"
)

// StatusRunner runs a *status command for the web chat (the IRC listener
// implements it, so both share one set of commands).
type StatusRunner interface {
	WebStatus(userID int64, n store.Network, text string, out func(string))
}

type Server struct {
	b       *bouncer.Bouncer
	status  StatusRunner                    // nil: *status isn't available in the web chat
	cl      Cluster                         // nil: a single node that serves every user
	secret  string                          // the cluster secret
	metrics atomic.Pointer[metricsq.Client] // nil: dashboards show no charts
	acc     *accounts.Service
	auth    *auth.Manager
	cfgp    atomic.Pointer[config.Config]
	// settings, when set, edits the server settings (Admin → Settings).
	settings *settings.Manager
	certs    *certs.Manager       // nil: no certificate report
	dir      *directory.Directory // nil: no network directory
	webApp   string               // web_root: the built web app to serve, or ""
	log      *slog.Logger
	up       websocket.Upgrader
}

func NewServer(b *bouncer.Bouncer, acc *accounts.Service, am *auth.Manager, cfg *config.Config, status StatusRunner, log *slog.Logger) *Server {
	s := &Server{
		b: b, acc: acc, auth: am, status: status, log: log,
		up: websocket.Upgrader{CheckOrigin: auth.CheckWebSocketOrigin},
	}
	s.cfgp.Store(cfg)
	return s
}

func (s *Server) st() store.Store { return s.b.Store() }

// ListenAndServe runs the API until ctx is cancelled.
func (s *Server) ListenAndServe(ctx context.Context, addr string) error {
	return s.serve(ctx, "api", addr, s.Handler(), nil)
}

// ListenAndServeTLS serves the API and web app over HTTPS (https_listen),
// with conf's certificates.
func (s *Server) ListenAndServeTLS(ctx context.Context, addr string, conf *tls.Config) error {
	h := s.Handler()
	hsts := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Strict-Transport-Security", "max-age=31536000")
		h.ServeHTTP(w, r)
	})
	return s.serve(ctx, "https", addr, hsts, conf)
}

// ListenAndServeHTTP serves h on a plain-HTTP port (http_listen: the
// redirect to HTTPS and ACME challenges).
func (s *Server) ListenAndServeHTTP(ctx context.Context, addr string, h http.Handler) error {
	return s.serve(ctx, "http (to https)", addr, h, nil)
}

func (s *Server) serve(ctx context.Context, name, addr string, h http.Handler, conf *tls.Config) error {
	ln, err := listen.TCP(addr)
	if err != nil {
		return err
	}
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second, TLSConfig: conf}
	go func() {
		<-ctx.Done()
		srv.Shutdown(context.Background())
	}()
	s.log.Info(name+" listening", "addr", addr)
	if conf != nil {
		err = srv.ServeTLS(ln, "", "")
	} else {
		err = srv.Serve(ln)
	}
	if err != http.ErrServerClosed {
		return err
	}
	return nil
}

// Handler is the public API, behind the load balancer.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// sign-in
	mux.HandleFunc("GET /api/config", s.handleConfig)
	mux.HandleFunc("GET /api/languages", s.handleLanguages)
	mux.HandleFunc("GET /api/languages/{code}", s.handleLanguage)
	mux.HandleFunc("POST /auth/password", s.auth.PasswordLogin)
	mux.HandleFunc("GET /auth/login", s.auth.OIDCLogin)
	mux.HandleFunc("GET /auth/callback", s.auth.Callback)
	mux.HandleFunc("POST /auth/logout", s.auth.Logout)

	s.routes(mux)
	mux.HandleFunc("PUT /api/provision/users/{username}", s.provisionAuth(s.handleProvisionUser))
	if s.webApp != "" {
		mux.Handle("/api/", http.NotFoundHandler()) // not the app's index.html
		mux.Handle("/auth/", http.NotFoundHandler())
		mux.Handle("/", webApp(s.webApp))
	}
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
	mux.HandleFunc("GET /api/me/language-pack", user(s.handleMyLanguagePack))
	mux.HandleFunc("PUT /api/me/language-pack", user(s.handlePutMyLanguagePack))
	mux.HandleFunc("DELETE /api/me/language-pack", user(s.handleDeleteMyLanguagePack))

	// networks
	mux.HandleFunc("GET /api/networks", user(s.handleListNetworks))
	mux.HandleFunc("POST /api/networks", user(s.handleCreateNetwork))
	mux.HandleFunc("GET /api/networks/{id}", user(s.handleGetNetwork))
	mux.HandleFunc("PATCH /api/networks/{id}", user(s.handleUpdateNetwork))
	mux.HandleFunc("GET /api/bindhosts", user(s.handleBindHosts))
	mux.HandleFunc("GET /api/modules", s.auth.RequireUser(s.handleModules))
	mux.HandleFunc("GET /api/directory", s.auth.RequireUser(s.handleDirectory))
	mux.HandleFunc("DELETE /api/networks/{id}", user(s.handleDeleteNetwork))
	mux.HandleFunc("POST /api/networks/{id}/connect", user(s.handleConnect))
	mux.HandleFunc("POST /api/networks/{id}/disconnect", user(s.handleDisconnect))
	mux.HandleFunc("GET /api/networks/{id}/messages", user(s.handleMessages))
	mux.HandleFunc("GET /api/networks/{id}/buffers", user(s.handleBuffers))
	mux.HandleFunc("GET /api/networks/{id}/log", user(s.handleNetworkLog))
	mux.HandleFunc("GET /api/networks/{id}/health", user(s.handleNetworkHealth))
	mux.HandleFunc("GET /api/networks/{id}/channel", user(s.handleChannel))
	mux.HandleFunc("POST /api/networks/{id}/read", user(s.handleMarkRead))
	mux.HandleFunc("GET /api/me/clients", user(s.handleMyClients))
	mux.HandleFunc("GET /api/me/events", user(s.handleMyEvents))
	mux.HandleFunc("GET /api/overview", user(s.handleOverview))
	mux.HandleFunc("GET /api/reconnects", user(s.handleReconnects))
	mux.HandleFunc("GET /api/networks/{id}/stream", user(s.handleStream))

	// administration
	mux.HandleFunc("GET /api/admin/overview", adminAny(s.handleAdminOverview))
	mux.HandleFunc("GET /api/admin/metrics", adminAny(s.handleAdminMetrics))
	mux.HandleFunc("GET /api/admin/settings", adminAny(s.handleGetSettings))
	mux.HandleFunc("PATCH /api/admin/settings", adminAny(s.handleSaveSettings))
	mux.HandleFunc("POST /api/admin/directory/refresh", adminAny(s.handleRefreshDirectory))
	mux.HandleFunc("PUT /api/admin/languages/{code}", adminAny(s.handlePutLanguage))
	mux.HandleFunc("DELETE /api/admin/languages/{code}", adminAny(s.handleDeleteLanguage))
	mux.HandleFunc("GET /api/admin/users", adminAny(s.handleAdminListUsers))
	mux.HandleFunc("POST /api/admin/users", adminAny(s.handleAdminCreateUser))
	mux.HandleFunc("GET /api/admin/users/{id}", admin(s.handleAdminGetUser))
	mux.HandleFunc("GET /api/admin/users/{id}/overview", admin(s.handleAdminUserOverview))
	mux.HandleFunc("GET /api/admin/users/{id}/reconnects", admin(s.handleAdminUserReconnects))
	mux.HandleFunc("GET /api/admin/users/{id}/networks/{nid}/log", admin(s.handleAdminNetworkLog))
	mux.HandleFunc("GET /api/admin/users/{id}/networks/{nid}/health", admin(s.handleAdminNetworkHealth))
	mux.HandleFunc("PATCH /api/admin/users/{id}", admin(s.handleAdminUpdateUser))
	mux.HandleFunc("DELETE /api/admin/users/{id}", admin(s.handleAdminDeleteUser))
	mux.HandleFunc("POST /api/admin/users/{id}/password", admin(s.handleAdminSetPassword))
	mux.HandleFunc("POST /api/admin/users/{id}/sessions/revoke", admin(s.handleAdminRevokeSessions))
}

// handleConfig tells the sign-in page which methods exist and the app how
// clients attach. Public.
func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"passwordLogin": s.cfg().Auth.PasswordLogin,
		"sso":           s.cfg().Auth.OIDCEnabled(),
		"ssoLabel":      s.cfg().Auth.OIDCLabel,
		"ircHost":       s.cfg().IRCHost,
		"ircPorts":      ircPorts(s.cfg().Listen),
		"ircSelfSigned": s.selfSigned(),
		// saved passwords and module settings are encrypted (secret_key set):
		// the forms mark those fields
		"secretsEncrypted": store.SecretsEncrypted(),
		"version":          version.Version,
	})
}

// selfSigned says whether IRC clients will see a self-signed certificate
// (and so need to be told to accept it).
func (s *Server) selfSigned() bool {
	if s.certs == nil {
		return true
	}
	r := s.certs.Report()
	if r.Source == "selfsigned" {
		return true
	}
	for _, c := range r.Certs {
		if c.Issuer == "self-signed" {
			return true
		}
	}
	return false
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

// decodeOptional reads a JSON body if there is one.
func decodeOptional(r *http.Request, v any) error {
	if r.ContentLength == 0 {
		return nil
	}
	return json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(v)
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

// cfg is the current config (settings can change at runtime).
func (s *Server) cfg() *config.Config { return s.cfgp.Load() }

// SetConfig swaps in a changed config.
func (s *Server) SetConfig(c *config.Config) { s.cfgp.Store(c) }
