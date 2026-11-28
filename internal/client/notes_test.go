package client

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/4evrd/evrd/internal/store"
)

// notes sends a line to *notes and waits for a reply from it containing want.
func (ic *ircClient) notes(line, want string) string {
	ic.t.Helper()
	ic.send("PRIVMSG *notes :" + line)
	got := ic.expect(want)
	if !strings.HasPrefix(got, ":*notes!notes@") {
		ic.t.Fatalf("not from *notes: %q", got)
	}
	return got
}

func TestNotesCommands(t *testing.T) {
	r := newRig(t)
	ic := r.attached()
	ic.notes("list", "You have no notes yet: /msg *notes add <key> <note> adds one.")
	ic.notes("add wifi  hunter2 at  home", "Note wifi saved.")
	ic.notes("add wifi again", "There's a note wifi already: mod wifi <note> changes it.")
	ic.notes("mod nope x", "No note nope.")
	ic.notes("mod wifi hunter3", "Note wifi saved.")
	ic.notes("+todo buy milk", "Note todo saved.")
	ic.notes("get wifi", "hunter3")
	ic.notes("list", "| todo | buy milk")
	ic.notes("-todo", "Note todo deleted.")
	ic.notes("del todo", "No note todo.")
	ic.notes("add", "Usage: add <key> <note>")
	ic.notes("add -bad x", "A note's key is one word")
	ic.notes("frobnicate", "Unknown command frobnicate")
	ic.notes("help", "| MOD")
	// *status's notes command is the same, answered by *status
	ic.status("notes add door 1234", "Note door saved.")
	ic.status("notes", "| door | 1234")

	notes, _ := r.st.Notes(context.Background(), r.user.ID)
	if len(notes) != 2 || notes[0].Key != "door" || notes[1].Key != "wifi" || notes[1].Text != "hunter3" {
		t.Fatalf("stored: %+v", notes)
	}

	// spacing inside a note is kept
	ic.notes("+spaced a  b", "Note spaced saved.")
	ic.notes("get spaced", "a  b")
}

func TestNotesAreShownOnLogin(t *testing.T) {
	r := newRig(t)
	r.st.PutNote(context.Background(), store.UserNote{UserID: r.user.ID, Key: "wifi", Text: "hunter2"})
	ic := r.attach("alice/one:alice-password")
	ic.expect(" 376 ")
	ic.expect("NOTICE alice_one :Your notes (1) - /msg *notes help to edit them:")
	ic.expect(":*notes!notes@")

	// not with ShowNotesOnLogin off
	r.setUser(func(u *store.User) { u.ShowNotesOnLogin = false })
	ic = r.attach("alice/one:alice-password")
	ic.expect(" 376 ")
	if ic.tryExpect("Your notes", 500*time.Millisecond) {
		t.Error("notes shown with ShowNotesOnLogin off")
	}
}

func TestNotesNickIsKnown(t *testing.T) {
	r := newRig(t)
	ic := r.attached()
	ic.send("WHOIS *notes")
	ic.expect(" 311 ")
	// a NOTICE to it goes nowhere, and no 401
	ic.send("NOTICE *notes :hi")
	ic.status("version", "evrd")
	for _, l := range r.irc.Lines() {
		if strings.Contains(l, "*notes") {
			t.Fatalf("reached the network: %q", l)
		}
	}
}
