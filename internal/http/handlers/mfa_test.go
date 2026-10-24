package handlers_test

import (
	"strings"
	"testing"
	"time"

	"github.com/4evrd/evrd/internal/config"
	"github.com/4evrd/evrd/internal/mfa"
	"github.com/4evrd/evrd/internal/store"
)

func TestSecondFactorSetup(t *testing.T) {
	e := newEnv(t)
	e.user("alice", store.RoleUser)
	c := e.login("alice")

	code, st := c.do("GET", "/api/me/mfa", nil)
	if code != 200 || st["totp"] != false || st["choice"] != "free" || st["required"] != false {
		t.Fatalf("before: %d %v", code, st)
	}
	// the toggle needs an app first
	if code, body := c.do("PATCH", "/api/me/mfa", map[string]any{"ircRequireMFA": true}); code != 422 || !strings.Contains(body["error"].(string), "authenticator app") {
		t.Fatalf("toggle without an app: %d %v", code, body)
	}
	if code, _ := c.do("POST", "/api/me/mfa/totp/confirm", map[string]any{"code": "123456"}); code != 422 {
		t.Fatalf("confirm before start: %d", code)
	}

	code, en := c.do("POST", "/api/me/mfa/totp", nil)
	if code != 200 || !strings.HasPrefix(en["qr"].(string), "<svg") || !strings.HasPrefix(en["url"].(string), "otpauth://totp/") {
		t.Fatalf("start: %d %v", code, en)
	}
	secret := strings.ReplaceAll(en["secret"].(string), " ", "")
	if code, _ := c.do("POST", "/api/me/mfa/totp/confirm", map[string]any{"code": "000000"}); code != 422 {
		t.Fatalf("a wrong first code: %d", code)
	}
	code, rc := c.do("POST", "/api/me/mfa/totp/confirm", map[string]any{"code": mfa.TOTPCode(secret, time.Now().Add(-30*time.Second))})
	if code != 200 || len(rc["recoveryCodes"].([]any)) != mfa.RecoveryCodeCount {
		t.Fatalf("confirm: %d %v", code, rc)
	}
	recovery := rc["recoveryCodes"].([]any)

	if code, st := c.do("PATCH", "/api/me/mfa", map[string]any{"ircRequireMFA": true}); code != 200 || st["ircRequireMFA"] != true || st["required"] != true {
		t.Fatalf("toggle on: %d %v", code, st)
	}
	if code, _ := c.do("PATCH", "/api/me/mfa", map[string]any{}); code != 400 {
		t.Fatalf("no toggle: %d", code)
	}

	// new recovery codes and removing the app take a code
	if code, _ := c.do("POST", "/api/me/mfa/recovery", map[string]any{"code": "000000"}); code != 422 {
		t.Fatalf("new codes with a wrong one: %d", code)
	}
	code, fresh := c.do("POST", "/api/me/mfa/recovery", map[string]any{"code": mfa.TOTPCode(secret, time.Now())})
	if code != 200 || len(fresh["recoveryCodes"].([]any)) != mfa.RecoveryCodeCount {
		t.Fatalf("new codes: %d %v", code, fresh)
	}
	if code, _ := c.do("POST", "/api/me/mfa/totp/remove", map[string]any{"code": recovery[0]}); code != 422 {
		t.Fatal("an old recovery code removed the app")
	}
	code, st = c.do("POST", "/api/me/mfa/totp/remove", map[string]any{"code": fresh["recoveryCodes"].([]any)[0]})
	if code != 200 || st["totp"] != false || st["ircRequireMFA"] != false {
		t.Fatalf("remove: %d %v", code, st)
	}
}

func TestSecondFactorPolicy(t *testing.T) {
	req := newEnv(t, func(c *config.Config) { c.Auth.IRCMFA = "required" })
	req.user("bob", store.RoleUser)
	c := req.login("bob")
	if code, st := c.do("GET", "/api/me/mfa", nil); code != 200 || st["choice"] != "locked" || st["required"] != true {
		t.Fatalf("required: %d %v", code, st)
	}
	if code, body := c.do("PATCH", "/api/me/mfa", map[string]any{"ircRequireMFA": false}); code != 409 || !strings.Contains(body["error"].(string), "requires") {
		t.Fatalf("turning it off while required: %d %v", code, body)
	}

	off := newEnv(t, func(c *config.Config) { c.Auth.IRCMFA = "off" })
	off.user("carol", store.RoleUser)
	c = off.login("carol")
	if code, st := c.do("GET", "/api/me/mfa", nil); code != 200 || st["choice"] != "hidden" || st["required"] != false {
		t.Fatalf("off: %d %v", code, st)
	}
	if code, _ := c.do("PATCH", "/api/me/mfa", map[string]any{"ircRequireMFA": true}); code != 409 {
		t.Fatalf("turning it on while off: %d", code)
	}

	// wrong codes count toward the lockout
	e := newEnv(t)
	e.user("dave", store.RoleUser)
	d := e.login("dave")
	_, en := d.do("POST", "/api/me/mfa/totp", nil)
	secret := strings.ReplaceAll(en["secret"].(string), " ", "")
	d.do("POST", "/api/me/mfa/totp/confirm", map[string]any{"code": mfa.TOTPCode(secret, time.Now().Add(-30*time.Second))})
	for range 10 {
		d.do("POST", "/api/me/mfa/recovery", map[string]any{"code": "000000"})
	}
	if code, _ := d.do("POST", "/api/me/mfa/recovery", map[string]any{"code": mfa.TOTPCode(secret, time.Now())}); code != 429 {
		t.Fatalf("after ten wrong codes: %d", code)
	}
}
