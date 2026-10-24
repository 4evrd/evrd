package handlers_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/4evrd/evrd/internal/config"
	"github.com/4evrd/evrd/internal/messages"
	"github.com/4evrd/evrd/internal/modules"
	"github.com/4evrd/evrd/internal/store"
)

// evrd's own words come back in the language the web app shows (the
// header), else the account's, else English: errors, events, the
// connection log, the modules and the settings.
func TestWordsInTheReadersLanguage(t *testing.T) {
	e := newEnv(t)
	u := e.user("alice", store.RoleUser)
	alice := e.login("alice")

	// an error: English, then in the web app's language, then the account's
	bad := map[string]any{"name": "a b", "host": "irc.example.org", "nick": "alice"}
	if code, out := alice.do("POST", "/api/networks", bad); code != 422 || out["error"] != "a network needs a name without spaces, / or :" {
		t.Fatalf("English: %d %v", code, out)
	}
	alice.lang = "it"
	if code, out := alice.do("POST", "/api/networks", bad); code != 422 || out["error"] != "una rete ha bisogno di un nome senza spazi, / o :" {
		t.Fatalf("the web app's language: %d %v", code, out)
	}
	if _, out := alice.do("PATCH", "/api/me", map[string]any{"email": "nope"}); out["error"] != "non sembra un indirizzo email" {
		t.Fatalf("a handler's own error: %v", out)
	}
	alice.lang = ""
	u.Language = "de"
	if err := e.acc.UpdateUser(context.Background(), u); err != nil {
		t.Fatal(err)
	}
	if _, out := alice.do("POST", "/api/networks", bad); out["error"] != "ein Netzwerk braucht einen Namen ohne Leerzeichen, / oder :" {
		t.Fatalf("the account's language: %v", out)
	}
	if _, out := alice.do("GET", "/api/networks/999999", nil); out["error"] != "nicht gefunden" {
		t.Fatalf("not found: %v", out)
	}

	// an event and the connection log
	alice.lang = "it"
	e.b.AddEvent(u.ID, "connected", messages.T("event.connected", "network", "libera"), messages.T("event.connectedAs", "host", "irc.libera.chat", "port", 6697, "nick", "alice"))
	_, evs := alice.doAny("GET", "/api/me/events", nil)
	if l, _ := evs.([]any); len(l) == 0 || l[0].(map[string]any)["title"] != "Connesso a libera" || l[0].(map[string]any)["detail"] != "irc.libera.chat:6697 come alice" {
		t.Fatalf("events: %v", evs)
	}
	code, n := alice.do("POST", "/api/networks", map[string]any{"name": "dev", "host": "127.0.0.1", "port": 1, "nick": "alice"})
	if code != 201 && code != 200 {
		t.Fatalf("create: %d %v", code, n)
	}
	id := fmt.Sprint(n["id"])
	alice.do("POST", "/api/networks/"+id+"/connect", nil)
	_, log := alice.doAny("GET", "/api/networks/"+id+"/log", nil)
	if s := fmt.Sprint(log); !strings.Contains(s, "Accesa") || strings.Contains(s, "Switched on") || strings.Contains(s, "msg:") {
		t.Fatalf("log: %v", log)
	}

	// the modules
	_, mods := alice.doAny("GET", "/api/modules", nil)
	if !strings.Contains(fmt.Sprint(mods), "Tieni il nick") || !strings.Contains(fmt.Sprint(mods), "Attesa (secondi)") {
		t.Fatalf("modules: %v", mods)
	}
}

// The words the modules and the settings page show all have English.
func TestModuleAndSettingWordsExist(t *testing.T) {
	has := func(key string) bool { return messages.English.T(messages.Area+key) != messages.Area+key }
	for _, m := range modules.All() {
		for _, k := range []string{"moduleTitle." + m.Name, "module." + m.Name} {
			if !has(k) {
				t.Errorf("no irc.%s", k)
			}
		}
		for _, st := range m.Settings {
			if !has("moduleSetting." + m.Name + "." + st.Key) {
				t.Errorf("no label for %s's %s", m.Name, st.Key)
			}
			if st.Help != "" && !has("moduleHelp."+m.Name+"."+st.Key) {
				t.Errorf("no help for %s's %s", m.Name, st.Key)
			}
		}
	}
	for _, s := range config.Specs {
		if !has("settingLabel." + s.Key) {
			t.Errorf("no label for %s", s.Key)
		}
		if s.Help != "" && !has("settingHelp."+s.Key) {
			t.Errorf("no help for %s", s.Key)
		}
		slug := strings.Map(func(r rune) rune {
			if r >= 'a' && r <= 'z' {
				return r
			}
			if r >= 'A' && r <= 'Z' {
				return r + 'a' - 'A'
			}
			return -1
		}, s.Group)
		if !has("settingGroup." + slug) {
			t.Errorf("no group %s", s.Group)
		}
	}
}
