// Package handlers serves evrd's HTTP + WebSocket API, consumed by the web
// app: sign-in, the signed-in user's own account, their networks and message
// history, a live per-network message stream, and user administration. The
// routes, and what guards them, are internal/http's.
//
// A user's own resources are always looked up through the session, never a
// request field, so one user can't reach another's. Handlers work through
// services (and the bouncer, for live state), never the repositories.
package handlers

import (
	"encoding/json"
	"errors"
	"github.com/4evrd/evrd/internal/mfa"
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
	"github.com/4evrd/evrd/internal/cluster"
	"github.com/4evrd/evrd/internal/config"
	"github.com/4evrd/evrd/internal/directory"
	"github.com/4evrd/evrd/internal/http/middleware"
	"github.com/4evrd/evrd/internal/languages"
	"github.com/4evrd/evrd/internal/messages"
	"github.com/4evrd/evrd/internal/metricsq"
	"github.com/4evrd/evrd/internal/settings"
	"github.com/4evrd/evrd/internal/store"
	"github.com/4evrd/evrd/internal/version"
)

// StatusRunner runs a *status command for the web chat (the IRC listener
// implements it, so both share one set of commands).
type StatusRunner interface {
	// lang is the language to answer in ("": the account's).
	WebStatus(userID int64, lang string, n store.Network, text string, out func(string))
}

// Handlers are the API's request handlers.
type Handlers struct {
	b       *bouncer.Bouncer
	status  StatusRunner                    // nil: *status isn't available in the web chat
	cl      Cluster                         // nil: a single node that serves every user
	secret  string                          // the cluster secret
	metrics atomic.Pointer[metricsq.Client] // nil: dashboards show no charts
	acc     accounts.AccountServiceImpl
	auth    auth.AuthServiceImpl
	langs   languages.LanguageServiceImpl
	msgs    messages.MessageServiceImpl
	state   cluster.StateServiceImpl
	mfa     mfa.MFAServiceImpl // nil: no second factors
	cfgp    atomic.Pointer[config.Config]
	// settings, when set, edits the server settings (Admin → Settings).
	settings *settings.Manager
	certs    *certs.Manager       // nil: no certificate report
	dir      *directory.Directory // nil: no network directory
	log      *slog.Logger
	up       websocket.Upgrader
}

// Services are what the API works through, besides the bouncer: handlers
// never reach the repositories themselves.
type Services struct {
	Accounts  accounts.AccountServiceImpl
	Auth      auth.AuthServiceImpl
	Languages languages.LanguageServiceImpl
	// Messages is evrd's IRC texts; nil: ones on Languages' packs.
	Messages messages.MessageServiceImpl
	Cluster  cluster.StateServiceImpl // nodes and leases (with or without a cluster)
	// MFA is users' second factors for IRC sign-ins; nil: none (tests).
	MFA mfa.MFAServiceImpl
}

// New makes the handlers on b and svc; status, when set, runs *status
// commands for the web chat.
func New(b *bouncer.Bouncer, svc Services, cfg *config.Config, status StatusRunner, log *slog.Logger) *Handlers {
	s := &Handlers{
		b: b, acc: svc.Accounts, auth: svc.Auth, langs: svc.Languages, msgs: svc.Messages, state: svc.Cluster, mfa: svc.MFA, status: status, log: log,
		up: websocket.Upgrader{CheckOrigin: middleware.CheckWebSocketOrigin},
	}
	if s.msgs == nil {
		s.msgs = messages.New(svc.Languages)
	}
	s.cfgp.Store(cfg)
	return s
}

// Config tells the sign-in page which methods exist and the app how
// clients attach. Public.
func (s *Handlers) Config(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"passwordLogin": s.cfg().Auth.PasswordLogin,
		"sso":           s.cfg().Auth.OIDCEnabled(),
		"ssoLabel":      s.cfg().Auth.OIDCLabel,
		// SSO accounts may use their provider password in IRC clients
		"ssoIRCPassword": s.cfg().Auth.SSOPasswordsOverIRC(),
		"ircHost":        s.cfg().IRCHost,
		"ircPorts":       ircPorts(s.cfg().Listen),
		"ircSelfSigned":  s.selfSigned(),
		// saved passwords and module settings are encrypted (secret_key set):
		// the forms mark those fields
		"secretsEncrypted": store.SecretsEncrypted(),
		"version":          version.Version,
	})
}

