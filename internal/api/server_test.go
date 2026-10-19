package api

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
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
	"github.com/4evrd/evrd/internal/store"
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
	e := newEnv(t)

	addr := freeAddr(t)
	runServer(t, func(ctx context.Context) error { return e.api.ListenAndServe(ctx, addr) })
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
		return e.api.ListenAndServeTLS(ctx, taddr, &tls.Config{Certificates: ts.TLS.Certificates})
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
	runServer(t, func(ctx context.Context) error { return e.api.ListenAndServeHTTP(ctx, haddr, redirect) })
	noFollow := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res = getUntilUp(t, noFollow, "http://"+haddr+"/")
	res.Body.Close()
	if res.StatusCode != http.StatusMovedPermanently {
		t.Fatalf("HTTP redirect port: %d", res.StatusCode)
	}

	iaddr := freeAddr(t)
	e.api.SetCluster(&fakeCluster{self: store.Node{ID: "me"}, p: &placement{owner: map[int64]store.Node{}}}, clusterSecret)
	runServer(t, func(ctx context.Context) error { return e.api.ListenInternal(ctx, iaddr) })
	res = getUntilUp(t, http.DefaultClient, "http://"+iaddr+"/internal/usage")
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("internal endpoint without the secret: %d", res.StatusCode)
	}

	// An address in use is an error, not a hang.
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	defer ln.Close()
	if err := e.api.ListenAndServe(context.Background(), ln.Addr().String()); err == nil {
		t.Fatal("listening on a busy address succeeded")
	}
}

// With web_root set, the API serves the app too — but never the app's
// index.html for an unknown API or auth path.
func TestHandlerServesTheWebApp(t *testing.T) {
	e := newEnv(t)
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("<app>"), 0o644)
	e.api.SetWebApp(dir)
	srv := httptest.NewServer(e.api.Handler())
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

func TestConfigEndpoint(t *testing.T) {
	e := newEnv(t, func(c *config.Config) {
		c.IRCHost = "irc.example.org"
		c.Listen = []string{"irc://:6667", "ircs://0.0.0.0:6697", ":7000", "tls://[::]:7001", "irc+insecure://:6668", "unix:///run/evrd.sock", "ws://:80", "irc://nope"}
	})
	_, cfg := e.anon().do("GET", "/api/config", nil)
	ports := []string{}
	for _, p := range cfg["ircPorts"].([]any) {
		pm := p.(map[string]any)
		p := fmt.Sprint(pm["port"])
		if pm["tls"] == true {
			p += "s"
		}
		ports = append(ports, p)
	}
	if got := strings.Join(ports, ","); got != "6697s,7001s,6667,7000,6668" {
		t.Fatalf("ports (TLS first, others dropped): %s", got)
	}
	if cfg["ircHost"] != "irc.example.org" || cfg["passwordLogin"] != true || cfg["sso"] != false || cfg["ircSelfSigned"] != true {
		t.Fatalf("config: %v", cfg)
	}
}

func TestSettingsUnavailable(t *testing.T) {
	e := newEnv(t)
	e.user("root", store.RoleAdmin)
	admin := e.login("root")
	req, _ := http.NewRequest("PATCH", e.srv.URL+"/api/admin/settings", bytes.NewReader([]byte("{")))
	res, err := admin.c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 400 {
		t.Fatalf("bad body: %d", res.StatusCode)
	}

	// Without a settings manager, the page says it isn't there.
	e2 := newEnv(t)
	e2.api.settings = nil // before any request is served
	e2.user("root", store.RoleAdmin)
	admin2 := e2.login("root")
	if code, _ := admin2.do("GET", "/api/admin/settings", nil); code != 404 {
		t.Fatalf("get: %d", code)
	}
	if code, _ := admin2.do("PATCH", "/api/admin/settings", map[string]any{"set": map[string]string{"irc_host": "x"}}); code != 404 {
		t.Fatalf("save: %d", code)
	}
}
