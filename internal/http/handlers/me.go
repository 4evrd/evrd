package handlers

import (
	"context"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/4evrd/evrd/internal/accounts"
	"github.com/4evrd/evrd/internal/auth"
	"github.com/4evrd/evrd/internal/store"
)

// Languages are the web app's built-in languages (web/src/lib/i18n). An
// account's language may also be a server language pack's, or its own pack's.
var Languages = []string{"en", "it", "es", "tr", "de", "id", "ja"}

// userDTO is what a user sees about their own account (and what an admin
// sees, plus the overrides from adminUserDTO).
func (s *Handlers) userDTO(u store.User) map[string]any {
	nets, _ := s.acc.CountNetworks(context.Background(), u.ID)
	return map[string]any{
		"id": u.ID, "username": u.Username, "email": u.Email, "role": u.Role,
		"suspended": u.Suspended, "suspendedReason": u.SuspendedReason,
		"hasPassword": u.PasswordHash != "", "sso": u.OIDCSubject != nil,
		"defaultNick": u.DefaultNick, "defaultAltNick": u.DefaultAltNick, "defaultRealname": u.DefaultRealname,
		"autoAway": u.AutoAway, "mustChangePassword": u.MustChangePassword, "language": u.Language,
		"createdAt": u.CreatedAt.Unix(), "lastLoginAt": unix(u.LastLoginAt), "lastSeenAt": unix(u.LastSeenAt),
		"limits":      s.acc.LimitsFor(u),
		"usage":       map[string]any{"networks": nets, "clients": s.b.Clients(u.ID), "attached": s.b.Attached(u.ID)},
		"otherAdmins": s.otherAdmins(u),
		"settings":    settingsDTO(u),
		// languagePack is the user's own translation (code, name), or nil
		"languagePack": s.myPackDTO(u.ID),
	}
}

// otherAdmins lists the admins besides u (shown before an admin deletes
// their own account).
func (s *Handlers) otherAdmins(u store.User) []string {
	if !u.IsAdmin() {
		return []string{}
	}
	users, err := s.acc.ListUsers(context.Background())
	if err != nil {
		return []string{}
	}
	out := []string{}
	for _, o := range users {
		if o.IsAdmin() && !o.Suspended && o.ID != u.ID {
			out = append(out, o.Username)
		}
	}
	return out
}

func (s *Handlers) Me(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.userDTO(auth.User(r)))
}

// UpdateMe changes the user's own profile. The email of an SSO account
// comes from the provider, so it isn't editable here.
func (s *Handlers) UpdateMe(w http.ResponseWriter, r *http.Request) {
	u := auth.User(r)
	var in struct {
		Email           *string            `json:"email"`
		DefaultNick     *string            `json:"defaultNick"`
		DefaultRealname *string            `json:"defaultRealname"`
		DefaultAltNick  *string            `json:"defaultAltNick"`
		AutoAway        *bool              `json:"autoAway"`
		Language        *string            `json:"language"`
		Settings        *userSettingsInput `json:"settings"`
	}
	if !s.decode(w, r, &in) {
		return
	}
	before := u
	if in.Settings != nil {
		if err := in.Settings.apply(&u); err != nil {
			s.fail(w, r, err)
			return
		}
		if u.BindHost != before.BindHost {
			if err := s.acc.CheckBindHost(before, u.BindHost); err != nil {
				s.fail(w, r, err)
				return
			}
		}
		if ip := s.clientAddr(r); ip != "" && !accounts.HostAllowed(u, ip) {
			s.say(w, r, http.StatusUnprocessableEntity, "web.lockout", "address", ip)
			return
		}
	}
	if in.Email != nil && u.OIDCSubject == nil {
		e := strings.TrimSpace(*in.Email)
		if e != "" && !strings.Contains(e, "@") {
			s.say(w, r, http.StatusUnprocessableEntity, "web.email")
			return
		}
		u.Email = e
	}
	if in.DefaultNick != nil {
		nick := strings.TrimSpace(*in.DefaultNick)
		if strings.ContainsAny(nick, " ,*?!@#:") || utf8.RuneCountInString(nick) > 30 {
			s.say(w, r, http.StatusUnprocessableEntity, "web.nick")
			return
		}
		u.DefaultNick = nick
	}
	if in.Language != nil {
		l := strings.TrimSpace(*in.Language)
		if l != "" && !s.languageAllowed(u, l) {
			s.say(w, r, http.StatusUnprocessableEntity, "web.language")
			return
		}
		u.Language = l
	}
	if in.DefaultRealname != nil {
		u.DefaultRealname = strings.TrimSpace(*in.DefaultRealname)
	}
	if in.DefaultAltNick != nil {
		alt := strings.TrimSpace(*in.DefaultAltNick)
		if strings.ContainsAny(alt, " ,*?!@#:") || utf8.RuneCountInString(alt) > 30 {
			s.say(w, r, http.StatusUnprocessableEntity, "web.nick")
			return
		}
		u.DefaultAltNick = alt
	}
	awayChanged := in.AutoAway != nil && *in.AutoAway != u.AutoAway
	if in.AutoAway != nil {
		u.AutoAway = *in.AutoAway
	}
	if err := s.acc.UpdateUser(r.Context(), u); err != nil {
		s.fail(w, r, err)
		return
	}
	if awayChanged {
		s.b.RefreshAway(u.ID)
	}
	if in.Settings != nil {
		s.b.ReconfigureUser(u.ID)
	}
	writeJSON(w, http.StatusOK, s.userDTO(u))
}

