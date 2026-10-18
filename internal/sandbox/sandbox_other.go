//go:build !(linux || darwin || freebsd || netbsd || openbsd || dragonfly)

package sandbox

import "errors"

// Enter isn't supported here.
func Enter(c Config) error {
	if c.Enabled() {
		return errors.New("chroot and user aren't supported on this system")
	}
	return nil
}

// Drop isn't supported here; as on Unix, it does nothing without a user.
func Drop(name string) error {
	if name != "" {
		return errors.New("user isn't supported on this system")
	}
	return nil
}
