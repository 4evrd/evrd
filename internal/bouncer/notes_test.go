package bouncer

import (
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/4evrd/evrd/internal/messages"
	"github.com/4evrd/evrd/internal/store"
)

func TestNotes(t *testing.T) {
	r := newLiveRig(t)
	alice := r.user("alice", nil)
	b := r.b
	if err := b.SetNote(alice.ID, "wifi", " hunter2 ", NoteAdd); err != nil {
		t.Fatal(err)
	}
	if err := b.SetNote(alice.ID, "wifi", "x", NoteAdd); !errors.Is(err, ErrNoteExists) {
		t.Errorf("add over one: %v", err)
	}
	if err := b.SetNote(alice.ID, "nope", "x", NoteMod); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("mod of none: %v", err)
	}
	if err := b.SetNote(alice.ID, "wifi", "hunter3", NoteMod); err != nil {
		t.Errorf("mod: %v", err)
	}
	notes, _ := b.Notes(alice.ID)
	if len(notes) != 1 || notes[0].Text != "hunter3" {
		t.Fatalf("notes: %+v", notes)
	}

	// what isn't a note says so in words
	for _, c := range []struct{ key, text, want string }{
		{"two words", "x", "notes.badKey"}, {"-dash", "x", "notes.badKey"}, {"+plus", "x", "notes.badKey"},
		{strings.Repeat("k", MaxNoteKey+1), "x", "notes.badKey"}, {"k", "", "notes.badText"},
		{"k", "two\nlines", "notes.badText"}, {"k", strings.Repeat("x", MaxNoteText+1), "notes.badText"},
	} {
		var me *messages.Error
		if err := b.SetNote(alice.ID, c.key, c.text, NoteSet); !errors.As(err, &me) || me.Text.Key != messages.Area+c.want {
			t.Errorf("%q %q: %v", c.key, c.text, err)
		}
	}

	// at most MaxNotes, but changing one is fine
	for i := len(notes); i < MaxNotes; i++ {
		if err := b.SetNote(alice.ID, "n"+strconv.Itoa(i), "x", NoteSet); err != nil {
			t.Fatal(err)
		}
	}
	var me *messages.Error
	if err := b.SetNote(alice.ID, "onemore", "x", NoteSet); !errors.As(err, &me) || me.Text.Key != messages.Area+"notes.tooMany" {
		t.Errorf("past the limit: %v", err)
	}
	if err := b.SetNote(alice.ID, "wifi", "hunter4", NoteSet); err != nil {
		t.Errorf("changing one at the limit: %v", err)
	}

	if err := b.DeleteNote(alice.ID, "wifi"); err != nil {
		t.Error(err)
	}
	if err := b.DeleteNote(alice.ID, "wifi"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("delete again: %v", err)
	}
}

func TestTellOthersAndSeenAddresses(t *testing.T) {
	r := newLiveRig(t)
	alice := r.user("alice", nil)
	heard := map[string][]messages.Text{}
	att := func(name string) *Attachment {
		a, err := r.b.AttachClient(alice.ID, 0, ClientInfo{}, func(string) {})
		if err != nil {
			t.Fatal(err)
		}
		a.Tell(func(t messages.Text) { heard[name] = append(heard[name], t) })
		return a
	}
	a, b := att("a"), att("b")
	r.b.TrackStream(alice.ID, ClientInfo{}, func(string) {}) // a web chat hears nothing
	if n := r.b.TellOthers(alice.ID, b.ID(), messages.T("x")); n != 1 || len(heard["a"]) != 1 || len(heard["b"]) != 0 {
		t.Errorf("told %d: %v", n, heard)
	}
	a.Release()
	if n := r.b.TellOthers(alice.ID, b.ID(), messages.T("x")); n != 0 {
		t.Errorf("told a released client: %d", n)
	}

	if !r.b.SeeClientIP(alice.ID, "192.0.2.1") || r.b.SeeClientIP(alice.ID, "192.0.2.1") || !r.b.SeeClientIP(alice.ID, "192.0.2.2") {
		t.Error("seen addresses")
	}
	if !r.b.SeeClientIP(alice.ID, "") {
		t.Error("no address counts as new")
	}
}
