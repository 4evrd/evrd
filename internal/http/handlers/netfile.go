package handlers

// Exporting a user's networks as a networks file (internal/netfile), and
// importing one, or a ZNC config (internal/zncimport), back in.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/4evrd/evrd/internal/accounts"
	"github.com/4evrd/evrd/internal/auth"
	"github.com/4evrd/evrd/internal/messages"
	"github.com/4evrd/evrd/internal/netfile"
	"github.com/4evrd/evrd/internal/store"
	"github.com/4evrd/evrd/internal/zncimport"
)

// maxImportBytes is the biggest file an import reads (a znc.conf with many
// users is well under it).
const maxImportBytes = 2 << 20

// ExportNetworks sends the user's networks as a networks file. Secrets go
// out sealed under the server's key; X-Evrd-Secrets says "sealed", or
// "omitted" when there's no key to seal them with.
func (s *Handlers) ExportNetworks(w http.ResponseWriter, r *http.Request) {
	s.exportNetworks(w, r, auth.User(r), "evrd-networks")
}

// AdminExportNetworks is ExportNetworks for the {id} user.
func (s *Handlers) AdminExportNetworks(w http.ResponseWriter, r *http.Request) {
	if u, ok := s.adminTarget(w, r); ok {
		s.exportNetworks(w, r, u, "evrd-networks-"+u.Username)
	}
}

func (s *Handlers) exportNetworks(w http.ResponseWriter, r *http.Request, u store.User, filename string) {
	nets, err := s.acc.ListNetworks(r.Context(), u.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	pbs := map[int64]map[string]store.BufferPlayback{}
	for _, n := range nets {
		p, err := s.b.Playback(n, u, "")
		if err != nil {
			s.fail(w, r, err)
			return
		}
		pbs[n.ID] = p.Buffers
	}
	out, sealed, err := netfile.Export(nets, pbs)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	secrets := "sealed"
	if !sealed {
		secrets = "omitted"
	}
	w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s-%s.yaml"`, filename, time.Now().Format("2006-01-02")))
	w.Header().Set("X-Evrd-Secrets", secrets)
	w.Header().Set("Cache-Control", "no-store")
	w.Write(out)
}

// importSource is a file read for an import: its users (one, named "", for
// a networks file) and their networks.
type importSource struct {
	format string // "evrd" or "znc"
	users  []zncimport.User
}

func readImport(text []byte) (importSource, error) {
	switch {
	case netfile.Looks(text):
		f, err := netfile.Parse(text)
		if err != nil {
			return importSource{}, err
		}
		return importSource{format: "evrd", users: []zncimport.User{{Networks: f.Networks}}}, nil
	case zncimport.LooksLikeConfig(text):
		users, err := zncimport.ParseConfig(text)
		return importSource{format: "znc", users: users}, err
	}
	return importSource{}, errNotImportable
}

var errNotImportable = errors.New("not a networks file or a ZNC config")

// importText reads an import request's {text}.
func (s *Handlers) importText(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxImportBytes)).Decode(v); err != nil {
		s.say(w, r, http.StatusRequestEntityTooLarge, "web.importTooBig", "max", "2 MB")
		return false
	}
	return true
}

func (s *Handlers) badImport(w http.ResponseWriter, r *http.Request, err error) {
	p := s.printer(r)
	if errors.Is(err, errNotImportable) || errors.Is(err, netfile.ErrNotNetworks) || errors.Is(err, zncimport.ErrNotConfig) {
		writeErr(w, http.StatusUnprocessableEntity, p.T(messages.Area+"web.importUnknown"))
		return
	}
	writeErr(w, http.StatusUnprocessableEntity, p.T(messages.Area+"web.importUnreadable", "error", err.Error()))
}

// notesOf are a network's notes in the reader's language.
func notesOf(p *messages.Printer, x *netfile.Network) []string {
	out := []string{}
	for _, n := range x.Notes() {
		out = append(out, p.T(n.Key, n.Args...))
	}
	return out
}

// PreviewImport reads a networks file or ZNC config ({text}) and says what's
// in it: its users and their networks, which of them the user already has,
// and what wouldn't carry over. Nothing secret is sent back.
func (s *Handlers) PreviewImport(w http.ResponseWriter, r *http.Request) {
	s.previewImport(w, r, auth.User(r))
}

// AdminPreviewImport is PreviewImport for the {id} user.
func (s *Handlers) AdminPreviewImport(w http.ResponseWriter, r *http.Request) {
	if u, ok := s.adminTarget(w, r); ok {
		s.previewImport(w, r, u)
	}
}

func (s *Handlers) previewImport(w http.ResponseWriter, r *http.Request, u store.User) {
	var in struct {
		Text string `json:"text"`
	}
	if !s.importText(w, r, &in) {
		return
	}
	src, err := readImport([]byte(in.Text))
	if err != nil {
		s.badImport(w, r, err)
		return
	}
	have, err := s.acc.ListNetworks(r.Context(), u.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"format": src.format, "users": previewUsers(s.printer(r), src.users, have)})
}

// previewUsers describes users' networks against the networks an account
// has (have).
func previewUsers(p *messages.Printer, users []zncimport.User, have []store.Network) []map[string]any {
	existing := func(name string) *store.Network {
		for i := range have {
			if strings.EqualFold(have[i].Name, strings.TrimSpace(name)) {
				return &have[i]
			}
		}
		return nil
	}
	out := []map[string]any{}
	for _, su := range users {
		nets := []map[string]any{}
		for i := range su.Networks {
			x := &su.Networks[i]
			n := store.Network{}
			if e := existing(x.Name); e != nil {
				n = *e
			}
			x.Apply(&n)
			servers := []string{}
			for _, sv := range n.ServerList() {
				servers = append(servers, fmt.Sprintf("%s:%s%d", sv.Host, map[bool]string{true: "+"}[sv.TLS], sv.Port))
			}
			nets = append(nets, map[string]any{
				"name": x.Name, "servers": servers, "nick": n.Nick, "channels": len(n.Channels),
				"exists": existing(x.Name) != nil, "enabled": x.Enabled == nil || *x.Enabled, "notes": notesOf(p, x),
			})
		}
		out = append(out, map[string]any{"name": su.Name, "networks": nets, "hasPassword": su.Pass != nil})
	}
	return out
}

