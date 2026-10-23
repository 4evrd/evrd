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
	h := links.NewHolder(slog.New(slog.NewTextHandler(io.Discard, nil)))
	h.Hash = "0123456789ab"
	go h.Serve(r.ctx, ln)
	if st := r.b.Holder(r.ctx); st.Reachable {
		t.Fatalf("not kept for a while: %+v", st)
	}
	r.b.holderAt = r.b.holderAt.Add(-holderTTL)
	if st := r.b.Holder(r.ctx); !st.Reachable || st.Protocol != links.Version || st.Hash != h.Hash || st.Version != "" || st.Older {
		t.Fatalf("running, nothing shipped to compare with: %+v", st)
	}

	// the evrd-links shipped with evrd is the holder, then another one
	r.b.SetShippedHolder(h.Hash)
	r.b.holderAt = r.b.holderAt.Add(-holderTTL)
	if st := r.b.Holder(r.ctx); st.Version != version.Version || st.Older {
		t.Fatalf("the shipped one: %+v", st)
	}
	r.b.SetShippedHolder("ba9876543210")
	r.b.holderAt = r.b.holderAt.Add(-holderTTL)
	if st := r.b.Holder(r.ctx); st.Version != "" || !st.Older {
		t.Fatalf("an older one: %+v", st)
	}
}
