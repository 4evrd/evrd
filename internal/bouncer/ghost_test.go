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

func TestADeviceReplacesItsGhost(t *testing.T) {
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
	alice, _ := st.CreateUser(t.Context(), store.User{Username: "alice"})
	why := map[string]string{}
	closer := func(name string) func(string) { return func(r string) { why[name] = r } }
	phone := ClientInfo{Device: "Pixel", Networks: []string{"libera"}}

	if _, err := b.AttachClient(alice.ID, 1, phone, closer("ghost")); err != nil {
		t.Fatal(err)
	}
	// the phone again: replaces its ghost, the limit of 1 notwithstanding
	again, err := b.AttachClient(alice.ID, 1, phone, closer("again"))
	if err != nil || why["ghost"] != DropReplaced || b.Clients(alice.ID) != 1 {
		t.Fatalf("replace: %v %v %d", err, why, b.Clients(alice.ID))
	}
	// another device, or the phone on another network, is a client of its own
	if _, err := b.AttachClient(alice.ID, 1, ClientInfo{Device: "Laptop", Networks: []string{"libera"}}, closer("laptop")); err != ErrTooManyClients {
		t.Fatalf("another device: %v", err)
	}
	if _, err := b.AttachClient(alice.ID, 2, ClientInfo{Device: "Pixel", Networks: []string{"oftc"}}, closer("oftc")); err != nil || why["again"] != "" {
		t.Fatalf("another network: %v %v", err, why)
	}
	// without a client password, clients can't be told apart: none replaced
	if _, err := b.AttachClient(alice.ID, 0, ClientInfo{Networks: []string{"libera"}}, closer("a")); err != nil {
		t.Fatal(err)
	}
	if _, err := b.AttachClient(alice.ID, 0, ClientInfo{Networks: []string{"libera"}}, closer("b")); err != nil || why["a"] != "" {
		t.Fatalf("no device: %v %v", err, why)
	}
	again.Release()
}

func TestClientsSharingAPasswordDontReplaceEachOther(t *testing.T) {
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
	alice, _ := st.CreateUser(t.Context(), store.User{Username: "alice"})
	why := map[string]string{}
	closer := func(name string) func(string) { return func(r string) { why[name] = r } }
	pc := ClientInfo{Device: "Shared", Networks: []string{"libera"}, IP: "192.0.2.1", Software: "mIRC"}

	// the first one answers when asked: the second is another client
	first, err := b.AttachClient(alice.ID, 0, pc, closer("first"))
	if err != nil {
		t.Fatal(err)
	}
	first.Answers(func(time.Duration) bool { return true })
	second, err := b.AttachClient(alice.ID, 0, ClientInfo{Device: "Shared", Networks: []string{"libera"}, IP: "192.0.2.2"}, closer("second"))
	if err != nil || why["first"] != "" || b.Clients(alice.ID) != 2 || second.Replaced() {
		t.Fatalf("shared: %v %v %d", err, why, b.Clients(alice.ID))
	}
	if s := second.Sharing(); len(s) != 1 || s[0].IP != "192.0.2.1" || s[0].Software != "mIRC" {
		t.Fatalf("sharing: %+v", s)
	}
	// they still count toward the limit
	if _, err := b.AttachClient(alice.ID, 2, ClientInfo{Device: "Laptop", Networks: []string{"libera"}}, closer("laptop")); err != ErrTooManyClients {
		t.Fatalf("limit: %v", err)
	}

	// one that doesn't answer is a ghost, replaced (the one that does stays)
	second.Answers(func(time.Duration) bool { return false })
	third, err := b.AttachClient(alice.ID, 0, pc, closer("third"))
	if err != nil || why["second"] != DropReplaced || why["first"] != "" || !third.Replaced() || len(third.Sharing()) != 1 {
		t.Fatalf("ghost: %v %v", err, why)
	}

	// clients back after a restart were all there together: none replaced
	if _, err := b.ReattachClient(alice.ID, pc, closer("held")); err != nil || why["third"] != "" || b.Clients(alice.ID) != 3 {
		t.Fatalf("reattach: %v %v %d", err, why, b.Clients(alice.ID))
	}
}
