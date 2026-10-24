package handlers_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
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
	for _, secret := range []string{"saslpass", "serverpass", "chankey", "enc:v1"} {
		if strings.Contains(fmtAny(pv), secret) {
			t.Errorf("the preview sends %q back: %v", secret, pv)
		}
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

func TestImportMerge(t *testing.T) {
	e := newEnv(t)
	e.user("alice", store.RoleUser)
	c := e.login("alice")
	c.do("POST", "/api/networks", map[string]any{"name": "libera", "host": "irc.libera.chat", "port": 6697, "tls": true, "nick": "mine", "channels": []string{"#a"}})
	file := "networks:\n  - name: libera\n    nick: theirs\n    realname: Filled In\n    channels: [{name: '#a'}, {name: '#b'}]\n"
	code, out := c.do("POST", "/api/networks/import", map[string]any{"text": file, "networks": []string{"libera"}, "existing": "merge"})
	if code != 200 || out["results"].([]any)[0].(map[string]any)["result"] != "merged" {
		t.Fatalf("merge: %d %v", code, out)
	}
	_, list := c.doAny("GET", "/api/networks", nil)
	n := list.([]any)[0].(map[string]any)
	if n["nick"] != "mine" || n["realname"] != "Filled In" || fmtAny(n["channels"]) != `["#a","#b"]` {
		t.Errorf("merged network: nick %v realname %v channels %v", n["nick"], n["realname"], n["channels"])
	}
}

func TestZNCMigration(t *testing.T) {
	e := newEnv(t)
	e.user("root", store.RoleAdmin)
	e.user("taken", store.RoleUser)
	admin := e.login("root")
	sum := sha256.Sum256([]byte("old znc password" + "pepper"))
	conf := "<User Carol>\n <Pass password>\n  Method = SHA256\n  Hash = " + hex.EncodeToString(sum[:]) + "\n  Salt = pepper\n </Pass>\n" +
		" <Network libera>\n  Server = irc.libera.chat +6697\n  BindHost = vhost.example.org\n </Network>\n</User>\n" +
		"<User taken>\n Pass = plain#whatever\n <Network oftc>\n  Server = irc.oftc.net +6697\n </Network>\n</User>\n" +
		"<User 9bad>\n <Network x>\n  Server = x 1\n </Network>\n</User>\n"

	code, pv := admin.do("POST", "/api/admin/znc/preview", map[string]any{"text": conf, "accounts": map[string]string{"9bad": "nine"}})
	if code != 200 {
		t.Fatalf("preview: %d %v", code, pv)
	}
	rows := pv["users"].([]any)
	carol, taken, nine := rows[0].(map[string]any), rows[1].(map[string]any), rows[2].(map[string]any)
	if carol["account"] != "carol" || carol["exists"] != false || carol["hasPassword"] != true {
		t.Errorf("carol: %v", carol)
	}
	if taken["exists"] != true || taken["accountID"] == nil {
		t.Errorf("taken: %v", taken)
	}
	if nine["account"] != "nine" || nine["accountError"] != nil {
		t.Errorf("a renamed account: %v", nine)
	}
	if strings.Contains(fmtAny(pv), "pepper") || strings.Contains(fmtAny(pv), hex.EncodeToString(sum[:])) {
		t.Errorf("the preview has the password hash: %v", pv)
	}
	if _, pv := admin.do("POST", "/api/admin/znc/preview", map[string]any{"text": conf}); pv["users"].([]any)[2].(map[string]any)["accountError"] == nil {
		t.Errorf("9bad isn't a valid account name: %v", pv)
	}

	code, out := admin.do("POST", "/api/admin/znc/accounts", map[string]any{"text": conf, "users": []map[string]any{
		{"znc": "Carol", "account": "carol", "email": "carol@example.org"},
		{"znc": "taken", "account": "taken"},
		{"znc": "nobody", "account": "nobody"},
	}})
	if code != 200 {
		t.Fatalf("accounts: %d %v", code, out)
	}
	acc := out["accounts"].([]any)
	if a := acc[0].(map[string]any); a["created"] != true || a["password"] != true || a["claimable"] != true {
		t.Errorf("carol's account: %v", a)
	}
	if a := acc[1].(map[string]any); a["created"] != false || a["id"] == nil {
		t.Errorf("an existing account: %v", a)
	}
	if a := acc[2].(map[string]any); a["error"] == nil {
		t.Errorf("a ZNC user not in the config: %v", a)
	}
	if _, err := e.acc.PasswordLogin("taken", "whatever"); err == nil {
		t.Error("an existing account's password was replaced by ZNC's")
	}

	// then the networks, as the admin, on carol's node
	id := int64(acc[0].(map[string]any)["id"].(float64))
	code, out = admin.do("POST", fmt.Sprintf("/api/admin/users/%d/networks/import", id), map[string]any{"text": conf, "user": "Carol", "networks": []string{"libera"}})
	if r := out["results"].([]any)[0].(map[string]any); code != 200 || r["result"] != "created" {
		t.Fatalf("carol's networks: %d %v", code, out)
	}
	carolC := e.anon()
	if code, body := carolC.do("POST", "/auth/password", map[string]string{"username": "carol", "password": "old znc password"}); code != 200 {
		t.Fatalf("carol signs in with her ZNC password: %d %v", code, body)
	}
	_, list := carolC.doAny("GET", "/api/networks", nil)
	if n := list.([]any)[0].(map[string]any); n["name"] != "libera" || n["bindHost"] != "vhost.example.org" {
		t.Errorf("carol's network (an admin brings the vhost over): %v", n)
	}
	res, file := admin.raw(fmt.Sprintf("/api/admin/users/%d/networks/export", id))
	if res.StatusCode != 200 || !strings.Contains(res.Header.Get("Content-Disposition"), "evrd-networks-carol-") || !bytes.Contains(file, []byte("libera")) {
		t.Errorf("admin export: %d %v", res.StatusCode, res.Header)
	}
}
