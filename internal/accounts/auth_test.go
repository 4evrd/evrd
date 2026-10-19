package accounts

import (
	"strings"
	"testing"

	"github.com/4evrd/evrd/internal/store"
)

func TestNormalizeAuth(t *testing.T) {
	n := store.Network{Name: "dal", Nick: "bob", Host: "irc.dal.net",
		AuthMethod: " NickServ ", ServicesNick: " NickServ@services.dal.net ", IdentifyArgs: "Password",
		IdentifyOn: "connect", GhostCmd: "GHOST", AuthTimeout: 30}
	if err := NormalizeNetwork(&n); err != nil {
		t.Fatal(err)
	}
	if n.AuthMethod != store.AuthNickServ || n.ServicesNick != "NickServ@services.dal.net" || n.IdentifyArgs != store.IdentifyPassword ||
		n.IdentifyOn != "" || n.GhostCmd != "ghost" {
		t.Fatalf("%+v", n)
	}
	// rows from before there was a choice are automatic
	old := store.Network{Name: "old", Nick: "bob", Host: "h"}
	if old.Auth() != store.AuthAuto {
		t.Fatal("an empty method is auto")
	}
	if err := NormalizeNetwork(&old); err != nil || old.AuthMethod != store.AuthAuto {
		t.Fatalf("%v %q", err, old.AuthMethod)
	}
	cmd := store.Network{Name: "un", Nick: "bob", Host: "h", AuthMethod: "command", AuthCommand: " /msg x@channels.undernet.org login %account% %password% "}
	if err := NormalizeNetwork(&cmd); err != nil || strings.HasSuffix(cmd.AuthCommand, " ") {
		t.Fatalf("%v %q", err, cmd.AuthCommand)
	}
}

func TestNormalizeAuthRejects(t *testing.T) {
	for want, f := range map[string]func(*store.Network){
		"login method":      func(n *store.Network) { n.AuthMethod = "kerberos" },
		"services nick":     func(n *store.Network) { n.ServicesNick = "Nick Serv" },
		"IDENTIFY takes":    func(n *store.Network) { n.IdentifyArgs = "both" },
		"identify on":       func(n *store.Network) { n.IdentifyOn = "later" },
		"free a taken nick": func(n *store.Network) { n.GhostCmd = "kill" },
		"one line":          func(n *store.Network) { n.AuthCommand = "a\nb" },
		"needs a command":   func(n *store.Network) { n.AuthMethod = store.AuthCommand },
		"timeout is 0–300":  func(n *store.Network) { n.AuthTimeout = 301 },
		"timeout is 0–300 ": func(n *store.Network) { n.AuthTimeout = -1 },
		"services nick ":    func(n *store.Network) { n.ServicesNick = strings.Repeat("n", 65) },
		"login command is ": func(n *store.Network) { n.AuthCommand = strings.Repeat("x", 401) },
	} {
		n := store.Network{Name: "n", Nick: "bob", Host: "h"}
		f(&n)
		err := NormalizeNetwork(&n)
		if !isValidation(err) || !strings.Contains(err.Error(), strings.TrimSpace(want)) {
			t.Errorf("%s: %v", want, err)
		}
	}
}

func TestAuthChangesReconnect(t *testing.T) {
	a := store.Network{Nick: "a", Servers: []store.Server{{Host: "h", Port: 1}}}
	for _, f := range []func(*store.Network){
		func(n *store.Network) { n.AuthMethod = store.AuthNickServ },
		func(n *store.Network) { n.ServicesNick = "NS" },
		func(n *store.Network) { n.IdentifyArgs = store.IdentifyAccount },
		func(n *store.Network) { n.IdentifyOn = store.IdentifyOnPrompt },
		func(n *store.Network) { n.AuthCommand = "x" },
		func(n *store.Network) { n.GhostCmd = "regain" },
		func(n *store.Network) { n.JoinAfterAuth = true },
		func(n *store.Network) { n.AuthTimeout = 9 },
	} {
		b := a
		f(&b)
		if !NeedsReconnect(a, b) {
			t.Errorf("%+v: no reconnect", b)
		}
	}
	b := a
	b.AuthMethod = store.AuthAuto // "" and auto are the same
	if NeedsReconnect(a, b) {
		t.Error("auto and empty differ")
	}
}
