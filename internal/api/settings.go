package api

import (
	"context"
	"net/http"
	"os"

	"github.com/4evrd/evrd/internal/auth"
	"github.com/4evrd/evrd/internal/certs"
	"github.com/4evrd/evrd/internal/config"
	"github.com/4evrd/evrd/internal/settings"
)

// SetSettings enables Admin → Settings.
func (s *Server) SetSettings(m *settings.Manager) { s.settings = m }

// SetCerts shows the certificates in use on Admin → Settings.
func (s *Server) SetCerts(m *certs.Manager) { s.certs = m }

func (s *Server) settingsDTO() map[string]any {
	confs, _ := s.st().NodeConfs(context.Background())
	nodes := []map[string]any{}
	for _, c := range confs {
		nodes = append(nodes, map[string]any{"node": c.NodeID, "path": c.Path, "wroteAt": unixT(c.WroteAt), "error": c.Error})
	}
	c := s.cfg()
	return map[string]any{
		"fields":    s.settings.Fields(os.Getenv),
		"bootstrap": s.settings.Bootstrap(),
		"writeConf": c.WriteConf,
		"clustered": c.Cluster.Enabled(),
		"confs":     nodes,
		"tls":       s.tlsDTO(c),
		"directory": s.directoryStatus(),
	}
}

func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	if s.settings == nil {
		writeErr(w, http.StatusNotFound, "settings aren't available")
		return
	}
	writeJSON(w, http.StatusOK, s.settingsDTO())
}

// handleSaveSettings changes settings: {"set": {key: value}, "reset": [key]}
// (reset goes back to the config file's value or the default).
func (s *Server) handleSaveSettings(w http.ResponseWriter, r *http.Request) {
	if s.settings == nil {
		writeErr(w, http.StatusNotFound, "settings aren't available")
		return
	}
	var in struct {
		Set   map[string]string `json:"set"`
		Reset []string          `json:"reset"`
	}
	if !decode(w, r, &in) {
		return
	}
	if err := s.settings.Save(in.Set, in.Reset, auth.User(r).Username); err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	s.log.Info("settings changed", "admin", auth.User(r).Username, "set", keys(in.Set), "reset", in.Reset)
	writeJSON(w, http.StatusOK, s.settingsDTO())
}

func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// tlsDTO says who handles HTTPS and which certificates evrd serves.
func (s *Server) tlsDTO(c *config.Config) map[string]any {
	out := map[string]any{"httpsListen": c.HTTPSListen, "httpListen": c.HTTPListen}
	if s.certs != nil {
		out["report"] = s.certs.Report()
	}
	return out
}
