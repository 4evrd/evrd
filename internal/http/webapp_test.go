package http

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestWebApp(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("index.html", "app")
	write("favicon.svg", "icon")
	write("_app/immutable/x.js", "js")
	h := webApp(dir)

	for _, c := range []struct {
		method, path string
		code         int
		body, cache  string
	}{
		{"GET", "/", 200, "app", "no-cache"},
		{"GET", "/networks/3", 200, "app", "no-cache"}, // the app's own route
		{"GET", "/favicon.svg", 200, "icon", ""},
		{"GET", "/_app/immutable/x.js", 200, "js", "public, max-age=31536000, immutable"},
		{"GET", "/_app", 200, "app", "no-cache"},  // a directory
		{"GET", "/../../etc/passwd", 400, "", ""}, // never outside dir
		{"POST", "/", 405, "", ""},
	} {
		req := httptest.NewRequest(c.method, c.path, nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		body, _ := io.ReadAll(rec.Body)
		if rec.Code != c.code || (c.body != "" && string(body) != c.body) {
			t.Errorf("%s %s: %d %q, want %d %q", c.method, c.path, rec.Code, body, c.code, c.body)
		}
		if c.code == http.StatusOK && rec.Header().Get("Cache-Control") != c.cache {
			t.Errorf("%s %s: Cache-Control %q, want %q", c.method, c.path, rec.Header().Get("Cache-Control"), c.cache)
		}
	}
}
