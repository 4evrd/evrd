package api

import (
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/4evrd/evrd/internal/store"
)

func TestSessions(t *testing.T) {
	e := newEnv(t)
	e.user("alice", store.RoleUser)
	e.user("bob", store.RoleUser)
	here, there, third := e.login("alice"), e.login("alice"), e.login("alice")
	bob := e.login("bob")

	code, out := here.doAny("GET", "/api/me/sessions", nil)
	list, _ := out.([]any)
	if code != 200 || len(list) != 3 {
		t.Fatalf("sessions: %d %v", code, out)
	}
	var current, other string
	for _, s := range list {
		sm := s.(map[string]any)
		if sm["method"] != "password" || sm["ip"] != "127.0.0.1" || sm["expiresAt"] == nil {
			t.Errorf("session: %v", sm)
		}
		if sm["current"] == true {
			current = sm["id"].(string)
		} else {
			other = sm["id"].(string)
		}
	}
	if current == "" || other == "" {
		t.Fatalf("exactly one session should be current: %v", list)
	}

	// Someone else can't revoke alice's session (and it's not an error).
	if code, _ := bob.do("DELETE", "/api/me/sessions/"+other, nil); code != 204 {
		t.Fatalf("bob revoking: %d", code)
	}
	if _, out := here.doAny("GET", "/api/me/sessions", nil); len(out.([]any)) != 3 {
		t.Fatal("bob revoked one of alice's sessions")
	}
	if code, _ := here.do("DELETE", "/api/me/sessions/"+other, nil); code != 204 {
		t.Fatalf("revoke: %d", code)
	}
	if _, out := here.doAny("GET", "/api/me/sessions", nil); len(out.([]any)) != 2 {
		t.Fatalf("after revoking one: %v", out)
	}
	if code, _ := here.do("POST", "/api/me/sessions/revoke-others", nil); code != 204 {
		t.Fatalf("revoke others: %d", code)
	}
	for _, c := range []*client{there, third} {
		if code, _ := c.do("GET", "/api/me", nil); code != 401 {
			t.Fatal("another session survived revoke-others")
		}
	}
	if code, _ := here.do("GET", "/api/me", nil); code != 200 {
		t.Fatal("revoke-others signed out the current session")
	}
	if code, _ := bob.do("GET", "/api/me", nil); code != 200 {
		t.Fatal("revoke-others signed out another user")
	}
}

func TestClientPasswordList(t *testing.T) {
	e := newEnv(t)
	e.user("alice", store.RoleUser)
	e.user("bob", store.RoleUser)
	alice, bob := e.login("alice"), e.login("bob")

	if code, out := alice.doAny("GET", "/api/me/client-passwords", nil); code != 200 || len(out.([]any)) != 0 {
		t.Fatalf("empty: %d %v", code, out)
	}
	if code, out := alice.do("POST", "/api/me/client-passwords", map[string]string{"name": "  "}); code != 422 {
		t.Fatalf("blank name: %d %v", code, out)
	}
	if code, _ := alice.do("POST", "/api/me/client-passwords", map[string]string{"name": strings.Repeat("x", 65)}); code != 422 {
		t.Fatalf("long name: %d", code)
	}
	_, cp := alice.do("POST", "/api/me/client-passwords", map[string]string{"name": "phone"})
	if _, err := ircLogin(e.acc, "alice", cp["password"].(string)); err != nil {
		t.Fatal(err)
	}
	code, out := alice.doAny("GET", "/api/me/client-passwords", nil)
	list := out.([]any)
	if code != 200 || len(list) != 1 {
		t.Fatalf("list: %d %v", code, out)
	}
	got := list[0].(map[string]any)
	if got["name"] != "phone" || got["password"] != nil || got["lastUsedAt"] == nil {
		t.Fatalf("listed password (no secret, last use recorded): %v", got)
	}
	id := strconv.Itoa(int(got["id"].(float64)))
	if code, _ := bob.do("DELETE", "/api/me/client-passwords/"+id, nil); code != 404 {
		t.Fatalf("bob deleting alice's: %d", code)
	}
	if code, _ := alice.do("DELETE", "/api/me/client-passwords/"+id, nil); code != 204 {
		t.Fatalf("delete: %d", code)
	}
	if code, _ := alice.do("DELETE", "/api/me/client-passwords/"+id, nil); code != 404 {
		t.Fatalf("delete twice: %d", code)
	}
}

