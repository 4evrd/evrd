//go:build linux || darwin || freebsd || netbsd || openbsd || dragonfly

package sandbox

import (
	"errors"
	"fmt"
	"os"
	"os/user"
	"strconv"
	"syscall"
)

// Enter chroots, drops privileges and restricts the process, in that order.
// It needs root for a chroot or a change of user.
func Enter(c Config) error {
	if !c.Enabled() {
		return nil
	}
	uid, gid, err := lookup(c.User)
	if err != nil {
		return err
	}
	if (c.Chroot != "" || uid >= 0) && os.Geteuid() != 0 {
		return errors.New("chroot and user need evrd to be started as root")
	}
	if c.Chroot != "" {
		if uid < 0 {
			return errors.New("chroot without user: root can leave a chroot, so set user too")
		}
		if err := syscall.Chroot(c.Chroot); err != nil {
			return fmt.Errorf("chroot %s: %w", c.Chroot, err)
		}
		if err := os.Chdir("/"); err != nil {
			return err
		}
	}
	if err := setUser(uid, gid); err != nil {
		return err
	}
	return restrict(c)
}

// Drop switches to name when running as root (and does nothing otherwise),
// so files a tool creates belong to the user the daemon runs as.
func Drop(name string) error {
	if name == "" || os.Geteuid() != 0 {
		return nil
	}
	uid, gid, err := lookup(name)
	if err != nil {
		return err
	}
	return setUser(uid, gid)
}

// lookup finds name's ids; -1 when name is empty or already the current
// user.
func lookup(name string) (uid, gid int, err error) {
	if name == "" {
		return -1, -1, nil
	}
	u, err := user.Lookup(name)
	if err != nil {
		return 0, 0, fmt.Errorf("user %s: %w", name, err)
	}
	if uid, err = strconv.Atoi(u.Uid); err != nil {
		return 0, 0, fmt.Errorf("user %s: uid %q", name, u.Uid)
	}
	if gid, err = strconv.Atoi(u.Gid); err != nil {
		return 0, 0, fmt.Errorf("user %s: gid %q", name, u.Gid)
	}
	if uid == 0 {
		return 0, 0, errors.New("user: refusing to run as root")
	}
	if uid == os.Getuid() && uid == os.Geteuid() && gid == os.Getgid() {
		return -1, -1, nil
	}
	return uid, gid, nil
}

func setUser(uid, gid int) error {
	if uid < 0 {
		return nil
	}
	if err := syscall.Setgroups([]int{gid}); err != nil {
		return fmt.Errorf("setgroups: %w", err)
	}
	if err := syscall.Setgid(gid); err != nil {
		return fmt.Errorf("setgid: %w", err)
	}
	if err := syscall.Setuid(uid); err != nil {
		return fmt.Errorf("setuid: %w", err)
	}
	if os.Getuid() != uid || os.Geteuid() != uid {
		return errors.New("setuid didn't take")
	}
	return nil
}
