package sandbox

import (
	"fmt"

	"golang.org/x/sys/unix"
)

// promises are what evrd needs once running: files in its data directory
// (SQLite and Pebble lock theirs), and the network.
const promises = "stdio rpath wpath cpath fattr flock inet dns"

// restrict unveils only the paths evrd still uses, then pledges.
func restrict(c Config) error {
	for _, p := range c.Read {
		if err := unix.Unveil(p, "r"); err != nil && err != unix.ENOENT {
			return fmt.Errorf("unveil %s: %w", p, err)
		}
	}
	for _, p := range c.Write {
		if err := unix.Unveil(p, "rwc"); err != nil {
			return fmt.Errorf("unveil %s: %w", p, err)
		}
	}
	if err := unix.UnveilBlock(); err != nil {
		return fmt.Errorf("unveil: %w", err)
	}
	p := promises
	if c.Promises != "" {
		p = c.Promises
	}
	if err := unix.PledgePromises(p); err != nil {
		return fmt.Errorf("pledge: %w", err)
	}
	return nil
}
