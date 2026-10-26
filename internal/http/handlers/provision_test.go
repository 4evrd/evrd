package handlers_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/4evrd/evrd/internal/config"
	"github.com/4evrd/evrd/internal/store"
)

func TestProvisionUser(t *testing.T) {
	e := newEnv(t, func(c *config.Config) { c.ProvisionToken = "s3cret-token" })
	put := func(token string, body any) (int, map[string]any) {
		b, _ := json.Marshal(body)
		req, _ := http.NewRequest("PUT", e.srv.URL+"/api/provision/users/Bob", bytes.NewReader(b))
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
	body := map[string]any{
		"email": "bob@example.org",
		"networks": []map[string]any{
			{"name": "libera", "servers": []map[string]any{{"host": "127.0.0.1", "port": 1, "tls": false}},
				"nick": "bob", "channels": []string{"#evrd", "#go-nuts"}, "saslUser": "bob", "password": "sasl-pass"},
			{"name": "oftc", "host": "127.0.0.1", "port": 1},
		},
	}
	if code, _ := put("", body); code != 401 {
		t.Fatalf("no token: %d", code)
	}
	if code, _ := put("wrong", body); code != 401 {
		t.Fatalf("wrong token: %d", code)
	}
	code, out := put("s3cret-token", body)
	if code != 200 || out["created"] != true || out["claimable"] != true {
		t.Fatalf("provision: %d %v", code, out)
	}
	nets := out["networks"].([]any)
	if len(nets) != 2 || nets[0].(map[string]any)["status"] != "created" {
		t.Fatalf("networks: %v", nets)
	}
	u, err := e.st.UserByUsername(t.Context(), "bob")
	if err != nil || u.PasswordHash != "" || u.OIDCSubject != nil || !u.SSOClaimable || u.Email != "bob@example.org" {
		t.Fatalf("account: %+v %v", u, err)
	}
	list, _ := e.st.ListNetworks(t.Context(), u.ID)
	if len(list) != 2 || list[0].SASLUser != "bob" || list[0].Password != "sasl-pass" || len(list[0].Channels) != 2 {
		t.Fatalf("stored networks: %+v", list)
	}

	// Again: nothing duplicated, the account isn't recreated.
	code, out = put("s3cret-token", body)
	if code != 200 || out["created"] != false || out["networks"].([]any)[1].(map[string]any)["status"] != "exists" {
		t.Fatalf("repeat: %d %v", code, out)
	}
	if list, _ := e.st.ListNetworks(t.Context(), u.ID); len(list) != 2 {
		t.Fatalf("repeat duplicated networks: %d", len(list))
	}

	// Connect on a repeat connects the existing networks (here: tries to —
	// the test's networks point at a closed local port, so only check it's asked).
	body["connect"] = true
	code, out = put("s3cret-token", body)
	if code != 200 {
		t.Fatalf("connect repeat: %d %v", code, out)
	}
	if list, _ := e.st.ListNetworks(t.Context(), u.ID); !list[0].Enabled {
		t.Fatalf("connect didn't enable %s", list[0].Name)
	}
}

func TestProvisioningOffWithoutToken(t *testing.T) {
	e := newEnv(t)
	req, _ := http.NewRequest("PUT", e.srv.URL+"/api/provision/users/bob", bytes.NewReader([]byte(`{}`)))
	req.Header.Set("Authorization", "Bearer ")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 404 {
		t.Fatalf("status %d", res.StatusCode)
	}
}

func TestProvisionedUser(t *testing.T) {
	e := newEnv(t, func(c *config.Config) { c.ProvisionToken = "s3cret-token" })
	call := func(method, token, user string, body any) (int, map[string]any) {
		var b []byte
		if body != nil {
			b, _ = json.Marshal(body)
		}
		req, _ := http.NewRequest(method, e.srv.URL+"/api/provision/users/"+user, bytes.NewReader(b))
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
	if code, _ := call("GET", "", "bob", nil); code != 401 {
		t.Fatalf("no token: %d", code)
	}
	if code, _ := call("GET", "s3cret-token", "bob", nil); code != 404 {
		t.Fatalf("no account: %d", code)
	}
	call("PUT", "s3cret-token", "bob", map[string]any{
		"email": "bob@example.org",
		"networks": []map[string]any{
			{"name": "libera", "host": "127.0.0.1", "port": 1, "bindHost": "2001:db8::1"},
			{"name": "oftc", "host": "127.0.0.1", "port": 1},
		},
	})
	code, out := call("GET", "s3cret-token", "Bob", nil)
	if code != 200 || out["username"] != "bob" || out["claimable"] != true {
		t.Fatalf("get: %d %v", code, out)
	}
	nets := out["networks"].([]any)
	if len(nets) != 2 {
		t.Fatalf("networks: %v", nets)
	}
	byName := map[string]map[string]any{}
	for _, n := range nets {
		m := n.(map[string]any)
		byName[m["name"].(string)] = m
	}
	if byName["libera"]["bindHost"] != "2001:db8::1" || byName["oftc"]["bindHost"] != "" ||
		byName["oftc"]["status"] != "disconnected" || byName["oftc"]["id"] == nil {
		t.Fatalf("networks: %v", byName)
	}

	// What it lists is what a PUT changes: a new bind host on every network.
	var all []map[string]any
	for name := range byName {
		all = append(all, map[string]any{"name": name, "bindHost": "2001:db8::2"})
	}
	call("PUT", "s3cret-token", "bob", map[string]any{"networks": all})
	_, out = call("GET", "s3cret-token", "bob", nil)
	for _, n := range out["networks"].([]any) {
		if m := n.(map[string]any); m["bindHost"] != "2001:db8::2" {
			t.Fatalf("bind host not replaced: %v", m)
		}
	}
}

func TestProvisionDeleteUser(t *testing.T) {
	e := newEnv(t, func(c *config.Config) { c.ProvisionToken = "s3cret-token" })
	call := func(method, token, user string, body any) int {
		var b []byte
		if body != nil {
			b, _ = json.Marshal(body)
		}
		req, _ := http.NewRequest(method, e.srv.URL+"/api/provision/users/"+user, bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode
	}
	call("PUT", "s3cret-token", "bob", map[string]any{
		"email":    "bob@example.org",
		"networks": []map[string]any{{"name": "libera", "host": "127.0.0.1", "port": 1}},
	})
	if code := call("DELETE", "", "bob", nil); code != 401 {
		t.Fatalf("no token: %d", code)
	}
	if code := call("DELETE", "s3cret-token", "nobody", nil); code != 404 {
		t.Fatalf("no account: %d", code)
	}
	if code := call("DELETE", "s3cret-token", "Bob", nil); code != 200 {
		t.Fatalf("delete: %d", code)
	}
	if code := call("GET", "s3cret-token", "bob", nil); code != 404 {
		t.Fatalf("bob after delete: %d", code)
	}
	e.user("root", store.RoleAdmin)
	if code := call("DELETE", "s3cret-token", "root", nil); code != 409 {
		t.Fatalf("admin: %d", code)
	}
	if code := call("GET", "s3cret-token", "root", nil); code != 200 {
		t.Fatalf("admin was deleted: %d", code)
	}
}
