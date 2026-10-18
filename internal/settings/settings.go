// Package settings layers the server settings changed in the admin UI
// (stored in the database, shared by every node) over the config file, keeps
// every node up to date, and applies changes live where it can.
//
//	defaults < evrd.conf < database (admin UI) < environment
//
// With settings_write_conf, each node also writes the values into its own
// config file. In a cluster, leave that off and let the database be the one
// place they live.
package settings

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"sync"
	"time"

	"github.com/4evrd/evrd/internal/config"
	"github.com/4evrd/evrd/internal/store"
)

// PollInterval is how often a node checks for settings changed elsewhere.
var PollInterval = 5 * time.Second

// Manager holds the effective config.
type Manager struct {
	st     store.Store
	base   *config.Config // the file (or defaults), without the environment
	nodeID string
	log    *slog.Logger

	mu      sync.Mutex
	eff     *config.Config
	running *config.Config // as at startup: what restart-only settings still are
	rows    map[string]store.Setting
	hash    string
	hooks   []func(*config.Config)
	managed map[string]bool // keys last written to the file
}

// New loads the database's settings over base.
func New(st store.Store, base *config.Config, nodeID string, log *slog.Logger) (*Manager, error) {
	m := &Manager{st: st, base: base, nodeID: nodeID, log: log, managed: map[string]bool{}}
	if confs, err := st.NodeConfs(); err == nil {
		for _, c := range confs {
			if c.NodeID == nodeID && c.Path == base.Path {
				for _, k := range c.Keys {
					m.managed[k] = true
				}
			}
		}
	}
	if _, err := m.reload(true); err != nil {
		return nil, err
	}
	m.running = m.eff
	return m, nil
}

// Config is the effective config now.
func (m *Manager) Config() *config.Config {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.eff
}

// OnChange registers f to run with the new config after every change.
func (m *Manager) OnChange(f func(*config.Config)) {
	m.mu.Lock()
	m.hooks = append(m.hooks, f)
	m.mu.Unlock()
}

// Run polls for changes made on other nodes until ctx ends.
func (m *Manager) Run(ctx context.Context) {
	t := time.NewTicker(PollInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if changed, err := m.reload(false); err != nil {
				m.log.Warn("reload settings", "err", err)
			} else if changed {
				m.log.Info("settings changed")
			}
		}
	}
}

// reload reads the settings; on a change it rebuilds the config, runs the
// hooks and writes the config file if that's on.
func (m *Manager) reload(first bool) (bool, error) {
	rows, err := m.st.Settings()
	if err != nil {
		return false, err
	}
	h := sha256.New()
	byKey := map[string]store.Setting{}
	values := map[string]string{}
	for _, r := range rows {
		if _, ok := config.SpecFor(r.Key); !ok {
			continue // a setting this version doesn't know
		}
		byKey[r.Key] = r
		values[r.Key] = r.Value
		h.Write([]byte(r.Key + "\x00" + r.Value + "\x00"))
	}
	hash := hex.EncodeToString(h.Sum(nil))
	m.mu.Lock()
	if !first && hash == m.hash {
		m.mu.Unlock()
		return false, nil
	}
	m.mu.Unlock()

	eff, err := m.base.WithSettings(values)
	if err != nil {
		return false, err
	}
	m.mu.Lock()
	m.eff, m.rows, m.hash = eff, byKey, hash
	hooks := append([]func(*config.Config){}, m.hooks...)
	m.mu.Unlock()
	if !first {
		for _, f := range hooks {
			f(eff)
		}
	}
	if m.base.WriteConf && m.base.Path != "" {
		m.writeConf(values)
	}
	return true, nil
}

// writeConf mirrors the database's settings into this node's config file:
// set keys are written, keys reset since the last write are removed.
func (m *Manager) writeConf(values map[string]string) {
	out := map[string]*string{}
	for k, v := range values {
		v := v
		out[k] = &v
	}
	m.mu.Lock()
	for k := range m.managed {
		if _, ok := values[k]; !ok {
			out[k] = nil
		}
	}
	m.mu.Unlock()
	err := config.RewriteFile(m.base.Path, out)
	rec := store.NodeConf{NodeID: m.nodeID, Path: m.base.Path, WroteAt: time.Now()}
	m.mu.Lock()
	if err != nil {
		rec.Error = err.Error()
		m.log.Warn("write settings to the config file", "path", m.base.Path, "err", err)
	} else {
		m.managed = map[string]bool{}
		for k := range values {
			m.managed[k] = true
		}
	}
	for k := range m.managed {
		rec.Keys = append(rec.Keys, k)
	}
	m.mu.Unlock()
	if err := m.st.SetNodeConf(rec); err != nil {
		m.log.Warn("record config write", "err", err)
	}
}

