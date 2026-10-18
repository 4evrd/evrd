// Command evrd-links is evrd's connection holder: it keeps users'
// connections to IRC networks open while evrd itself restarts (see
// internal/links). It's small and changes rarely, so upgrading evrd almost
// never needs it restarted.
//
//	evrd-links -socket /var/evrd/run/links.sock -chroot /var/evrd -user _evrd
//
// Started as root, it creates the socket (owned by -user, mode 0600), then
// chroots, becomes -user and, on OpenBSD, pledges only what passing lines
// needs — no files at all.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"os/user"
	"strconv"
	"syscall"

	"github.com/4evrd/evrd/internal/links"
	"github.com/4evrd/evrd/internal/sandbox"
)

func main() {
	socket := flag.String("socket", "", "unix socket to listen on (required)")
	chroot := flag.String("chroot", "", "chroot here after creating the socket")
	usr := flag.String("user", "", "run as this user (and own the socket)")
	buffer := flag.Int("buffer", 4<<20, "bytes kept per link while evrd is away")
	showVersion := flag.Bool("version", false, "print the protocol version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println("evrd-links protocol", links.Version)
		return
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if *socket == "" {
		log.Error("-socket is required")
		os.Exit(2)
	}

	if fi, err := os.Lstat(*socket); err == nil && fi.Mode()&os.ModeSocket != 0 {
		os.Remove(*socket) // left from a previous run
	}
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: *socket, Net: "unix"})
	if err != nil {
		log.Error("listen", "socket", *socket, "err", err)
		os.Exit(1)
	}
	ln.SetUnlinkOnClose(true)
	if err := os.Chmod(*socket, 0o600); err != nil {
		log.Error("chmod socket", "err", err)
		os.Exit(1)
	}
	if *usr != "" && os.Geteuid() == 0 {
		u, err := user.Lookup(*usr)
		if err != nil {
			log.Error("user", "err", err)
			os.Exit(1)
		}
		uid, _ := strconv.Atoi(u.Uid)
		gid, _ := strconv.Atoi(u.Gid)
		if err := os.Chown(*socket, uid, gid); err != nil {
			log.Error("chown socket", "err", err)
			os.Exit(1)
		}
	}

	sandbox.Preload() // CA certificates, for checking servers' certificates
	sb := sandbox.Config{Chroot: *chroot, User: *usr, Promises: "stdio inet unix recvfd"}
	if err := sandbox.Enter(sb); err != nil {
		log.Error("sandbox", "err", err)
		os.Exit(1)
	}

	h := links.NewHolder(log)
	h.MaxBuffer = *buffer
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	log.Info("holding IRC connections", "socket", *socket, "chroot", *chroot, "user", *usr)
	if err := h.Serve(ctx, ln); err != nil {
		log.Error("serve", "err", err)
		os.Exit(1)
	}
}
