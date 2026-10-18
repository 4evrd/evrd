//go:build !openbsd && (linux || darwin || freebsd || netbsd || dragonfly)

package sandbox

// restrict: only OpenBSD has unveil and pledge.
func restrict(Config) error { return nil }
