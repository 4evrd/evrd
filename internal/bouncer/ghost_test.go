package bouncer

import (
	"io"
	"log/slog"
	"path/filepath"
	"testing"

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
