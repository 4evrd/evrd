package handlers

import "testing"

func TestTypingOf(t *testing.T) {
	for line, want := range map[string]string{
		"@+typing=active :bob!b@h TAGMSG #evrd":      "#evrd bob active",
		"@+typing=paused :bob!b@h TAGMSG alice":      "alice bob paused",
		"@+typing=done;+x=y :bob TAGMSG #evrd":       "#evrd bob done",
		"@+typing=active :alice!a@h TAGMSG #evrd":    "", // the user's own
		"@+typing=nope :bob!b@h TAGMSG #evrd":        "", // not a typing state
		"@+draft/react=👍 :bob!b@h TAGMSG #evrd":      "", // another tag
		"@+typing=active :bob!b@h PRIVMSG #evrd :hi": "",
		":bob!b@h TAGMSG #evrd":                      "",
		"@+typing=active :bob!b@h TAGMSG":            "",
	} {
		got := ""
		if f, ok := typingOf(line, "Alice"); ok {
			if f["type"] != "typing" {
				t.Fatalf("%q: frame %v", line, f)
			}
			got = f["target"] + " " + f["nick"] + " " + f["state"]
		}
		if got != want {
			t.Errorf("%q: got %q, want %q", line, got, want)
		}
	}
}
