package handlers

// The managed API (managed mode, docs/managed.md): the managing service
// (Boson's auth service) keeps the accounts, each with the one network.
// Every call carries managed_token as a bearer token.
//
//	GET    /api/managed/health             managed mode's state
//	GET    /api/managed/users              every account
//	GET    /api/managed/users/{username}   one account (404: none)
//	PUT    /api/managed/users/{username}   create or change one (managed.UserInput)
//	DELETE /api/managed/users/{username}   remove one, with its history
//	GET    /api/managed/settings           the live settings (guests, the nick guard's grace)
//	PUT    /api/managed/settings           change some (managed.LiveInput)
//	DELETE /api/managed/settings           back to the config's
//	GET    /api/managed/channels/{channel}/members/{nick}
//	                                       a nick's standing in a channel

import (
	"errors"
	"net/http"
	"strings"

	"github.com/4evrd/evrd/internal/accounts"
	"github.com/4evrd/evrd/internal/managed"
	"github.com/4evrd/evrd/internal/store"
)

// SetManaged serves the managed API with p.
func (s *Handlers) SetManaged(p *managed.Provisioner) { s.prov = p }

// ManagedToken is managed_token, for the managed API's middleware.
func (s *Handlers) ManagedToken() string { return s.cfg().Managed.Token }

// managedFail answers the managed API's errors in plain words.
func (s *Handlers) managedFail(w http.ResponseWriter, r *http.Request, err error) {
	var ve *accounts.ValidationError
	switch {
	case errors.Is(err, managed.ErrBadInput):
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
	case errors.As(err, &ve):
		writeErr(w, http.StatusUnprocessableEntity, ve.Text.English())
	case errors.Is(err, managed.ErrSubjectTaken):
		writeErr(w, http.StatusConflict, err.Error())
	case errors.Is(err, store.ErrNotFound):
		writeErr(w, http.StatusNotFound, "no such account")
	default:
		s.log.Error("managed api", "path", r.URL.Path, "err", err)
		writeErr(w, http.StatusInternalServerError, "something went wrong")
	}
}

// ManagedHealth is GET /api/managed/health.
func (s *Handlers) ManagedHealth(w http.ResponseWriter, r *http.Request) {
	h, err := s.prov.Health(r.Context())
	if err != nil {
		s.managedFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, h)
}

// ManagedUsers is GET /api/managed/users.
func (s *Handlers) ManagedUsers(w http.ResponseWriter, r *http.Request) {
	us, err := s.prov.List(r.Context())
	if err != nil {
		s.managedFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": us})
}

// ManagedUser is GET /api/managed/users/{username}.
func (s *Handlers) ManagedUser(w http.ResponseWriter, r *http.Request) {
	u, err := s.prov.Get(r.Context(), r.PathValue("username"))
	if err != nil {
		s.managedFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, u)
}

// ManagedPutUser is PUT /api/managed/users/{username}: 201 when it made
// the account, else 200.
func (s *Handlers) ManagedPutUser(w http.ResponseWriter, r *http.Request) {
	var in managed.UserInput
	if !s.decode(w, r, &in) {
		return
	}
	u, err := s.prov.Put(r.Context(), r.PathValue("username"), in)
	if err != nil {
		s.managedFail(w, r, err)
		return
	}
	code := http.StatusOK
	if u.Created {
		code = http.StatusCreated
		s.log.Info("managed: created account", "user", u.Username)
	}
	writeJSON(w, code, u)
}

// ManagedDeleteUser is DELETE /api/managed/users/{username}.
func (s *Handlers) ManagedDeleteUser(w http.ResponseWriter, r *http.Request) {
	if err := s.prov.Delete(r.Context(), r.PathValue("username")); err != nil {
		s.managedFail(w, r, err)
		return
	}
	s.log.Info("managed: deleted account", "user", r.PathValue("username"))
	writeJSON(w, http.StatusOK, map[string]any{"username": r.PathValue("username"), "deleted": true})
}

// ManagedSettings is GET /api/managed/settings.
func (s *Handlers) ManagedSettings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.prov.Live())
}

// ManagedPutSettings is PUT /api/managed/settings: the fields it carries
// change, at once and on every node.
func (s *Handlers) ManagedPutSettings(w http.ResponseWriter, r *http.Request) {
	var in managed.LiveInput
	if !s.decode(w, r, &in) {
		return
	}
	l, err := s.prov.SetLive(r.Context(), in)
	if err != nil {
		s.managedFail(w, r, err)
		return
	}
	s.log.Info("managed: live settings changed", "guests", l.Guests, "guests_max", l.GuestsMax, "guests_per_ip", l.GuestsPerIP,
		"guest_linger", l.GuestLingerSeconds, "nick_guard_grace", l.NickGuardGraceSeconds)
	writeJSON(w, http.StatusOK, l)
}

// ManagedResetSettings is DELETE /api/managed/settings: the config's
// settings again.
func (s *Handlers) ManagedResetSettings(w http.ResponseWriter, r *http.Request) {
	l, err := s.prov.ResetLive(r.Context())
	if err != nil {
		s.managedFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, l)
}

// ManagedChannelMember is GET /api/managed/channels/{channel}/members/{nick}
// (the channel with its # escaped, %23boson): whether nick is in channel
// and its highest status there, as this node's connections see it. known
// is false when none of them is in the channel (then present is false
// too: nobody here can tell). Boson's voice server asks it for guests'
// channel membership on a bridged network.
func (s *Handlers) ManagedChannelMember(w http.ResponseWriter, r *http.Request) {
	channel, nick := strings.TrimSpace(r.PathValue("channel")), strings.TrimSpace(r.PathValue("nick"))
	if channel == "" || nick == "" || strings.ContainsAny(channel+nick, " ,\r\n\x00") || !strings.ContainsRune("#&+!", rune(channel[0])) {
		writeErr(w, http.StatusUnprocessableEntity, "need a channel (#name) and a nick")
		return
	}
	m := s.b.ChannelMember(channel, nick)
	out := map[string]any{"channel": channel, "nick": nick, "known": m.Joined, "present": m.Present, "prefix": m.Prefix, "mode": m.Mode}
	if m.Present {
		out["nick"] = m.Nick
	}
	writeJSON(w, http.StatusOK, out)
}
