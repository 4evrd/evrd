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
	"errors"
	"flag"
	"fmt"
	"io"
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

// geteuid is os.Geteuid; tests stand in for root with it.
var geteuid = os.Geteuid

func main() {
	os.Exit(run(context.Background(), os.Args, os.Stdout, os.Stderr))
}

// run is evrd-links: args are its command line (args[0] the program name),
// and the result is the exit status. It serves until ctx is cancelled or
// SIGINT or SIGTERM arrives.
func run(parent context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
	flags.SetOutput(stderr)
	socket := flags.String("socket", "", "unix socket to listen on (required)")
	chroot := flags.String("chroot", "", "chroot here after creating the socket")
	usr := flags.String("user", "", "run as this user (and own the socket)")
	buffer := flags.Int("buffer", 4<<20, "bytes kept per link while evrd is away")
	showVersion := flags.Bool("version", false, "print the protocol version and exit")
	if err := flags.Parse(args[1:]); err != nil {
		// as flag.ExitOnError: 0 for -h, else 2 (the error is printed)
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if *showVersion {
		fmt.Fprintln(stdout, "evrd-links protocol", links.Version)
		return 0
	}
	log := slog.New(slog.NewTextHandler(stderr, nil))
	if *socket == "" {
		log.Error("-socket is required")
		return 2
	}

	if fi, err := os.Lstat(*socket); err == nil && fi.Mode()&os.ModeSocket != 0 {
		os.Remove(*socket) // left from a previous run
	}
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: *socket, Net: "unix"})
	if err != nil {
		log.Error("listen", "socket", *socket, "err", err)
		return 1
	}
	// Not unlinked on exit: inside the chroot the path doesn't exist, and
	// pledge forbids it anyway. The next start removes it (above).
	ln.SetUnlinkOnClose(false)
	if err := os.Chmod(*socket, 0o600); err != nil {
		log.Error("chmod socket", "err", err)
		return 1
	}
	if *usr != "" && geteuid() == 0 {
		u, err := user.Lookup(*usr)
		if err != nil {
			log.Error("user", "err", err)
			return 1
		}
		uid, _ := strconv.Atoi(u.Uid)
		gid, _ := strconv.Atoi(u.Gid)
		if err := os.Chown(*socket, uid, gid); err != nil {
			log.Error("chown socket", "err", err)
			return 1
		}
	}

	sandbox.Preload() // CA certificates, for checking servers' certificates
	sb := sandbox.Config{Chroot: *chroot, User: *usr, Promises: "stdio inet unix recvfd"}
	if err := sandbox.Enter(sb); err != nil {
		log.Error("sandbox", "err", err)
		return 1
	}

	h := links.NewHolder(log)
	h.MaxBuffer = *buffer
	ctx, stop := signal.NotifyContext(parent, syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	log.Info("holding IRC connections", "socket", *socket, "chroot", *chroot, "user", *usr)
	if err := h.Serve(ctx, ln); err != nil {
		log.Error("serve", "err", err)
		return 1
	}
	return 0
}