func (s *Handlers) ChangePassword(w http.ResponseWriter, r *http.Request) {
	u := auth.User(r)
	if u.OIDCSubject != nil && u.PasswordHash == "" {
		s.say(w, r, http.StatusConflict, "web.ssoPassword")
		return
	}
	var in struct{ Current, Next string }
	if !s.decode(w, r, &in) {
		return
	}
	if err := s.acc.ChangePassword(&u, in.Current, in.Next); err != nil {
		s.fail(w, r, err)
		return
	}
	// Sign out everywhere else: whoever knew the old password is out.
	_ = s.auth.DeleteUserSessions(r.Context(), u.ID, auth.SessionOf(r).ID)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// DeleteMe deletes the caller's own account. The body must repeat the
// username, so a stray request can't do it.
func (s *Handlers) DeleteMe(w http.ResponseWriter, r *http.Request) {
	u := auth.User(r)
	var in struct{ Confirm string }
	if !s.decode(w, r, &in) {
		return
	}
	if in.Confirm != u.Username {
		s.say(w, r, http.StatusUnprocessableEntity, "web.confirmDelete")
		return
	}
	if err := s.acc.GuardAdminChange(u, false); err != nil {
		s.fail(w, r, err)
		return
	}
	s.deleteUser(u)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Handlers) deleteUser(u store.User) {
	if err := s.b.DeleteUser(u.ID); err != nil {
		s.log.Error("delete user", "user", u.Username, "err", err)
		return
	}
	s.log.Info("deleted user", "user", u.Username)
}

// --- sessions ------------------------------------------------------------

func (s *Handlers) sessionsDTO(userID int64, current string) []map[string]any {
	list, _ := s.auth.ListSessions(context.Background(), userID)
	out := make([]map[string]any, 0, len(list))
	for _, x := range list {
		out = append(out, map[string]any{
			"id": x.ID, "current": x.ID == current, "method": x.Method,
			"ip": x.IP, "userAgent": x.UserAgent,
			"createdAt": x.CreatedAt.Unix(), "lastSeenAt": x.LastSeenAt.Unix(), "expiresAt": x.ExpiresAt.Unix(),
		})
	}
	return out
}

func (s *Handlers) ListSessions(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.sessionsDTO(auth.User(r).ID, auth.SessionOf(r).ID))
}

func (s *Handlers) RevokeSession(w http.ResponseWriter, r *http.Request) {
	if err := s.auth.DeleteSession(r.Context(), auth.User(r).ID, r.PathValue("id")); err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Handlers) RevokeOtherSessions(w http.ResponseWriter, r *http.Request) {
	if err := s.auth.DeleteUserSessions(r.Context(), auth.User(r).ID, auth.SessionOf(r).ID); err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- IRC client passwords ------------------------------------------------

func (s *Handlers) ListClientPasswords(w http.ResponseWriter, r *http.Request) {
	list, err := s.acc.ListClientPasswords(r.Context(), auth.User(r).ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := make([]map[string]any, 0, len(list))
	for _, c := range list {
		out = append(out, map[string]any{
			"id": c.ID, "name": c.Name, "createdAt": c.CreatedAt.Unix(), "lastUsedAt": unix(c.LastUsedAt),
			// a device remembered when its sign-in was allowed by link
			"remembered": c.Remembered, "expiresAt": unix(c.ExpiresAt),
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// CreateClientPassword returns the new secret — the only time it's
// ever shown.
func (s *Handlers) CreateClientPassword(w http.ResponseWriter, r *http.Request) {
	var in struct{ Name string }
	if !s.decode(w, r, &in) {
		return
	}
	cp, secret, err := s.acc.NewClientPassword(auth.User(r).ID, in.Name)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id": cp.ID, "name": cp.Name, "createdAt": cp.CreatedAt.Unix(), "lastUsedAt": nil, "password": secret,
	})
}

func (s *Handlers) DeleteClientPassword(w http.ResponseWriter, r *http.Request) {
	if err := s.acc.DeleteClientPassword(r.Context(), auth.User(r).ID, PathID(r)); err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
