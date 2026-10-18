package api

import (
	"net/http"
	"strings"

	"code.ircforever.org/ircforever/evrd/internal/accounts"
	"code.ircforever.org/ircforever/evrd/internal/auth"
	"code.ircforever.org/ircforever/evrd/internal/store"
)

func (s *Server) handleAdminOverview(w http.ResponseWriter, r *http.Request) {
	users, err := s.st().ListUsers()
	if err != nil {
		s.fail(w, err)
		return
	}
	counts, err := s.st().NetworkCounts()
	if err != nil {
		s.fail(w, err)
		return
	}
	var admins, suspended, sso int
	for _, u := range users {
		if u.IsAdmin() {
			admins++
		}
		if u.Suspended {
			suspended++
		}
		if u.OIDCSubject != nil {
			sso++
		}
	}
	var nets int64
	for _, n := range counts {
		nets += n
	}
	live := s.clusterUsage(r.Context())
	nodes := 1
	if s.cl != nil {
		nodes = len(s.cl.Nodes())
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"users": len(users), "admins": admins, "suspended": suspended, "sso": sso,
		"networks": nets, "connectedNetworks": live.Connected, "attachedClients": live.Attached,
		"defaults": s.acc.Defaults(), "nodes": nodes,
	})
}

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
	users, err := s.st().ListUsers()
	if err != nil {
		s.fail(w, err)
		return
	}
	counts, _ := s.st().NetworkCounts()
	clients := s.clusterUsage(r.Context()).Clients
	out := make([]map[string]any, 0, len(users))
	for _, u := range users {
		out = append(out, map[string]any{
			"id": u.ID, "username": u.Username, "email": u.Email, "role": u.Role,
			"suspended": u.Suspended, "sso": u.OIDCSubject != nil,
			"createdAt": u.CreatedAt.Unix(), "lastLoginAt": unix(u.LastLoginAt),
			"networks": counts[u.ID], "clients": clients[u.ID],
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleAdminCreateUser(w http.ResponseWriter, r *http.Request) {
	var in struct{ Username, Email, Password, Role string }
	if !decode(w, r, &in) {
		return
	}
	u, err := s.acc.Create(accounts.CreateInput{Username: in.Username, Email: in.Email, Password: in.Password, Role: in.Role})
	if err != nil {
		s.fail(w, err)
		return
	}
	s.log.Info("admin created user", "admin", auth.User(r).Username, "user", u.Username)
	writeJSON(w, http.StatusOK, s.adminUserDTO(u))
}

// adminTarget loads the {id} user, answering 404 itself.
func (s *Server) adminTarget(w http.ResponseWriter, r *http.Request) (store.User, bool) {
	u, err := s.st().UserByID(pathID(r))
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
	nets, _ := s.st().ListNetworks(u.ID)
	netsOut := make([]map[string]any, 0, len(nets))
	for _, n := range nets {
		netsOut = append(netsOut, s.networkDTO(n))
	}
	d := s.adminUserDTO(u)
	d["networks"] = netsOut
	d["sessions"] = s.sessionsDTO(u.ID, "")
	cps, _ := s.st().ListClientPasswords(u.ID)
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
	if err := s.st().UpdateUser(u); err != nil {
		s.fail(w, err)
		return
	}
	switch {
	case u.Suspended && !before.Suspended:
		// Cut them off everywhere: web sessions, IRC clients, upstreams.
		_ = s.st().DeleteUserSessions(u.ID, "")
		s.b.StopUser(u.ID)
		s.log.Info("suspended user", "admin", me.Username, "user", u.Username)
	case !u.Suspended && before.Suspended:
		s.b.ResumeUser(u.ID)
		s.log.Info("unsuspended user", "admin", me.Username, "user", u.Username)
	}
	writeJSON(w, http.StatusOK, s.adminUserDTO(u))
}

func (s *Server) handleAdminSetPassword(w http.ResponseWriter, r *http.Request) {
	u, ok := s.adminTarget(w, r)
	if !ok {
		return
	}
	var in struct{ Password string }
	if !decode(w, r, &in) {
		return
	}
	if err := s.acc.SetPassword(&u, in.Password); err != nil {
		s.fail(w, err)
		return
	}
	keep := ""
	if u.ID == auth.User(r).ID {
		keep = auth.SessionOf(r).ID
	}
	_ = s.st().DeleteUserSessions(u.ID, keep)
	s.log.Info("admin set password", "admin", auth.User(r).Username, "user", u.Username)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
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
	if err := s.st().DeleteUserSessions(u.ID, keep); err != nil {
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
