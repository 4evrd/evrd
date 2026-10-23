package handlers_test

import (
	"strconv"
	"testing"

	"github.com/4evrd/evrd/internal/store"
)

func userPath(u store.User) string { return "/api/admin/users/" + strconv.FormatInt(u.ID, 10) }

func TestAdminListAndGetUsers(t *testing.T) {
	e := newEnv(t)
	e.user("root", store.RoleAdmin)
	alice := e.user("alice", store.RoleUser)
	spam := e.user("spam", store.RoleUser)
	admin, ac := e.login("root"), e.login("alice")
	path, _, _ := e.liveNetwork(ac, "fake")
	ac.do("POST", "/api/me/client-passwords", map[string]string{"name": "laptop"})
	admin.do("PATCH", userPath(spam), map[string]any{"suspended": true})
	ws, _, err := ac.stream(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	next(t, ws) // ready: alice is online

	code, out := admin.doAny("GET", "/api/admin/users", nil)
	if code != 200 {
		t.Fatalf("list: %d %v", code, out)
	}
	byName := map[string]map[string]any{}
	for _, u := range out.([]any) {
		byName[u.(map[string]any)["username"].(string)] = u.(map[string]any)
	}
	if len(byName) != 3 {
		t.Fatalf("users: %v", out)
	}
	if a := byName["alice"]; a["status"] != "online" || a["networks"] != 1.0 || a["online"] != 1.0 || a["clients"] != 0.0 {
		t.Fatalf("alice: %v", a)
	}
	if byName["spam"]["status"] != "suspended" || byName["root"]["status"] != "offline" || byName["root"]["role"] != "admin" {
		t.Fatalf("statuses: %v", byName)
	}

	code, u := admin.do("GET", userPath(alice), nil)
	if code != 200 || u["username"] != "alice" || u["clientPasswords"] != 1.0 {
		t.Fatalf("get: %d %v", code, u)
	}
	if nets := u["networks"].([]any); len(nets) != 1 || nets[0].(map[string]any)["status"] != "connected" {
		t.Fatalf("networks: %v", u["networks"])
	}
	if s := u["sessions"].([]any); len(s) != 1 || s[0].(map[string]any)["current"] != false {
		t.Fatalf("sessions: %v", u["sessions"])
	}
	if o := u["overrides"].(map[string]any); o["maxNetworks"] != nil {
		t.Fatalf("overrides: %v", o)
	}
	if code, _ := admin.do("GET", "/api/admin/users/9999", nil); code != 404 {
		t.Fatalf("missing user: %d", code)
	}

	code, ov := admin.do("GET", userPath(alice)+"/overview", nil)
	if code != 200 {
		t.Fatalf("user overview: %d %v", code, ov)
	}
	if ov["usage"].(map[string]any)["networks"] != 1.0 || len(ov["sessions"].([]any)) != 1 {
		t.Fatalf("user overview: %v", ov)
	}
	if pws := ov["clientPasswords"].([]any); len(pws) != 1 || pws[0].(map[string]any)["name"] != "laptop" {
		t.Fatalf("client passwords: %v", pws)
	}
	if code, _ := admin.do("GET", "/api/admin/users/9999/overview", nil); code != 404 {
		t.Fatalf("missing user's overview: %d", code)
	}
	if code, _ := ac.do("GET", userPath(alice)+"/overview", nil); code != 403 {
		t.Fatalf("a user reading an admin page: %d", code)
	}
}

func TestAdminUpdateUser(t *testing.T) {
	e := newEnv(t)
	e.user("root", store.RoleAdmin)
	alice := e.user("alice", store.RoleUser)
	admin := e.login("root")
	ac := e.login("alice")
	_, _, nid := e.liveNetwork(ac, "fake")

	if code, _ := admin.do("PATCH", userPath(alice), map[string]any{"role": "god"}); code != 422 {
		t.Fatalf("bad role: %d", code)
	}
	if code, _ := admin.do("PATCH", "/api/admin/users/9999", map[string]any{"role": "user"}); code != 404 {
		t.Fatalf("missing user: %d", code)
	}
	code, out := admin.do("PATCH", userPath(alice), map[string]any{"email": " a@example.org ", "denySetBindHost": true, "settings": map[string]any{"quitMsg": "later"}})
	if code != 200 || out["email"] != "a@example.org" {
		t.Fatalf("update: %d %v", code, out)
	}
	if u, _ := e.st.UserByID(t.Context(), alice.ID); !u.DenySetBindHost || u.QuitMsg != "later" {
		t.Fatalf("stored: %+v", u)
	}

	// Suspending stops the user's networks; unsuspending brings them back.
	if code, out := admin.do("PATCH", userPath(alice), map[string]any{"suspended": true, "suspendedReason": " spam "}); code != 200 || out["suspendedReason"] != "spam" {
		t.Fatalf("suspend: %d %v", code, out)
	}
	if _, live := e.b.ConnFor(nid); live {
		t.Fatal("a suspended user's network is still running")
	}
	if code, out := admin.do("PATCH", userPath(alice), map[string]any{"suspended": false}); code != 200 || out["suspended"] != false || out["suspendedReason"] != "" {
		t.Fatalf("unsuspend: %d %v", code, out)
	}
	if _, live := e.b.ConnFor(nid); !live {
		t.Fatal("unsuspending didn't resume the user's network")
	}

	// Promote, then the former only admin can step down.
	if code, out := admin.do("PATCH", userPath(alice), map[string]any{"role": "admin"}); code != 200 || out["role"] != "admin" {
		t.Fatalf("promote: %d %v", code, out)
	}
	root, _ := e.st.UserByUsername(t.Context(), "root")
	if code, out := admin.do("PATCH", userPath(root), map[string]any{"role": "user"}); code != 200 || out["role"] != "user" {
		t.Fatalf("step down: %d %v", code, out)
	}
	if code, _ := admin.do("GET", "/api/admin/users", nil); code != 403 {
		t.Fatalf("a demoted admin still has admin access: %d", code)
	}
}

func TestAdminSetPasswordAndRevokeSessions(t *testing.T) {
	e := newEnv(t)
	root := e.user("root", store.RoleAdmin)
	alice := e.user("alice", store.RoleUser)
	admin, other := e.login("root"), e.login("root")
	ac := e.login("alice")

	if code, _ := admin.do("POST", userPath(alice)+"/password", map[string]string{"password": "short"}); code != 422 {
		t.Fatalf("short password: %d", code)
	}
	code, out := admin.do("POST", userPath(alice)+"/password", map[string]string{"password": "chosen-by-admin"})
	if code != 200 || out["password"] != nil {
		t.Fatalf("chosen password (not echoed): %d %v", code, out)
	}
	if code, _ := ac.do("GET", "/api/me", nil); code != 401 {
		t.Fatal("alice's session survived a password reset")
	}
	if code, _ := admin.do("POST", "/api/admin/users/9999/password", nil); code != 404 {
		t.Fatalf("missing user: %d", code)
	}
	// Resetting your own keeps you signed in here.
	if code, _ := admin.do("POST", userPath(root)+"/password", map[string]string{"password": "root-new-password"}); code != 200 {
		t.Fatal("own reset")
	}
	if code, _ := admin.do("GET", "/api/me", nil); code != 200 {
		t.Fatal("own reset signed this session out")
	}
	if code, _ := other.do("GET", "/api/me", nil); code != 401 {
		t.Fatal("own reset kept the other session")
	}

	ac = e.anon()
	ac.do("POST", "/auth/password", map[string]string{"username": "alice", "password": "chosen-by-admin"})
	if code, _ := admin.do("POST", userPath(alice)+"/sessions/revoke", nil); code != 204 {
		t.Fatalf("revoke: %d", code)
	}
	if code, _ := ac.do("GET", "/api/me", nil); code != 401 {
		t.Fatal("revoked session still works")
	}
	other = e.anon()
	other.do("POST", "/auth/password", map[string]string{"username": "root", "password": "root-new-password"})
	if code, _ := admin.do("POST", userPath(root)+"/sessions/revoke", nil); code != 204 {
		t.Fatal("revoke own")
	}
	if code, _ := admin.do("GET", "/api/me", nil); code != 200 {
		t.Fatal("revoking your own sessions signed this one out")
	}
	if code, _ := other.do("GET", "/api/me", nil); code != 401 {
		t.Fatal("revoking your own sessions kept another")
	}
	if code, _ := admin.do("POST", "/api/admin/users/9999/sessions/revoke", nil); code != 404 {
		t.Fatalf("missing user: %d", code)
	}
}

func TestAdminDeleteUserGuards(t *testing.T) {
	e := newEnv(t)
	root := e.user("root", store.RoleAdmin)
	root2 := e.user("root2", store.RoleAdmin)
	admin := e.login("root")
	if code, out := admin.do("DELETE", userPath(root), nil); code != 422 {
		t.Fatalf("deleting yourself here: %d %v", code, out)
	}
	if code, _ := admin.do("DELETE", "/api/admin/users/9999", nil); code != 404 {
		t.Fatalf("missing user: %d", code)
	}
	if code, _ := admin.do("DELETE", userPath(root2), nil); code != 204 {
		t.Fatalf("delete another admin: %d", code)
	}
	if _, err := e.st.UserByID(t.Context(), root2.ID); err == nil {
		t.Fatal("still there")
	}
}
