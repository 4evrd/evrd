package bouncer

import (
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"code.ircforever.org/ircforever/evrd/internal/history"
	"code.ircforever.org/ircforever/evrd/internal/store"
)

func TestHistoryFollowsNetworksAndUsers(t *testing.T) {
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

	alice, _ := st.CreateUser(store.User{Username: "alice"})
	bob, _ := st.CreateUser(store.User{Username: "bob"})
	a1, _ := st.CreateNetwork(store.Network{UserID: alice.ID, Name: "a1"})
	a2, _ := st.CreateNetwork(store.Network{UserID: alice.ID, Name: "a2"})
	b1, _ := st.CreateNetwork(store.Network{UserID: bob.ID, Name: "b1"})
	for _, n := range []store.Network{a1, a2, b1} {
		hist.Add(history.Message{NetworkID: n.ID, Target: "#x", Text: "hi", Time: time.Now()})
	}
	has := func(id int64) bool { m, _ := hist.Recent(id, "#x", 1); return len(m) == 1 }

	if err := b.DeleteNetwork(alice.ID, a1.ID); err != nil || has(a1.ID) {
		t.Fatalf("delete network: err=%v history left=%v", err, has(a1.ID))
	}
	// Deleted behind evrd's back (as evrctl does): housekeeping drops it.
	st.DeleteNetwork(bob.ID, b1.ID)
	users, _ := st.ListUsers()
	b.dropOrphanHistory(users)
	if has(b1.ID) || !has(a2.ID) {
		t.Fatalf("orphan cleanup: b1 left=%v a2 kept=%v", has(b1.ID), has(a2.ID))
	}
	if err := b.DeleteUser(alice.ID); err != nil || has(a2.ID) {
		t.Fatalf("delete user: err=%v history left=%v", err, has(a2.ID))
	}
}
