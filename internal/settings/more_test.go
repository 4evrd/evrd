package settings

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/4evrd/evrd/internal/config"
	"github.com/4evrd/evrd/internal/store"
)

// failing wraps a store, failing the calls it's told to.
type failing struct {
	store.Store
	mu                                  sync.Mutex
	settings, save, nodeConf, nodeConfs error
}

func (f *failing) fail(settings, save, nodeConf error) {
	f.mu.Lock()
	f.settings, f.save, f.nodeConf = settings, save, nodeConf
	f.mu.Unlock()
}

func (f *failing) Settings(ctx context.Context) ([]store.Setting, error) {
	f.mu.Lock()
	err := f.settings
	f.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return f.Store.Settings(ctx)
}

func (f *failing) SaveSettings(ctx context.Context, set map[string]string, del []string, by string) error {
	f.mu.Lock()
	err := f.save
	f.mu.Unlock()
	if err != nil {
		return err
	}
	return f.Store.SaveSettings(ctx, set, del, by)
}

func (f *failing) SetNodeConf(ctx context.Context, c store.NodeConf) error {
	f.mu.Lock()
	err := f.nodeConf
	f.mu.Unlock()
	if err != nil {
		return err
	}
	return f.Store.SetNodeConf(ctx, c)
}

func (f *failing) NodeConfs(ctx context.Context) ([]store.NodeConf, error) {
	if f.nodeConfs != nil {
		return nil, f.nodeConfs
	}
	return f.Store.NodeConfs(ctx)
}

