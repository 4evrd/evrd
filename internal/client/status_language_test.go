package client

import (
	"context"
	"testing"

	"github.com/4evrd/evrd/internal/messages"
)

// language picks the account's language from IRC: *status answers in it
// from then on, and the web app has it too.
func TestLanguageFromIRC(t *testing.T) {
	r := newRig(t)
	r.srv.SetMessages(messages.New(nil))
	ic := r.attached()
	lang := func() string {
		u, _ := r.acc.UserByID(context.Background(), r.user.ID)
		return u.Language
	}

	ic.status("language", "You haven't picked a language")
	ic.expect("ro Română")
	ic.status("language klingon", `No language "klingon"`)
	ic.status("language ro", "Limba setată la Română (ro)")
	if got := lang(); got != "ro" {
		t.Fatalf("saved %q", got)
	}
	ic.status("foo", `Comandă necunoscută "foo"`)
	ic.status("lang Deutsch", "Sprache auf Deutsch (de) gesetzt")
	ic.status("language", "Deine Sprache: Deutsch (de).")
	ic.status("language -", "Language cleared")
	if got := lang(); got != "" {
		t.Fatalf("cleared to %q", got)
	}
}
