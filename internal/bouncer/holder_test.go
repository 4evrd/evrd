package bouncer

import (
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/4evrd/evrd/internal/links"
	"github.com/4evrd/evrd/internal/version"
)

func TestHolderStatus(t *testing.T) {
	r := newLiveRig(t)
	if st := r.b.Holder(r.ctx); st.Configured {
		t.Fatalf("no holder: %+v", st)
	}

	dir, err := os.MkdirTemp("", "links")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	path := filepath.Join(dir, "l.sock") // short: unix socket paths are limited
	r.b.SetLinks(links.NewClient(path))
	if st := r.b.Holder(r.ctx); !st.Configured || st.Reachable {
		t.Fatalf("holder not running: %+v", st)
	}

	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	go links.NewHolder(slog.New(slog.NewTextHandler(io.Discard, nil))).Serve(r.ctx, ln)
	if st := r.b.Holder(r.ctx); st.Reachable {
		t.Fatalf("not kept for a while: %+v", st)
	}
	r.b.holderAt = r.b.holderAt.Add(-holderTTL)
	if st := r.b.Holder(r.ctx); !st.Reachable || st.Protocol != links.Version || st.Build != version.Version {
		t.Fatalf("running: %+v", st)
	}
}
