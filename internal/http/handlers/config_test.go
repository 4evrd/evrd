package handlers_test

import (
	"bytes"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/4evrd/evrd/internal/config"
	"github.com/4evrd/evrd/internal/store"
)

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
	e2.api.SetSettings(nil) // before any request is served
	e2.user("root", store.RoleAdmin)
	admin2 := e2.login("root")
	if code, _ := admin2.do("GET", "/api/admin/settings", nil); code != 404 {
		t.Fatalf("get: %d", code)
	}
	if code, _ := admin2.do("PATCH", "/api/admin/settings", map[string]any{"set": map[string]string{"irc_host": "x"}}); code != 404 {
		t.Fatalf("save: %d", code)
	}
}
