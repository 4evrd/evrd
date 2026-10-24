// Package http is evrd's web server: the API's routes, the middleware in
// front of them, and the listeners. The handlers are internal/http/handlers.
//
// Every route except /api/config, /api/languages, /api/irc-messages and the
// sign-in endpoints
// needs a session cookie (see internal/auth); /api/admin/* also needs the
// admin role.
//
// In a cluster, a request about a user is served by the node that owns that
// user (see middleware.Owner): any other node forwards it there.
package http

import (
	"context"
	"crypto/tls"
	"log/slog"
	"net/http"
	"time"

	"github.com/4evrd/evrd/internal/auth"
	"github.com/4evrd/evrd/internal/bouncer"
	"github.com/4evrd/evrd/internal/certs"
	"github.com/4evrd/evrd/internal/config"
	"github.com/4evrd/evrd/internal/directory"
	"github.com/4evrd/evrd/internal/http/handlers"
	"github.com/4evrd/evrd/internal/http/middleware"
	"github.com/4evrd/evrd/internal/listen"
	"github.com/4evrd/evrd/internal/metricsq"
	"github.com/4evrd/evrd/internal/settings"
)

// Services are what the handlers work through, besides the bouncer.
type Services = handlers.Services

// StatusRunner runs a *status command for the web chat.
type StatusRunner = handlers.StatusRunner

// Cluster is what the server needs from internal/cluster.
type Cluster = handlers.Cluster

// Server routes requests to the handlers, through the middleware.
type Server struct {
	h      *handlers.Handlers
	auth   auth.AuthServiceImpl
	owner  *middleware.Owner
	secret string // the cluster secret
	webApp string // web_root: the built web app to serve, or ""
	log    *slog.Logger
}

// New builds the handlers on b and svc and the server in front of them.
func New(b *bouncer.Bouncer, svc Services, cfg *config.Config, status StatusRunner, log *slog.Logger) *Server {
	return &Server{
		h:     handlers.New(b, svc, cfg, status, log),
		auth:  svc.Auth,
		owner: &middleware.Owner{ClientIP: svc.Auth.ClientIP, Log: log},
		log:   log,
	}
}

// SetCluster makes the server forward requests to the owning node.
func (s *Server) SetCluster(cl Cluster, secret string) {
	s.h.SetCluster(cl, secret)
	s.owner.Cluster, s.owner.Secret, s.secret = cl, secret, secret
}

// SetConfig swaps in a changed config.
func (s *Server) SetConfig(c *config.Config) { s.h.SetConfig(c) }

// SetSettings lets admins edit the server settings (Admin → Settings).
func (s *Server) SetSettings(m *settings.Manager) { s.h.SetSettings(m) }

// SetCerts reports on m's certificates.
func (s *Server) SetCerts(m *certs.Manager) { s.h.SetCerts(m) }

// SetDirectory offers d's networks when adding one.
func (s *Server) SetDirectory(d *directory.Directory) { s.h.SetDirectory(d) }

// SetMetrics reads the dashboards' charts from m.
func (s *Server) SetMetrics(m *metricsq.Client) { s.h.SetMetrics(m) }

// Handler is the public API, behind the load balancer.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	h := s.h

	// sign-in
	mux.HandleFunc("GET /api/config", h.Config)
	mux.HandleFunc("GET /api/languages", h.ListLanguages)
	mux.HandleFunc("GET /api/languages/{code}", h.GetLanguage)
	mux.HandleFunc("GET /api/irc-messages", h.IRCCoverage)
	mux.HandleFunc("GET /api/irc-messages/{code}", h.IRCMessages)
	mux.HandleFunc("POST /auth/password", s.auth.PasswordLogin)
	mux.HandleFunc("GET /auth/login", s.auth.OIDCLogin)
	mux.HandleFunc("GET /auth/callback", s.auth.Callback)
	mux.HandleFunc("POST /auth/logout", s.auth.Logout)

	s.routes(mux)
	mux.HandleFunc("PUT /api/provision/users/{username}", middleware.ProvisionToken(h.ProvisionToken, h.ProvisionUser))
	if s.webApp != "" {
		mux.Handle("/api/", http.NotFoundHandler()) // not the app's index.html
		mux.Handle("/auth/", http.NotFoundHandler())
		mux.Handle("/", webApp(s.webApp))
	}
	return middleware.SameOrigin(middleware.Traced(false, mux))
}

