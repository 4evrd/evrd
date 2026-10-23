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

func TestMarkReadAtOnlyMovesForward(t *testing.T) {
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
	n, _ := st.CreateNetwork(t.Context(), store.Network{UserID: u.ID, Name: "net", Nick: "alice"})
	b.initReads(n)

	base := time.Now().Add(-time.Hour).Truncate(time.Millisecond)
	for i, text := range []string{"one", "two alice", "three"} {
		m := history.Message{NetworkID: n.ID, Target: "#go", Sender: "bob", Kind: "message", Text: text, Time: base.Add(time.Duration(i) * time.Minute)}
		hist.Add(m)
		b.observe(n, "alice", m)
	}
	unread := func() (int, int) {
		for _, s := range b.Buffers(n.ID) {
			if s.Name == "#go" {
				return s.Unread, s.Mentions
			}
		}
		return -1, -1
	}
	// read up to the first line: two left, one a mention
	got, err := b.MarkReadAt(n.ID, "#Go", base)
	if err != nil || !got.Equal(base) {
		t.Fatalf("marked %v %v", got, err)
	}
	if u, m := unread(); u != 2 || m != 1 {
		t.Fatalf("unread %d, mentions %d", u, m)
	}
	// back in time: stays where it was
	if got, _ := b.MarkReadAt(n.ID, "#go", base.Add(-time.Minute)); !got.Equal(base) {
		t.Fatalf("moved back to %v", got)
	}
	if ms, _ := b.ReadMarkers(n.ID); !ms["#go"].Equal(base) {
		t.Fatalf("markers %v", ms)
	}
	b.MarkReadAt(n.ID, "#go", base.Add(2*time.Minute))
	if u, m := unread(); u != 0 || m != 0 {
		t.Fatalf("all read: unread %d, mentions %d", u, m)
	}
	// a buffer the node doesn't track yet: just the marker
	if got, _ := b.MarkReadAt(n.ID, "#new", base); !got.Equal(base) {
		t.Fatalf("new buffer: %v", got)
	}
}
