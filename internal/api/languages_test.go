package api

import (
	"fmt"
	"strings"
	"testing"

	"github.com/4evrd/evrd/internal/store"
)

func pack(code, name string, messages map[string]any) map[string]any {
	return map[string]any{"code": code, "name": name, "messages": messages}
}

func TestPackValidation(t *testing.T) {
	e := newEnv(t)
	e.user("root", store.RoleAdmin)
	admin := e.login("root")
	big := map[string]any{}
	for i := range 300 {
		big[fmt.Sprintf("k.%d", i)] = strings.Repeat("x", 1900)
	}
	for _, c := range []struct {
		name string
		body any
		want string
	}{
		{"not json", "hello", "isn't a language pack"},
		{"bad code", pack("DE", "Deutsch", map[string]any{}), "isn't a language code"},
		{"long code", pack("de-toolongregion", "Deutsch", map[string]any{}), "isn't a language code"},
		{"no name", pack("de", "", map[string]any{}), "1 to 40"},
		{"long name", pack("de", strings.Repeat("n", 41), map[string]any{}), "1 to 40"},
		{"no messages", map[string]any{"code": "de", "name": "Deutsch"}, "no \"messages\""},
		{"bad key", pack("de", "Deutsch", map[string]any{"a key": "x"}), "isn't a message key"},
		{"long key", pack("de", "Deutsch", map[string]any{strings.Repeat("k", 101): "x"}), "isn't a message key"},
		{"long text", pack("de", "Deutsch", map[string]any{"a.b": strings.Repeat("x", 2001)}), "longer than 2000"},
		{"number", pack("de", "Deutsch", map[string]any{"a.b": 3}), "must be text"},
		{"bad plural", pack("de", "Deutsch", map[string]any{"a.b": map[string]any{"one": "x", "lots": "y", "other": "z"}}), "isn't a plural form"},
		{"plural without other", pack("de", "Deutsch", map[string]any{"a.b": map[string]any{"one": "x"}}), "need \"other\""},
		{"long plural", pack("de", "Deutsch", map[string]any{"a.b": map[string]any{"other": strings.Repeat("x", 2001)}}), "longer than 2000"},
		{"oversized", pack("de", "Deutsch", big), "larger than 512 KiB"},
		{"wrong code", pack("fr", "Français", map[string]any{}), "not \"de\""},
	} {
		code, out := admin.do("PUT", "/api/admin/languages/de", c.body)
		if code != 422 || !strings.Contains(fmt.Sprint(out["error"]), c.want) {
			t.Errorf("%s: %d %v, want 422 %q", c.name, code, out, c.want)
		}
		code, out = admin.do("PUT", "/api/me/language-pack", c.body)
		if c.name != "wrong code" && (code != 422 || !strings.Contains(fmt.Sprint(out["error"]), c.want)) {
			t.Errorf("%s (own): %d %v, want 422 %q", c.name, code, out, c.want)
		}
	}
	if _, msg, err := parsePack(make([]byte, MaxPackSize+1)); err == nil || msg != "" {
		t.Error("parsePack took an oversized file")
	}
}

func TestServerLanguagePacks(t *testing.T) {
	e := newEnv(t)
	e.user("root", store.RoleAdmin)
	e.user("alice", store.RoleUser)
	admin, alice, anon := e.login("root"), e.login("alice"), e.anon()

	if code, out := anon.doAny("GET", "/api/languages", nil); code != 200 || len(out.([]any)) != 0 {
		t.Fatalf("empty list: %d %v", code, out)
	}
	if code, _ := anon.do("GET", "/api/languages/de", nil); code != 404 {
		t.Fatalf("missing pack: %d", code)
	}
	body := pack("de", "Deutsch", map[string]any{"shell.overview": "Übersicht", "format.minutesAgo": map[string]any{"one": "vor {count} Minute", "other": "vor {count} Minuten"}})
	if code, _ := alice.do("PUT", "/api/admin/languages/de", body); code != 403 {
		t.Fatalf("a user uploading: %d", code)
	}
	code, out := admin.do("PUT", "/api/admin/languages/de", body)
	if code != 200 || out["code"] != "de" || out["name"] != "Deutsch" || out["updatedAt"] == nil || out["messages"] != nil {
		t.Fatalf("put: %d %v", code, out)
	}

	// Public: no session needed.
	code, list := anon.doAny("GET", "/api/languages", nil)
	if l, _ := list.([]any); code != 200 || len(l) != 1 || l[0].(map[string]any)["code"] != "de" || l[0].(map[string]any)["messages"] != nil {
		t.Fatalf("list: %d %v", code, list)
	}
	code, out = anon.do("GET", "/api/languages/de", nil)
	m, _ := out["messages"].(map[string]any)
	if code != 200 || out["name"] != "Deutsch" || m["shell.overview"] != "Übersicht" || m["format.minutesAgo"].(map[string]any)["other"] != "vor {count} Minuten" {
		t.Fatalf("get: %d %v", code, out)
	}

	// Replacing it keeps one pack.
	admin.do("PUT", "/api/admin/languages/de", pack("de", "Deutsch (Schweiz)", map[string]any{}))
	if _, list := anon.doAny("GET", "/api/languages", nil); len(list.([]any)) != 1 || list.([]any)[0].(map[string]any)["name"] != "Deutsch (Schweiz)" {
		t.Fatalf("after replacing: %v", list)
	}

	if code, _ := alice.do("DELETE", "/api/admin/languages/de", nil); code != 403 {
		t.Fatalf("a user deleting: %d", code)
	}
	if code, _ := admin.do("DELETE", "/api/admin/languages/de", nil); code != 204 {
		t.Fatalf("delete: %d", code)
	}
	if code, _ := admin.do("DELETE", "/api/admin/languages/de", nil); code != 404 {
		t.Fatalf("delete twice: %d", code)
	}
}

