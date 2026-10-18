package settings

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/4evrd/evrd/internal/config"
	"github.com/4evrd/evrd/internal/store"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func setup(t *testing.T, conf string) (store.Store, string) {
	t.Helper()
	st, err := store.Open("sqlite:" + filepath.Join(t.TempDir(), "evrd.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	path := filepath.Join(t.TempDir(), "evrd.conf")
	os.WriteFile(path, []byte(conf), 0o600)
	return st, path
}

func load(t *testing.T, path string) *config.Config {
	t.Helper()
	c, err := config.LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func field(fs []Field, key string) Field {
	for _, f := range fs {
		if f.Key == key {
			return f
		}
	}
	return Field{}
}

func TestSettingsLayerAndApply(t *testing.T) {
	st, path := setup(t, "max_networks 3\n")
	m, err := New(st, load(t, path), "n1", quiet)
	if err != nil {
		t.Fatal(err)
	}
	var seen *config.Config
	m.OnChange(func(c *config.Config) { seen = c })

	if m.Config().Limits.MaxNetworks != 3 || field(m.Fields(os.Getenv), "max_networks").Source != "file" {
		t.Fatal("the file is the base")
	}
	if err := m.Save(map[string]string{"max_networks": "8", "session_ttl": "12h", "oidc_issuer": "https://id.example.org"}, nil, "root"); err != nil {
		t.Fatal(err)
	}
	if seen == nil || seen.Limits.MaxNetworks != 8 || m.Config().Auth.SessionTTL.Hours() != 12 {
		t.Fatal("a save applies at once")
	}
	fs := m.Fields(os.Getenv)
	if f := field(fs, "max_networks"); f.Source != "ui" || f.Value != "8" || f.FileValue != "3" || f.UpdatedBy != "root" {
		t.Fatalf("field: %+v", f)
	}
	if !field(fs, "oidc_issuer").Pending || field(fs, "max_networks").Pending {
		t.Fatal("only restart-only settings are pending")
	}
	if err := m.Save(nil, []string{"max_networks"}, "root"); err != nil || m.Config().Limits.MaxNetworks != 3 {
		t.Fatalf("reset goes back to the file: %v %d", err, m.Config().Limits.MaxNetworks)
	}

	for _, bad := range []map[string]string{{"max_networks": "-2"}, {"listen": ":1"}, {"password_login": "perhaps"}} {
		if m.Save(bad, nil, "root") == nil {
			t.Errorf("accepted %v", bad)
		}
	}

	// Secrets are never shown back.
	m.Save(map[string]string{"oidc_client_secret": "s3cret"}, nil, "root")
	if f := field(m.Fields(os.Getenv), "oidc_client_secret"); f.Value != "" || !f.Set {
		t.Fatalf("secret field: %+v", f)
	}

	// Another node (sharing the database) picks the change up.
	other, _ := New(st, load(t, path), "n2", quiet)
	m.Save(map[string]string{"irc_host": "irc.example.org"}, nil, "root")
	other.reload(false)
	if other.Config().IRCHost != "irc.example.org" {
		t.Fatal("the other node didn't follow")
	}
}

func TestWriteConf(t *testing.T) {
	st, path := setup(t, "# mine\nsettings_write_conf yes\nmax_networks 3\n")
	m, err := New(st, load(t, path), "n1", quiet)
	if err != nil {
		t.Fatal(err)
	}
	m.Save(map[string]string{"max_networks": "9", "bindhost": "192.0.2.1 192.0.2.2"}, nil, "root")
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), "# mine\nsettings_write_conf yes\nmax_networks 9\n") || !strings.Contains(string(b), "bindhost 192.0.2.1 192.0.2.2") {
		t.Fatalf("file:\n%s", b)
	}
	confs, _ := st.NodeConfs()
	if len(confs) != 1 || confs[0].Error != "" || confs[0].NodeID != "n1" {
		t.Fatalf("node status: %+v", confs)
	}

	// Reset while a second node (same file layout, own file) is down; when
	// it comes back it still removes the line it wrote.
	path2 := filepath.Join(t.TempDir(), "evrd.conf")
	os.WriteFile(path2, []byte("settings_write_conf yes\n"), 0o600)
	n2, _ := New(st, load(t, path2), "n2", quiet) // writes max_networks 9, bindhost
	if b, _ := os.ReadFile(path2); !strings.Contains(string(b), "max_networks 9") {
		t.Fatalf("n2 file: %s", b)
	}
	_ = n2
	m.Save(nil, []string{"max_networks"}, "root") // n2 is "down"
	if b, _ := os.ReadFile(path); strings.Contains(string(b), "max_networks") {
		t.Fatalf("reset should remove the directive:\n%s", b)
	}
	New(st, load(t, path2), "n2", quiet) // n2 restarts
	b2, _ := os.ReadFile(path2)
	if strings.Contains(string(b2), "max_networks") || !strings.Contains(string(b2), "bindhost 192.0.2.1 192.0.2.2") {
		t.Fatalf("n2 after restart:\n%s", b2)
	}

	// A file that can't be written is reported, not fatal.
	os.Chmod(filepath.Dir(path2), 0o500)
	t.Cleanup(func() { os.Chmod(filepath.Dir(path2), 0o700) })
	n2b, err := New(st, load(t, path2), "n2", quiet)
	if err != nil {
		t.Fatal(err)
	}
	_ = n2b
	confs, _ = st.NodeConfs()
	var bad bool
	for _, c := range confs {
		bad = bad || (c.NodeID == "n2" && c.Error != "")
	}
	if !bad && os.Geteuid() != 0 {
		t.Fatal("a failed write isn't recorded")
	}
}