// selfSigned says whether IRC clients will see a self-signed certificate
// (and so need to be told to accept it).
func (s *Handlers) selfSigned() bool {
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
func (s *Handlers) decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(v); err != nil {
		s.say(w, r, http.StatusBadRequest, "web.badRequest")
		return false
	}
	return true
}

// fail maps an error to a response: validation problems are the caller's
// fault (422) and their message is shown; anything else is logged and hidden.
func (s *Handlers) fail(w http.ResponseWriter, r *http.Request, err error) {
	var ve *accounts.ValidationError
	p := s.printer(r)
	switch {
	case errors.As(err, &ve):
		writeErr(w, http.StatusUnprocessableEntity, p.Say(ve))
	case errors.Is(err, accounts.ErrUsernameTaken), errors.Is(err, accounts.ErrLastAdmin):
		writeErr(w, http.StatusConflict, p.Say(err))
	case errors.Is(err, accounts.ErrInvalidCredentials):
		s.say(w, r, http.StatusUnprocessableEntity, "web.wrongPassword")
	case errors.Is(err, store.ErrNotFound):
		s.say(w, r, http.StatusNotFound, "web.notFound")
	case errors.Is(err, bouncer.ErrSuspended):
		s.say(w, r, http.StatusForbidden, "web.suspended")
	case errors.Is(err, bouncer.ErrNotOwner):
		s.say(w, r, http.StatusServiceUnavailable, "web.moving")
	case errors.As(err, new(*messages.Error)):
		// something the user can fix, said in words (a wrong code…)
		writeErr(w, http.StatusUnprocessableEntity, p.Say(err))
	default:
		s.log.Error("api", "err", err)
		s.say(w, r, http.StatusInternalServerError, "web.failed")
	}
}

// LanguageHeader is the language the web app is showing, sent with each
// request so errors come back in it.
const LanguageHeader = "X-Evrd-Language"

// printer is the language to answer r in: the one the web app shows
// (LanguageHeader), else the account's, else English; with the signed-in
// user's own pack.
func (s *Handlers) printer(r *http.Request) *messages.Printer {
	u := auth.User(r)
	if l := requestLanguage(r); l != "" {
		u.Language = l
	}
	return s.msgs.For(r.Context(), u)
}

// requestLanguage is the language the web app is showing: LanguageHeader,
// or ?lang= on a WebSocket (which sends no headers of its own); "" for
// none.
func requestLanguage(r *http.Request) string {
	l := r.Header.Get(LanguageHeader)
	if l == "" {
		l = r.URL.Query().Get("lang")
	}
	if !packCode.MatchString(l) {
		return ""
	}
	return l
}

// say answers with an error: irc.<key> (internal/messages), with
// name/value pairs of arguments, in r's language.
func (s *Handlers) say(w http.ResponseWriter, r *http.Request, code int, key string, args ...any) {
	writeErr(w, code, s.printer(r).T(messages.Area+key, args...))
}

// decodeOptional reads a JSON body if there is one.
func decodeOptional(r *http.Request, v any) error {
	if r.ContentLength == 0 {
		return nil
	}
	return json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(v)
}

// PathID is the request's {id}.
func PathID(r *http.Request) int64 {
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
func (s *Handlers) cfg() *config.Config { return s.cfgp.Load() }

// ProvisionToken is the provisioning API's bearer token ("" when it's off).
func (s *Handlers) ProvisionToken() string { return s.cfg().ProvisionToken }

// SetConfig swaps in a changed config.
func (s *Handlers) SetConfig(c *config.Config) { s.cfgp.Store(c) }
