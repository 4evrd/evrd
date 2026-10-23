// Package evrdlinks is the evrd-links command, evrd's connection holder: it
// keeps users' connections to IRC networks open while evrd itself restarts
// (see internal/links). It's small and changes rarely, so upgrading evrd almost
// never needs it restarted.
//
//	evrd-links -socket /var/evrd/run/links.sock -chroot /var/evrd -user _evrd
//
// Started as root, it creates the socket (owned by -user, mode 0600), then
// chroots, becomes -user and, on OpenBSD, pledges only what passing lines
// needs — no files at all.
package evrdlinks

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"os/user"
	"strconv"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/4evrd/evrd/internal/commands"
	"github.com/4evrd/evrd/internal/links"
	"github.com/4evrd/evrd/internal/sandbox"
)

// geteuid is os.Geteuid; tests stand in for root with it.
var geteuid = os.Geteuid

// Execute is evrd-links: args are its command line (args[0] the program
// name), and the result is the exit status. It serves until ctx is
// cancelled or SIGINT or SIGTERM arrives.
func Execute(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	var o options
	root := &cobra.Command{
		Use:   "evrd-links",
		Short: "Hold evrd's IRC connections while evrd restarts",
		Args:  cobra.ArbitraryArgs, // ignored, as they always were
		RunE: func(c *cobra.Command, _ []string) error {
			return commands.Status(o.run(c.Context(), stdout, stderr))
		},
	}
	root.SetOut(stderr) // help and usage, where the flag package printed them
	root.SetErr(stderr)
	f := root.Flags()
	f.StringVar(&o.socket, "socket", "", "unix socket to listen on (required)")
	f.StringVar(&o.chroot, "chroot", "", "chroot here after creating the socket")
	f.StringVar(&o.user, "user", "", "run as this user (and own the socket)")
	f.IntVar(&o.buffer, "buffer", 4<<20, "bytes kept per link while evrd is away")
	f.BoolVar(&o.version, "version", false, "print the protocol version and exit")
	return commands.Execute(ctx, root, args)
}

// options are evrd-links' flags.
type options struct {
	socket, chroot, user string
	buffer               int
	version              bool
}

// run holds connections as o says, and the result is the exit status.
func (o *options) run(parent context.Context, stdout, stderr io.Writer) int {
	if o.version {
		fmt.Fprintln(stdout, "evrd-links protocol", links.Version)
		return 0
	}
	log := slog.New(slog.NewTextHandler(stderr, nil))
	if o.socket == "" {
		log.Error("-socket is required")
		return 2
	}

	if fi, err := os.Lstat(o.socket); err == nil && fi.Mode()&os.ModeSocket != 0 {
		os.Remove(o.socket) // left from a previous run
	}
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: o.socket, Net: "unix"})
	if err != nil {
		log.Error("listen", "socket", o.socket, "err", err)
		return 1
	}
	// Not unlinked on exit: inside the chroot the path doesn't exist, and
	// pledge forbids it anyway. The next start removes it (above).
	ln.SetUnlinkOnClose(false)
	if err := os.Chmod(o.socket, 0o600); err != nil {
		log.Error("chmod socket", "err", err)
		return 1
	}
	if o.user != "" && geteuid() == 0 {
		u, err := user.Lookup(o.user)
		if err != nil {
			log.Error("user", "err", err)
			return 1
		}
		uid, _ := strconv.Atoi(u.Uid)
		gid, _ := strconv.Atoi(u.Gid)
		if err := os.Chown(o.socket, uid, gid); err != nil {
			log.Error("chown socket", "err", err)
			return 1
		}
	}

	sandbox.Preload() // CA certificates, for checking servers' certificates
	sb := sandbox.Config{Chroot: o.chroot, User: o.user, Promises: "stdio inet unix recvfd"}
	if err := sandbox.Enter(sb); err != nil {
		log.Error("sandbox", "err", err)
		return 1
	}

	h := links.NewHolder(log)
	h.MaxBuffer = o.buffer
	ctx, stop := signal.NotifyContext(parent, syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	log.Info("holding IRC connections", "socket", o.socket, "chroot", o.chroot, "user", o.user)
	if err := h.Serve(ctx, ln); err != nil {
		log.Error("serve", "err", err)
		return 1
	}
	return 0
}
