package handlers_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/4evrd/evrd/internal/accounts"
	"github.com/4evrd/evrd/internal/auth"
	"github.com/4evrd/evrd/internal/bouncer"
	"github.com/4evrd/evrd/internal/cluster"
	"github.com/4evrd/evrd/internal/config"
	"github.com/4evrd/evrd/internal/history"
	evrdhttp "github.com/4evrd/evrd/internal/http"
	"github.com/4evrd/evrd/internal/languages"
	"github.com/4evrd/evrd/internal/managed"
)

// managedAPI is the API server in managed mode.
func managedAPI(t *testing.T) (*httptest.Server, *accounts.Service) {
	t.Helper()
	st := openStore(t)
	cfg := config.Default()
	cfg.Managed.On, cfg.Managed.Network, cfg.Managed.Host, cfg.Managed.Port = true, "ExampleNet", "127.0.0.1", 1
	cfg.Managed.Token = "api-token"
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	acc := accounts.New(st, cfg.Limits)
	am, err := auth.New(context.Background(), st, acc, cfg.Auth, "", log)
	if err != nil {
		t.Fatal(err)
	}
	hist, err := history.Open(filepath.Join(scratchDir(t), "history"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { hist.Close() })
	b := bouncer.New(st, hist, log)
	api := evrdhttp.New(b, evrdhttp.Services{Accounts: acc, Auth: am, Languages: languages.New(st), Cluster: cluster.NewStateService(st)}, cfg, nil, log)
	api.SetManaged(&managed.Provisioner{Mode: managed.New(cfg), Accounts: acc, Bouncer: b})
	srv := httptest.NewServer(api.Handler())
	t.Cleanup(srv.Close)
	return srv, acc
}

func managedCall(t *testing.T, srv *httptest.Server, token, method, path string, body any) (int, map[string]any) {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, srv.URL+path, rd)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(res.Body).Decode(&out)
	return res.StatusCode, out
}

func TestManagedAPI(t *testing.T) {
	srv, acc := managedAPI(t)
	call := func(method, path string, body any) (int, map[string]any) {
		return managedCall(t, srv, "api-token", method, path, body)
	}

	// the token, always
	for _, tok := range []string{"", "wrong"} {
		if code, _ := managedCall(t, srv, tok, "GET", "/api/managed/health", nil); code != 401 {
			t.Errorf("token %q: %d", tok, code)
		}
	}
	// nothing of the web app's API, sign-in or the web app itself
	for _, p := range []string{"/api/config", "/api/me", "/auth/login", "/api/admin/users", "/", "/api/provision/users/bob"} {
		if code, _ := call("GET", p, nil); code != 404 {
			t.Errorf("%s: %d", p, code)
		}
	}

	code, out := call("PUT", "/api/managed/users/Alice", map[string]any{
		"subject": "usr_1", "nick": "Alice", "realname": "Alice A", "channels": []string{"#chat"},
		"auth": map[string]any{"method": "sasl", "account": "alice", "password": "pw"},
	})
	if code != 201 || out["username"] != "alice" || out["nick"] != "Alice" || out["status"] != "disconnected" || out["password"] != nil {
		t.Fatalf("create: %d %v", code, out)
	}
	if a := out["auth"].(map[string]any); a["method"] != "sasl" || a["hasPassword"] != true {
		t.Errorf("auth %v", a)
	}
	if code, out = call("PUT", "/api/managed/users/alice", map[string]any{"disabled": true}); code != 200 || out["disabled"] != true {
		t.Errorf("disable: %d %v", code, out)
	}
	if code, out = call("GET", "/api/managed/users/alice", nil); code != 200 || out["subject"] != "usr_1" {
		t.Errorf("get: %d %v", code, out)
	}
	if code, out = call("GET", "/api/managed/users", nil); code != 200 || len(out["users"].([]any)) != 1 {
		t.Errorf("list: %d %v", code, out)
	}
	if code, out = call("GET", "/api/managed/health", nil); code != 200 || out["network"].(map[string]any)["name"] != "ExampleNet" ||
		out["users"].(map[string]any)["total"] != float64(1) || out["ok"] != false {
		t.Errorf("health: %d %v", code, out)
	}

	// refusals
	for name, tc := range map[string]struct {
		path string
		body any
		code int
	}{
		"bad nick":      {"/api/managed/users/bob", map[string]any{"nick": "a b"}, 422},
		"bad username":  {"/api/managed/users/no!", map[string]any{}, 422},
		"taken subject": {"/api/managed/users/bob", map[string]any{"subject": "usr_1"}, 409},
		"not json":      {"/api/managed/users/bob", "nope", 400},
	} {
		if code, out := call("PUT", tc.path, tc.body); code != tc.code {
			t.Errorf("%s: %d %v", name, code, out)
		}
	}
	if code, _ := call("GET", "/api/managed/users/nobody", nil); code != 404 {
		t.Errorf("missing: %d", code)
	}
	if code, _ := call("DELETE", "/api/managed/users/alice", nil); code != 200 {
		t.Errorf("delete: %d", code)
	}
	if _, err := acc.UserByUsername(context.Background(), "alice"); err == nil {
		t.Error("still there")
	}
	if code, _ := call("DELETE", "/api/managed/users/alice", nil); code != 404 {
		t.Errorf("delete again: %d", code)
	}
}