func TestRunFollowsOtherNodes(t *testing.T) {
	old := PollInterval
	PollInterval = 10 * time.Millisecond
	t.Cleanup(func() { PollInterval = old })

	st, path := setup(t, "")
	a, err := New(st, load(t, path), "a", quiet)
	if err != nil {
		t.Fatal(err)
	}
	b, err := New(st, load(t, path), "b", quiet)
	if err != nil {
		t.Fatal(err)
	}
	changes := make(chan *config.Config, 10)
	b.OnChange(func(c *config.Config) { changes <- c })
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { b.Run(ctx); close(done) }()

	if err := a.Save(map[string]string{"max_clients": "4"}, nil, "root"); err != nil {
		t.Fatal(err)
	}
	select {
	case c := <-changes:
		if c.Limits.MaxClients != 4 || b.Config().Limits.MaxClients != 4 {
			t.Fatalf("b's config: %d", c.Limits.MaxClients)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("b didn't pick the change up")
	}
	// Nothing changed: no hook runs.
	time.Sleep(50 * time.Millisecond)
	select {
	case <-changes:
		t.Fatal("a hook ran without a change")
	default:
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run didn't return when its context ended")
	}
}

func TestRunSurvivesStoreErrors(t *testing.T) {
	old := PollInterval
	PollInterval = 5 * time.Millisecond
	t.Cleanup(func() { PollInterval = old })

	st, path := setup(t, "")
	f := &failing{Store: st}
	m, err := New(f, load(t, path), "n1", quiet)
	if err != nil {
		t.Fatal(err)
	}
	f.fail(errors.New("db down"), nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { m.Run(ctx); close(done) }()
	time.Sleep(40 * time.Millisecond)
	f.fail(nil, nil, nil)
	st.SaveSettings(context.Background(), map[string]string{"max_clients": "6"}, nil, "other")
	deadline := time.Now().Add(5 * time.Second)
	for m.Config().Limits.MaxClients != 6 {
		if time.Now().After(deadline) {
			t.Fatal("polling didn't recover after the database came back")
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	<-done
}

func TestNewErrors(t *testing.T) {
	st, path := setup(t, "")
	f := &failing{Store: st}
	f.fail(errors.New("db down"), nil, nil)
	if _, err := New(f, load(t, path), "n1", quiet); err == nil {
		t.Error("New ignored a database error")
	}
	// A value in the database that no longer validates fails startup
	// loudly rather than being half-applied.
	f.fail(nil, nil, nil)
	st.SaveSettings(t.Context(), map[string]string{"max_clients": "many"}, nil, "old")
	if _, err := New(f, load(t, path), "n1", quiet); err == nil || !strings.Contains(err.Error(), "max_clients") {
		t.Errorf("a bad stored value: %v", err)
	}
	// Keys this version doesn't know are ignored, and NodeConfs failing
	// isn't fatal.
	st.SaveSettings(t.Context(), map[string]string{"from_the_future": "1"}, []string{"max_clients"}, "new")
	f.nodeConfs = errors.New("no table")
	m, err := New(f, load(t, path), "n1", quiet)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Fields(os.Getenv)) != len(config.Specs) {
		t.Error("an unknown key shows up")
	}
}

func TestSaveErrors(t *testing.T) {
	st, path := setup(t, "")
	f := &failing{Store: st}
	m, err := New(f, load(t, path), "n1", quiet)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Save(nil, []string{"listen"}, "root"); err == nil || err.Error() != "listen can't be changed from the web app" {
		t.Errorf("resetting a bootstrap directive: %v", err)
	}
	if err := m.Save(map[string]string{"tls": "acme", "irc_host": "localhost"}, nil, "root"); err == nil || !strings.Contains(err.Error(), "Let's Encrypt") {
		t.Errorf("acme without a public host: %v", err)
	}
	f.fail(nil, errors.New("disk full"), nil)
	if err := m.Save(map[string]string{"max_clients": "2"}, nil, "root"); err == nil || err.Error() != "disk full" {
		t.Errorf("a store error: %v", err)
	}
	if m.Config().Limits.MaxClients != 0 {
		t.Error("a failed save was applied")
	}
}

func TestFieldsSources(t *testing.T) {
	st, path := setup(t, "metrics_url https://evrd:pw@vm.example.org\nirc_host file.example\n")
	m, err := New(st, load(t, path), "n1", quiet)
	if err != nil {
		t.Fatal(err)
	}
	env := func(k string) string {
		if k == "EVRD_METRICS_URL" {
			return "https://vm.env"
		}
		return ""
	}
	fs := m.Fields(env)
	if f := field(fs, "metrics_url"); f.Source != "env" || f.FileValue != "https://evrd:xxxxx@vm.example.org" || !f.InFile {
		t.Errorf("metrics_url: %+v", f)
	}
	if f := field(fs, "max_networks"); f.Source != "default" || f.InFile || f.FileValue != "0" {
		t.Errorf("max_networks: %+v", f)
	}
	if f := field(fs, "oidc_client_secret"); f.Set || f.Value != "" {
		t.Errorf("an unset secret: %+v", f)
	}
	if f := field(fs, "irc_host"); f.Source != "file" || f.Value != "file.example" {
		t.Errorf("irc_host: %+v", f)
	}
}

func TestBootstrap(t *testing.T) {
	st, path := setup(t, "listen ircs://:6697\nlisten wss://:8097\ndb postgres://evrd:hunter2@db/evrd\n"+
		"history scylla://h1,h2/evrd\nprovision_token tok\nsecret_key_file /k\nsettings_write_conf yes\nchroot /var/evrd\nuser _evrd\n")
	m, err := New(st, load(t, path), "n1", quiet)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, r := range m.Bootstrap() {
		got[r["key"]] = r["value"]
	}
	want := map[string]string{
		"listen": "ircs://:6697 wss://:8097", "db": "postgres://evrd:•••@db/evrd", "history": "scylla://h1,h2/evrd",
		"secret_key": "set", "provision_token": "set", "settings_write_conf": "yes", "chroot": "/var/evrd",
		"user": "_evrd", "config file": path,
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
	if strings.Contains(strings.Join(mapValues(got), " "), "hunter2") {
		t.Error("the database password is shown")
	}

	st2, path2 := setup(t, "")
	m2, _ := New(st2, load(t, path2), "n1", quiet)
	for _, r := range m2.Bootstrap() {
		switch r["key"] {
		case "secret_key", "provision_token":
			if r["value"] != "not set" {
				t.Errorf("%s: %q", r["key"], r["value"])
			}
		case "settings_write_conf":
			if r["value"] != "no" {
				t.Errorf("settings_write_conf: %q", r["value"])
			}
		}
	}
}

func mapValues(m map[string]string) []string {
	var out []string
	for _, v := range m {
		out = append(out, v)
	}
	return out
}

func TestRedactDSN(t *testing.T) {
	for in, want := range map[string]string{
		"postgres://evrd:pw@db/evrd":      "postgres://evrd:•••@db/evrd",
		"postgres://evrd:p@ss@db/evrd":    "postgres://evrd:•••@db/evrd",
		"postgres://evrd@db/evrd":         "postgres://evrd@db/evrd",
		"sqlite:/var/evrd/evrd.db":        "sqlite:/var/evrd/evrd.db",
		"user@host://x":                   "user@host://x",
		"pebble:/data/history":            "pebble:/data/history",
		"":                                "",
		"scylla://u:p@h1,h2/ks?consist=1": "scylla://u:•••@h1,h2/ks?consist=1",
	} {
		if got := redactDSN(in); got != want {
			t.Errorf("redactDSN(%q) = %q, want %q", in, got, want)
		}
	}
	if joinStrings(nil) != "" || joinStrings([]string{"a", "b"}) != "a b" {
		t.Error("joinStrings")
	}
}

func TestWriteConfRecordsStoreError(t *testing.T) {
	st, path := setup(t, "settings_write_conf yes\n")
	f := &failing{Store: st}
	f.fail(nil, nil, errors.New("no table"))
	// Recording the write failing isn't fatal: the file is still written.
	m, err := New(f, load(t, path), "n1", quiet)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Save(map[string]string{"max_clients": "2"}, nil, "root"); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); !strings.Contains(string(b), "max_clients 2") {
		t.Fatalf("file:\n%s", b)
	}
}