func TestUpdateMeValidation(t *testing.T) {
	e := newEnv(t)
	e.user("alice", store.RoleUser)
	c := e.login("alice")
	for _, body := range []map[string]any{
		{"email": "not-an-email"},
		{"defaultNick": "has space"},
		{"defaultNick": strings.Repeat("n", 31)},
		{"defaultAltNick": "a@b"},
	} {
		if code, out := c.do("PATCH", "/api/me", body); code != 422 {
			t.Errorf("%v: %d %v, want 422", body, code, out)
		}
	}
	code, me := c.do("PATCH", "/api/me", map[string]any{"email": " alice@example.org ", "defaultNick": " al ", "defaultRealname": " Alice A "})
	if code != 200 || me["email"] != "alice@example.org" || me["defaultNick"] != "al" || me["defaultRealname"] != "Alice A" {
		t.Fatalf("update: %d %v", code, me)
	}
	// New networks start from the defaults.
	_, n := c.do("POST", "/api/networks", map[string]any{"name": "x", "host": "irc.example.org"})
	if n["nick"] != "al" || n["realname"] != "Alice A" {
		t.Fatalf("network defaults: %v", n)
	}
	if code, _ := c.do("PATCH", "/api/me", map[string]any{"email": ""}); code != 200 {
		t.Fatalf("clearing the email: %d", code)
	}
}

// An SSO account's email and password belong to the provider.
func TestSSOAccountsKeepProviderFields(t *testing.T) {
	e := newEnv(t)
	u := e.user("sam", store.RoleUser)
	c := e.login("sam")
	sub := "sso|sam"
	u, _ = e.st.UserByID(u.ID)
	u.OIDCSubject, u.PasswordHash, u.Email = &sub, "", "sam@idp.example"
	if err := e.st.UpdateUser(u); err != nil {
		t.Fatal(err)
	}
	if code, out := c.do("POST", "/api/me/password", map[string]string{"current": "", "next": "something-long"}); code != 409 {
		t.Fatalf("password change for SSO: %d %v", code, out)
	}
	code, me := c.do("PATCH", "/api/me", map[string]any{"email": "other@example.org"})
	if code != 200 || me["email"] != "sam@idp.example" || me["sso"] != true || me["hasPassword"] != false {
		t.Fatalf("SSO email: %d %v", code, me)
	}
}

func TestDeleteMe(t *testing.T) {
	e := newEnv(t)
	e.user("alice", store.RoleUser)
	c := e.login("alice")
	c.do("POST", "/api/networks", map[string]any{"name": "x", "host": "irc.example.org"})
	if code, _ := c.do("DELETE", "/api/me", map[string]string{"confirm": "Alice"}); code != 422 {
		t.Fatalf("wrong confirmation: %d", code)
	}
	req, _ := http.NewRequest("DELETE", e.srv.URL+"/api/me", strings.NewReader("nope"))
	res, err := c.c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 400 {
		t.Fatalf("bad body: %d", res.StatusCode)
	}
	if code, out := c.do("DELETE", "/api/me", map[string]string{"confirm": "alice"}); code != 200 || out["ok"] != true {
		t.Fatalf("delete: %d %v", code, out)
	}
	if code, _ := c.do("GET", "/api/me", nil); code != 401 {
		t.Fatal("deleted account still signed in")
	}
	if _, err := e.st.UserByUsername("alice"); err == nil {
		t.Fatal("account still in the database")
	}
	if code, _ := e.anon().do("POST", "/auth/password", map[string]string{"username": "alice", "password": "alice-password"}); code == 200 {
		t.Fatal("deleted account can sign in")
	}
}

// An admin can delete their own account while another admin remains, and
// sees who that is beforehand.
func TestAdminDeletesOwnAccount(t *testing.T) {
	e := newEnv(t)
	e.user("root", store.RoleAdmin)
	e.user("root2", store.RoleAdmin)
	e.user("alice", store.RoleUser)
	c := e.login("root")
	_, me := c.do("GET", "/api/me", nil)
	if o := me["otherAdmins"].([]any); len(o) != 1 || o[0] != "root2" {
		t.Fatalf("otherAdmins: %v", me["otherAdmins"])
	}
	if _, me := e.login("alice").do("GET", "/api/me", nil); len(me["otherAdmins"].([]any)) != 0 {
		t.Fatalf("a user's otherAdmins: %v", me["otherAdmins"])
	}
	if code, out := c.do("DELETE", "/api/me", map[string]string{"confirm": "root"}); code != 200 {
		t.Fatalf("delete: %d %v", code, out)
	}
}

func TestMeLanguage(t *testing.T) {
	e := newEnv(t)
	e.user("alice", store.RoleUser)
	c := e.login("alice")
	if _, me := c.do("GET", "/api/me", nil); me["language"] != "" {
		t.Fatalf("default: %v", me["language"])
	}
	if code, _ := c.do("PATCH", "/api/me", map[string]any{"language": "klingon"}); code != 422 {
		t.Fatalf("unknown language: %d", code)
	}
	if code, me := c.do("PATCH", "/api/me", map[string]any{"language": " it "}); code != 200 || me["language"] != "it" {
		t.Fatalf("italian: %d %v", code, me["language"])
	}
	// "" goes back to following the browser
	if code, me := c.do("PATCH", "/api/me", map[string]any{"language": ""}); code != 200 || me["language"] != "" {
		t.Fatalf("browser: %d %v", code, me["language"])
	}
}
