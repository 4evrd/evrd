package handlers_test

import (
	"context"
	"testing"

	"github.com/4evrd/evrd/internal/ircsignin"
	"github.com/4evrd/evrd/internal/store"
)

func TestIRCSignInPage(t *testing.T) {
	e := newEnv(t)
	alice := e.user("alice", store.RoleUser)
	e.user("bob", store.RoleUser)
	svc := ircsignin.New(e.st, e.acc)
	ctx := context.Background()
	a := e.login("alice")

	_, tok, _ := svc.Begin(ctx, ircsignin.Request{Username: "alice", Network: "libera", Nick: "al", IP: "192.0.2.7", Secret: "a-secret-long-enough-to-remember"})
	code, p := a.do("GET", "/api/irc-signins/"+tok, nil)
	if code != 200 || p["state"] != "pending" || p["yours"] != true || p["nick"] != "al" || p["network"] != "libera" ||
		p["canRememberSecret"] != true || p["sameAddress"] != false || p["rememberDays"] != float64(30) {
		t.Fatalf("lookup: %d %v", code, p)
	}
	if code, _ := a.do("GET", "/api/irc-signins/not-a-token", nil); code != 404 {
		t.Fatalf("a wrong token: %d", code)
	}
	// bob sees it isn't his, and can't allow it (but may turn it away)
	b := e.login("bob")
	if _, p := b.do("GET", "/api/irc-signins/"+tok, nil); p["yours"] != false {
		t.Fatalf("bob's view: %v", p)
	}
	if code, _ := b.do("POST", "/api/irc-signins/"+tok+"/allow", map[string]any{}); code != 403 {
		t.Fatalf("bob allows: %d", code)
	}
	if code, _ := a.do("POST", "/api/irc-signins/"+tok+"/allow", map[string]any{"remember": "forever"}); code != 400 {
		t.Fatalf("a bad remember: %d", code)
	}
	code, out := a.do("POST", "/api/irc-signins/"+tok+"/allow", map[string]any{"remember": "password"})
	if code != 200 || out["clientPassword"] == nil {
		t.Fatalf("allow: %d %v", code, out)
	}
	if u, _, err := e.acc.IRCLogin("alice", out["clientPassword"].(string)); err != nil || u.ID != alice.ID {
		t.Fatalf("the device's password: %v", err)
	}
	if code, _ := a.do("POST", "/api/irc-signins/"+tok+"/allow", nil); code != 404 {
		t.Fatalf("used twice: %d", code)
	}
	// the device shows on the clients page as remembered
	code, list := a.doAny("GET", "/api/me/client-passwords", nil)
	if cps, _ := list.([]any); code != 200 || len(cps) != 1 || cps[0].(map[string]any)["remembered"] != true || cps[0].(map[string]any)["expiresAt"] == nil {
		t.Fatalf("client passwords: %d %v", code, list)
	}

	_, tok, _ = svc.Begin(ctx, ircsignin.Request{IP: "192.0.2.8"})
	if code, _ := b.do("POST", "/api/irc-signins/"+tok+"/deny", nil); code != 200 {
		t.Fatalf("deny: %d", code)
	}
	if code, _ := e.anon().do("GET", "/api/irc-signins/"+tok, nil); code != 401 {
		t.Fatalf("signed out: %d", code)
	}
}
