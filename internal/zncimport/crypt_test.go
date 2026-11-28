package zncimport

import "testing"

// ZNC's crypt keeps its keys in its moddata, not the config: the import
// says to set them again under evrd's crypt module, and doesn't skip it.
func TestCryptFromZNC(t *testing.T) {
	users, err := ParseConfig([]byte("<Network efnet>\n\tServer = irc.efnet.org 6667\n\tLoadModule = crypt\n</Network>\n"))
	if err != nil {
		t.Fatal(err)
	}
	x := users[0].Networks[0]
	notes := map[string]any{}
	for _, no := range x.Notes() {
		notes[no.Key] = no.Args[1]
	}
	if notes["irc.znc.moduleData"] != "crypt" || notes["irc.znc.modulesSkipped"] != nil {
		t.Fatalf("notes %v", x.Notes())
	}
}
