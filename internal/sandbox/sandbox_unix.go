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

// The system calls Enter and Drop make, as variables so tests can run
// them without root.
var (
	geteuid    = os.Geteuid
	getuid     = os.Getuid
	getgid     = os.Getgid
	lookupUser = user.Lookup
	chroot     = syscall.Chroot
	chdir      = os.Chdir
	setgroups  = syscall.Setgroups
	setgid     = syscall.Setgid
	setuid     = syscall.Setuid
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
	if (c.Chroot != "" || uid >= 0) && geteuid() != 0 {
		return errors.New("chroot and user need evrd to be started as root")
	}
	if c.Chroot != "" {
		if uid < 0 {
			return errors.New("chroot without user: root can leave a chroot, so set user too")
		}
		if err := chroot(c.Chroot); err != nil {
			return fmt.Errorf("chroot %s: %w", c.Chroot, err)
		}
		if err := chdir("/"); err != nil {
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
	if name == "" || geteuid() != 0 {
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
	u, err := lookupUser(name)
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
	if uid == getuid() && uid == geteuid() && gid == getgid() {
		return -1, -1, nil
	}
	return uid, gid, nil
}

func setUser(uid, gid int) error {
	if uid < 0 {
		return nil
	}
	if err := setgroups([]int{gid}); err != nil {
		return fmt.Errorf("setgroups: %w", err)
	}
	if err := setgid(gid); err != nil {
		return fmt.Errorf("setgid: %w", err)
	}
	if err := setuid(uid); err != nil {
		return fmt.Errorf("setuid: %w", err)
	}
	if getuid() != uid || geteuid() != uid {
		return errors.New("setuid didn't take")
	}
	return nil
}
