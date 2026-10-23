package evrd

import (
	"context"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/4evrd/evrd/internal/links"
)

func TestNotHeld(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dir, err := os.MkdirTemp("", "links")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	path := filepath.Join(dir, "l.sock") // short: unix socket paths are limited
	sock, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	go links.NewHolder(slog.New(slog.NewTextHandler(io.Discard, nil))).Serve(ctx, sock)
	holder := links.NewClient(path)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	if err := holder.Listen(ctx, ln, addr, true); err != nil {
		t.Fatal(err)
	}
	all := []string{"ircs://" + addr, "irc+insecure://127.0.0.1:1"}
	if got := notHeld(ctx, holder, all); !slices.Equal(got, all[1:]) {
		t.Errorf("held: %v", got)
	}
	if got := notHeld(ctx, links.NewClient(filepath.Join(dir, "none.sock")), all); !slices.Equal(got, all) {
		t.Errorf("no holder: %v", got)
	}
}
