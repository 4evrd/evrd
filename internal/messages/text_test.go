package messages

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"golang.org/x/text/language"
)

func TestTextAndErrors(t *testing.T) {
	it := &Printer{layers: []catalog{builtin["it"]}, tag: language.Italian}

	// a Text keeps its arguments, a Text argument stays one
	tx := T("log.couldntConnect", "reason", T("reason.timedOut"))
	if tx.English() != "Couldn't connect: connection timed out" {
		t.Errorf("English: %q", tx.English())
	}
	if got := it.Text(tx); got != "Impossibile connettersi: connessione scaduta" {
		t.Errorf("Italian: %q", got)
	}
	// stored as JSON and read back, it says the same
	b, _ := json.Marshal(tx)
	var back Text
	if err := json.Unmarshal(b, &back); err != nil || it.Text(back) != it.Text(tx) {
		t.Errorf("JSON %s: %v", b, err)
	}
	// counts come back as text: plurals still work
	if got := it.Text(T("count.networks", "count", 1)); got != "1 rete" {
		t.Errorf("count from text: %q", got)
	}
	if got := it.Text(Raw("Closing Link: bye")); got != "Closing Link: bye" {
		t.Errorf("raw: %q", got)
	}
	if (Text{}).English() != "" || !(Text{}).IsZero() {
		t.Error("zero Text")
	}

	// an Error is English as an error, in the reader's language when said
	err := Errorf("err.noModule", "module", `"x"`)
	wrapped := fmt.Errorf("saving: %w", err)
	if err.Error() != `no module "x"` || TextOf(wrapped).Key != "irc.err.noModule" {
		t.Errorf("error: %q / %+v", err.Error(), TextOf(wrapped))
	}
	if got := it.Say(wrapped); got != `nessun modulo "x"` {
		t.Errorf("said: %q", got)
	}
	plain := errors.New("disk full")
	if it.Say(plain) != "disk full" || TextOf(plain).Key != "irc.raw" || TextOf(nil).Key != "" || it.Say(nil) != "" {
		t.Error("plain errors pass through")
	}
	// an error argument that knows its words is put in the language too
	if got := it.T("irc.saveFailed", "error", err); got != `Impossibile salvare: nessun modulo "x"` {
		t.Errorf("error argument: %q", got)
	}
	if got := T("saveFailed", "error", err); got.Sub["error"].Key != "irc.err.noModule" {
		t.Errorf("error argument kept in words: %+v", got)
	}

	for in, want := range map[string]string{
		"a network needs a nick": "A network needs a nick.",
		"è troppo lungo":         "È troppo lungo.",
		"done.":                  "Done.",
		"ネットワークがありません。": "ネットワークがありません。",
		"really?": "Really?",
		"":        "",
	} {
		if got := Sentence(in); got != want {
			t.Errorf("Sentence(%q) = %q, want %q", in, got, want)
		}
	}
}

// A key is a text or a group of them, never both: a file with nested keys
// (YAML, i18next JSON) couldn't hold both.
func TestKeysAreLeaves(t *testing.T) {
	for k := range builtin["en"] {
		for other := range builtin["en"] {
			if strings.HasPrefix(other, k+".") {
				t.Errorf("%s is a text and a group (%s)", k, other)
			}
		}
	}
}

// Every irc.* key evrd's code names has an English text. Keys made at run
// time (a prefix and a name) are checked by their packages' tests.
func TestKeysInCodeExist(t *testing.T) {
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`messages\.(?:T|Errorf)\("([^"]+)"`),
		regexp.MustCompile(`\binvalid\("([^"]+)"`),
		regexp.MustCompile(`se\.(?:say|tr)\("([^"]+)"`),
		regexp.MustCompile(`s\.say\(w, r, [^,]+, "([^"]+)"`),
		regexp.MustCompile(`messages\.Area\s*\+\s*"([^"]+)"`),
		regexp.MustCompile(`tOr\(p, "([^"]+)"`),
	}
	prefix := map[*regexp.Regexp]string{patterns[1]: "err."}
	found := 0
	err := filepath.WalkDir("..", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, re := range patterns {
			for _, m := range re.FindAllStringSubmatch(string(src), -1) {
				key := prefix[re] + m[1]
				if strings.HasSuffix(key, ".") {
					continue // a prefix
				}
				found++
				if _, ok := builtin["en"][Area+key]; !ok {
					t.Errorf("%s: irc.%s has no English text", path, key)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if found < 300 {
		t.Errorf("found only %d keys in the code", found)
	}
}
