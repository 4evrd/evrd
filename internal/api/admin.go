package api

import (
	"net/http"
	"strings"

	"github.com/4evrd/evrd/internal/accounts"
	"github.com/4evrd/evrd/internal/auth"
	"github.com/4evrd/evrd/internal/store"
)

// adminUserDTO adds what only an admin sees: the raw per-user overrides
// (null = server default).
func (s *Server) adminUserDTO(u store.User) map[string]any {
	d := s.userDTO(u)
	d["overrides"] = map[string]*int{
		"maxNetworks": u.MaxNetworks, "maxClients": u.MaxClients, "backlogLines": u.BacklogLines,
	}
	return d
}

func (s *Server) handleAdminListUsers(w http.ResponseWriter, r *http.Request) {
	users, err := s.acc.ListUsers(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	counts, _ := s.acc.NetworkCounts(r.Context())
	live := s.clusterUsage(r.Context())
	out := make([]map[string]any, 0, len(users))
	for _, u := range users {
		lim := s.acc.LimitsFor(u)
		status := "offline"
		switch {
		case u.Suspended:
			status = "suspended"
		case live.Online[u.ID] > 0:
			status = "online"
		}
		out = append(out, map[string]any{
			"id": u.ID, "username": u.Username, "email": u.Email, "role": u.Role,
			"suspended": u.Suspended, "sso": u.OIDCSubject != nil,
			"createdAt": u.CreatedAt.Unix(), "lastLoginAt": unix(u.LastLoginAt), "lastSeenAt": unix(u.LastSeenAt),
			"networks": counts[u.ID], "clients": live.Clients[u.ID], "online": live.Online[u.ID],
			"status": status, "limits": lim,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleAdminCreateUser(w http.ResponseWriter, r *http.Request) {
	var in struct{ Username, Email, Password, Role string }
	if !decode(w, r, &in) {
		return
	}
	// An admin-chosen password is temporary: the user changes it at first
	// sign-in.
	u, err := s.acc.Create(accounts.CreateInput{Username: in.Username, Email: in.Email, Password: in.Password, Role: in.Role, Temporary: in.Password != ""})
	if err != nil {
		s.fail(w, err)
		return
	}
	s.log.Info("admin created user", "admin", auth.User(r).Username, "user", u.Username)
	writeJSON(w, http.StatusOK, s.adminUserDTO(u))
}

// adminTarget loads the {id} user, answering 404 itself.
func (s *Server) adminTarget(w http.ResponseWriter, r *http.Request) (store.User, bool) {
	u, err := s.acc.UserByID(r.Context(), pathID(r))
	if err != nil {
		s.fail(w, err)
		return store.User{}, false
	}
	return u, true
}

func (s *Server) handleAdminGetUser(w http.ResponseWriter, r *http.Request) {
	u, ok := s.adminTarget(w, r)
	if !ok {
		return
	}
	nets, _ := s.acc.ListNetworks(r.Context(), u.ID)
	netsOut := make([]map[string]any, 0, len(nets))
	for _, n := range nets {
		netsOut = append(netsOut, s.networkDTO(n))
	}
	d := s.adminUserDTO(u)
	d["networks"] = netsOut
	d["sessions"] = s.sessionsDTO(u.ID, "")
	cps, _ := s.acc.ListClientPasswords(r.Context(), u.ID)
	d["clientPasswords"] = len(cps)
	writeJSON(w, http.StatusOK, d)
}

// handleAdminUpdateUser changes role, suspension, email and limits. Fields
// left out are unchanged; "limits", when present, replaces all three
// overrides (null = server default, 0 = unlimited).
func (s *Server) handleAdminUpdateUser(w http.ResponseWriter, r *http.Request) {
	u, ok := s.adminTarget(w, r)
	if !ok {
		return
	}
	me := auth.User(r)
	var in struct {
		Role            *string `json:"role"`
		Suspended       *bool   `json:"suspended"`
		SuspendedReason *string `json:"suspendedReason"`
		Email           *string `json:"email"`
		Limits          *struct {
			MaxNetworks  *int `json:"maxNetworks"`
			MaxClients   *int `json:"maxClients"`
			BacklogLines *int `json:"backlogLines"`
		} `json:"limits"`
		// Bouncer settings, as the user edits them; admins may use any bind
		// host and lock it (denySetBindHost).
		Settings        *userSettingsInput `json:"settings"`
		DenySetBindHost *bool              `json:"denySetBindHost"`
	}
	if !decode(w, r, &in) {
		return
	}
	before := u
	if in.Role != nil {
		if *in.Role != store.RoleUser && *in.Role != store.RoleAdmin {
			writeErr(w, http.StatusUnprocessableEntity, "role must be user or admin")
			return
		}
		u.Role = *in.Role
	}
	if in.Suspended != nil {
		if *in.Suspended && u.ID == me.ID {
			writeErr(w, http.StatusUnprocessableEntity, "you can't suspend yourself")
			return
		}
		u.Suspended = *in.Suspended
		if !u.Suspended {
			u.SuspendedReason = ""
		}
	}
	if in.SuspendedReason != nil && u.Suspended {
		u.SuspendedReason = strings.TrimSpace(*in.SuspendedReason)
	}
	if in.Email != nil {
		u.Email = strings.TrimSpace(*in.Email)
	}
	if l := in.Limits; l != nil {
		for _, v := range []*int{l.MaxNetworks, l.MaxClients, l.BacklogLines} {
			if v != nil && *v < 0 {
				writeErr(w, http.StatusUnprocessableEntity, "limits can't be negative (0 means unlimited)")
				return
			}
		}
		u.MaxNetworks, u.MaxClients, u.BacklogLines = l.MaxNetworks, l.MaxClients, l.BacklogLines
	}
	if err := s.acc.GuardAdminChange(before, u.IsAdmin() && !u.Suspended); err != nil {
		s.fail(w, err)
		return
	}
	if in.Settings != nil {
		if err := in.Settings.apply(&u); err != nil {
			s.fail(w, err)
			return
		}
	}
	if in.DenySetBindHost != nil {
		u.DenySetBindHost = *in.DenySetBindHost
	}
	if err := s.acc.UpdateUser(r.Context(), u); err != nil {
		s.fail(w, err)
		return
	}
	switch {
	case u.Suspended && !before.Suspended:
		// Cut them off everywhere: web sessions, IRC clients, upstreams.
		_ = s.auth.DeleteUserSessions(r.Context(), u.ID, "")
		s.b.StopUser(u.ID)
		s.log.Info("suspended user", "admin", me.Username, "user", u.Username)
	case !u.Suspended && before.Suspended:
		s.b.ResumeUser(u.ID)
		s.log.Info("unsuspended user", "admin", me.Username, "user", u.Username)
	}
	if in.Settings != nil {
		s.b.ReconfigureUser(u.ID)
	}
	writeJSON(w, http.StatusOK, s.adminUserDTO(u))
}

func (s *Server) handleAdminSetPassword(w http.ResponseWriter, r *http.Request) {
	u, ok := s.adminTarget(w, r)
	if !ok {
		return
	}
	// With no password given, a random one is generated and returned once.
	// Either way it's temporary: the user must change it at next sign-in.
	var in struct{ Password string }
	_ = decodeOptional(r, &in)
	pw, err := s.acc.SetTemporaryPassword(&u, in.Password)
	if err != nil {
		s.fail(w, err)
		return
	}
	keep := ""
	if u.ID == auth.User(r).ID {
		keep = auth.SessionOf(r).ID
	}
	_ = s.auth.DeleteUserSessions(r.Context(), u.ID, keep)
	s.log.Info("admin set a temporary password", "admin", auth.User(r).Username, "user", u.Username)
	out := map[string]any{"ok": true}
	if in.Password == "" {
		out["password"] = pw
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleAdminRevokeSessions(w http.ResponseWriter, r *http.Request) {
	u, ok := s.adminTarget(w, r)
	if !ok {
		return
	}
	keep := ""
	if u.ID == auth.User(r).ID {
		keep = auth.SessionOf(r).ID
	}
	if err := s.auth.DeleteUserSessions(r.Context(), u.ID, keep); err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleAdminDeleteUser(w http.ResponseWriter, r *http.Request) {
	u, ok := s.adminTarget(w, r)
	if !ok {
		return
	}
	if u.ID == auth.User(r).ID {
		writeErr(w, http.StatusUnprocessableEntity, "delete your own account from the Account page")
		return
	}
	if err := s.acc.GuardAdminChange(u, false); err != nil {
		s.fail(w, err)
		return
	}
	s.deleteUser(u)
	w.WriteHeader(http.StatusNoContent)
}
