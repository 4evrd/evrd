package http

import (
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/4evrd/evrd/internal/config"
)

// freeAddr is a local address nothing listens on.
func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	return addr
}

// runServer runs serve until the test ends, then checks it returned nil.
func runServer(t *testing.T, serve func(ctx context.Context) error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- serve(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("after shutdown: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("the server didn't stop")
		}
	})
}

func getUntilUp(t *testing.T, c *http.Client, url string) *http.Response {
	t.Helper()
	var res *http.Response
	eventually(t, url+" to answer", func() bool {
		var err error
		res, err = c.Get(url)
		return err == nil
	})
	return res
}

func TestListeners(t *testing.T) {
	s := newServer(t)

	addr := freeAddr(t)
	runServer(t, func(ctx context.Context) error { return s.ListenAndServe(ctx, addr) })
	res := getUntilUp(t, http.DefaultClient, "http://"+addr+"/api/config")
	res.Body.Close()
	if res.StatusCode != 200 || res.Header.Get("Strict-Transport-Security") != "" {
		t.Fatalf("plain API: %d %v", res.StatusCode, res.Header)
	}

	// HTTPS adds HSTS.
	ts := httptest.NewTLSServer(nil) // for a certificate the client trusts
	defer ts.Close()
	taddr := freeAddr(t)
	runServer(t, func(ctx context.Context) error {
		return s.ListenAndServeTLS(ctx, taddr, &tls.Config{Certificates: ts.TLS.Certificates})
	})
	res = getUntilUp(t, ts.Client(), "https://"+taddr+"/api/config")
	res.Body.Close()
	if res.StatusCode != 200 || res.Header.Get("Strict-Transport-Security") != "max-age=31536000" {
		t.Fatalf("HTTPS: %d %v", res.StatusCode, res.Header)
	}

	haddr := freeAddr(t)
	redirect := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://example.org/", http.StatusMovedPermanently)
	})
	runServer(t, func(ctx context.Context) error { return s.ListenAndServeHTTP(ctx, haddr, redirect) })
	noFollow := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res = getUntilUp(t, noFollow, "http://"+haddr+"/")
	res.Body.Close()
	if res.StatusCode != http.StatusMovedPermanently {
		t.Fatalf("HTTP redirect port: %d", res.StatusCode)
	}

	iaddr := freeAddr(t)
	s.SetCluster(soloCluster{}, clusterSecret)
	runServer(t, func(ctx context.Context) error { return s.ListenInternal(ctx, iaddr) })
	res = getUntilUp(t, http.DefaultClient, "http://"+iaddr+"/internal/usage")
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("internal endpoint without the secret: %d", res.StatusCode)
	}

	// An address in use is an error, not a hang.
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	defer ln.Close()
	if err := s.ListenAndServe(context.Background(), ln.Addr().String()); err == nil {
		t.Fatal("listening on a busy address succeeded")
	}
}

// With web_root set, the API serves the app too — but never the app's
// index.html for an unknown API or auth path.
func TestHandlerServesTheWebApp(t *testing.T) {
	s := newServer(t)
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("<app>"), 0o644)
	s.SetWebApp(dir)
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()
	get := func(path string) (int, string) {
		res, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return res.StatusCode, string(b)
	}
	if code, body := get("/networks/1"); code != 200 || body != "<app>" {
		t.Fatalf("app route: %d %q", code, body)
	}
	for _, p := range []string{"/api/nope", "/auth/nope"} {
		if code, body := get(p); code != 404 || strings.Contains(body, "<app>") {
			t.Errorf("%s: %d %q", p, code, body)
		}
	}
	if code, body := get("/api/config"); code != 200 || !strings.Contains(body, "passwordLogin") {
		t.Fatalf("the API still answers: %d %q", code, body)
	}
}

// The setters reach the handlers: a changed config is what /api/config
// answers.
func TestSetters(t *testing.T) {
	s := newServer(t)
	s.SetSettings(nil)
	s.SetCerts(nil)
	s.SetDirectory(nil)
	s.SetMetrics(nil)
	cfg := config.Default()
	cfg.IRCHost = "irc.example.org"
	s.SetConfig(cfg)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/api/config", nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"ircHost":"irc.example.org"`) {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

func TestWebChatRoute(t *testing.T) {
	for _, tc := range []struct {
		method, path string
		ok           bool
	}{
		{"GET", "/api/me", true},
		{"GET", "/api/networks", true},
		{"GET", "/api/networks/12", true},
		{"GET", "/api/networks/12/stream", true},
		{"GET", "/api/networks/12/messages", true},
		{"POST", "/api/networks/12/read", true},
		{"POST", "/auth/web-chat/revoke", true},
		{"POST", "/api/networks", false},
		{"PATCH", "/api/networks/12", false},
		{"GET", "/api/networks/export", false},
		{"GET", "/api/networks/x/stream", false},
		{"GET", "/api/networks/12/stream/x", false},
		{"GET", "/api/me/sessions", false},
		{"GET", "/auth/web-chat", false},
	} {
		if got := WebChatRoute(tc.method, tc.path); got != tc.ok {
			t.Errorf("%s %s: %v", tc.method, tc.path, got)
		}
	}
}
