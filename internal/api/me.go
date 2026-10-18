package api

import (
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/4evrd/evrd/internal/auth"
	"github.com/4evrd/evrd/internal/store"
)

// userDTO is what a user sees about their own account (and what an admin
// sees, plus the overrides from adminUserDTO).
func (s *Server) userDTO(u store.User) map[string]any {
	nets, _ := s.st().CountNetworks(u.ID)
	return map[string]any{
		"id": u.ID, "username": u.Username, "email": u.Email, "role": u.Role,
		"suspended": u.Suspended, "suspendedReason": u.SuspendedReason,
		"hasPassword": u.PasswordHash != "", "sso": u.OIDCSubject != nil,
		"defaultNick": u.DefaultNick, "defaultAltNick": u.DefaultAltNick, "defaultRealname": u.DefaultRealname,
		"autoAway": u.AutoAway, "mustChangePassword": u.MustChangePassword,
		"createdAt": u.CreatedAt.Unix(), "lastLoginAt": unix(u.LastLoginAt), "lastSeenAt": unix(u.LastSeenAt),
		"limits":      s.acc.LimitsFor(u),
		"usage":       map[string]any{"networks": nets, "clients": s.b.Clients(u.ID), "attached": s.b.Attached(u.ID)},
		"otherAdmins": s.otherAdmins(u),
	}
}

// otherAdmins lists the admins besides u (shown before an admin deletes
// their own account).
func (s *Server) otherAdmins(u store.User) []string {
	if !u.IsAdmin() {
		return []string{}
	}
	users, err := s.st().ListUsers()
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

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.userDTO(auth.User(r)))
}

// handleUpdateMe changes the user's own profile. The email of an SSO account
// comes from the provider, so it isn't editable here.
func (s *Server) handleUpdateMe(w http.ResponseWriter, r *http.Request) {
	u := auth.User(r)
	var in struct {
		Email           *string `json:"email"`
		DefaultNick     *string `json:"defaultNick"`
		DefaultRealname *string `json:"defaultRealname"`
		DefaultAltNick  *string `json:"defaultAltNick"`
		AutoAway        *bool   `json:"autoAway"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.Email != nil && u.OIDCSubject == nil {
		e := strings.TrimSpace(*in.Email)
		if e != "" && !strings.Contains(e, "@") {
			writeErr(w, http.StatusUnprocessableEntity, "that doesn't look like an email address")
			return
		}
		u.Email = e
	}
	if in.DefaultNick != nil {
		nick := strings.TrimSpace(*in.DefaultNick)
		if strings.ContainsAny(nick, " ,*?!@#:") || utf8.RuneCountInString(nick) > 30 {
			writeErr(w, http.StatusUnprocessableEntity, "nicks can't contain spaces or , * ? ! @ # : and are at most 30 characters")
			return
		}
		u.DefaultNick = nick
	}
	if in.DefaultRealname != nil {
		u.DefaultRealname = strings.TrimSpace(*in.DefaultRealname)
	}
	if in.DefaultAltNick != nil {
		alt := strings.TrimSpace(*in.DefaultAltNick)
		if strings.ContainsAny(alt, " ,*?!@#:") || utf8.RuneCountInString(alt) > 30 {
			writeErr(w, http.StatusUnprocessableEntity, "nicks can't contain spaces or , * ? ! @ # : and are at most 30 characters")
			return
		}
		u.DefaultAltNick = alt
	}
	awayChanged := in.AutoAway != nil && *in.AutoAway != u.AutoAway
	if in.AutoAway != nil {
		u.AutoAway = *in.AutoAway
	}
	if err := s.st().UpdateUser(u); err != nil {
		s.fail(w, err)
		return
	}
	if awayChanged {
		s.b.RefreshAway(u.ID)
	}
	writeJSON(w, http.StatusOK, s.userDTO(u))
}

func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	u := auth.User(r)
	if u.OIDCSubject != nil && u.PasswordHash == "" {
		writeErr(w, http.StatusConflict, "your password is managed by your sign-in provider")
		return
	}
	var in struct{ Current, Next string }
	if !decode(w, r, &in) {
		return
	}
	if err := s.acc.ChangePassword(&u, in.Current, in.Next); err != nil {
		s.fail(w, err)
		return
	}
	// Sign out everywhere else: whoever knew the old password is out.
	_ = s.st().DeleteUserSessions(u.ID, auth.SessionOf(r).ID)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleDeleteMe deletes the caller's own account. The body must repeat the
// username, so a stray request can't do it.
func (s *Server) handleDeleteMe(w http.ResponseWriter, r *http.Request) {
	u := auth.User(r)
	var in struct{ Confirm string }
	if !decode(w, r, &in) {
		return
	}
	if in.Confirm != u.Username {
		writeErr(w, http.StatusUnprocessableEntity, "type your username to confirm")
		return
	}
	if err := s.acc.GuardAdminChange(u, false); err != nil {
		s.fail(w, err)
		return
	}
	s.deleteUser(u)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) deleteUser(u store.User) {
	if err := s.b.DeleteUser(u.ID); err != nil {
		s.log.Error("delete user", "user", u.Username, "err", err)
		return
	}
	s.log.Info("deleted user", "user", u.Username)
}

// --- sessions ------------------------------------------------------------

func (s *Server) sessionsDTO(userID int64, current string) []map[string]any {
	list, _ := s.st().ListSessions(userID)
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

func (s *Server) handleListSessions(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.sessionsDTO(auth.User(r).ID, auth.SessionOf(r).ID))
}

func (s *Server) handleRevokeSession(w http.ResponseWriter, r *http.Request) {
	if err := s.st().DeleteSession(auth.User(r).ID, r.PathValue("id")); err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleRevokeOtherSessions(w http.ResponseWriter, r *http.Request) {
	if err := s.st().DeleteUserSessions(auth.User(r).ID, auth.SessionOf(r).ID); err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- IRC client passwords ------------------------------------------------

func (s *Server) handleListClientPasswords(w http.ResponseWriter, r *http.Request) {
	list, err := s.st().ListClientPasswords(auth.User(r).ID)
	if err != nil {
		s.fail(w, err)
		return
	}
	out := make([]map[string]any, 0, len(list))
	for _, c := range list {
		out = append(out, map[string]any{
			"id": c.ID, "name": c.Name, "createdAt": c.CreatedAt.Unix(), "lastUsedAt": unix(c.LastUsedAt),
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// handleCreateClientPassword returns the new secret — the only time it's
// ever shown.
func (s *Server) handleCreateClientPassword(w http.ResponseWriter, r *http.Request) {
	var in struct{ Name string }
	if !decode(w, r, &in) {
		return
	}
	cp, secret, err := s.acc.NewClientPassword(auth.User(r).ID, in.Name)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id": cp.ID, "name": cp.Name, "createdAt": cp.CreatedAt.Unix(), "lastUsedAt": nil, "password": secret,
	})
}

func (s *Server) handleDeleteClientPassword(w http.ResponseWriter, r *http.Request) {
	if err := s.st().DeleteClientPassword(auth.User(r).ID, pathID(r)); err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
