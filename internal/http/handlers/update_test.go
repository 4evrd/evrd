package handlers_test

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/4evrd/evrd/internal/store"
	"github.com/4evrd/evrd/internal/update"
	"github.com/4evrd/evrd/internal/version"
)

func TestUpdates(t *testing.T) {
	old := version.Version
	version.Version = "0.1.0"
	t.Cleanup(func() { version.Version = old })
	feed := `{"tag_name":"v0.1.0"}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/latest" {
			http.NotFound(w, r) // no archive: an install fails
			return
		}
		io.WriteString(w, feed)
	}))
	defer srv.Close()

	e := newEnv(t)
	e.user("root", store.RoleAdmin)
	e.user("joe", store.RoleUser)
	admin := e.login("root")
	for _, rt := range []string{"GET /api/admin/update", "POST /api/admin/update/check", "POST /api/admin/update/install"} {
		m, p, _ := strings.Cut(rt, " ")
		if code, _ := admin.do(m, p, nil); code != 404 {
			t.Fatalf("%s without an updater: %d", rt, code)
		}
	}
	if _, set := admin.do("GET", "/api/admin/settings", nil); set["update"] != nil {
		t.Fatalf("settings without an updater: %v", set["update"])
	}

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	u := update.New(update.Options{Source: srv.URL + "/latest", Dir: t.TempDir(), Allowed: true, Mode: update.ModeExec}, nil, log, func(update.Restart) { t.Error("restarted") })
	e.api.SetUpdater(u)
	if code, _ := e.login("joe").do("GET", "/api/admin/update", nil); code != 403 {
		t.Fatalf("a user: %d", code)
	}
	code, st := admin.do("POST", "/api/admin/update/check", nil)
	if code != 200 || st["current"] != "0.1.0" || st["available"] != false || st["checkedAt"] == nil || st["latest"].(map[string]any)["version"] != "0.1.0" {
		t.Fatalf("check: %d %v", code, st)
	}
	if code, out := admin.do("POST", "/api/admin/update/install", nil); code != 409 || !strings.Contains(out["error"].(string), "no newer release") {
		t.Fatalf("install the same version: %d %v", code, out)
	}

	feed = `{"tag_name":"v0.2.0","html_url":"https://example.org/r","published_at":"2026-11-01T00:00:00Z"}`
	admin.do("POST", "/api/admin/update/check", nil)
	_, set := admin.do("GET", "/api/admin/settings", nil)
	st = set["update"].(map[string]any)
	if st["available"] != true || st["canInstall"] != true || st["latest"].(map[string]any)["url"] != "https://example.org/r" {
		t.Fatalf("settings: %v", st)
	}
	code, st = admin.do("POST", "/api/admin/update/install", nil)
	if code != 202 || st["requested"] != "0.2.0" {
		t.Fatalf("install: %d %v", code, st)
	}
	eventually(t, "the failed install", func() bool {
		_, st := admin.do("GET", "/api/admin/update", nil)
		return st["installError"] != "" && st["installing"] == ""
	})

	u.SetAllowed(false, "", "self_update is off")
	if code, out := admin.do("POST", "/api/admin/update/install", nil); code != 409 || !strings.Contains(out["error"].(string), "self_update is off") {
		t.Fatalf("install when it can't: %d %v", code, out)
	}
}
