package upstream

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/4evrd/evrd/internal/messages"
	"github.com/4evrd/evrd/internal/store"
)

// italian is a printer in Italian, for checking evrd's words are kept as
// key and arguments.
var italian = messages.New(nil).For(context.Background(), store.User{Language: "it"})

func TestReasonAndHintKeysExist(t *testing.T) {
	var keys []string
	for _, k := range fixedReasons {
		keys = append(keys, "reason."+k)
	}
	for _, k := range certReasons {
		keys = append(keys, "reason."+k)
	}
	for _, h := range hints {
		keys = append(keys, "hint."+h.key)
	}
	for _, k := range keys {
		if got := messages.English.T(messages.Area + k); got == messages.Area+k {
			t.Errorf("irc.%s has no English text", k)
		}
	}
}

// A failure is logged, explained and announced in words, so each reader
// sees it in their language; the server's own lines stay as it sent them.
func TestAFailureInWords(t *testing.T) {
	host, port := sayAndClose(t, "ERROR :Closing Link: 203.0.113.9 (K-Lined: no bouncers)\r\n")
	var rec logRec
	var mu sync.Mutex
	var events []Event
	c := New(store.Network{ID: 32, Name: "Orixon", Nick: "dora", Host: host, Port: port}, quiet,
		Options{OnLog: rec.add, OnEvent: func(e Event) { mu.Lock(); events = append(events, e); mu.Unlock() }})
	run(t, c)
	eventually(t, "the failure announced", func() bool { mu.Lock(); defer mu.Unlock(); return len(events) > 0 })

	var failed, explained bool
	for _, e := range rec.all() {
		switch {
		case e.From == "server":
			if !e.Msg.IsZero() {
				t.Errorf("a server line in words: %+v", e)
			}
		case e.Msg.IsZero():
			t.Errorf("evrd's entry %q isn't in words", e.Text)
		case e.Text != e.Msg.English():
			t.Errorf("entry %q, its words in English %q", e.Text, e.Msg.English())
		case strings.HasPrefix(e.Text, "Couldn't connect"):
			failed = italian.Text(e.Msg) == "Impossibile connettersi: Closing Link: 203.0.113.9 (K-Lined: no bouncers)"
		case e.Msg.Key == "irc.hint.banned":
			explained = strings.Contains(italian.Text(e.Msg), "rifiuta le connessioni")
		}
	}
	if !failed || !explained {
		t.Errorf("failure %v, hint %v in %+v", failed, explained, rec.all())
	}
	mu.Lock()
	e := events[0]
	mu.Unlock()
	if e.Kind != "failed" || e.Title != "Orixon: couldn't connect: Closing Link: 203.0.113.9 (K-Lined: no bouncers)" ||
		italian.Text(e.TitleText) != "Orixon: impossibile connettersi: Closing Link: 203.0.113.9 (K-Lined: no bouncers)" ||
		e.DetailText.Key != "irc.hint.banned" || e.Detail != e.DetailText.English() {
		t.Errorf("event %+v", e)
	}
	if h := c.Health(); h.LastErrorText.English() != h.LastError || h.HintText.Key != "irc.hint.banned" {
		t.Errorf("health %+v", h)
	}
}

func TestRedact(t *testing.T) {
	tx := messages.T("log.identified", "said", "pw hunter2", "x", messages.T("raw", "text", "hunter2!"))
	r := redact(tx, "hunter2")
	if r.Args["said"] != "pw ••••••" || r.Sub["x"].Args["text"] != "••••••!" || tx.Args["said"] != "pw hunter2" {
		t.Errorf("redacted %+v (from %+v)", r, tx)
	}
	if !redact(messages.Text{}, "x").IsZero() {
		t.Error("zero")
	}
	if durText(0).English() != "a moment" || durText(90e9).English() != "1m30s" {
		t.Errorf("durText %q %q", durText(0).English(), durText(90e9).English())
	}
}
