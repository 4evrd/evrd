package handlers_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/4evrd/evrd/internal/store"
)

func (c *client) raw(path string) (*http.Response, []byte) {
	c.e.t.Helper()
	res, err := c.c.Get(c.e.srv.URL + path)
	if err != nil {
		c.e.t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res, b
}

func TestExportImportNetworks(t *testing.T) {
	t.Cleanup(func() { store.SetSecretKeys(nil) })
	store.SetSecretKeys(bytes.Repeat([]byte{9}, 32))
	e := newEnv(t)
	e.user("alice", store.RoleUser)
	e.user("bob", store.RoleUser)
	alice, bob := e.login("alice"), e.login("bob")
	code, n := alice.do("POST", "/api/networks", map[string]any{
		"name": "libera", "password": "saslpass", "authMethod": "sasl",
		"servers":         []map[string]any{{"host": "irc.libera.chat", "port": 6697, "tls": true, "password": "serverpass"}},
		"channels":        []string{"#evrd"},
		"channelSettings": map[string]any{"#evrd": map[string]any{"key": "chankey"}},
	})
	if code != 200 {
		t.Fatalf("create: %d %v", code, n)
	}
	res, file := alice.raw("/api/networks/export")
	if res.StatusCode != 200 || res.Header.Get("X-Evrd-Secrets") != "sealed" || !strings.Contains(res.Header.Get("Content-Disposition"), "attachment") {
		t.Fatalf("export: %d %v\n%s", res.StatusCode, res.Header, file)
	}
	for _, s := range []string{"saslpass", "serverpass", "chankey"} {
		if bytes.Contains(file, []byte(s)) {
			t.Errorf("export has %q:\n%s", s, file)
		}
	}

	code, pv := bob.do("POST", "/api/networks/import/preview", map[string]any{"text": string(file)})
	if code != 200 || pv["format"] != "evrd" {
		t.Fatalf("preview: %d %v", code, pv)
	}
	pn := pv["users"].([]any)[0].(map[string]any)["networks"].([]any)[0].(map[string]any)
	if pn["name"] != "libera" || pn["exists"] != false || pn["channels"].(float64) != 1 || len(pn["notes"].([]any)) != 0 {
		t.Errorf("previewed network: %v", pn)
	}
	if strings.Contains(strings.ToLower(fmtAny(pv)), "pass") {
		t.Errorf("the preview sends secrets back: %v", pv)
	}
	imp := func(c *client, body map[string]any) map[string]any {
		t.Helper()
		code, out := c.do("POST", "/api/networks/import", body)
		if code != 200 {
			t.Fatalf("import: %d %v", code, out)
		}
		return out["results"].([]any)[0].(map[string]any)
	}
	if r := imp(bob, map[string]any{"text": string(file), "networks": []string{"libera"}}); r["result"] != "created" {
		t.Fatalf("bob's import: %v", r)
	}
	_, list := bob.doAny("GET", "/api/networks", nil)
	got := list.([]any)[0].(map[string]any)
	if got["hasPassword"] != true || got["servers"].([]any)[0].(map[string]any)["hasPassword"] != true || got["authMethod"] != "sasl" {
		t.Errorf("imported network: %v", got)
	}
	if k := got["channelSettings"].(map[string]any)["#evrd"].(map[string]any)["key"]; k != "chankey" {
		t.Errorf("channel key %v", k)
	}
	if r := imp(bob, map[string]any{"text": string(file), "networks": []string{"libera"}}); r["result"] != "skipped" {
		t.Errorf("again: %v", r)
	}
	if r := imp(bob, map[string]any{"text": string(file), "networks": []string{"LIBERA"}, "existing": "update"}); r["result"] != "updated" {
		t.Errorf("update: %v", r)
	}

	// a ZNC config: pick the user, then the networks
	conf := "<User admin>\n Nick = adm\n <Network one>\n  Server = irc.one.example +6697\n  LoadModule = nickserv\n </Network>\n" +
		" <Network two>\n  Server = irc.two.example 6667\n </Network>\n</User>\n<User other>\n <Network three>\n  Server = x 1\n </Network>\n</User>\n"
	code, pv = alice.do("POST", "/api/networks/import/preview", map[string]any{"text": conf})
	if code != 200 || pv["format"] != "znc" || len(pv["users"].([]any)) != 2 {
		t.Fatalf("znc preview: %d %v", code, pv)
	}
	one := pv["users"].([]any)[0].(map[string]any)["networks"].([]any)[0].(map[string]any)
	if one["nick"] != "adm" || len(one["notes"].([]any)) != 1 || one["servers"].([]any)[0] != "irc.one.example:+6697" {
		t.Errorf("znc network: %v", one)
	}
	if r := imp(alice, map[string]any{"text": conf, "user": "admin", "networks": []string{"two"}}); r["result"] != "created" {
		t.Errorf("znc import: %v", r)
	}
	_, list = alice.doAny("GET", "/api/networks", nil)
	if len(list.([]any)) != 2 {
		t.Errorf("only the network picked: %v", list)
	}
	if code, out := alice.do("POST", "/api/networks/import", map[string]any{"text": conf, "user": "nobody"}); code != 422 {
		t.Errorf("unknown user: %d %v", code, out)
	}
	if code, out := alice.do("POST", "/api/networks/import/preview", map[string]any{"text": "hello"}); code != 422 || out["error"] == nil {
		t.Errorf("not a file: %d %v", code, out)
	}
}

func fmtAny(v any) string { b, _ := json.Marshal(v); return string(b) }