// ImportNetworks adds the networks picked ({networks}, by name) of one user
// ({user}; "" for a networks file) from {text}. A network the user already
// has (by name) is skipped, updated from the file ({existing: "update"}) or
// merged with it ("merge"); {connect} connects the ones the file has
// enabled.
func (s *Handlers) ImportNetworks(w http.ResponseWriter, r *http.Request) {
	s.importNetworks(w, r, auth.User(r), false)
}

// AdminImportNetworks is ImportNetworks into the {id} user's account, with
// an admin's say: any bind host, and past the account's network limit.
func (s *Handlers) AdminImportNetworks(w http.ResponseWriter, r *http.Request) {
	if u, ok := s.adminTarget(w, r); ok {
		s.importNetworks(w, r, u, true)
	}
}

func (s *Handlers) importNetworks(w http.ResponseWriter, r *http.Request, u store.User, admin bool) {
	var in struct {
		Text     string   `json:"text"`
		User     string   `json:"user"`
		Networks []string `json:"networks"`
		Existing string   `json:"existing"`
		Connect  bool     `json:"connect"`
	}
	if !s.importText(w, r, &in) {
		return
	}
	src, err := readImport([]byte(in.Text))
	if err != nil {
		s.badImport(w, r, err)
		return
	}
	var picked *zncimport.User
	for i := range src.users {
		if src.users[i].Name == in.User {
			picked = &src.users[i]
		}
	}
	if picked == nil {
		s.say(w, r, http.StatusUnprocessableEntity, "web.importNoUser", "user", in.User)
		return
	}
	want := map[string]bool{}
	for _, n := range in.Networks {
		want[strings.ToLower(strings.TrimSpace(n))] = true
	}
	p := s.printer(r)
	results := []map[string]any{}
	for i := range picked.Networks {
		x := &picked.Networks[i]
		if !want[strings.ToLower(strings.TrimSpace(x.Name))] {
			continue
		}
		res := map[string]any{"name": x.Name}
		result, err := s.importNetwork(r.Context(), u, x, in.Existing, in.Connect, admin)
		res["result"] = result
		if err != nil {
			var ve *accounts.ValidationError
			if errors.As(err, &ve) {
				res["error"] = p.Say(ve)
			} else {
				s.log.Error("import network", "user", u.Username, "network", x.Name, "err", err)
				res["error"] = p.T(messages.Area + "web.failed")
			}
		}
		res["notes"] = notesOf(p, x)
		results = append(results, res)
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}

// importNetwork saves one network from a file: "created", "updated",
// "merged", "skipped" (it exists and existing is neither "update" nor
// "merge") or "failed".
func (s *Handlers) importNetwork(ctx context.Context, u store.User, x *netfile.Network, existing string, connect, admin bool) (string, error) {
	have, err := s.acc.ListNetworks(ctx, u.ID)
	if err != nil {
		return "failed", err
	}
	var before *store.Network
	for i := range have {
		if strings.EqualFold(have[i].Name, strings.TrimSpace(x.Name)) {
			before = &have[i]
		}
	}
	if before != nil && existing != "update" && existing != "merge" {
		return "skipped", nil
	}
	n := store.Network{
		UserID: u.ID, Nick: u.DefaultNick, Username: u.DefaultIdent,
		Realname: u.DefaultRealname, TrustPKI: true, FloodRate: 2, FloodBurst: 9,
	}
	if before != nil {
		n = *before
	}
	var pb netfile.Settings
	result := "updated"
	if before != nil && existing == "merge" {
		p, err := s.b.Playback(*before, u, "")
		if err != nil {
			return "failed", err
		}
		pb, result = x.Merge(&n, p.Buffers), "merged"
	} else {
		pb = x.Apply(&n)
	}
	checkAs := u
	if admin {
		checkAs = asAdmin(u)
	}
	if err := s.checkNetwork(checkAs, &n, before); err != nil {
		return "failed", err
	}
	if before == nil {
		if !admin {
			if err := s.acc.CanCreateNetwork(u); err != nil {
				return "failed", err
			}
		}
		if n, err = s.acc.CreateNetwork(ctx, n); err != nil {
			return "failed", err
		}
		result = "created"
	} else {
		if err := s.acc.UpdateNetwork(ctx, n); err != nil {
			return "failed", err
		}
		s.b.Reconfigure(n)
		if accounts.NeedsReconnect(*before, n) && s.b.Status(n.ID) != "disconnected" {
			if err := s.b.Reconnect(n.ID); err != nil {
				return "failed", err
			}
		}
	}
	for buf, set := range pb {
		err := s.b.UpdatePlayback(n.ID, []string{buf}, func(bp *store.BufferPlayback) {
			if set.Lines != nil {
				l := min(max(*set.Lines, 0), maxPlaybackLines)
				bp.Lines = &l
			}
			if set.AutoClear != nil && buf != "" {
				bp.AutoClear = set.AutoClear
			}
		})
		if err != nil {
			return "failed", err
		}
	}
	if connect && (x.Enabled == nil || *x.Enabled) && s.b.Status(n.ID) == "disconnected" {
		if err := s.b.Connect(n.ID); err != nil {
			return result, err
		}
	}
	return result, nil
}
