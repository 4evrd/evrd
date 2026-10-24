package handlers

// IRC sign-in by link (internal/ircsignin): the page a link opens shows the
// IRC client waiting and lets its owner allow it (remembering the device,
// if they like) or turn it away.

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/4evrd/evrd/internal/auth"
	"github.com/4evrd/evrd/internal/ircsignin"
)

// IRCSignIn is GET /api/irc-signins/{token}: the waiting client, and
// whether the signed-in user may allow it.
func (s *Handlers) IRCSignIn(w http.ResponseWriter, r *http.Request) {
	if s.signIns == nil {
		s.say(w, r, http.StatusNotFound, "web.notFound")
		return
	}
	p, err := s.signIns.Lookup(r.Context(), r.PathValue("token"))
	if err != nil {
		s.signInFailed(w, r, err)
		return
	}
	u := auth.User(r)
	yours := (p.UserID == 0 || p.UserID == u.ID) && (p.Username == "" || strings.EqualFold(p.Username, u.Username))
	writeJSON(w, http.StatusOK, map[string]any{
		"state": p.State, "username": p.Username, "network": p.Network, "nick": p.Nick, "ip": p.IP, "software": p.Software,
		"createdAt": p.CreatedAt.Unix(), "expiresAt": p.ExpiresAt.Unix(),
		// may this user allow it (it named someone else, or is a second
		// factor for another account: no)
		"yours": yours,
		// a second factor for a client that already gave its password
		"secondFactor": p.UserID != 0,
		// the client sent a secret long enough to become its password
		"canRememberSecret": p.SecretHash != "",
		"rememberDays":      s.cfg().Auth.IRCRememberDays,
		// the client isn't where this browser is: worth a second look
		"sameAddress": p.IP != "" && p.IP == s.clientAddr(r),
	})
}

// AllowIRCSignIn is POST /api/irc-signins/{token}/allow {remember}:
// remember is "", "password" (a new client password, in the answer, shown
// once) or "secret" (the one the client sent).
func (s *Handlers) AllowIRCSignIn(w http.ResponseWriter, r *http.Request) {
	if s.signIns == nil {
		s.say(w, r, http.StatusNotFound, "web.notFound")
		return
	}
	var in struct {
		Remember string `json:"remember"`
	}
	if err := decodeOptional(r, &in); err != nil {
		s.say(w, r, http.StatusBadRequest, "web.badRequest")
		return
	}
	switch in.Remember {
	case ircsignin.RememberNone, ircsignin.RememberPassword, ircsignin.RememberSecret:
	default:
		s.say(w, r, http.StatusBadRequest, "web.badRequest")
		return
	}
	ttl := time.Duration(max(s.cfg().Auth.IRCRememberDays, 1)) * 24 * time.Hour
	secret, err := s.signIns.Allow(r.Context(), r.PathValue("token"), auth.User(r), in.Remember, ttl)
	if err != nil {
		s.signInFailed(w, r, err)
		return
	}
	s.log.Info("irc sign-in allowed", "user", auth.User(r).Username, "remember", in.Remember)
	out := map[string]any{"ok": true}
	if secret != "" {
		out["clientPassword"] = secret
	}
	writeJSON(w, http.StatusOK, out)
}

// DenyIRCSignIn is POST /api/irc-signins/{token}/deny.
func (s *Handlers) DenyIRCSignIn(w http.ResponseWriter, r *http.Request) {
	if s.signIns == nil {
		s.say(w, r, http.StatusNotFound, "web.notFound")
		return
	}
	if err := s.signIns.Deny(r.Context(), r.PathValue("token"), auth.User(r)); err != nil {
		s.signInFailed(w, r, err)
		return
	}
	s.log.Info("irc sign-in turned away", "user", auth.User(r).Username)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// signInFailed answers a refused link: gone (404), not this user's (403),
// or another error.
func (s *Handlers) signInFailed(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ircsignin.ErrGone):
		writeErr(w, http.StatusNotFound, s.printer(r).Say(err))
	case errors.Is(err, ircsignin.ErrNotYours), errors.Is(err, ircsignin.ErrForbidden):
		writeErr(w, http.StatusForbidden, s.printer(r).Say(err))
	default:
		s.fail(w, r, err)
	}
}
