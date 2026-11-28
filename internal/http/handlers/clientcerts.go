package handlers

// The user's client certificates (Account → Security): TLS certificates
// that sign their IRC clients in without a password (certauth), and the
// certificates their attached IRC clients sent, to add with a click.

import (
	"net/http"
	"slices"

	"github.com/4evrd/evrd/internal/accounts"
	"github.com/4evrd/evrd/internal/auth"
	"github.com/4evrd/evrd/internal/store"
)

// SetClientCerts manages users' client certificates with c.
func (s *Handlers) SetClientCerts(c accounts.ClientCertServiceImpl) { s.clientCerts = c }

func clientCertDTO(c store.ClientCert) map[string]any {
	return map[string]any{
		"id": c.ID, "fingerprint": c.Fingerprint, "label": c.Label,
		"createdAt": c.CreatedAt.Unix(), "lastUsedAt": unix(c.LastUsedAt),
	}
}

func (s *Handlers) clientCertsOn(w http.ResponseWriter, r *http.Request) bool {
	if s.clientCerts == nil {
		s.say(w, r, http.StatusNotFound, "web.notFound")
		return false
	}
	return true
}

// ListClientCerts is GET /api/me/client-certs: the user's certificates,
// and the ones their attached IRC clients sent (added says whether it's
// one of them).
func (s *Handlers) ListClientCerts(w http.ResponseWriter, r *http.Request) {
	if !s.clientCertsOn(w, r) {
		return
	}
	u := auth.User(r)
	list, err := s.clientCerts.ListClientCerts(r.Context(), u.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	certs := make([]map[string]any, 0, len(list))
	for _, c := range list {
		certs = append(certs, clientCertDTO(c))
	}
	seen := []map[string]any{}
	for _, c := range s.b.ClientsOf(u.ID) {
		if c.CertFP == "" || slices.ContainsFunc(seen, func(m map[string]any) bool { return m["fingerprint"] == c.CertFP }) {
			continue
		}
		added := slices.ContainsFunc(list, func(k store.ClientCert) bool { return k.Fingerprint == c.CertFP })
		seen = append(seen, map[string]any{"fingerprint": c.CertFP, "software": c.Software, "device": c.Device, "ip": c.IP, "added": added})
	}
	writeJSON(w, http.StatusOK, map[string]any{"certs": certs, "clients": seen})
}

// AddClientCert is POST /api/me/client-certs {fingerprint, label}.
func (s *Handlers) AddClientCert(w http.ResponseWriter, r *http.Request) {
	if !s.clientCertsOn(w, r) {
		return
	}
	var in struct{ Fingerprint, Label string }
	if !s.decode(w, r, &in) {
		return
	}
	c, err := s.clientCerts.AddClientCert(r.Context(), auth.User(r).ID, in.Fingerprint, in.Label)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, clientCertDTO(c))
}

// DeleteClientCert is DELETE /api/me/client-certs/{id}.
func (s *Handlers) DeleteClientCert(w http.ResponseWriter, r *http.Request) {
	if !s.clientCertsOn(w, r) {
		return
	}
	if err := s.clientCerts.DeleteClientCert(r.Context(), auth.User(r).ID, PathID(r)); err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
