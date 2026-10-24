package client

import (
	"context"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/4evrd/evrd/internal/languages"
	"github.com/4evrd/evrd/internal/messages"
	"github.com/4evrd/evrd/internal/modules"
	"github.com/4evrd/evrd/internal/store"
	"github.com/4evrd/evrd/internal/upstream"
)

// Every irc.* key the code says has an English text.
func TestStatusKeysExist(t *testing.T) {
	has := func(key string) bool { return messages.English.T(messages.Area+key) != messages.Area+key }
	used := regexp.MustCompile(`se\.(?:say|tr)\("([^"]+)"`)
	files, _ := os.ReadDir(".")
	n := 0
	for _, f := range files {
		if !strings.HasSuffix(f.Name(), ".go") || strings.HasSuffix(f.Name(), "_test.go") {
			continue
		}
		src, err := os.ReadFile(f.Name())
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range used.FindAllStringSubmatch(string(src), -1) {
			if strings.HasSuffix(m[1], ".") {
				continue // a prefix: the keys it makes are checked below
			}
			n++
			if !has(m[1]) {
				t.Errorf("%s: irc.%s has no English text", f.Name(), m[1])
			}
		}
	}
	if n < 100 {
		t.Errorf("found only %d keys in the code", n)
	}
	var made []string
	for _, c := range statusCmds {
		made = append(made, "help."+c.name)
	}
	for cmd := range notHere {
		made = append(made, "notHere."+cmd)
	}
	for _, m := range modules.All() {
		made = append(made, "module."+m.Name)
	}
	for _, st := range []upstream.Status{upstream.StatusConnected, upstream.StatusConnecting, upstream.StatusReconnecting, upstream.StatusDisconnected} {
		made = append(made, "state."+string(st))
	}
	made = append(made, "role."+store.RoleUser, "role."+store.RoleAdmin)
	for _, what := range []string{"buffers", "channelBuffers", "queryBuffers"} {
		made = append(made, "count."+what, "clear.none."+what)
	}
	for _, k := range made {
		if !has(k) {
			t.Errorf("irc.%s has no English text", k)
		}
	}
}

// *status answers in the account's language, read for each command, with
// the user's own pack on top.
func TestStatusInTheUsersLanguage(t *testing.T) {
	r := newRig(t)
	langs := languages.New(r.st)
	msgs := messages.New(langs)
	r.srv.SetMessages(msgs)
	ic := r.attached()
	ic.status("nope", `Unknown command "nope" — try help.`)

	setLanguage := func(code string) {
		u, err := r.acc.UserByID(context.Background(), r.user.ID)
		if err != nil {
			t.Fatal(err)
		}
		u.Language = code
		if err := r.acc.UpdateUser(context.Background(), u); err != nil {
			t.Fatal(err)
		}
	}
	setLanguage("it")
	ic.status("nope", `Comando sconosciuto "nope": prova help.`)
	ic.status("help", "Comandi di evrd")
	ic.status("detach #nope", "Non sei su #nope.")

	if err := langs.PutUserLanguagePack(context.Background(), store.UserLanguagePack{UserID: r.user.ID, Code: "it", Name: "Italiano",
		Messages: `{"irc.notOn":"Qui non ci sei: {channel}"}`}); err != nil {
		t.Fatal(err)
	}
	msgs.Forget()
	ic.status("detach #nope", "Qui non ci sei: #nope")

	setLanguage("")
	ic.status("detach #nope", "You're not on #nope.")
}
