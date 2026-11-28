package handlers_test

import (
	"context"
	"strings"
	"testing"

	"github.com/4evrd/evrd/internal/store"
)

func TestAwayMessageAndClientNotifySettings(t *testing.T) {
	e := newEnv(t)
	u := e.user("alice", store.RoleUser)
	c := e.login("alice")
	_, me := c.do("GET", "/api/me", nil)
	s := me["settings"].(map[string]any)
	if me["awayMessage"] != "" || s["clientNotify"] != false || s["showNotesOnLogin"] != true {
		t.Fatalf("defaults: %v %v", me["awayMessage"], s)
	}
	if code, me := c.do("PATCH", "/api/me", map[string]any{"awayMessage": "  Gone since %awaytime%  "}); code != 200 || me["awayMessage"] != "Gone since %awaytime%" {
		t.Fatalf("set: %d %v", code, me)
	}
	for _, bad := range []string{"two\nlines", strings.Repeat("x", 301)} {
		if code, out := c.do("PATCH", "/api/me", map[string]any{"awayMessage": bad}); code != 422 || out["error"] != "The away message is one line of up to 300 characters." {
			t.Errorf("%q: %d %v", bad, code, out)
		}
	}
	code, me := c.do("PATCH", "/api/me", map[string]any{"settings": map[string]any{
		"clientNotify": true, "clientNotifyNewOnly": true, "clientNotifyOnDisconnect": true, "showNotesOnLogin": false}})
	if code != 200 {
		t.Fatalf("settings: %d %v", code, me)
	}
	got, _ := e.st.UserByID(context.Background(), u.ID)
	if got.AwayMessage != "Gone since %awaytime%" || !got.ClientNotify || !got.ClientNotifyNewOnly || !got.ClientNotifyOnDisconnect || got.ShowNotesOnLogin {
		t.Errorf("saved: %+v", got)
	}
	// an admin edits them too
	e.user("root", store.RoleAdmin)
	admin := e.login("root")
	if code, out := admin.do("PATCH", userPath(u), map[string]any{"settings": map[string]any{"clientNotify": false}}); code != 200 {
		t.Fatalf("admin: %d %v", code, out)
	}
	if got, _ := e.st.UserByID(context.Background(), u.ID); got.ClientNotify {
		t.Error("the admin's change didn't stick")
	}
}

func TestNotesAPI(t *testing.T) {
	e := newEnv(t)
	u := e.user("alice", store.RoleUser)
	e.user("bob", store.RoleUser)
	c := e.login("alice")
	if code, _ := c.do("PUT", "/api/me/notes/wifi", map[string]any{"text": "hunter2"}); code != 200 {
		t.Fatalf("put: %d", code)
	}
	if code, _ := c.do("PUT", "/api/me/notes/wifi", map[string]any{"text": "hunter3"}); code != 200 {
		t.Fatalf("put again: %d", code)
	}
	if code, out := c.do("PUT", "/api/me/notes/two%20words", map[string]any{"text": "x"}); code != 422 || !strings.Contains(out["error"].(string), "one word") {
		t.Errorf("a bad key: %d %v", code, out)
	}
	if code, out := c.do("PUT", "/api/me/notes/k", map[string]any{"text": ""}); code != 422 || !strings.Contains(out["error"].(string), "one line") {
		t.Errorf("no text: %d %v", code, out)
	}
	notes, _ := e.st.Notes(context.Background(), u.ID)
	if len(notes) != 1 || notes[0].Text != "hunter3" {
		t.Fatalf("stored: %+v", notes)
	}
	if code, _ := c.do("GET", "/api/me/notes", nil); code != 200 {
		t.Errorf("list: %d", code)
	}
	// bob has none of alice's
	if code, _ := e.login("bob").do("DELETE", "/api/me/notes/wifi", nil); code != 404 {
		t.Errorf("bob deleting alice's: %d", code)
	}
	if code, _ := c.do("DELETE", "/api/me/notes/wifi", nil); code != 204 {
		t.Errorf("delete: %d", code)
	}
	if code, _ := c.do("DELETE", "/api/me/notes/wifi", nil); code != 404 {
		t.Errorf("delete again: %d", code)
	}
	if code, _ := e.anon().do("GET", "/api/me/notes", nil); code != 401 {
		t.Errorf("without a session: %d", code)
	}
}

func TestZNCSettingsComeOver(t *testing.T) {
	e := newEnv(t)
	e.user("root", store.RoleAdmin)
	admin := e.login("root")
	conf := "<User dave>\n LoadModule = clientnotify\n <Network libera>\n  Server = irc.libera.chat +6697\n  LoadModule = simple_away -timer 300 Gone since %awaytime%\n </Network>\n</User>\n"
	code, out := admin.do("POST", "/api/admin/znc/accounts", map[string]any{"text": conf, "users": []map[string]any{{"znc": "dave", "account": "dave"}}})
	if code != 200 {
		t.Fatalf("accounts: %d %v", code, out)
	}
	u, err := e.st.UserByUsername(context.Background(), "dave")
	if err != nil {
		t.Fatal(err)
	}
	if !u.AutoAway || u.AutoAwayDelay == nil || *u.AutoAwayDelay != 300 || u.AwayMessage != "Gone since %awaytime%" || !u.ClientNotify {
		t.Errorf("settings: %+v", u)
	}
}
