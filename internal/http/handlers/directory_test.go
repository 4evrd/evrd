package handlers_test

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/4evrd/evrd/internal/directory"
	"github.com/4evrd/evrd/internal/store"
)

const testDirectory = `{"version": 1, "updated": "2026-10-19T00:00:00Z", "networks": [
  {"id": "dev", "name": "Dev ircd", "servers": [{"host": "ircd", "port": 6667, "tls": false}], "channels": ["#dev"]}
]}`

func TestDirectory(t *testing.T) {
	e := newEnv(t)
	e.user("root", store.RoleAdmin)
	e.user("joe", store.RoleUser)
	if code, _ := e.anon().do("GET", "/api/directory", nil); code != 401 {
		t.Fatalf("signed out: %d", code)
	}
	joe := e.login("joe")
	// none set: an empty list
	code, out := joe.do("GET", "/api/directory", nil)
	if code != 200 || len(out["networks"].([]any)) != 0 {
		t.Fatalf("without a directory: %d %v", code, out)
	}
	if code, _ := e.login("root").do("POST", "/api/admin/directory/refresh", nil); code != 404 {
		t.Fatalf("refresh without a directory: %d", code)
	}

	path := filepath.Join(t.TempDir(), "networks.json")
	os.WriteFile(path, []byte(testDirectory), 0o644)
	d := directory.New(path, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	e.api.SetDirectory(d)
	_, out = joe.do("GET", "/api/directory", nil)
	if len(out["networks"].([]any)) != 0 || out["source"] != path || out["fetchedAt"] != nil {
		t.Fatalf("not fetched yet: %v", out)
	}
	if err := d.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	_, out = joe.do("GET", "/api/directory", nil)
	nets := out["networks"].([]any)
	if len(nets) != 1 || nets[0].(map[string]any)["id"] != "dev" || out["updated"] == nil || out["fetchedAt"] == nil {
		t.Fatalf("directory: %v", out)
	}

	// admins see how it's doing, and can check again now
	if code, _ := joe.do("POST", "/api/admin/directory/refresh", nil); code != 403 {
		t.Fatalf("a user refreshed: %d", code)
	}
	admin := e.login("root")
	_, set := admin.do("GET", "/api/admin/settings", nil)
	if st := set["directory"].(map[string]any); st["networks"] != 1.0 || st["error"] != "" {
		t.Fatalf("status: %v", st)
	}
	os.WriteFile(path, []byte(`{"broken`), 0o644)
	code, st := admin.do("POST", "/api/admin/directory/refresh", nil)
	if code != 200 || st["error"] == "" || st["networks"] != 1.0 {
		t.Fatalf("refresh of a broken file: %d %v", code, st)
	}
}
