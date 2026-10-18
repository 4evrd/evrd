// Package sandbox confines evrd once it has started: it chroots into its
// data directory, drops to an unprivileged user, and on OpenBSD unveils only
// the paths it still needs and pledges the system calls it makes.
//
// Everything outside the chroot must be read before Enter: the config file,
// the secret key, TLS certificates, the system's CA certificates and the
// local time zone (see Preload).
package sandbox

import (
	"crypto/x509"
	"time"
)

// Config says how to confine the process.
type Config struct {
	// Chroot is the directory to chroot into ("" = don't).
	Chroot string
	// User is the user to run as from now on ("" = stay as is).
	User string
	// Read and Write are the paths (as seen after the chroot) evrd still
	// opens: read-only, and read-write-create. Used by unveil on OpenBSD.
	Read, Write []string
	// Promises, when set, replaces the default pledge promises (OpenBSD).
	Promises string
}

// Enabled reports whether there's anything to do.
func (c Config) Enabled() bool { return c.Chroot != "" || c.User != "" }

// Preload reads what the Go runtime would otherwise load lazily from
// outside the chroot: the system's CA certificates (for TLS to IRC networks
// and the SSO provider) and the local time zone.
func Preload() {
	x509.SystemCertPool()
	time.Now().Zone()
}
