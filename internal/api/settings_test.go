package api

import (
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
