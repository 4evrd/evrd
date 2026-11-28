package zncimport

import (
	"testing"

	"github.com/4evrd/evrd/internal/store"
)

// autoreply and autoattach carry over with what's on their LoadModule line.
func TestAutoReplyAndAutoAttach(t *testing.T) {
	net := func(conf string) (store.Network, map[string]string) {
		t.Helper()
		users, err := ParseConfig([]byte("<User a>\n<Network n>\nServer = irc.example.net 6667\n" + conf + "</Network>\n</User>\n"))
		if err != nil {
			t.Fatal(err)
		}
		store.SetSecretKeys(nil)
		var n store.Network
		x := users[0].Networks[0]
		x.Apply(&n)
		notes := map[string]string{}
		for _, no := range x.Notes() {
			notes[no.Key] = no.Args[1].(string)
		}
		return n, notes
	}

	n, notes := net("LoadModule = autoreply Out for lunch, back at 2\nLoadModule = autoattach #home !#noise #work*\n")
	if m := n.Modules["autoreply"]; !m.Enabled || m.Settings["reply"] != "Out for lunch, back at 2" {
		t.Errorf("autoreply %+v", m)
	}
	if m := n.Modules["autoattach"]; !m.Enabled || m.Settings["rules"] != "#home\n!#noise\n#work*" {
		t.Errorf("autoattach %+v", m)
	}
	if len(notes) != 0 {
		t.Errorf("notes %v", notes)
	}

	// no arguments: autoreply's default reply; autoattach's rules are in
	// ZNC's own data, so it waits to be filled in
	n, notes = net("LoadModule = autoreply\nLoadModule = autoattach\n")
	if m := n.Modules["autoreply"]; !m.Enabled || len(m.Settings) != 0 {
		t.Errorf("autoreply %+v", m)
	}
	if _, ok := n.Modules["autoattach"]; ok || notes["irc.znc.moduleData"] != "autoattach" {
		t.Errorf("autoattach %+v, notes %v", n.Modules, notes)
	}
}
