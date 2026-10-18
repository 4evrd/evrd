package api

import (
	"strconv"
	"strings"
	"testing"

	"github.com/4evrd/evrd/internal/store"
)

func TestNetworkSettingsRoundTrip(t *testing.T) {
	e := newEnv(t)
	e.user("gus", store.RoleUser)
	c := e.login("gus")
	fp := strings.Repeat("0f", 32)
	code, n := c.do("POST", "/api/networks", map[string]any{
		"name": "libera", "nick": "gus", "altNick": "gus_", "username": "gident", "quitMsg": "bye",
		"servers": []map[string]any{
			{"host": "irc.libera.chat", "port": 6697, "tls": true, "password": "srvpass"},
			{"host": "irc2.libera.chat", "port": 6667, "tls": false},
		},
		"saslUser": "gusacct", "password": "saslpw", "trustPKI": false, "trustAllCerts": false,
		"fingerprints": []string{fp}, "encoding": "^windows-1252", "floodRate": 0, "floodBurst": 4, "joinDelay": 5,
		"channels": []string{"#a", "#b"}, "channelSettings": map[string]any{"#a": map[string]any{"key": "k", "detached": true}},
	})
	if code != 200 {
		t.Fatalf("create: %d %v", code, n)
	}
	id := strconv.Itoa(int(n["id"].(float64)))
	_, got := c.do("GET", "/api/networks/"+id, nil)
	servers := got["servers"].([]any)
	first := servers[0].(map[string]any)
	if len(servers) != 2 || first["host"] != "irc.libera.chat" || first["hasPassword"] != true || first["password"] != nil {
		t.Fatalf("servers (passwords hidden): %v", servers)
	}
	for k, want := range map[string]any{
		"altNick": "gus_", "username": "gident", "quitMsg": "bye", "saslUser": "gusacct", "trustPKI": false,
		"encoding": "^windows-1252", "floodRate": 0.0, "floodBurst": 4.0, "joinDelay": 5.0, "hasPassword": true,
		"host": "irc.libera.chat", "port": 6697.0,
	} {
		if got[k] != want {
			t.Errorf("%s = %v, want %v", k, got[k], want)
		}
	}
	if cs := got["channelSettings"].(map[string]any)["#a"].(map[string]any); cs["key"] != "k" || cs["detached"] != true {
		t.Errorf("channel settings: %v", cs)
	}

	// Editing the server list without a password keeps the saved one.
	code, out := c.do("PATCH", "/api/networks/"+id, map[string]any{"servers": []map[string]any{
		{"host": "irc2.libera.chat", "port": 6667}, {"host": "irc.libera.chat", "port": 6697, "tls": true},
	}})
	if code != 200 {
		t.Fatalf("reorder: %d %v", code, out)
	}
	nets, _ := e.st.ListNetworks(1)
	var saved store.Network
	for _, x := range nets {
		if x.Name == "libera" {
			saved = x
		}
	}
	if saved.Servers[1].Password != "srvpass" || saved.Host != "irc2.libera.chat" {
		t.Fatalf("after reorder: %+v host=%s", saved.Servers, saved.Host)
	}

	for _, bad := range []map[string]any{
		{"fingerprints": []string{"nope"}},
		{"encoding": "klingon"},
		{"servers": []map[string]any{}},
		{"bindHost": "192.0.2.1"}, // no bind hosts offered
	} {
		if code, _ := c.do("PATCH", "/api/networks/"+id, bad); code != 422 {
			t.Errorf("%v: got %d, want 422", bad, code)
		}
	}
}

func TestBindHostsAndUserSettings(t *testing.T) {
	e := newEnv(t)
	e.acc.SetBindHosts([]string{"192.0.2.1"})
	e.user("root", store.RoleAdmin)
	u := e.user("hal", store.RoleUser)
	c := e.login("hal")
	_, b := c.do("GET", "/api/bindhosts", nil)
	if hosts := b["hosts"].([]any); len(hosts) != 1 || b["locked"] != false {
		t.Fatalf("bindhosts: %v", b)
	}
	if code, out := c.do("PATCH", "/api/me", map[string]any{"settings": map[string]any{
		"bindHost": "192.0.2.1", "quitMsg": "later", "playbackLines": 100, "autoClearChanBuffer": true,
		"timestampFormat": "%H:%M", "timezone": "Europe/Berlin", "ctcpReplies": map[string]string{"version": "irssi"},
	}}); code != 200 {
		t.Fatalf("settings: %d %v", code, out)
	}
	_, me := c.do("GET", "/api/me", nil)
	s := me["settings"].(map[string]any)
	if s["bindHost"] != "192.0.2.1" || s["playbackLines"] != 100.0 || s["ctcpReplies"].(map[string]any)["VERSION"] != "irssi" || s["timezone"] != "Europe/Berlin" {
		t.Fatalf("saved settings: %v", s)
	}
	for _, bad := range []map[string]any{
		{"bindHost": "198.51.100.1"},
		{"timezone": "Mars/Olympus"},
		{"playbackLines": 9999},
		{"ctcpReplies": map[string]string{"two words": "x"}},
		{"allowedHosts": []string{"10.0.0.0/8"}}, // the test client is on 127.0.0.1: a lockout
	} {
		if code, _ := c.do("PATCH", "/api/me", map[string]any{"settings": bad}); code != 422 {
			t.Errorf("%v: got %d, want 422", bad, code)
		}
	}
	if code, out := c.do("PATCH", "/api/me", map[string]any{"settings": map[string]any{"allowedHosts": []string{"127.0.0.0/8"}}}); code != 200 {
		t.Fatalf("allowed hosts including this client: %d %v", code, out)
	}

	// An admin locks the bind host; the user can't change it any more.
	admin := e.login("root")
	if code, out := admin.do("PATCH", "/api/admin/users/"+strconv.FormatInt(u.ID, 10), map[string]any{
		"denySetBindHost": true, "settings": map[string]any{"bindHost": "203.0.113.5"},
	}); code != 200 {
		t.Fatalf("admin: %d %v", code, out)
	}
	_, b = c.do("GET", "/api/bindhosts", nil)
	if b["locked"] != true {
		t.Fatalf("locked: %v", b)
	}
	if code, _ := c.do("PATCH", "/api/me", map[string]any{"settings": map[string]any{"bindHost": "192.0.2.1"}}); code != 422 {
		t.Fatal("a locked user changed their bind host")
	}
}

func TestAllowedHostsBlockSignIn(t *testing.T) {
	e := newEnv(t)
	u := e.user("ivy", store.RoleUser)
	u.AllowedHosts = []string{"10.0.0.0/8"}
	e.st.UpdateUser(u)
	if code, out := e.anon().do("POST", "/auth/password", map[string]string{"username": "ivy", "password": "ivy-password"}); code != 403 {
		t.Fatalf("sign-in from outside the allowed hosts: %d %v", code, out)
	}
}
