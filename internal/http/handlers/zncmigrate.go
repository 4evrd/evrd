package handlers

// Bringing a whole ZNC over (Admin → Users → Import from ZNC): each ZNC user
// in a config goes to an evrd account, made here when it doesn't exist yet,
// keeping the user's ZNC password (checked once, then rehashed) and, given
// an email, claimable by the owner's first SSO sign-in. The page then
// imports each user's networks through AdminImportNetworks, on the node
// that serves that account.

import (
	"errors"
	"net/http"
	"strings"

	"github.com/4evrd/evrd/internal/accounts"
	"github.com/4evrd/evrd/internal/messages"
	"github.com/4evrd/evrd/internal/store"
	"github.com/4evrd/evrd/internal/zncimport"
)

// zncConfig reads a request's ZNC config ({text}) into v, and its users.
func (s *Handlers) zncConfig(w http.ResponseWriter, r *http.Request, text string) ([]zncimport.User, bool) {
	if !zncimport.LooksLikeConfig([]byte(text)) {
		s.badImport(w, r, zncimport.ErrNotConfig)
		return nil, false
	}
	users, err := zncimport.ParseConfig([]byte(text))
	if err != nil {
		s.badImport(w, r, err)
		return nil, false
	}
	return users, true
}

// AdminPreviewZNC reads a ZNC config ({text}) and says, for each of its
// users, the evrd account it would go to ({accounts}: ZNC user → account
// name; by default the ZNC name, if it makes a valid one), whether that
// account exists, and the user's networks against that account's.
func (s *Handlers) AdminPreviewZNC(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Text     string            `json:"text"`
		Accounts map[string]string `json:"accounts"`
	}
	if !s.importText(w, r, &in) {
		return
	}
	users, ok := s.zncConfig(w, r, in.Text)
	if !ok {
		return
	}
	p := s.printer(r)
	out := []map[string]any{}
	for _, zu := range users {
		if zu.Name == "" {
			continue // networks without their <User>: import those into one account
		}
		name, set := in.Accounts[zu.Name]
		if !set {
			name = zu.Name
		}
		row := map[string]any{"account": strings.ToLower(strings.TrimSpace(name))}
		var have []store.Network
		if norm, err := accounts.NormalizeUsername(name); err != nil {
			row["accountError"] = p.Say(err)
		} else {
			row["account"] = norm
			u, err := s.acc.UserByUsername(r.Context(), norm)
			switch {
			case err == nil:
				row["accountID"], row["exists"] = u.ID, true
				if have, err = s.acc.ListNetworks(r.Context(), u.ID); err != nil {
					s.fail(w, r, err)
					return
				}
			case errors.Is(err, store.ErrNotFound):
				row["exists"] = false
			default:
				s.fail(w, r, err)
				return
			}
		}
		pv := previewUsers(p, []zncimport.User{zu}, have)[0]
		for k, v := range pv {
			row[k] = v
		}
		out = append(out, row)
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": out})
}

// AdminCreateZNCAccounts makes the accounts ZNC users go to ({users}: the
// ZNC user, the account name and optionally an email) where they don't
// exist yet. A new account keeps the ZNC user's password when the config has
// it, and with an email is claimable by its owner's first SSO sign-in. An
// account that exists is left as it is. It answers each one's account id,
// and whether it was made.
func (s *Handlers) AdminCreateZNCAccounts(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Text  string `json:"text"`
		Users []struct {
			ZNC     string `json:"znc"`
			Account string `json:"account"`
			Email   string `json:"email"`
		} `json:"users"`
	}
	if !s.importText(w, r, &in) {
		return
	}
	users, ok := s.zncConfig(w, r, in.Text)
	if !ok {
		return
	}
	byName := map[string]zncimport.User{}
	for _, zu := range users {
		byName[zu.Name] = zu
	}
	p := s.printer(r)
	out := []map[string]any{}
	for _, want := range in.Users {
		res := map[string]any{"znc": want.ZNC, "account": want.Account}
		zu, found := byName[want.ZNC]
		if !found || want.ZNC == "" {
			res["error"] = p.T(messages.Area+"web.importNoUser", "user", want.ZNC)
			out = append(out, res)
			continue
		}
		name, err := accounts.NormalizeUsername(want.Account)
		if err != nil {
			res["error"] = p.Say(err)
			out = append(out, res)
			continue
		}
		res["account"] = name
		if u, err := s.acc.UserByUsername(r.Context(), name); err == nil {
			res["id"], res["created"] = u.ID, false
			out = append(out, res)
			continue
		} else if !errors.Is(err, store.ErrNotFound) {
			s.fail(w, r, err)
			return
		}
		ci := accounts.CreateInput{Username: name, Email: strings.TrimSpace(want.Email), Role: store.RoleUser, SSOClaimable: strings.TrimSpace(want.Email) != ""}
		if zu.Pass != nil {
			if h, err := accounts.ZNCPasswordHash(zu.Pass.Method, zu.Pass.Hash, zu.Pass.Salt); err == nil {
				ci.PasswordHash = h
			} else {
				res["note"] = p.T(messages.Area + "znc.passwordUnusable")
			}
		}
		u, err := s.acc.Create(ci)
		if err != nil {
			var ve *accounts.ValidationError
			if errors.As(err, &ve) || errors.Is(err, accounts.ErrUsernameTaken) {
				res["error"] = p.Say(err)
			} else {
				s.log.Error("znc account", "account", name, "err", err)
				res["error"] = p.T(messages.Area + "web.failed")
			}
			out = append(out, res)
			continue
		}
		res["id"], res["created"], res["password"], res["claimable"] = u.ID, true, ci.PasswordHash != "", ci.SSOClaimable
		out = append(out, res)
	}
	writeJSON(w, http.StatusOK, map[string]any{"accounts": out})
}
