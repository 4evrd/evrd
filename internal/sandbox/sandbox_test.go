//go:build linux || darwin || freebsd || netbsd || dragonfly

package sandbox

import (
	"os"
	"os/user"
	"strings"
	"testing"
)

func TestEnabled(t *testing.T) {
	if (Config{}).Enabled() || (Config{Read: []string{"/x"}, Promises: "stdio"}).Enabled() {
		t.Error("no chroot or user: nothing to do")
	}
	if !(Config{Chroot: "/var/evrd"}).Enabled() || !(Config{User: "_evrd"}).Enabled() {
		t.Error("a chroot or user enables it")
	}
}

func TestEnterWithNothingToDo(t *testing.T) {
	if err := Enter(Config{}); err != nil {
		t.Fatal(err)
	}
}

func notRoot(t *testing.T) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("running as root: this checks what happens without it")
	}
}

func TestEnterAsTheCurrentUser(t *testing.T) {
	notRoot(t)
	me, err := user.Current()
	if err != nil {
		t.Skip(err)
	}
	// Already that user: nothing to switch, so no root needed (and the
	// restrictions, where the OS has them, still apply).
	if err := Enter(Config{User: me.Username}); err != nil {
		t.Fatalf("staying as %s: %v", me.Username, err)
	}
}

func TestEnterNeedsRoot(t *testing.T) {
	notRoot(t)
	err := Enter(Config{Chroot: t.TempDir()})
	if err == nil || !strings.Contains(err.Error(), "started as root") {
		t.Fatalf("chroot without root: %v", err)
	}
}

func TestEnterUnknownUser(t *testing.T) {
	err := Enter(Config{User: "evrd-no-such-user-xyz"})
	if err == nil || !strings.Contains(err.Error(), "evrd-no-such-user-xyz") {
		t.Fatalf("unknown user: %v", err)
	}
	if err := Drop("evrd-no-such-user-xyz"); os.Geteuid() == 0 && err == nil {
		t.Fatal("Drop to an unknown user as root")
	}
}

func TestLookup(t *testing.T) {
	if uid, gid, err := lookup(""); uid != -1 || gid != -1 || err != nil {
		t.Errorf("no user: %d %d %v", uid, gid, err)
	}
	if _, _, err := lookup("root"); err == nil || !strings.Contains(err.Error(), "refusing to run as root") {
		t.Errorf("root: %v", err)
	}
	me, err := user.Current()
	if err != nil || me.Uid == "0" {
		return
	}
	if uid, gid, err := lookup(me.Username); uid != -1 || gid != -1 || err != nil {
		t.Errorf("the current user needs no switch: %d %d %v", uid, gid, err)
	}
}

func TestDropDoesNothingWithoutRoot(t *testing.T) {
	notRoot(t)
	if err := Drop(""); err != nil {
		t.Fatal(err)
	}
	if err := Drop("evrd-no-such-user-xyz"); err != nil {
		t.Fatalf("not root: Drop doesn't even look the user up: %v", err)
	}
	if err := setUser(-1, -1); err != nil {
		t.Fatal(err)
	}
}

func TestPreload(t *testing.T) {
	Preload() // loads the CA pool and zone; mustn't panic
}
