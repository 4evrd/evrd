package identfile

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/4evrd/evrd/internal/store"
)

var hosts = map[string][]netip.Addr{
	"alice.example.org": {netip.MustParseAddr("2001:db8::a"), netip.MustParseAddr("198.51.100.1")}, // the v4 is the host's own
	"bob.example.org":   {netip.MustParseAddr("2001:db8::b")},
	"shared.example":    {netip.MustParseAddr("2001:db8::5")},
}

func resolve(_ context.Context, host string) ([]netip.Addr, error) {
	if a, ok := hosts[host]; ok {
		return a, nil
	}
	return nil, errors.New("no such host")
}

func TestEntries(t *testing.T) {
	users := []store.User{
		{ID: 1, Username: "alice", BindHost: "alice.example.org"},
		{ID: 2, Username: "bob", DefaultIdent: "bobby"},
		{ID: 3, Username: "carol"},
		{ID: 4, Username: "dave", BindHost: "shared.example"},
		{ID: 5, Username: "erin", BindHost: "shared.example"},
		{ID: 6, Username: "frank", BindHost: "gone.example"},
		{ID: 7, Username: "q\"uote{s}", BindHost: "2001:db8::7"},
	}
	nets := []store.Network{
		{UserID: 1, Username: "al"}, {UserID: 1, Username: "al"}, // both say "al"
		{UserID: 2, BindHost: "bob.example.org", Username: "b1"},
		{UserID: 2, BindHost: "bob.example.org", Username: "b2"}, // they disagree: the default ident
		{UserID: 3},              // no bind host: not in the file
		{UserID: 4}, {UserID: 5}, // one address, two users: left out
		{UserID: 99, BindHost: "x.org"}, // no such user
	}
	got := Entries(context.Background(), users, nets, resolve, []netip.Addr{netip.MustParseAddr("198.51.100.1")})
	var lines []string
	for _, e := range got {
		lines = append(lines, e.Addr.String()+"="+e.Ident)
	}
	if want := "2001:db8::7=quotes 2001:db8::a=al 2001:db8::b=bobby"; strings.Join(lines, " ") != want {
		t.Fatalf("entries: %v\nwant %s", lines, want)
	}
}

func TestClean(t *testing.T) {
	for in, want := range map[string]string{"weareone": "weareone", "a\" } global { reply \"x": "aglobalrep", "abcdefghijklmn": "abcdefghij", "ünï": "n", "": ""} {
		if got := clean(in); got != want {
			t.Errorf("clean(%q) = %q, want %q", in, got, want)
		}
	}
}

type src struct {
	users []store.User
	nets  []store.Network
}

func (s *src) ListUsers() ([]store.User, error)      { return s.users, nil }
func (s *src) AllNetworks() ([]store.Network, error) { return s.nets, nil }

func TestUpdateWritesOnChange(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".oidentd.conf")
	s := &src{users: []store.User{{ID: 1, Username: "alice", BindHost: "alice.example.org"}}}
	w := New(path, s, slog.New(slog.NewTextHandler(io.Discard, nil)))
	w.Resolve = resolve
	if err := w.Update(context.Background()); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	want := "global { hide }\nfrom 198.51.100.1 { reply \"alice\" }\nfrom 2001:db8::a { reply \"alice\" }\n"
	if !strings.HasSuffix(string(b), want) || !strings.HasPrefix(string(b), "# ") {
		t.Fatalf("file:\n%s", b)
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o644 {
		t.Fatalf("mode %v", fi.Mode())
	}
	// Unchanged: not rewritten.
	os.Remove(path)
	if err := w.Update(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("rewritten with nothing changed")
	}
	// A new bind host: rewritten.
	s.users = append(s.users, store.User{ID: 2, Username: "bob", BindHost: "bob.example.org"})
	if err := w.Update(context.Background()); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); !strings.Contains(string(b), "from 2001:db8::b { reply \"bob\" }") {
		t.Fatalf("file:\n%s", b)
	}
}

func TestRunWritesThenStops(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".oidentd.conf")
	w := New(path, &src{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { w.Run(ctx); close(done) }()
	for i := 0; i < 200; i++ {
		if _, err := os.Stat(path); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
	if b, _ := os.ReadFile(path); !strings.Contains(string(b), "global { hide }") {
		t.Fatalf("file: %q", b)
	}
	// A directory that isn't there: Update says so.
	w.Path = filepath.Join(t.TempDir(), "missing", ".oidentd.conf")
	w.last = ""
	if err := w.Update(context.Background()); err == nil {
		t.Fatal("no error writing into a missing directory")
	}
}
