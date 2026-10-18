package api

import (
	"crypto/subtle"
	"errors"
	"net/http"
	"strings"

	"github.com/4evrd/evrd/internal/accounts"
	"github.com/4evrd/evrd/internal/bouncer"
	"github.com/4evrd/evrd/internal/store"
)

// The provisioning API lets a trusted tool (managebot, moving users over from
// ZNC) create an account and its networks before the owner ever signs in. It
// is off unless provision_token is set, and every call carries that token:
//
//	PUT /api/provision/users/{username}
//	Authorization: Bearer <provision_token>
//	{"email": "…", "connect": true, "networks": [{…as for POST /api/networks}]}
//
// A missing account is created SSO-only and claimable: the owner's first SSO
// sign-in with the same username and email takes it over. Networks whose
// name the account already has are left as they are (so a call can be
// repeated); "connect" connects every listed network, new or not — so a
// caller can create first, take the old bouncer off IRC, then connect.

type provisionInput struct {
	Email    string         `json:"email"`
	Connect  bool           `json:"connect"`
	Networks []networkInput `json:"networks"`
}

type provisionedNetwork struct {
	Name   string `json:"name"`
	ID     int64  `json:"id,omitempty"`
	Status string `json:"status"` // created | exists | failed
	Error  string `json:"error,omitempty"`
}

func (s *Server) provisionAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		want := s.cfg().ProvisionToken
		got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if want == "" {
			writeErr(w, http.StatusNotFound, "provisioning is off (set provision_token)")
			return
		}
		if !ok || subtle.ConstantTimeCompare([]byte(got), []byte(want)) != 1 {
			writeErr(w, http.StatusUnauthorized, "bad provisioning token")
			return
		}
		next(w, r)
	}
}

func (s *Server) handleProvisionUser(w http.ResponseWriter, r *http.Request) {
	var in provisionInput
	if !decode(w, r, &in) {
		return
	}
	name, err := accounts.NormalizeUsername(r.PathValue("username"))
	if err != nil {
		s.fail(w, err)
		return
	}
	created := false
	u, err := s.st().UserByUsername(name)
	switch {
	case errors.Is(err, store.ErrNotFound):
		u, err = s.acc.Create(accounts.CreateInput{Username: name, Email: in.Email, Role: store.RoleUser, SSOClaimable: true})
		if err != nil {
			s.fail(w, err)
			return
		}
		created = true
		s.log.Info("provisioned account", "user", name)
	case err != nil:
		s.fail(w, err)
		return
	}

	existing, err := s.st().ListNetworks(u.ID)
	if err != nil {
		s.fail(w, err)
		return
	}
	have := map[string]int64{}
	for _, n := range existing {
		have[strings.ToLower(n.Name)] = n.ID
	}
	out := []provisionedNetwork{}
	for _, ni := range in.Networks {
		n := store.Network{
			UserID: u.ID, Nick: u.DefaultNick, Username: u.DefaultIdent,
			Realname: u.DefaultRealname, TrustPKI: true, FloodRate: 2, FloodBurst: 9,
		}
		ni.apply(&n)
		res := provisionedNetwork{Name: n.Name}
		if id, ok := have[strings.ToLower(n.Name)]; ok {
			res.ID, res.Status = id, "exists"
			if in.Connect {
				res.Error = s.provisionConnect(id)
			}
			out = append(out, res)
			continue
		}
		// Per-user network limits don't apply: this is an admin bringing an
		// account over as it was.
		if err := s.checkNetwork(u, &n, nil); err != nil {
			res.Status, res.Error = "failed", err.Error()
			out = append(out, res)
			continue
		}
		n, err := s.st().CreateNetwork(n)
		if err != nil {
			res.Status, res.Error = "failed", err.Error()
			out = append(out, res)
			continue
		}
		have[strings.ToLower(n.Name)] = n.ID
		res.ID, res.Status = n.ID, "created"
		if in.Connect {
			res.Error = s.provisionConnect(n.ID)
		}
		out = append(out, res)
	}
	s.log.Info("provisioned networks", "user", name, "networks", len(out))
	writeJSON(w, http.StatusOK, map[string]any{
		"username": u.Username, "id": u.ID, "created": created,
		"claimable": u.SSOClaimable, "networks": out,
	})
}

// provisionConnect connects a network, returning why it couldn't ("" if it
// did, or will: another node serves this user and connects it).
func (s *Server) provisionConnect(id int64) string {
	err := s.b.Connect(id)
	if errors.Is(err, bouncer.ErrNotOwner) {
		_ = s.st().SetNetworkEnabled(id, true)
		return ""
	}
	if err != nil {
		return "not connected: " + err.Error()
	}
	return ""
}
