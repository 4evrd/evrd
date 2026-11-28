package client

// *notes: the account's notes (ZNC's notes module), as ZNC has them —
// /msg *notes list | add <key> <note> | mod <key> <note> | del <key> |
// get <key>, and +<key> <note> (set) or -<key> (delete) for short. *status's
// notes command takes the same. With the account's ShowNotesOnLogin, a
// client is sent them as it attaches.

import (
	"errors"
	"fmt"
	"strings"

	"github.com/4evrd/evrd/internal/bouncer"
	"github.com/4evrd/evrd/internal/store"
)

const notesNick = "*notes"

// notesQuery runs a line sent to *notes, which answers it.
func (se *session) notesQuery(text string) {
	se.pr = nil // the account's language may have changed since
	se.replyAs = notesNick
	defer func() { se.replyAs = "" }()
	se.notesCommand(text)
}

// cmdNotes is *status's notes: *notes's commands, answered by *status.
func (se *session) cmdNotes(args []string) { se.notesCommand(strings.Join(args, " ")) }

// notesCommand runs one of *notes's commands.
func (se *session) notesCommand(text string) {
	word, rest := cutWord(text)
	cmd := strings.ToLower(word)
	switch {
	case cmd == "" || cmd == "list":
		se.listNotes()
	case cmd == "help":
		se.notesHelp()
	case cmd == "add" || cmd == "mod" || cmd == "set":
		key, note := cutWord(rest)
		if key == "" || note == "" {
			se.say("notes.usage." + cmd)
			return
		}
		mode := map[string]int{"add": bouncer.NoteAdd, "mod": bouncer.NoteMod, "set": bouncer.NoteSet}[cmd]
		se.setNote(key, note, mode)
	case cmd == "del" || cmd == "delete" || cmd == "rem":
		key, _ := cutWord(rest)
		if key == "" {
			se.say("notes.usage.del")
			return
		}
		se.delNote(key)
	case cmd == "get":
		key, _ := cutWord(rest)
		if key == "" {
			se.say("notes.usage.get")
			return
		}
		se.getNote(key)
	case len(word) > 1 && word[0] == '+':
		if strings.TrimSpace(rest) == "" {
			se.say("notes.usage.set")
			return
		}
		se.setNote(word[1:], rest, bouncer.NoteSet)
	case len(word) > 1 && word[0] == '-':
		se.delNote(word[1:])
	default:
		se.say("notes.unknown", "command", word, "notes", notesNick)
	}
}

// cutWord splits off s's first word, the rest keeping its spacing.
func cutWord(s string) (word, rest string) {
	s = strings.TrimLeft(s, " ")
	word, rest, _ = strings.Cut(s, " ")
	return word, strings.TrimSpace(rest)
}

func (se *session) setNote(key, note string, mode int) {
	err := se.srv.b.SetNote(se.uid, key, note, mode)
	switch {
	case err == nil:
		se.say("notes.saved", "key", key)
	case errors.Is(err, bouncer.ErrNoteExists):
		se.say("notes.exists", "key", key)
	case errors.Is(err, store.ErrNotFound):
		se.say("notes.noSuch", "key", key)
	default:
		se.sayErr(err)
	}
}

func (se *session) delNote(key string) {
	err := se.srv.b.DeleteNote(se.uid, key)
	switch {
	case err == nil:
		se.say("notes.deleted", "key", key)
	case errors.Is(err, store.ErrNotFound):
		se.say("notes.noSuch", "key", key)
	default:
		se.say("notes.failed", "error", err)
	}
}

func (se *session) getNote(key string) {
	notes, err := se.srv.b.Notes(se.uid)
	if err != nil {
		se.say("notes.failed", "error", err)
		return
	}
	for _, n := range notes {
		if n.Key == key {
			se.statusf("%s", n.Text)
			return
		}
	}
	se.say("notes.noSuch", "key", key)
}

func (se *session) listNotes() {
	notes, err := se.srv.b.Notes(se.uid)
	if err != nil {
		se.say("notes.failed", "error", err)
		return
	}
	if len(notes) == 0 {
		se.say("notes.none", "notes", notesNick)
		return
	}
	rows := [][]string{{se.tr("notes.key"), se.tr("notes.note")}}
	for _, n := range notes {
		rows = append(rows, []string{n.Key, n.Text})
	}
	for _, l := range markdownTable(rows) {
		se.statusf("%s", l)
	}
}

func (se *session) notesHelp() {
	rows := [][]string{{se.tr("help.command"), se.tr("help.arguments"), se.tr("help.description")}}
	// command, arguments, and its help text's key
	for _, c := range [][3]string{{"LIST", "", "list"}, {"ADD", "<key> <note>", "add"}, {"MOD", "<key> <note>", "mod"},
		{"DEL", "<key>", "del"}, {"GET", "<key>", "get"}, {"+<key>", "<note>", "set"}, {"-<key>", "", "del"}} {
		rows = append(rows, []string{c[0], c[1], se.tr("notes.help." + c[2])})
	}
	for _, l := range markdownTable(rows) {
		se.statusf("%s", l)
	}
}

// showNotes sends an IRC client the account's notes as it attaches, as
// notices from *notes (with the account's ShowNotesOnLogin).
func (se *session) showNotes() {
	if se.web != nil || se.isManaged() || !se.account().ShowNotesOnLogin {
		return
	}
	notes, err := se.srv.b.Notes(se.uid)
	if err != nil || len(notes) == 0 {
		return
	}
	notice := func(text string) {
		se.send(fmt.Sprintf(":%s!notes@%s NOTICE %s :%s", notesNick, se.serverName(), se.unick, ircText(text)))
	}
	notice(se.tr("notes.onLogin", "count", len(notes), "notes", notesNick))
	for _, n := range notes {
		notice(n.Key + ": " + n.Text)
	}
}
