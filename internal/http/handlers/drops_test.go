package handlers_test

import (
	"fmt"
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

func TestAdminClientDrops(t *testing.T) {
	e := newEnv(t)
	alice := e.user("alice", store.RoleUser)
	bob := e.user("bob", store.RoleUser)
	e.user("root", store.RoleAdmin)
	now := time.Now()
	// alice's phone: two ping timeouts, back after a minute from the first
	phone := bouncer.ClientInfo{Device: "Pixel", Software: "Goguma", Since: now.Add(-time.Hour)}
	e.b.RecordDisconnect(alice.ID, phone, "libera", bouncer.DropPingTimeout, now.Add(-50*time.Minute))
	phone.Since = now.Add(-49 * time.Minute)
	e.b.RecordDisconnect(alice.ID, phone, "libera", bouncer.DropPingTimeout, now.Add(-10*time.Minute))
	// bob's desktop: three slow-link drops, back after 5 s, 20 s and 2 min
	desk := bouncer.ClientInfo{Device: "Desktop", Software: "HexChat", Since: now.Add(-3 * time.Hour)}
	for _, back := range []time.Duration{5 * time.Second, 20 * time.Second, 2 * time.Minute} {
		ended := desk.Since.Add(30 * time.Minute)
		e.b.RecordDisconnect(bob.ID, desk, "oftc", bouncer.DropWriteStalled, ended)
		desk.Since = ended.Add(back)
	}
	// four days ago: only in 7d and 30d
	e.b.RecordDisconnect(bob.ID, bouncer.ClientInfo{Software: "irssi", Since: now.Add(-97 * time.Hour)}, "oftc", bouncer.DropReset, now.Add(-96*time.Hour))

	code, body := e.login("root").do("GET", "/api/admin/client-drops", nil)
	if code != 200 || body["range"] != "24h" || body["drops"] != float64(5) || body["accounts"] != float64(2) || body["devices"] != float64(2) {
		t.Fatalf("24h: %d %v", code, body)
	}
	reasons := body["reasons"].(map[string]any)
	if reasons[bouncer.DropWriteStalled] != float64(3) || reasons[bouncer.DropPingTimeout] != float64(2) || reasons[bouncer.DropReset] != nil {
		t.Fatalf("reasons: %v", reasons)
	}
	// the phone came back once (after 60 s); its last drop isn't back yet
	ping := body["gaps"].(map[string]any)[bouncer.DropPingTimeout].(map[string]any)
	if ping["back"] != float64(1) || ping["gone"] != float64(1) || ping["median"] != float64(60) {
		t.Fatalf("ping timeout gaps: %v", ping)
	}
	// the desktop's three: 5 s, 20 s, then 2 min, the last not back yet
	slow := body["gaps"].(map[string]any)[bouncer.DropWriteStalled].(map[string]any)
	if slow["back"] != float64(2) || slow["median"] != float64(12.5) || slow["p90"] != float64(20) {
		t.Fatalf("stopped reading gaps: %v", slow)
	}
	buckets := body["buckets"].([]any)
	if len(buckets) != 6 || buckets[0].(map[string]any)[bouncer.DropWriteStalled] != float64(1) ||
		buckets[1].(map[string]any)[bouncer.DropWriteStalled] != float64(1) || buckets[1].(map[string]any)[bouncer.DropPingTimeout] != float64(1) ||
		buckets[5].(map[string]any)[bouncer.DropPingTimeout] != float64(1) {
		t.Fatalf("buckets: %v", buckets)
	}
	// over time: hourly slots like the health page, the last one now's;
	// every drop in its slot, and every reason listed
	tl := body["timeline"].(map[string]any)
	start, step, n := int64(body["start"].(float64)), int64(body["step"].(float64)), int(body["points"].(float64))
	if step != 3600 || n != 24 || start%3600 != 0 || len(tl) != len(bouncer.DropReasons) {
		t.Fatalf("timeline: start %d step %d points %d, %d reasons", start, step, n, len(tl))
	}
	slot := func(at time.Time) int { return int((at.Unix() - start) / step) }
	pings := tl[bouncer.DropPingTimeout].([]any)
	if pings[slot(now.Add(-50*time.Minute))].(float64) < 1 || pings[slot(now.Add(-10*time.Minute))].(float64) < 1 {
		t.Fatalf("ping timeouts over time: %v", pings)
	}
	total := 0.0
	for _, pts := range tl {
		for _, v := range pts.([]any) {
			total += v.(float64)
		}
	}
	if total != 5 {
		t.Fatalf("timeline total %v, want 5", total)
	}
	if _, wk := e.login("root").do("GET", "/api/admin/client-drops?range=30d", nil); wk["step"] != float64(86400) || wk["points"] != float64(30) {
		t.Fatalf("30d slots: %v %v", wk["step"], wk["points"])
	}
	top := body["top"].([]any)
	// when each device last dropped, in seconds like the rest of the API
	if last, ok := top[0].(map[string]any)["last"].(float64); !ok || int64(last) > now.Unix() || int64(last) < now.Add(-4*time.Hour).Unix() {
		t.Fatalf("last drop: %v", top[0].(map[string]any)["last"])
	}
	if len(top) != 2 || top[0].(map[string]any)["user"] != "bob" || top[0].(map[string]any)["device"] != "Desktop" || top[1].(map[string]any)["user"] != "alice" {
		t.Fatalf("top: %v", top)
	}

	// one user: everything narrows to theirs, and the reply names them
	_, mine := e.login("root").do("GET", fmt.Sprintf("/api/admin/client-drops?user=%d", alice.ID), nil)
	if mine["drops"] != float64(2) || mine["accounts"] != float64(1) || mine["user"].(map[string]any)["username"] != "alice" ||
		len(mine["top"].([]any)) != 1 || mine["reasons"].(map[string]any)[bouncer.DropWriteStalled] != nil {
		t.Fatalf("alice's: %v", mine)
	}
	if code, _ := e.login("root").do("GET", "/api/admin/client-drops?user=99999", nil); code != 404 {
		t.Fatalf("no such user: %d", code)
	}
	if body["user"] != nil {
		t.Fatalf("everyone's names no user: %v", body["user"])
	}

	// by a reason: only the devices that had it, most first
	_, body = e.login("root").do("GET", "/api/admin/client-drops?sort=ping_timeout&limit=5", nil)
	top = body["top"].([]any)
	if body["sort"] != bouncer.DropPingTimeout || len(top) != 1 || top[0].(map[string]any)["device"] != "Pixel" {
		t.Fatalf("by ping timeout: %v", body)
	}
	// a week: the old reset too; an unknown range or sort falls back
	if _, body = e.login("root").do("GET", "/api/admin/client-drops?range=7d", nil); body["drops"] != float64(6) {
		t.Fatalf("7d: %v", body["drops"])
	}
	if _, body = e.login("root").do("GET", "/api/admin/client-drops?range=1y&sort=nonsense&limit=1", nil); body["range"] != "24h" || body["sort"] != "drops" || len(body["top"].([]any)) != 1 {
		t.Fatalf("fallbacks: %v", body)
	}

	if code, _ := e.login("alice").do("GET", "/api/admin/client-drops", nil); code != 403 {
		t.Fatalf("not an admin: %d", code)
	}
}
