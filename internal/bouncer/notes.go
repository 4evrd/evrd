package bouncer

// A user's notes (ZNC's notes module): short texts under a key, edited in
// the web app and from IRC (/msg *notes), and shown to IRC clients as they
// attach.

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/4evrd/evrd/internal/messages"
	"github.com/4evrd/evrd/internal/store"
)

// Limits on notes: a key is a word of up to MaxNoteKey characters, a note
// one line of up to MaxNoteText, and a user has at most MaxNotes.
const (
	MaxNoteKey  = 64
	MaxNoteText = 400
	MaxNotes    = 100
)

// Notes lists a user's notes by key.
func (b *Bouncer) Notes(userID int64) ([]store.UserNote, error) {
	return b.store.Notes(context.Background(), userID)
}

// How SetNote treats a note that exists, or doesn't.
const (
	NoteSet = iota // add it or change it
	NoteAdd        // only add it: ErrNoteExists when there's one (ZNC's Add)
	NoteMod        // only change it: store.ErrNotFound when there's none (ZNC's Mod)
)

// ErrNoteExists is SetNote's NoteAdd for a key that has a note.
var ErrNoteExists = errors.New("bouncer: the note exists")

// SetNote adds a note under key or changes the one there, as mode says.
// Errors about the key or text say what's wrong in words.
func (b *Bouncer) SetNote(userID int64, key, text string, mode int) error {
	key, text = strings.TrimSpace(key), strings.TrimSpace(text)
	if key == "" || strings.ContainsAny(key[:1], "+-") || strings.ContainsAny(key, " \t\r\n\x00") || utf8.RuneCountInString(key) > MaxNoteKey {
		return messages.Errorf("notes.badKey", "max", MaxNoteKey)
	}
	if text == "" || strings.ContainsAny(text, "\r\n\x00") || utf8.RuneCountInString(text) > MaxNoteText {
		return messages.Errorf("notes.badText", "max", MaxNoteText)
	}
	notes, err := b.Notes(userID)
	if err != nil {
		return err
	}
	have := false
	for _, n := range notes {
		have = have || n.Key == key
	}
	switch {
	case mode == NoteMod && !have:
		return store.ErrNotFound
	case mode == NoteAdd && have:
		return ErrNoteExists
	case !have && len(notes) >= MaxNotes:
		return messages.Errorf("notes.tooMany", "max", MaxNotes)
	}
	return b.store.PutNote(context.Background(), store.UserNote{UserID: userID, Key: key, Text: text, UpdatedAt: time.Now()})
}

// DeleteNote drops a note, failing with store.ErrNotFound when there's none.
func (b *Bouncer) DeleteNote(userID int64, key string) error {
	ok, err := b.store.DeleteNote(context.Background(), userID, strings.TrimSpace(key))
	if err == nil && !ok {
		err = store.ErrNotFound
	}
	return err
}
