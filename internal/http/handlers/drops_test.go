package handlers_test

import (
	"testing"
	"time"

	"github.com/4evrd/evrd/internal/bouncer"
	"github.com/4evrd/evrd/internal/store"
)

func TestClientDrops(t *testing.T) {
	e := newEnv(t)
	alice := e.user("alice", store.RoleUser)
	root := e.user("root", store.RoleAdmin)
	now := time.Now()
	phone := bouncer.ClientInfo{Device: "Pixel", Software: "Goguma", Since: now.Add(-time.Hour)}
	e.b.RecordDisconnect(alice.ID, phone, "libera", bouncer.DropPingTimeout, now.Add(-50*time.Minute))
	phone.Since = now.Add(-49 * time.Minute)
	e.b.RecordDisconnect(alice.ID, phone, "libera", bouncer.DropPingTimeout, now.Add(-10*time.Minute))
	e.b.RecordDisconnect(root.ID, bouncer.ClientInfo{Software: "irssi", Since: now.Add(-5 * time.Minute)}, "oftc", bouncer.DropQuit, now)
	// two days ago: out of the period
	e.b.RecordDisconnect(alice.ID, bouncer.ClientInfo{Software: "old", Since: now.Add(-50 * time.Hour)}, "x", bouncer.DropReset, now.Add(-49*time.Hour))

	code, body := e.login("alice").do("GET", "/api/me/client-drops", nil)
	devs, _ := body["devices"].([]any)
	if code != 200 || len(devs) != 1 || body["hours"] != float64(24) {
		t.Fatalf("alice's: %d %v", code, body)
	}
	d := devs[0].(map[string]any)
	if d["device"] != "Pixel" || d["drops"] != float64(2) || d["mainReason"] != bouncer.DropPingTimeout || d["medianGap"] != float64(60) {
		t.Fatalf("the phone: %v", d)
	}

	_, ov := e.login("root").do("GET", "/api/admin/overview", nil)
	sum, _ := ov["clientDrops"].(map[string]any)
	if sum["drops"] != float64(3) || sum["accounts"] != float64(2) {
		t.Fatalf("overview: %v", ov["clientDrops"])
	}
	if code, _ := e.anon().do("GET", "/api/me/client-drops", nil); code != 401 {
		t.Fatalf("signed out: %d", code)
	}
}
