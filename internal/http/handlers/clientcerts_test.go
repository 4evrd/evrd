package handlers_test

import (
	"strconv"
	"strings"
	"testing"

	"github.com/4evrd/evrd/internal/accounts"
	"github.com/4evrd/evrd/internal/bouncer"
	"github.com/4evrd/evrd/internal/store"
)

func TestClientCertsAPI(t *testing.T) {
	e := newEnv(t)
	alice := e.user("alice", store.RoleUser)
	e.user("bob", store.RoleUser)
	c, bob := e.login("alice"), e.login("bob")

	// off without the service
	if code, _ := c.do("GET", "/api/me/client-certs", nil); code != 404 {
		t.Fatalf("no service: %d", code)
	}
	e.api.SetClientCerts(accounts.NewClientCerts(e.st, e.st))

	fp := strings.Repeat("ab", 32)
	code, out := c.do("POST", "/api/me/client-certs", map[string]any{"fingerprint": strings.ToUpper(fp), "label": "laptop"})
	if code != 200 || out["fingerprint"] != fp || out["label"] != "laptop" || out["lastUsedAt"] != nil {
		t.Fatalf("add: %d %v", code, out)
	}
	id := strconv.FormatInt(int64(out["id"].(float64)), 10)
	for _, bad := range []map[string]any{{"fingerprint": "nope"}, {"fingerprint": fp}} {
		if code, out := c.do("POST", "/api/me/client-certs", bad); code != 422 || out["error"] == "" {
			t.Fatalf("%v: %d %v", bad, code, out)
		}
	}

	// an attached IRC client's certificate is offered, and whether it's added
	other := strings.Repeat("cd", 32)
	for _, f := range []string{fp, other} {
		att, err := e.b.AttachClient(alice.ID, 0, bouncer.ClientInfo{Device: "device " + f[:2], Software: "WeeChat", IP: "192.0.2.1", CertFP: f}, func(string) {})
		if err != nil {
			t.Fatal(err)
		}
		defer att.Release()
	}
	code, out = c.do("GET", "/api/me/client-certs", nil)
	certs, _ := out["certs"].([]any)
	clients, _ := out["clients"].([]any)
	if code != 200 || len(certs) != 1 || len(clients) != 2 {
		t.Fatalf("list: %d %v", code, out)
	}
	for _, x := range clients {
		m := x.(map[string]any)
		if m["software"] != "WeeChat" || m["added"] != (m["fingerprint"] == fp) {
			t.Errorf("client: %v", m)
		}
	}
	if _, out := bob.do("GET", "/api/me/client-certs", nil); len(out["certs"].([]any)) != 0 || len(out["clients"].([]any)) != 0 {
		t.Fatalf("bob sees: %v", out)
	}

	// only the owner deletes it
	if code, _ := bob.do("DELETE", "/api/me/client-certs/"+id, nil); code != 404 {
		t.Fatalf("bob deleting: %d", code)
	}
	if code, _ := c.do("DELETE", "/api/me/client-certs/"+id, nil); code != 204 {
		t.Fatalf("delete: %d", code)
	}
	if _, out := c.do("GET", "/api/me/client-certs", nil); len(out["certs"].([]any)) != 0 {
		t.Fatalf("after deleting: %v", out)
	}
}
