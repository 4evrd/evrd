package handlers_test

import (
	"testing"

	"github.com/4evrd/evrd/internal/store"
)

func TestAutoAwayDelay(t *testing.T) {
	e := newEnv(t)
	e.user("alice", store.RoleUser)
	c := e.login("alice")
	if _, me := c.do("GET", "/api/me", nil); me["autoAwayDelay"] != float64(store.DefaultAutoAwayDelay) {
		t.Fatalf("default: %v", me["autoAwayDelay"])
	}
	if code, me := c.do("PATCH", "/api/me", map[string]any{"autoAway": true, "autoAwayDelay": 300}); code != 200 || me["autoAwayDelay"] != float64(300) {
		t.Fatalf("set: %d %v", code, me)
	}
	if code, me := c.do("PATCH", "/api/me", map[string]any{"autoAwayDelay": 0}); code != 200 || me["autoAwayDelay"] != float64(0) {
		t.Fatalf("at once: %d %v", code, me)
	}
	for _, bad := range []int{-1, 3601} {
		if code, out := c.do("PATCH", "/api/me", map[string]any{"autoAwayDelay": bad}); code != 422 || out["error"] != "The auto-away delay is 0 to 60 minutes." {
			t.Fatalf("%d: %d %v", bad, code, out)
		}
	}
}
