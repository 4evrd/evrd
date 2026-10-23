package handlers_test

import (
	"context"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/4evrd/evrd/internal/config"
	"github.com/4evrd/evrd/internal/links"
	"github.com/4evrd/evrd/internal/store"
	"github.com/4evrd/evrd/internal/version"
)

// testHolderHash is the binary hash the test holders report.
const testHolderHash = "0123456789ab"

// runHolder runs a connection holder for the test; its client.
func runHolder(t *testing.T) *links.Client {
	t.Helper()
	dir, err := os.MkdirTemp("", "links")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	path := filepath.Join(dir, "l.sock") // short: unix socket paths are limited
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	h := links.NewHolder(slog.New(slog.NewTextHandler(io.Discard, nil)))
	h.Hash = testHolderHash
	go h.Serve(ctx, ln)
	return links.NewClient(path)
}

func TestAdminOverviewVersions(t *testing.T) {
	holder := func(e *env) map[string]any {
		t.Helper()
		e.user("root", store.RoleAdmin)
		code, ov := e.login("root").do("GET", "/api/admin/overview", nil)
		if code != 200 || ov["version"] != version.Version {
			t.Fatalf("overview: %d %v", code, ov)
		}
		h, _ := ov["holder"].(map[string]any)
		return h
	}

	if h := holder(newEnv(t)); h["configured"] != false || h["holdsClients"] != false {
		t.Fatalf("no holder: %v", h)
	}

	e := newEnv(t)
	e.b.SetLinks(runHolder(t))
	e.b.SetShippedHolder(testHolderHash)
	if h := holder(e); h["reachable"] != true || h["protocol"] != float64(links.Version) || h["version"] != version.Version || h["older"] != false || h["holdsClients"] != true {
		t.Fatalf("holder: %v", h)
	}

	off := newEnv(t, func(c *config.Config) { c.KeepClientsOut = true })
	off.b.SetLinks(runHolder(t))
	if h := holder(off); h["reachable"] != true || h["holdsClients"] != false {
		t.Fatalf("hold_clients no: %v", h)
	}

	gone := newEnv(t)
	gone.b.SetLinks(links.NewClient(filepath.Join(t.TempDir(), "none.sock")))
	if h := holder(gone); h["configured"] != true || h["reachable"] != false {
		t.Fatalf("unreachable: %v", h)
	}
}
