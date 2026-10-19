package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/4evrd/evrd/internal/bouncer"
	"github.com/4evrd/evrd/internal/config"
)

const provisionToken = "s3cret-token"

func (e *env) provision(username string, body any) (int, map[string]any) {
	e.t.Helper()
	var raw []byte
	if s, ok := body.(string); ok {
		raw = []byte(s)
	} else {
		raw, _ = json.Marshal(body)
	}
	req, _ := http.NewRequest("PUT", e.srv.URL+"/api/provision/users/"+username, bytes.NewReader(raw))
	req.Header.Set("Authorization", "Bearer "+provisionToken)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer res.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(res.Body).Decode(&out)
	return res.StatusCode, out
}

func TestProvisionValidation(t *testing.T) {
	e := newEnv(t, func(c *config.Config) { c.ProvisionToken = provisionToken })
	if code, _ := e.provision("bob", "{nope"); code != 400 {
		t.Fatalf("bad body: %d", code)
	}
	if code, out := e.provision("not%20valid", map[string]any{}); code != 422 {
		t.Fatalf("bad username: %d %v", code, out)
	}
	code, out := e.provision("bob", map[string]any{"networks": []map[string]any{
		{"name": "good", "host": "irc.example.org"},
		{"name": "noserver"},
		{"name": "GOOD", "host": "irc.example.org"}, // the same name: already there
	}})
	if code != 200 {
		t.Fatalf("provision: %d %v", code, out)
	}
	nets := out["networks"].([]any)
	status := func(i int) string { return nets[i].(map[string]any)["status"].(string) }
	if status(0) != "created" || status(1) != "failed" || status(2) != "exists" || nets[1].(map[string]any)["error"] == "" {
		t.Fatalf("results: %v", nets)
	}
	// Per-user network limits don't apply to provisioning.
	u, _ := e.st.UserByUsername("bob")
	for _, name := range []string{"two", "three"} {
		if _, out := e.provision("bob", map[string]any{"networks": []map[string]any{{"name": name, "host": "irc.example.org"}}}); out["networks"].([]any)[0].(map[string]any)["status"] != "created" {
			t.Fatalf("%s: %v", name, out)
		}
	}
	if list, _ := e.st.ListNetworks(u.ID); len(list) != 3 {
		t.Fatalf("networks: %d", len(list))
	}
}

func TestProvisionConnect(t *testing.T) {
	e := newEnv(t, func(c *config.Config) { c.ProvisionToken = provisionToken })
	irc := newFakeIRC(t)
	code, out := e.provision("bob", map[string]any{"connect": true, "networks": []map[string]any{
		{"name": "fake", "host": "127.0.0.1", "port": irc.port(), "tls": false},
	}})
	if code != 200 {
		t.Fatalf("provision: %d %v", code, out)
	}
	n := out["networks"].([]any)[0].(map[string]any)
	id := int64(n["id"].(float64))
	t.Cleanup(func() { e.api.b.Disconnect(id) })
	if n["status"] != "created" || n["error"] != nil {
		t.Fatalf("result: %v", n)
	}
	irc.expect("USER ")

	// A suspended account's networks can't be connected: said per network.
	u, _ := e.st.UserByUsername("bob")
	u.Suspended = true
	e.st.UpdateUser(u)
	e.api.b.StopUser(u.ID)
	_, out = e.provision("bob", map[string]any{"connect": true, "networks": []map[string]any{{"name": "fake"}}})
	if n := out["networks"].([]any)[0].(map[string]any); n["status"] != "exists" || n["error"] != "not connected: "+bouncer.ErrSuspended.Error() {
		t.Fatalf("suspended: %v", n)
	}
}

// When another node runs the account, provisioning only marks the networks
// enabled: the owner connects them.
func TestProvisionConnectOnAnotherNode(t *testing.T) {
	e := newEnv(t, func(c *config.Config) { c.ProvisionToken = provisionToken })
	e.api.b.SetOwnership(func(int64) bool { return false })
	_, out := e.provision("bob", map[string]any{"connect": true, "networks": []map[string]any{{"name": "x", "host": "irc.example.org"}}})
	n := out["networks"].([]any)[0].(map[string]any)
	if n["status"] != "created" || n["error"] != nil {
		t.Fatalf("result: %v", n)
	}
	got, _ := e.st.NetworkByID(int64(n["id"].(float64)))
	if !got.Enabled {
		t.Fatal("not enabled for the owner to connect")
	}
	if _, live := e.api.b.ConnFor(got.ID); live {
		t.Fatal("connected on a node that doesn't own the account")
	}
}

// Provisioning brings bind hosts over as they were, though the bouncer
// offers none to pick, and sets one on a network already there.
func TestProvisionBindHost(t *testing.T) {
	e := newEnv(t, func(c *config.Config) { c.ProvisionToken = provisionToken })
	net := func(name, bind string) map[string]any {
		n := map[string]any{"name": name, "host": "irc.example.org"}
		if bind != "" {
			n["bindHost"] = bind
		}
		return n
	}
	if _, out := e.provision("bob", map[string]any{"networks": []map[string]any{net("plain", ""), net("vhost", " bob.example.org ")}}); out["networks"].([]any)[1].(map[string]any)["status"] != "created" {
		t.Fatalf("with a bind host: %v", out)
	}
	_, out := e.provision("bob", map[string]any{"networks": []map[string]any{net("plain", "bob.example.org")}})
	if r := out["networks"].([]any)[0].(map[string]any); r["status"] != "exists" || r["error"] != nil {
		t.Fatalf("setting it later: %v", r)
	}
	u, _ := e.st.UserByUsername("bob")
	list, _ := e.st.ListNetworks(u.ID)
	for _, n := range list {
		if n.BindHost != "bob.example.org" {
			t.Fatalf("%s: bind host %q", n.Name, n.BindHost)
		}
	}
	// The account itself gains nothing: its own edits still go by the list.
	if u.IsAdmin() || u.BindHost != "" {
		t.Fatalf("account: %+v", u)
	}
}