// Save validates and stores changes (set: key → value; del: back to the file
// or default), then applies them here at once; other nodes follow within
// PollInterval.
func (m *Manager) Save(set map[string]string, del []string, by string) error {
	for k, v := range set {
		if err := config.Check(k, v); err != nil {
			return err
		}
	}
	for _, k := range del {
		if _, ok := config.SpecFor(k); !ok {
			return &invalidKey{k}
		}
	}
	// The whole result must be a valid config too.
	m.mu.Lock()
	values := map[string]string{}
	for k, r := range m.rows {
		values[k] = r.Value
	}
	m.mu.Unlock()
	for k, v := range set {
		values[k] = v
	}
	for _, k := range del {
		delete(values, k)
	}
	c, err := m.base.WithSettings(values)
	if err != nil {
		return err
	}
	if err := c.CheckTLS(); err != nil {
		return err
	}
	if err := m.st.SaveSettings(set, del, by); err != nil {
		return err
	}
	_, err = m.reload(false)
	return err
}

type invalidKey struct{ key string }

func (e *invalidKey) Error() string { return e.key + " can't be changed from the web app" }

// Field is one setting as the admin page shows it.
type Field struct {
	config.Spec
	Value     string `json:"value"`          // effective (secrets: "" — see Set)
	Set       bool   `json:"set"`            // a secret has a value
	Source    string `json:"source"`         // default | file | ui | env
	FileValue string `json:"fileValue"`      // what the file (or default) says
	InFile    bool   `json:"inFile"`         // the file sets it (else FileValue is the default)
	Pending   bool   `json:"restartPending"` // changed, waiting for a restart
	UpdatedBy string `json:"updatedBy,omitempty"`
	UpdatedAt int64  `json:"updatedAt,omitempty"`
}

// Fields lists every editable setting with its value and where it comes from.
func (m *Manager) Fields(env func(string) string) []Field {
	m.mu.Lock()
	eff, running, rows := m.eff, m.running, m.rows
	m.mu.Unlock()
	var out []Field
	for _, s := range config.Specs {
		f := Field{Spec: s, Value: eff.Value(s.Key), FileValue: m.base.Value(s.Key), Source: "default"}
		if m.base.InFile(s.Key) {
			f.Source, f.InFile = "file", true
		}
		if r, ok := rows[s.Key]; ok {
			f.Source, f.UpdatedBy, f.UpdatedAt = "ui", r.UpdatedBy, r.UpdatedAt.Unix()
		}
		if s.Env != "" && env(s.Env) != "" {
			f.Source = "env"
		}
		f.Pending = s.Restart && running.Value(s.Key) != eff.Value(s.Key)
		if s.Kind == "secret" {
			f.Set, f.Value, f.FileValue = f.Value != "", "", ""
		}
		out = append(out, f)
	}
	return out
}

// Bootstrap lists the file/env-only directives (read-only in the UI).
func (m *Manager) Bootstrap() []map[string]string {
	c := m.Config()
	rows := []map[string]string{
		{"key": "listen", "value": joinStrings(c.Listen)},
		{"key": "api", "value": c.API},
		{"key": "db", "value": redactDSN(c.DB)},
		{"key": "history", "value": redactDSN(c.History)},
		{"key": "node_id", "value": c.Cluster.NodeID},
		{"key": "internal_listen", "value": c.Cluster.InternalListen},
		{"key": "internal_url", "value": c.Cluster.InternalURL},
		{"key": "secret_key", "value": map[bool]string{true: "set", false: "not set"}[c.Secrets.Key != "" || c.Secrets.KeyFile != ""]},
		{"key": "web_root", "value": c.WebRoot},
		{"key": "chroot", "value": c.Chroot},
		{"key": "user", "value": c.User},
		{"key": "settings_write_conf", "value": map[bool]string{true: "yes", false: "no"}[c.WriteConf]},
		{"key": "config file", "value": c.Path},
	}
	return rows
}

func joinStrings(s []string) string {
	out := ""
	for i, v := range s {
		if i > 0 {
			out += " "
		}
		out += v
	}
	return out
}

// redactDSN hides a password in a DSN (postgres://user:pass@host/db).
func redactDSN(dsn string) string {
	at := -1
	for i := len(dsn) - 1; i >= 0; i-- {
		if dsn[i] == '@' {
			at = i
			break
		}
	}
	scheme := 0
	for i := 0; i+2 < len(dsn); i++ {
		if dsn[i:i+3] == "://" {
			scheme = i + 3
			break
		}
	}
	if at < 0 || scheme == 0 || at < scheme {
		return dsn
	}
	userinfo := dsn[scheme:at]
	for i := 0; i < len(userinfo); i++ {
		if userinfo[i] == ':' {
			return dsn[:scheme] + userinfo[:i] + ":•••" + dsn[at:]
		}
	}
	return dsn
}
