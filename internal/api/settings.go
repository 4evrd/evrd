package api

import (
	"net/http"
	"os"

	"github.com/4evrd/evrd/internal/auth"
	"github.com/4evrd/evrd/internal/settings"
)

// SetSettings enables Admin → Settings.
func (s *Server) SetSettings(m *settings.Manager) { s.settings = m }

func (s *Server) settingsDTO() map[string]any {
	confs, _ := s.st().NodeConfs()
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
