package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/4evrd/evrd/internal/accounts"
	"github.com/4evrd/evrd/internal/auth"
	"github.com/4evrd/evrd/internal/bouncer"
	"github.com/4evrd/evrd/internal/cluster"
	"github.com/4evrd/evrd/internal/config"
	"github.com/4evrd/evrd/internal/history"
	"github.com/4evrd/evrd/internal/languages"
	"github.com/4evrd/evrd/internal/settings"
	"github.com/4evrd/evrd/internal/store"
)

type env struct {
	t   *testing.T
	srv *httptest.Server
	st  store.Store
	acc *accounts.Service
	api *Server
}

func newEnv(t *testing.T, mutate ...func(*config.Config)) *env {
	t.Helper()
	st, err := store.Open("sqlite:" + filepath.Join(t.TempDir(), "evrd.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return newEnvOn(t, st, mutate...)
}

// newEnvOn is newEnv on an existing store: several of them sharing one are
// the nodes of a cluster.
func newEnvOn(t *testing.T, st store.Store, mutate ...func(*config.Config)) *env {
	t.Helper()
	cfg := config.Default()
	cfg.Auth.CookieSecure = false
	cfg.Limits = config.Limits{MaxNetworks: 2, MaxClients: 3, BacklogLines: 100}
	for _, m := range mutate {
		m(cfg)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	acc := accounts.New(st, cfg.Limits)
	am, err := auth.New(context.Background(), st, acc, cfg.Auth, "", log)
	if err != nil {
		t.Fatal(err)
	}
	hist, err := history.Open(filepath.Join(t.TempDir(), "history"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { hist.Close() })
	b := bouncer.New(st, hist, log)
	api := NewServer(b, Services{Accounts: acc, Auth: am, Languages: languages.New(st), Cluster: cluster.NewStateService(st)}, cfg, nil, log)
	sm, err := settings.New(st, cfg, "test", log)
	if err != nil {
		t.Fatal(err)
	}
	api.SetSettings(sm)
	sm.OnChange(func(c *config.Config) { acc.SetDefaults(c.Limits); api.SetConfig(c) })
	srv := httptest.NewServer(api.Handler())
	t.Cleanup(srv.Close)
	return &env{t: t, srv: srv, st: st, acc: acc, api: api}
}

func (e *env) user(name, role string) store.User {
	e.t.Helper()
	u, err := e.acc.Create(accounts.CreateInput{Username: name, Password: name + "-password", Role: role})
	if err != nil {
		e.t.Fatal(err)
	}
	return u
}

// client is a browser: a cookie jar, same-origin requests.
type client struct {
	e *env
	c *http.Client
}

func (e *env) anon() *client {
	jar, _ := cookiejar.New(nil)
	return &client{e: e, c: &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

func (e *env) login(name string) *client {
	e.t.Helper()
	c := e.anon()
	if code, body := c.do("POST", "/auth/password", map[string]string{"username": name, "password": name + "-password"}); code != 200 {
		e.t.Fatalf("login %s: %d %s", name, code, body)
	}
	return c
}

func (c *client) do(method, path string, body any) (int, map[string]any) {
	c.e.t.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, c.e.srv.URL+path, r)
	req.Header.Set("Content-Type", "application/json")
	res, err := c.c.Do(req)
	if err != nil {
		c.e.t.Fatal(err)
	}
	defer res.Body.Close()
	var out map[string]any
	raw, _ := io.ReadAll(res.Body)
	_ = json.Unmarshal(raw, &out)
	return res.StatusCode, out
}

func TestEveryPrivateRouteNeedsASession(t *testing.T) {
	e := newEnv(t)
	c := e.anon()
	for _, rt := range []string{
		"GET /api/me", "PATCH /api/me", "DELETE /api/me", "POST /api/me/password",
		"GET /api/me/sessions", "DELETE /api/me/sessions/x", "POST /api/me/sessions/revoke-others",
		"GET /api/me/client-passwords", "POST /api/me/client-passwords", "DELETE /api/me/client-passwords/1",
		"GET /api/networks", "POST /api/networks", "PATCH /api/networks/1", "DELETE /api/networks/1",
		"POST /api/networks/1/connect", "POST /api/networks/1/disconnect",
		"GET /api/networks/1/messages", "GET /api/networks/1/buffers", "GET /api/networks/1/stream",
		"GET /api/admin/overview", "GET /api/admin/users", "POST /api/admin/users",
		"GET /api/admin/users/1", "PATCH /api/admin/users/1", "DELETE /api/admin/users/1",
		"POST /api/admin/users/1/password", "POST /api/admin/users/1/sessions/revoke",
		"GET /api/me/language-pack", "PUT /api/me/language-pack", "DELETE /api/me/language-pack",
		"PUT /api/admin/languages/de", "DELETE /api/admin/languages/de",
	} {
		method, path, _ := strings.Cut(rt, " ")
		if code, _ := c.do(method, path, map[string]any{}); code != http.StatusUnauthorized {
			t.Errorf("%s without a session: got %d, want 401", rt, code)
		}
	}
}

func TestAdminRoutesRefuseUsers(t *testing.T) {
	e := newEnv(t)
	e.user("root", store.RoleAdmin)
	e.user("alice", store.RoleUser)
	c := e.login("alice")
	for _, rt := range []string{"GET /api/admin/overview", "GET /api/admin/users", "POST /api/admin/users", "PATCH /api/admin/users/1", "DELETE /api/admin/users/1", "PUT /api/admin/languages/de", "DELETE /api/admin/languages/de"} {
		method, path, _ := strings.Cut(rt, " ")
		if code, _ := c.do(method, path, map[string]any{}); code != http.StatusForbidden {
			t.Errorf("%s as a user: got %d, want 403", rt, code)
		}
	}
}

func TestPasswordLogin(t *testing.T) {
	e := newEnv(t)
	e.user("alice", store.RoleUser)
	c := e.anon()
	if code, _ := c.do("POST", "/auth/password", map[string]string{"username": "alice", "password": "nope-nope"}); code != 401 {
		t.Fatalf("wrong password: got %d", code)
	}
	if code, _ := c.do("POST", "/auth/password", map[string]string{"username": "ALICE", "password": "alice-password"}); code != 200 {
		t.Fatalf("right password (any case): got %d", code)
	}
	code, me := c.do("GET", "/api/me", nil)
	if code != 200 || me["username"] != "alice" || me["role"] != "user" {
		t.Fatalf("me: %d %v", code, me)
	}
	if code, out := c.do("POST", "/auth/logout", nil); code != 200 || out["redirect"] != "/" {
		t.Fatalf("logout: %d %v", code, out)
	}
	if code, _ := c.do("GET", "/api/me", nil); code != 401 {
		t.Fatalf("after logout: got %d", code)
	}
}

func TestPasswordLoginCanBeTurnedOff(t *testing.T) {
	e := newEnv(t, func(c *config.Config) { c.Auth.PasswordLogin = false })
	e.user("alice", store.RoleUser)
	if code, _ := e.anon().do("POST", "/auth/password", map[string]string{"username": "alice", "password": "alice-password"}); code != 403 {
		t.Fatalf("got %d, want 403", code)
	}
}

func TestCrossSiteWritesAreRefused(t *testing.T) {
	e := newEnv(t)
	e.user("alice", store.RoleUser)
	c := e.login("alice")
	req, _ := http.NewRequest("POST", e.srv.URL+"/api/me/sessions/revoke-others", nil)
	req.Header.Set("Origin", "https://evil.example")
	res, err := c.c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 403 {
		t.Fatalf("cross-site POST: got %d, want 403", res.StatusCode)
	}
}

func TestNetworksAreScopedAndLimited(t *testing.T) {
	e := newEnv(t)
	e.user("alice", store.RoleUser)
	e.user("bob", store.RoleUser)
	alice, bob := e.login("alice"), e.login("bob")

	net := map[string]any{"name": "Libera", "host": "irc.libera.chat", "port": 6697, "tls": true}
	code, n := alice.do("POST", "/api/networks", net)
	if code != 200 || n["nick"] != "alice" {
		t.Fatalf("create: %d %v (nick should default to the username)", code, n)
	}
	id := int(n["id"].(float64))
	for _, rt := range []string{"DELETE", "GET"} {
		path := "/api/networks/" + strconv.Itoa(id)
		if rt == "GET" {
			path += "/messages"
		}
		if code, _ := bob.do(rt, path, nil); code != 404 {
			t.Errorf("bob %s alice's network: got %d, want 404", rt, code)
		}
	}
	if code, out := alice.do("POST", "/api/networks", net); code != 422 || !strings.Contains(out["error"].(string), "already have") {
		t.Fatalf("duplicate name: %d %v", code, out)
	}
	net["name"] = "OFTC"
	alice.do("POST", "/api/networks", net)
	net["name"] = "Third"
	if code, out := alice.do("POST", "/api/networks", net); code != 422 || !strings.Contains(out["error"].(string), "limit of 2") {
		t.Fatalf("third network over the limit: %d %v", code, out)
	}
}

func TestClientPasswords(t *testing.T) {
	e := newEnv(t)
	e.user("alice", store.RoleUser)
	c := e.login("alice")
	code, cp := c.do("POST", "/api/me/client-passwords", map[string]string{"name": "laptop irssi"})
	if code != 200 || cp["password"] == "" {
		t.Fatalf("create: %d %v", code, cp)
	}
	secret := cp["password"].(string)
	if _, err := ircLogin(e.acc, "alice", secret); err != nil {
		t.Fatalf("IRC login with the client password: %v", err)
	}
	if _, err := ircLogin(e.acc, "alice", "alice-password"); err != nil {
		t.Fatalf("IRC login with the account password: %v", err)
	}
	if code, _ := c.do("DELETE", "/api/me/client-passwords/"+strconv.Itoa(int(cp["id"].(float64))), nil); code != 204 {
		t.Fatalf("delete: %d", code)
	}
	if _, err := ircLogin(e.acc, "alice", secret); err == nil {
		t.Fatal("deleted client password still works")
	}
}

func TestChangePasswordSignsOutOtherSessions(t *testing.T) {
	e := newEnv(t)
	e.user("alice", store.RoleUser)
	here, there := e.login("alice"), e.login("alice")
	if code, _ := here.do("POST", "/api/me/password", map[string]string{"current": "wrong", "next": "a-new-password"}); code != 422 {
		t.Fatalf("wrong current password: got %d", code)
	}
	if code, out := here.do("POST", "/api/me/password", map[string]string{"current": "alice-password", "next": "a-new-password"}); code != 200 {
		t.Fatalf("change: %d %v", code, out)
	}
	if code, _ := here.do("GET", "/api/me", nil); code != 200 {
		t.Fatal("the session that changed the password was signed out")
	}
	if code, _ := there.do("GET", "/api/me", nil); code != 401 {
		t.Fatal("the other session is still signed in")
	}
}

func TestSuspendCutsOffAndLastAdminIsKept(t *testing.T) {
	e := newEnv(t)
	root := e.user("root", store.RoleAdmin)
	alice := e.user("alice", store.RoleUser)
	admin, ac := e.login("root"), e.login("alice")

	if code, out := admin.do("PATCH", "/api/admin/users/"+strconv.Itoa(int(alice.ID)), map[string]any{"suspended": true, "suspendedReason": "spam"}); code != 200 || out["suspended"] != true {
		t.Fatalf("suspend: %d %v", code, out)
	}
	if code, _ := ac.do("GET", "/api/me", nil); code != 401 {
		t.Fatal("suspended user's session still works")
	}
	if code, _ := e.anon().do("POST", "/auth/password", map[string]string{"username": "alice", "password": "alice-password"}); code != 403 {
		t.Fatal("suspended user can sign in")
	}
	if _, err := ircLogin(e.acc, "alice", "alice-password"); err != accounts.ErrSuspended {
		t.Fatalf("suspended user's IRC login: %v", err)
	}

	// The only admin can't demote, suspend or delete themselves away.
	if code, _ := admin.do("PATCH", "/api/admin/users/"+strconv.Itoa(int(root.ID)), map[string]any{"role": "user"}); code != 409 {
		t.Fatalf("demoting the last admin: got %d, want 409", code)
	}
	if code, _ := admin.do("PATCH", "/api/admin/users/"+strconv.Itoa(int(root.ID)), map[string]any{"suspended": true}); code != 422 {
		t.Fatalf("suspending yourself: got %d, want 422", code)
	}
	if code, _ := admin.do("DELETE", "/api/me", map[string]string{"confirm": "root"}); code != 409 {
		t.Fatalf("deleting the last admin: got %d, want 409", code)
	}
}

func TestAdminLimitsOverride(t *testing.T) {
	e := newEnv(t)
	e.user("root", store.RoleAdmin)
	alice := e.user("alice", store.RoleUser)
	admin := e.login("root")
	path := "/api/admin/users/" + strconv.Itoa(int(alice.ID))

	code, out := admin.do("PATCH", path, map[string]any{"limits": map[string]any{"maxNetworks": 0, "maxClients": nil, "backlogLines": 50}})
	if code != 200 {
		t.Fatalf("set limits: %d %v", code, out)
	}
	lim := out["limits"].(map[string]any)
	if lim["maxNetworks"] != 0.0 || lim["maxClients"] != 3.0 || lim["backlogLines"] != 50.0 {
		t.Fatalf("effective limits: %v", lim)
	}
	if code, _ := admin.do("PATCH", path, map[string]any{"limits": map[string]any{"maxNetworks": -1}}); code != 422 {
		t.Fatalf("negative limit: got %d", code)
	}
}

func TestAdminCreateAndDelete(t *testing.T) {
	e := newEnv(t)
	e.user("root", store.RoleAdmin)
	admin := e.login("root")
	if code, out := admin.do("POST", "/api/admin/users", map[string]any{"username": "Bad Name"}); code != 422 {
		t.Fatalf("invalid username: %d %v", code, out)
	}
	code, out := admin.do("POST", "/api/admin/users", map[string]any{"username": "carol", "password": "carol-password"})
	if code != 200 {
		t.Fatalf("create: %d %v", code, out)
	}
	if code, _ := admin.do("POST", "/api/admin/users", map[string]any{"username": "carol"}); code != 409 {
		t.Fatalf("duplicate: got %d", code)
	}
	carol := e.login("carol")
	if code, _ := admin.do("DELETE", "/api/admin/users/"+strconv.Itoa(int(out["id"].(float64))), nil); code != 204 {
		t.Fatalf("delete: %d", code)
	}
	if code, _ := carol.do("GET", "/api/me", nil); code != 401 {
		t.Fatal("deleted user's session still works")
	}
}

func TestUpdateNetwork(t *testing.T) {
	e := newEnv(t)
	e.user("alice", store.RoleUser)
	e.user("bob", store.RoleUser)
	alice, bob := e.login("alice"), e.login("bob")
	_, n := alice.do("POST", "/api/networks", map[string]any{"name": "libera", "host": "irc.libera.chat", "password": "secret"})
	_, other := alice.do("POST", "/api/networks", map[string]any{"name": "oftc", "host": "irc.oftc.net"})
	path := "/api/networks/" + strconv.Itoa(int(n["id"].(float64)))

	code, out := alice.do("PATCH", path, map[string]any{"nick": "al", "port": 6667, "tls": false, "channels": []string{"#a", " #b "}})
	if code != 200 || out["nick"] != "al" || out["port"] != 6667.0 || out["hasPassword"] != true {
		t.Fatalf("update: %d %v", code, out)
	}
	if ch := out["channels"].([]any); len(ch) != 2 || ch[1] != "#b" {
		t.Fatalf("channels: %v", ch)
	}
	if code, out := alice.do("PATCH", path, map[string]any{"password": ""}); code != 200 || out["hasPassword"] != false {
		t.Fatalf("clear password: %d %v", code, out)
	}
	if code, _ := alice.do("PATCH", path, map[string]any{"channels": []string{"nope"}}); code != 422 {
		t.Fatalf("bad channel: got %d", code)
	}
	if code, _ := alice.do("PATCH", path, map[string]any{"name": other["name"]}); code != 422 {
		t.Fatalf("duplicate name: got %d", code)
	}
	if code, _ := bob.do("PATCH", path, map[string]any{"nick": "x"}); code != 404 {
		t.Fatalf("someone else's network: got %d", code)
	}
}

func ircLogin(acc *accounts.Service, user, secret string) (store.User, error) {
	u, _, err := acc.IRCLogin(user, secret)
	return u, err
}
