package handlers

import (
	"context"
	"net/http"
	"os"
	"strings"

	"github.com/4evrd/evrd/internal/auth"
	"github.com/4evrd/evrd/internal/certs"
	"github.com/4evrd/evrd/internal/config"
	"github.com/4evrd/evrd/internal/messages"
	"github.com/4evrd/evrd/internal/settings"
)

// SetSettings enables Admin → Settings.
func (s *Handlers) SetSettings(m *settings.Manager) { s.settings = m }

// SetCerts shows the certificates in use on Admin → Settings.
func (s *Handlers) SetCerts(m *certs.Manager) { s.certs = m }

// settingsDTO is the settings page's data, its words in p's language.
func (s *Handlers) settingsDTO(p *messages.Printer) map[string]any {
	confs, _ := s.state.NodeConfs(context.Background())
	nodes := []map[string]any{}
	for _, c := range confs {
		nodes = append(nodes, map[string]any{"node": c.NodeID, "path": c.Path, "wroteAt": unixT(c.WroteAt), "error": c.Error})
	}
	c := s.cfg()
	return map[string]any{
		"fields":    settingFields(p, s.settings.Fields(os.Getenv)),
		"bootstrap": s.settings.Bootstrap(),
		"writeConf": c.WriteConf,
		"clustered": c.Cluster.Enabled(),
		"confs":     nodes,
		"tls":       s.tlsDTO(c),
		"directory": s.directoryStatus(),
	}
}

func (s *Handlers) GetSettings(w http.ResponseWriter, r *http.Request) {
	if s.settings == nil {
		s.say(w, r, http.StatusNotFound, "web.noSettings")
		return
	}
	writeJSON(w, http.StatusOK, s.settingsDTO(s.printer(r)))
}

// SaveSettings changes settings: {"set": {key: value}, "reset": [key]}
// (reset goes back to the config file's value or the default).
func (s *Handlers) SaveSettings(w http.ResponseWriter, r *http.Request) {
	if s.settings == nil {
		s.say(w, r, http.StatusNotFound, "web.noSettings")
		return
	}
	var in struct {
		Set   map[string]string `json:"set"`
		Reset []string          `json:"reset"`
	}
	if !s.decode(w, r, &in) {
		return
	}
	if err := s.settings.Save(in.Set, in.Reset, auth.User(r).Username); err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	s.log.Info("settings changed", "admin", auth.User(r).Username, "set", keys(in.Set), "reset", in.Reset)
	writeJSON(w, http.StatusOK, s.settingsDTO(s.printer(r)))
}

// settingFields are fields with their group, label and help in p's
// language (irc.settingGroup.*, irc.settingLabel.*, irc.settingHelp.*).
func settingFields(p *messages.Printer, fields []settings.Field) []settings.Field {
	for i, f := range fields {
		fields[i].GroupLabel = tOr(p, "settingGroup."+groupSlug(f.Group), f.Group)
		fields[i].Label = tOr(p, "settingLabel."+f.Key, f.Label)
		if f.Help != "" {
			fields[i].Help = tOr(p, "settingHelp."+f.Key, f.Help)
		}
	}
	return fields
}

// tOr is irc.<key> in p's language, or english when no language has it.
func tOr(p *messages.Printer, key, english string) string {
	if t := p.T(messages.Area + key); t != messages.Area+key {
		return t
	}
	return english
}

// groupSlug is a group's name as a key: its lowercase letters.
func groupSlug(g string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' {
			return r
		}
		if r >= 'A' && r <= 'Z' {
			return r + 'a' - 'A'
		}
		return -1
	}, g)
}

func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// tlsDTO says who handles HTTPS and which certificates evrd serves.
func (s *Handlers) tlsDTO(c *config.Config) map[string]any {
	out := map[string]any{"httpsListen": c.HTTPSListen, "httpListen": c.HTTPListen}
	if s.certs != nil {
		out["report"] = s.certs.Report()
	}
	return out
}
