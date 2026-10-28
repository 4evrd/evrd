package handlers_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/4evrd/evrd/internal/store"
)

func TestAdminIRCSignIns(t *testing.T) {
	e := newEnv(t)
	alice := e.user("alice", store.RoleUser)
	e.user("root", store.RoleAdmin)
	now := time.Now()
	e.b.RecordSignIn(store.IRCSignIn{UserID: alice.ID, At: now.Add(-time.Hour), IP: "192.0.2.1", Format: "<username>/<network>:<password>", Network: "libera", Result: "ok"})
	e.b.RecordSignIn(store.IRCSignIn{UserID: alice.ID, At: now.Add(-time.Minute), IP: "192.0.2.1", Nick: "al", Ident: "alice", Format: "<password>", Result: "link", Matched: "laptop", PasswordLen: 19})
	e.b.RecordSignIn(store.IRCSignIn{At: now, IP: "198.51.100.9", Format: "<username>:<password>", Result: "invalid"})
	e.b.RecordSignIn(store.IRCSignIn{UserID: alice.ID, At: now.Add(-40 * 24 * time.Hour), Format: "<password>", Result: "link"}) // too old

	code, body := e.login("root").do("GET", "/api/admin/irc-signins", nil)
	rows, _ := body["signIns"].([]any)
	if code != 200 || len(rows) != 3 {
		t.Fatalf("everyone: %d %v", code, body)
	}
	first := rows[0].(map[string]any)
	if first["user"] != nil || first["format"] != "<username>:<password>" || first["full"] != false {
		t.Fatalf("newest, nobody's: %v", first)
	}
	alone := rows[1].(map[string]any)
	if alone["user"] != "alice" || alone["matched"] != "laptop" || alone["passwordLength"] != float64(19) || alone["ident"] != "alice" {
		t.Fatalf("password alone: %v", alone)
	}
	if full := rows[2].(map[string]any); full["full"] != true || full["network"] != "libera" {
		t.Fatalf("full form: %v", full)
	}

	_, body = e.login("root").do("GET", fmt.Sprintf("/api/admin/irc-signins?user=%d", alice.ID), nil)
	if rows, _ := body["signIns"].([]any); len(rows) != 2 || body["user"].(map[string]any)["username"] != "alice" {
		t.Fatalf("alice's: %v", body)
	}
	_, body = e.login("root").do("GET", "/api/admin/irc-signins?user=unknown", nil)
	if rows, _ := body["signIns"].([]any); len(rows) != 1 {
		t.Fatalf("unknown: %v", body)
	}
	if code, _ := e.login("alice").do("GET", "/api/admin/irc-signins", nil); code != 403 {
		t.Fatalf("a user: %d", code)
	}
}
