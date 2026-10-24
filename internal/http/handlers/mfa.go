package handlers

// A user's second factors for IRC sign-ins (Account → Security): the
// authenticator app, its recovery codes, and their "require a second factor
// for IRC" toggle as the admin's irc_mfa allows it.

import (
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/4evrd/evrd/internal/auth"
	"github.com/4evrd/evrd/internal/mfa"
	"github.com/4evrd/evrd/internal/store"
)

// mfaDTO is the user's second factors and what the policy lets them do.
func (s *Handlers) mfaDTO(r *http.Request, u store.User) (map[string]any, error) {
	st, err := s.mfa.Status(r.Context(), u.ID)
	if err != nil {
		return nil, err
	}
	p := mfa.PolicyOf(s.cfg().Auth)
	return map[string]any{
		"totp": st.TOTP, "recoveryCodes": st.RecoveryCodes,
		// the user's toggle: hidden (irc_mfa off), locked on (required) or theirs
		"choice": p.UserChoice(), "ircRequireMFA": u.IRCRequireMFA,
		// whether an IRC sign-in with a password needs a code now
		"required":        p.Needed(u.IRCRequireMFA, false),
		"clientPasswords": p.ClientPasswords,
	}, nil
}

// MFA is GET /api/me/mfa.
func (s *Handlers) MFA(w http.ResponseWriter, r *http.Request) {
	if !s.mfaOn(w, r) {
		return
	}
	s.writeMFA(w, r, auth.User(r))
}

// writeMFA answers with u's second factors.
func (s *Handlers) writeMFA(w http.ResponseWriter, r *http.Request, u store.User) {
	dto, err := s.mfaDTO(r, u)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, dto)
}

func (s *Handlers) mfaOn(w http.ResponseWriter, r *http.Request) bool {
	if s.mfa == nil {
		s.say(w, r, http.StatusNotFound, "web.notFound")
		return false
	}
	return true
}

// UpdateMFA is PATCH /api/me/mfa: the user's "require a second factor for
// IRC" toggle. It can't be changed while the admin requires it or has it
// off, and can't be turned on without an authenticator app.
func (s *Handlers) UpdateMFA(w http.ResponseWriter, r *http.Request) {
	if !s.mfaOn(w, r) {
		return
	}
	var in struct {
		IRCRequireMFA *bool `json:"ircRequireMFA"`
	}
	if !s.decode(w, r, &in) || in.IRCRequireMFA == nil {
		if in.IRCRequireMFA == nil {
			s.say(w, r, http.StatusBadRequest, "web.badRequest")
		}
		return
	}
	u := auth.User(r)
	switch mfa.PolicyOf(s.cfg().Auth).UserChoice() {
	case "hidden":
		s.say(w, r, http.StatusConflict, "mfa.policyOff")
		return
	case "locked":
		s.say(w, r, http.StatusConflict, "mfa.policyRequired")
		return
	}
	if *in.IRCRequireMFA {
		st, err := s.mfa.Status(r.Context(), u.ID)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		if !st.TOTP {
			s.fail(w, r, mfa.ErrNeedsFactor)
			return
		}
	}
	u.IRCRequireMFA = *in.IRCRequireMFA
	if err := s.acc.UpdateUser(r.Context(), u); err != nil {
		s.fail(w, r, err)
		return
	}
	s.writeMFA(w, r, u)
}

// StartTOTP is POST /api/me/mfa/totp: a new secret to add to an
// authenticator app (confirmed with ConfirmTOTP).
func (s *Handlers) StartTOTP(w http.ResponseWriter, r *http.Request) {
	if !s.mfaOn(w, r) {
		return
	}
	e, err := s.mfa.StartTOTP(r.Context(), auth.User(r), s.totpIssuer())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, e)
}

// totpIssuer names this server in authenticator apps: the public URL's
// host, or the IRC host.
func (s *Handlers) totpIssuer() string {
	if u, err := url.Parse(s.cfg().PublicURL); err == nil && u.Hostname() != "" {
		return u.Hostname()
	}
	if h := s.cfg().IRCHost; h != "" && h != "localhost" {
		return h
	}
	return "evrd"
}

// ConfirmTOTP is POST /api/me/mfa/totp/confirm {code}: the first code from
// the app switches it on; the answer has the recovery codes (shown once).
func (s *Handlers) ConfirmTOTP(w http.ResponseWriter, r *http.Request) {
	if !s.mfaOn(w, r) {
		return
	}
	code, ok := s.mfaCode(w, r)
	if !ok {
		return
	}
	codes, err := s.mfa.ConfirmTOTP(r.Context(), auth.User(r).ID, code)
	if err != nil {
		s.mfaFailed(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"recoveryCodes": codes})
}

// DisableTOTP is POST /api/me/mfa/totp/remove {code}: with a code from the
// app (or a recovery code), it and the recovery codes go, and so does the
// user's toggle (it would leave them unable to sign in from IRC).
func (s *Handlers) DisableTOTP(w http.ResponseWriter, r *http.Request) {
	if !s.mfaOn(w, r) {
		return
	}
	u := auth.User(r)
	if !s.verifyCode(w, r, u) {
		return
	}
	if err := s.mfa.DisableTOTP(r.Context(), u.ID); err != nil {
		s.fail(w, r, err)
		return
	}
	if u.IRCRequireMFA {
		u.IRCRequireMFA = false
		if err := s.acc.UpdateUser(r.Context(), u); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	s.writeMFA(w, r, u)
}

// NewRecoveryCodes is POST /api/me/mfa/recovery {code}: new recovery codes,
// the old ones no longer working.
func (s *Handlers) NewRecoveryCodes(w http.ResponseWriter, r *http.Request) {
	if !s.mfaOn(w, r) {
		return
	}
	u := auth.User(r)
	if !s.verifyCode(w, r, u) {
		return
	}
	codes, err := s.mfa.NewRecoveryCodes(r.Context(), u.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"recoveryCodes": codes})
}

// mfaCode reads {code} from the request.
func (s *Handlers) mfaCode(w http.ResponseWriter, r *http.Request) (string, bool) {
	var in struct {
		Code string `json:"code"`
	}
	if !s.decode(w, r, &in) {
		return "", false
	}
	if strings.TrimSpace(in.Code) == "" {
		s.fail(w, r, mfa.ErrWrongCode)
		return "", false
	}
	return strings.TrimSpace(in.Code), true
}

// verifyCode checks the request's code for u, counting wrong ones toward
// the sign-in lockout (so a stolen session can't guess them).
func (s *Handlers) verifyCode(w http.ResponseWriter, r *http.Request, u store.User) bool {
	key := strings.ToLower(u.Username)
	if f := s.auth.Failures(); f != nil && f.Blocked(key) {
		s.say(w, r, http.StatusTooManyRequests, "web.tooManyAttempts")
		return false
	}
	code, ok := s.mfaCode(w, r)
	if !ok {
		return false
	}
	if err := s.mfa.Verify(r.Context(), u.ID, code); err != nil {
		s.mfaFailed(w, r, err)
		return false
	}
	return true
}

// mfaFailed answers a refused code (422, counted toward the lockout) or
// another error.
func (s *Handlers) mfaFailed(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, mfa.ErrWrongCode) {
		if f := s.auth.Failures(); f != nil {
			f.Add(strings.ToLower(auth.User(r).Username))
		}
	}
	s.fail(w, r, err)
}
