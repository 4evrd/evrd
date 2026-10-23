package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/4evrd/evrd/internal/config"
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
