package bouncer

import (
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/4evrd/evrd/internal/history"
	"github.com/4evrd/evrd/internal/store"
)

func TestMentions(t *testing.T) {
	for _, c := range []struct {
		text string
		want bool
	}{
		{"hey alice", true},
		{"Alice: ping", true},
		{"@alice!", true},
		{"malice aforethought", false},
		{"alice_ is someone else", false},
		{"nothing here", false},
	} {
		if got := mentions(c.text, "alice"); got != c.want {
			t.Errorf("mentions(%q) = %v, want %v", c.text, got, c.want)
		}
	}
}

func TestUnreadAndMarkRead(t *testing.T) {
	st, err := store.Open("sqlite:" + filepath.Join(t.TempDir(), "evrd.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	hist, err := history.Open(filepath.Join(t.TempDir(), "history"))
	if err != nil {
		t.Fatal(err)
	}
	defer hist.Close()
	b := New(st, hist, slog.New(slog.NewTextHandler(io.Discard, nil)))
	u, _ := st.CreateUser(t.Context(), store.User{Username: "alice"})
	n, _ := st.CreateNetwork(t.Context(), store.Network{UserID: u.ID, Name: "net"})
	b.initReads(n)

	now := time.Now()
	say := func(target, from, text string) {
		b.observe(n, "alice", history.Message{NetworkID: n.ID, Target: target, Sender: from, Kind: "message", Text: text, Time: now})
	}
	say("#go", "bob", "morning")
	say("#go", "bob", "alice: you there?")
	say("carol", "carol", "psst")
	b.observe(n, "alice", history.Message{NetworkID: n.ID, Target: "#go", Sender: "dave", Kind: "join", Time: now})

	get := func(name string) BufferState {
		for _, s := range b.Buffers(n.ID) {
			if s.Name == name {
				return s
			}
		}
		t.Fatalf("no buffer %s", name)
		return BufferState{}
	}
	if s := get("#go"); s.Unread != 2 || s.Mentions != 1 {
		t.Fatalf("#go: %+v", s)
	}
	// Private messages always count as mentions.
	if s := get("carol"); s.Unread != 1 || s.Mentions != 1 {
		t.Fatalf("carol: %+v", s)
	}
	// Speaking in a buffer reads it.
	say("carol", "alice", "hi")
	if s := get("carol"); s.Unread != 0 || s.Mentions != 0 {
		t.Fatalf("carol after reply: %+v", s)
	}
	if err := b.MarkRead(n.ID, "#go"); err != nil {
		t.Fatal(err)
	}
	if s := get("#go"); s.Unread != 0 || s.ReadAt.IsZero() {
		t.Fatalf("#go after mark: %+v", s)
	}
	markers, _ := st.ReadMarkers(t.Context(), n.ID)
	if _, ok := markers["#go"]; !ok {
		t.Fatalf("read marker not stored: %v", markers)
	}
}

func TestBuffersHideTheNameless(t *testing.T) {
	st, err := store.Open("sqlite:" + filepath.Join(t.TempDir(), "evrd.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	hist, err := history.Open(filepath.Join(t.TempDir(), "history"))
	if err != nil {
		t.Fatal(err)
	}
	defer hist.Close()
	b := New(st, hist, slog.New(slog.NewTextHandler(io.Discard, nil)))
	u, _ := st.CreateUser(t.Context(), store.User{Username: "alice"})
	n, _ := st.CreateNetwork(t.Context(), store.Network{UserID: u.ID, Name: "net"})

	// what older versions filed prefix-less server notices under
	now := time.Now()
	hist.Add(history.Message{NetworkID: n.ID, Target: "", Kind: "notice", Text: "*** Looking up your hostname", Time: now})
	hist.Add(history.Message{NetworkID: n.ID, Target: "irc.example.org", Sender: "irc.example.org", Kind: "notice", Text: "hi", Time: now})
	names := func() (out []string) {
		for _, s := range b.Buffers(n.ID) {
			out = append(out, s.Name)
		}
		return out
	}
	// not running here: from history
	if got := names(); len(got) != 1 || got[0] != "irc.example.org" {
		t.Fatalf("from history: %q", got)
	}
	// running here
	b.initReads(n)
	b.observe(n, "alice", history.Message{NetworkID: n.ID, Target: "", Kind: "notice", Text: "*** again", Time: now})
	for _, name := range names() {
		if name == "" {
			t.Fatalf("live buffers include a nameless one: %q", names())
		}
	}
}

func TestModulesOffServerWide(t *testing.T) {
	st, err := store.Open("sqlite:" + filepath.Join(t.TempDir(), "evrd.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	hist, err := history.Open(filepath.Join(t.TempDir(), "history"))
	if err != nil {
		t.Fatal(err)
	}
	defer hist.Close()
	b := New(st, hist, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if len(b.ModulesOff()) != 0 {
		t.Fatal("modules off before anyone said so")
	}
	b.SetModulesOff([]string{"AutoOp", "autovoice"})
	if off := b.ModulesOff(); !off["autoop"] || !off["autovoice"] || off["perform"] {
		t.Fatalf("off: %v", off)
	}
	b.SetModulesOff(nil)
	if len(b.ModulesOff()) != 0 {
		t.Fatal("still off")
	}
}