func TestPersonalLanguagePack(t *testing.T) {
	e := newEnv(t)
	e.user("root", store.RoleAdmin)
	alice := e.user("alice", store.RoleUser)
	e.user("bob", store.RoleUser)
	ac, bc, admin := e.login("alice"), e.login("bob"), e.login("root")

	if code, _ := ac.do("GET", "/api/me/language-pack", nil); code != 404 {
		t.Fatalf("no pack yet: %d", code)
	}
	if _, me := ac.do("GET", "/api/me", nil); me["languagePack"] != nil {
		t.Fatalf("me without a pack: %v", me["languagePack"])
	}
	// The language must be one the app has, a server pack's or alice's own.
	if code, _ := ac.do("PATCH", "/api/me", map[string]any{"language": "xx"}); code != 422 {
		t.Fatalf("unknown language: %d", code)
	}
	if code, _ := ac.do("PATCH", "/api/me", map[string]any{"language": "it"}); code != 200 {
		t.Fatalf("built-in language: %d", code)
	}

	code, out := ac.do("PUT", "/api/me/language-pack", pack("xx", "Mine", map[string]any{"account.language": "Lingo"}))
	if code != 200 || out["code"] != "xx" || out["name"] != "Mine" {
		t.Fatalf("put: %d %v", code, out)
	}
	code, out = ac.do("GET", "/api/me/language-pack", nil)
	if code != 200 || out["messages"].(map[string]any)["account.language"] != "Lingo" {
		t.Fatalf("get: %d %v", code, out)
	}
	if _, me := ac.do("GET", "/api/me", nil); me["languagePack"].(map[string]any)["code"] != "xx" {
		t.Fatalf("me: %v", me["languagePack"])
	}
	if code, _ := ac.do("PATCH", "/api/me", map[string]any{"language": "xx"}); code != 200 {
		t.Fatalf("own pack's language: %d", code)
	}
	// Someone else's pack isn't bob's to pick, or to see.
	if code, _ := bc.do("PATCH", "/api/me", map[string]any{"language": "xx"}); code != 422 {
		t.Fatalf("bob picking alice's language: %d", code)
	}
	if code, _ := bc.do("GET", "/api/me/language-pack", nil); code != 404 {
		t.Fatalf("bob sees a pack: %d", code)
	}
	// A server pack's language is anyone's.
	admin.do("PUT", "/api/admin/languages/de", pack("de", "Deutsch", map[string]any{}))
	if code, _ := bc.do("PATCH", "/api/me", map[string]any{"language": "de"}); code != 200 {
		t.Fatalf("server pack's language: %d", code)
	}

	if code, _ := ac.do("DELETE", "/api/me/language-pack", nil); code != 204 {
		t.Fatalf("delete: %d", code)
	}
	if code, _ := ac.do("DELETE", "/api/me/language-pack", nil); code != 404 {
		t.Fatalf("delete twice: %d", code)
	}

	// Deleting the account deletes the pack.
	ac.do("PUT", "/api/me/language-pack", pack("xx", "Mine", map[string]any{}))
	if code, _ := admin.do("DELETE", userPath(alice), nil); code != 200 && code != 204 {
		t.Fatalf("delete alice: %d", code)
	}
	if _, err := e.st.UserLanguagePack(t.Context(), alice.ID); err != store.ErrNotFound {
		t.Fatalf("pack survived its user: %v", err)
	}
}