// InternalHandler serves requests forwarded from other nodes. It must only be
// reachable between nodes.
func (s *Server) InternalHandler() http.Handler {
	mux := http.NewServeMux()
	s.routes(mux)
	mux.HandleFunc("GET /internal/usage", s.h.InternalUsage)
	return s.auth.Forwarded(s.secret, middleware.Traced(true, mux))
}

// routes registers the signed-in routes. Each is served on the node that
// owns the user it's about: the caller for /api/me and /api/networks, the
// {id} user for /api/admin/users/{id}.
func (s *Server) routes(mux *http.ServeMux) {
	h := s.h
	caller := func(r *http.Request) int64 { return auth.User(r).ID }
	user := func(f http.HandlerFunc) http.HandlerFunc { return s.auth.RequireUser(s.owner.At(caller, f)) }
	admin := func(f http.HandlerFunc) http.HandlerFunc { return s.auth.RequireAdmin(s.owner.At(handlers.PathID, f)) }
	adminAny := s.auth.RequireAdmin // about every user: served here

	// the signed-in user's own account
	mux.HandleFunc("GET /api/me", user(h.Me))
	mux.HandleFunc("PATCH /api/me", user(h.UpdateMe))
	mux.HandleFunc("DELETE /api/me", user(h.DeleteMe))
	mux.HandleFunc("POST /api/me/password", user(h.ChangePassword))
	mux.HandleFunc("GET /api/me/sessions", user(h.ListSessions))
	mux.HandleFunc("DELETE /api/me/sessions/{id}", user(h.RevokeSession))
	mux.HandleFunc("POST /api/me/sessions/revoke-others", user(h.RevokeOtherSessions))
	mux.HandleFunc("GET /api/me/client-passwords", user(h.ListClientPasswords))
	mux.HandleFunc("POST /api/me/client-passwords", user(h.CreateClientPassword))
	mux.HandleFunc("DELETE /api/me/client-passwords/{id}", user(h.DeleteClientPassword))
	mux.HandleFunc("GET /api/me/language-pack", user(h.MyLanguagePack))
	mux.HandleFunc("PUT /api/me/language-pack", user(h.PutMyLanguagePack))
	mux.HandleFunc("DELETE /api/me/language-pack", user(h.DeleteMyLanguagePack))

	// networks
	mux.HandleFunc("GET /api/networks", user(h.ListNetworks))
	mux.HandleFunc("POST /api/networks", user(h.CreateNetwork))
	mux.HandleFunc("GET /api/networks/{id}", user(h.GetNetwork))
	mux.HandleFunc("PATCH /api/networks/{id}", user(h.UpdateNetwork))
	mux.HandleFunc("GET /api/bindhosts", user(h.BindHosts))
	mux.HandleFunc("GET /api/modules", s.auth.RequireUser(h.Modules))
	mux.HandleFunc("GET /api/directory", s.auth.RequireUser(h.Directory))
	mux.HandleFunc("DELETE /api/networks/{id}", user(h.DeleteNetwork))
	mux.HandleFunc("POST /api/networks/{id}/connect", user(h.Connect))
	mux.HandleFunc("POST /api/networks/{id}/disconnect", user(h.Disconnect))
	mux.HandleFunc("GET /api/networks/{id}/messages", user(h.Messages))
	mux.HandleFunc("GET /api/networks/{id}/buffers", user(h.Buffers))
	mux.HandleFunc("GET /api/networks/{id}/log", user(h.NetworkLog))
	mux.HandleFunc("GET /api/networks/{id}/health", user(h.NetworkHealth))
	mux.HandleFunc("GET /api/networks/{id}/channel", user(h.Channel))
	mux.HandleFunc("POST /api/networks/{id}/read", user(h.MarkRead))
	mux.HandleFunc("GET /api/networks/{id}/playback", user(h.Playback))
	mux.HandleFunc("PATCH /api/networks/{id}/playback", user(h.SetPlayback))
	mux.HandleFunc("POST /api/networks/{id}/playback/clear", user(h.ClearPlayback))
	mux.HandleFunc("GET /api/me/clients", user(h.MyClients))
	mux.HandleFunc("GET /api/me/events", user(h.MyEvents))
	mux.HandleFunc("GET /api/overview", user(h.Overview))
	mux.HandleFunc("GET /api/reconnects", user(h.Reconnects))
	mux.HandleFunc("GET /api/networks/{id}/stream", user(h.Stream))

	// administration
	mux.HandleFunc("GET /api/admin/overview", adminAny(h.AdminOverview))
	mux.HandleFunc("GET /api/admin/metrics", adminAny(h.AdminMetrics))
	mux.HandleFunc("GET /api/admin/settings", adminAny(h.GetSettings))
	mux.HandleFunc("PATCH /api/admin/settings", adminAny(h.SaveSettings))
	mux.HandleFunc("POST /api/admin/directory/refresh", adminAny(h.RefreshDirectory))
	mux.HandleFunc("PUT /api/admin/languages/{code}", adminAny(h.PutLanguage))
	mux.HandleFunc("DELETE /api/admin/languages/{code}", adminAny(h.DeleteLanguage))
	mux.HandleFunc("GET /api/admin/users", adminAny(h.AdminListUsers))
	mux.HandleFunc("POST /api/admin/users", adminAny(h.AdminCreateUser))
	mux.HandleFunc("GET /api/admin/users/{id}", admin(h.AdminGetUser))
	mux.HandleFunc("GET /api/admin/users/{id}/overview", admin(h.AdminUserOverview))
	mux.HandleFunc("GET /api/admin/users/{id}/reconnects", admin(h.AdminUserReconnects))
	mux.HandleFunc("GET /api/admin/users/{id}/networks/{nid}/log", admin(h.AdminNetworkLog))
	mux.HandleFunc("GET /api/admin/users/{id}/networks/{nid}/health", admin(h.AdminNetworkHealth))
	mux.HandleFunc("PATCH /api/admin/users/{id}", admin(h.AdminUpdateUser))
	mux.HandleFunc("DELETE /api/admin/users/{id}", admin(h.AdminDeleteUser))
	mux.HandleFunc("POST /api/admin/users/{id}/password", admin(h.AdminSetPassword))
	mux.HandleFunc("POST /api/admin/users/{id}/sessions/revoke", admin(h.AdminRevokeSessions))
}

// ListenAndServe runs the API until ctx is cancelled.
func (s *Server) ListenAndServe(ctx context.Context, addr string) error {
	return s.serve(ctx, "api", addr, s.Handler(), nil)
}

// ListenAndServeTLS serves the API and web app over HTTPS (https_listen),
// with conf's certificates.
func (s *Server) ListenAndServeTLS(ctx context.Context, addr string, conf *tls.Config) error {
	return s.serve(ctx, "https", addr, middleware.HSTS(s.Handler()), conf)
}

// ListenAndServeHTTP serves h on a plain-HTTP port (http_listen: the
// redirect to HTTPS and ACME challenges).
func (s *Server) ListenAndServeHTTP(ctx context.Context, addr string, h http.Handler) error {
	return s.serve(ctx, "http (to https)", addr, h, nil)
}

// ListenInternal runs the internal endpoint until ctx is cancelled.
func (s *Server) ListenInternal(ctx context.Context, addr string) error {
	return s.serve(ctx, "internal api", addr, s.InternalHandler(), nil)
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
