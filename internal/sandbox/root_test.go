//go:build linux || darwin || freebsd || netbsd || dragonfly

package sandbox

import (
	"errors"
	"os/user"
	"strings"
	"syscall"
	"testing"
)

// fakeRoot pretends the process runs as root, recording the system calls
// made instead of making them.
type fakeRoot struct {
	calls     []string
	uid       int
	fail      string // the call to fail
	ignoreUID bool   // setuid "succeeds" without changing anything
}

func asRoot(t *testing.T) *fakeRoot {
	t.Helper()
	f := &fakeRoot{uid: 0}
	saved := []any{geteuid, getuid, getgid, lookupUser, chroot, chdir, setgroups, setgid, setuid}
	t.Cleanup(func() {
		geteuid, getuid, getgid = saved[0].(func() int), saved[1].(func() int), saved[2].(func() int)
		lookupUser = saved[3].(func(string) (*user.User, error))
		chroot, chdir = saved[4].(func(string) error), saved[5].(func(string) error)
		setgroups = saved[6].(func([]int) error)
		setgid, setuid = saved[7].(func(int) error), saved[8].(func(int) error)
	})
	call := func(name string) error {
		f.calls = append(f.calls, name)
		if f.fail == strings.Fields(name)[0] {
			return syscall.EPERM
		}
		return nil
	}
	geteuid = func() int { return f.uid }
	getuid = func() int { return f.uid }
	getgid = func() int { return 0 }
	lookupUser = func(name string) (*user.User, error) {
		switch name {
		case "_evrd":
			return &user.User{Username: name, Uid: "990", Gid: "991"}, nil
		case "badUID":
			return &user.User{Username: name, Uid: "x", Gid: "1"}, nil
		case "badGID":
			return &user.User{Username: name, Uid: "5", Gid: "y"}, nil
		case "root":
			return &user.User{Username: name, Uid: "0", Gid: "0"}, nil
		}
		return nil, user.UnknownUserError(name)
	}
	chroot = func(dir string) error { return call("chroot " + dir) }
	chdir = func(dir string) error { return call("chdir " + dir) }
	setgroups = func(g []int) error { return call("setgroups") }
	setgid = func(gid int) error { return call("setgid") }
	setuid = func(uid int) error {
		if err := call("setuid"); err != nil {
			return err
		}
		if !f.ignoreUID {
			f.uid = uid
		}
		return nil
	}
	return f
}

func TestEnterChrootsThenDropsPrivileges(t *testing.T) {
	f := asRoot(t)
	if err := Enter(Config{Chroot: "/var/evrd", User: "_evrd"}); err != nil {
		t.Fatal(err)
	}
	want := "chroot /var/evrd|chdir /|setgroups|setgid|setuid"
	if got := strings.Join(f.calls, "|"); got != want {
		t.Fatalf("calls %q, want %q", got, want)
	}
	if f.uid != 990 {
		t.Fatalf("uid %d", f.uid)
	}
}

func TestEnterAsRootRefusesUnsafeSetups(t *testing.T) {
	asRoot(t)
	for _, tc := range []struct {
		c    Config
		want string
	}{
		{Config{Chroot: "/var/evrd"}, "chroot without user"},
		{Config{User: "root"}, "refusing to run as root"},
		{Config{User: "badUID"}, `uid "x"`},
		{Config{User: "badGID"}, `gid "y"`},
		{Config{User: "nobody-here"}, "nobody-here"},
	} {
		if err := Enter(tc.c); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%+v: %v, want %q", tc.c, err, tc.want)
		}
	}
}

func TestEnterReportsFailedSystemCalls(t *testing.T) {
	for _, call := range []string{"chroot", "chdir", "setgroups", "setgid", "setuid"} {
		f := asRoot(t)
		f.fail = call
		err := Enter(Config{Chroot: "/var/evrd", User: "_evrd"})
		if !errors.Is(err, syscall.EPERM) {
			t.Errorf("%s failing: %v", call, err)
		}
		if call != "chdir" && !strings.HasPrefix(err.Error(), call) {
			t.Errorf("%s failing: %q doesn't say which call", call, err)
		}
	}
}

func TestSetuidMustTakeEffect(t *testing.T) {
	f := asRoot(t)
	f.ignoreUID = true
	if err := Enter(Config{User: "_evrd"}); err == nil || !strings.Contains(err.Error(), "didn't take") {
		t.Fatalf("a setuid that changed nothing: %v", err)
	}
}

func TestDropAsRoot(t *testing.T) {
	f := asRoot(t)
	if err := Drop("nobody-here"); err == nil {
		t.Fatal("dropping to an unknown user")
	}
	if err := Drop("_evrd"); err != nil || f.uid != 990 {
		t.Fatalf("drop: %v, uid %d", err, f.uid)
	}
	// Not root any more: Drop does nothing.
	f.calls = nil
	if err := Drop("_evrd"); err != nil || len(f.calls) != 0 {
		t.Fatalf("second drop: %v %q", err, f.calls)
	}
}
