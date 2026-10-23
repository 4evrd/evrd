package handlers_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"testing"

	"github.com/4evrd/evrd/internal/store"
)

// doAny is do for endpoints that answer with something other than an object.
func (c *client) doAny(method, path string, body any) (int, any) {
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
	var out any
	raw, _ := io.ReadAll(res.Body)
	_ = json.Unmarshal(raw, &out)
	return res.StatusCode, out
}

func TestTemporaryPasswordMustBeChanged(t *testing.T) {
	e := newEnv(t)
	e.user("root", store.RoleAdmin)
	u := e.user("dana", store.RoleUser)
	admin := e.login("root")

	code, out := admin.do("POST", "/api/admin/users/"+strconv.FormatInt(u.ID, 10)+"/password", nil)
	if code != 200 {
		t.Fatalf("reset: %d %v", code, out)
	}
	temp, _ := out["password"].(string)
	if len(temp) < 10 {
		t.Fatalf("generated password too short: %q", temp)
	}
	// The old password stops working; the new one signs in but must be changed.
	if code, _ := e.anon().do("POST", "/auth/password", map[string]string{"username": "dana", "password": "dana-password"}); code == 200 {
		t.Fatal("old password still works")
	}
	c := e.anon()
	if code, out := c.do("POST", "/auth/password", map[string]string{"username": "dana", "password": temp}); code != 200 {
		t.Fatalf("temporary login: %d %v", code, out)
	}
	if _, me := c.do("GET", "/api/me", nil); me["mustChangePassword"] != true {
		t.Fatalf("mustChangePassword not set: %v", me)
	}
	if code, out := c.do("POST", "/api/me/password", map[string]string{"current": temp, "next": "short"}); code != 422 {
		t.Fatalf("short password accepted: %d %v", code, out)
	}
	if code, out := c.do("POST", "/api/me/password", map[string]string{"current": temp, "next": "dana-new-password"}); code != 200 {
		t.Fatalf("change: %d %v", code, out)
	}
	if _, me := c.do("GET", "/api/me", nil); me["mustChangePassword"] != false {
		t.Fatalf("mustChangePassword still set: %v", me)
	}
}

func TestProfileDefaults(t *testing.T) {
	e := newEnv(t)
	e.user("erin", store.RoleUser)
	c := e.login("erin")
	if code, out := c.do("PATCH", "/api/me", map[string]any{"defaultAltNick": "erin_", "autoAway": true}); code != 200 {
		t.Fatalf("patch: %d %v", code, out)
	}
	_, me := c.do("GET", "/api/me", nil)
	if me["defaultAltNick"] != "erin_" || me["autoAway"] != true {
		t.Fatalf("defaults not saved: %v", me)
	}
}

func TestOverviewWithoutMetrics(t *testing.T) {
	e := newEnv(t)
	e.user("fay", store.RoleUser)
	c := e.login("fay")
	if code, out := c.do("POST", "/api/networks", map[string]any{"name": "lib", "host": "irc.example.org", "port": 6697, "tls": true, "nick": "fay", "channels": []string{"#a"}, "enabled": false}); code != 200 && code != 201 {
		t.Fatalf("add network: %d %v", code, out)
	}
	code, ov := c.do("GET", "/api/overview", nil)
	if code != 200 {
		t.Fatalf("overview: %d %v", code, ov)
	}
	nets, _ := ov["networks"].([]any)
	if len(nets) != 1 {
		t.Fatalf("networks: %v", ov["networks"])
	}
	if tr, _ := ov["traffic"].(map[string]any); tr == nil || tr["available"] != false {
		t.Fatalf("traffic without VictoriaMetrics should be unavailable: %v", ov["traffic"])
	}
	if code, _ := c.doAny("GET", "/api/me/clients", nil); code != 200 {
		t.Fatalf("clients: %d", code)
	}
	if code, _ := c.doAny("GET", "/api/me/events?limit=5", nil); code != 200 {
		t.Fatalf("events: %d", code)
	}
}

func TestAdminOverview(t *testing.T) {
	e := newEnv(t)
	e.user("root", store.RoleAdmin)
	s := e.user("spam", store.RoleUser)
	admin := e.login("root")
	admin.do("PATCH", "/api/admin/users/"+strconv.FormatInt(s.ID, 10), map[string]any{"suspended": true})

	code, ov := admin.do("GET", "/api/admin/overview", nil)
	if code != 200 {
		t.Fatalf("overview: %d %v", code, ov)
	}
	if ov["users"] != 2.0 || ov["admins"] != 1.0 || ov["suspended"] != 1.0 {
		t.Fatalf("counts: %v", ov)
	}
	attn, _ := ov["attention"].([]any)
	found := false
	for _, a := range attn {
		if a.(map[string]any)["kind"] == "suspended" {
			found = true
		}
	}
	if !found {
		t.Fatalf("suspended user not flagged: %v", attn)
	}
	st, _ := ov["storage"].(map[string]any)
	if acc, _ := st["accounts"].(map[string]any); acc["up"] != true {
		t.Fatalf("storage: %v", st)
	}
	if code, _ := e.login("root").do("GET", "/api/admin/overview", nil); code != 200 {
		t.Fatal("second admin session")
	}
	if code, _ := e.anon().do("GET", "/api/admin/overview", nil); code != 401 {
		t.Fatalf("anonymous overview: %d", code)
	}
}
