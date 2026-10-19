package api

import (
	"bytes"
	"strings"
	"testing"

	"github.com/4evrd/evrd/internal/store"
)

func TestAdminSettings(t *testing.T) {
	e := newEnv(t)
	e.user("root", store.RoleAdmin)
	e.user("joe", store.RoleUser)
	if code, _ := e.login("joe").do("GET", "/api/admin/settings", nil); code != 403 {
		t.Fatalf("a user read the settings: %d", code)
	}
	admin := e.login("root")
	code, out := admin.do("GET", "/api/admin/settings", nil)
	if code != 200 || len(out["fields"].([]any)) == 0 || out["bootstrap"] == nil {
		t.Fatalf("get: %d %v", code, out)
	}
	code, out = admin.do("PATCH", "/api/admin/settings", map[string]any{"set": map[string]string{"max_networks": "4", "irc_host": "irc.example.org"}})
	if code != 200 {
		t.Fatalf("save: %d %v", code, out)
	}
	_, cfg := e.anon().do("GET", "/api/config", nil)
	if cfg["ircHost"] != "irc.example.org" {
		t.Fatalf("irc_host applies live: %v", cfg)
	}
	_, me := admin.do("GET", "/api/me", nil)
	if me["limits"].(map[string]any)["maxNetworks"] != 4.0 {
		t.Fatalf("limits apply live: %v", me["limits"])
	}
	if code, out := admin.do("PATCH", "/api/admin/settings", map[string]any{"set": map[string]string{"session_ttl": "forever"}}); code != 422 {
		t.Fatalf("bad value: %d %v", code, out)
	}
	if code, _ := admin.do("PATCH", "/api/admin/settings", map[string]any{"set": map[string]string{"db": "sqlite:x"}}); code != 422 {
		t.Fatal("a bootstrap setting was changed")
	}
	admin.do("PATCH", "/api/admin/settings", map[string]any{"reset": []string{"max_networks"}})
	_, me = admin.do("GET", "/api/me", nil)
	if me["limits"].(map[string]any)["maxNetworks"] != 2.0 { // the test env's default
		t.Fatalf("reset: %v", me["limits"])
	}
}

func TestTLSSettings(t *testing.T) {
	e := newEnv(t)
	e.user("root", store.RoleAdmin)
	admin := e.login("root")
	_, out := admin.do("GET", "/api/admin/settings", nil)
	tlsOut, _ := out["tls"].(map[string]any)
	if tlsOut == nil || tlsOut["httpsListen"] != "" {
		t.Fatalf("tls: %v", out["tls"])
	}
	// Let's Encrypt can't sign localhost: refused, not saved.
	set := func(kv map[string]string) (int, map[string]any) {
		return admin.do("PATCH", "/api/admin/settings", map[string]any{"set": kv})
	}
	if code, out := set(map[string]string{"tls": "acme"}); code != 422 || !strings.Contains(out["error"].(string), "public host name") {
		t.Fatalf("acme with localhost: %d %v", code, out)
	}
	if code, out := set(map[string]string{"tls": "acme", "irc_host": "irc.example.org"}); code != 200 {
		t.Fatalf("acme with a public host: %d %v", code, out)
	}
	if code, _ := set(map[string]string{"tls": "files"}); code != 422 {
		t.Fatalf("files without paths: %d", code)
	}
	if code, _ := set(map[string]string{"tls": "letsencrypt"}); code != 422 {
		t.Fatalf("an unknown source: %d", code)
	}
}

// The forms mark encrypted fields only when a key is set.
func TestConfigSaysWhetherSecretsAreEncrypted(t *testing.T) {
	e := newEnv(t)
	t.Cleanup(func() { store.SetSecretKeys(nil) })
	store.SetSecretKeys(nil)
	if _, cfg := e.anon().do("GET", "/api/config", nil); cfg["secretsEncrypted"] != false {
		t.Fatalf("without a key: %v", cfg["secretsEncrypted"])
	}
	store.SetSecretKeys(bytes.Repeat([]byte{3}, 32))
	if _, cfg := e.anon().do("GET", "/api/config", nil); cfg["secretsEncrypted"] != true {
		t.Fatalf("with a key: %v", cfg["secretsEncrypted"])
	}
}
