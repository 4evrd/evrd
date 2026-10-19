package main

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/4evrd/evrd/internal/irctls"
	"github.com/4evrd/evrd/internal/links"
)

// syncBuffer is a bytes.Buffer safe to write from the holder's goroutines
// while the test reads it.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// sockDir is a short temporary directory: unix socket paths are limited to
// about 100 bytes, and t.TempDir's can be longer.
func sockDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "el")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

func TestVersion(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run(context.Background(), []string{"evrd-links", "-version"}, &out, &errOut); code != 0 {
		t.Fatalf("exit %d, stderr %q", code, errOut.String())
	}
	if want := "evrd-links protocol " + strconv.Itoa(links.Version) + "\n"; out.String() != want {
		t.Fatalf("stdout %q, want %q", out.String(), want)
	}
}

func TestFlags(t *testing.T) {
	for _, tc := range []struct {
		args []string
		code int
		msg  string
	}{
		{[]string{"-nope"}, 2, "flag provided but not defined: -nope"},
		{[]string{"-buffer", "lots"}, 2, `invalid value "lots" for flag -buffer`},
		{[]string{"-h"}, 0, "-socket string"},
		{nil, 2, "-socket is required"},
	} {
		var out, errOut bytes.Buffer
		code := run(context.Background(), append([]string{"evrd-links"}, tc.args...), &out, &errOut)
		if code != tc.code || !strings.Contains(errOut.String(), tc.msg) {
			t.Errorf("%v: exit %d, stderr %q; want %d and %q", tc.args, code, errOut.String(), tc.code, tc.msg)
		}
		if out.Len() != 0 {
			t.Errorf("%v: stdout %q", tc.args, out.String())
		}
	}
}

// start runs evrd-links with args until the test ends, and waits for its
// socket to answer.
func start(t *testing.T, sock string, args ...string) (stop func() int, errOut *syncBuffer) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	errOut = &syncBuffer{}
	done := make(chan int, 1)
	go func() {
		done <- run(ctx, append([]string{"evrd-links", "-socket", sock}, args...), io.Discard, errOut)
	}()
	stopped := false
	stop = func() int {
		t.Helper()
		if stopped {
			return 0
		}
		stopped = true
		cancel()
		select {
		case code := <-done:
			return code
		case <-time.After(5 * time.Second):
			t.Fatal("evrd-links didn't stop")
			return -1
		}
	}
	t.Cleanup(func() { stop() })
	for i := 0; ; i++ {
		if strings.Contains(errOut.String(), "holding IRC connections") {
			return stop, errOut
		}
		select {
		case code := <-done:
			t.Fatalf("exited early (%d): %s", code, errOut.String())
		default:
		}
		if i == 250 {
			t.Fatalf("never started: %s", errOut.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestHoldsConnections(t *testing.T) {
	sock := filepath.Join(sockDir(t), "links.sock")
	// A socket left from an earlier run is replaced.
	old, err := net.ListenUnix("unix", &net.UnixAddr{Name: sock, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	old.SetUnlinkOnClose(false)
	old.Close()

	stop, errOut := start(t, sock, "-buffer", "1024")
	fi, err := os.Lstat(sock)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&os.ModeSocket == 0 || fi.Mode().Perm() != 0o600 {
		t.Fatalf("socket mode %v, want a socket with 0600", fi.Mode())
	}

	// An IRC server, and a connection to it handed to the holder.
	srv, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		c, err := srv.Accept()
		if err == nil {
			accepted <- c
		}
	}()
	tcp, err := net.Dial("tcp", srv.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	server := <-accepted
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cl := links.NewClient(sock)
	stream, err := cl.Open(ctx, "alice/libera", tcp, false, "irc.example", irctls.Trust{})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if _, err := io.WriteString(stream, "NICK alice\r\n"); err != nil {
		t.Fatal(err)
	}
	server.SetReadDeadline(time.Now().Add(5 * time.Second))
	line, err := bufio.NewReader(server).ReadString('\n')
	if err != nil || line != "NICK alice\r\n" {
		t.Fatalf("server read %q, %v", line, err)
	}
	ls, err := cl.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(ls) != 1 || ls[0].Link != "alice/libera" {
		t.Fatalf("links: %+v", ls)
	}

	// Stopping closes the links it held.
	if code := stop(); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	server.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := io.Copy(io.Discard, server); err != nil {
		t.Fatalf("server connection not closed: %v", err)
	}
	if _, err := cl.List(ctx); err == nil {
		t.Fatal("holder still answering after it stopped")
	}
}

func TestListenFails(t *testing.T) {
	sock := filepath.Join(sockDir(t), "missing", "links.sock")
	var errOut bytes.Buffer
	if code := run(context.Background(), []string{"evrd-links", "-socket", sock}, io.Discard, &errOut); code != 1 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(errOut.String(), "msg=listen") || !strings.Contains(errOut.String(), sock) {
		t.Fatalf("stderr %q", errOut.String())
	}
}

func TestSandboxFails(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("as root this would really chroot")
	}
	dir := sockDir(t)
	var errOut bytes.Buffer
	code := run(context.Background(), []string{"evrd-links", "-socket", filepath.Join(dir, "l.sock"), "-chroot", dir}, io.Discard, &errOut)
	if code != 1 || !strings.Contains(errOut.String(), "msg=sandbox") || !strings.Contains(errOut.String(), "started as root") {
		t.Fatalf("exit %d, stderr %q", code, errOut.String())
	}
	// The socket was made (and kept) before the sandbox failed.
	if fi, err := os.Lstat(filepath.Join(dir, "l.sock")); err != nil || fi.Mode()&os.ModeSocket == 0 {
		t.Fatalf("socket: %v %v", fi, err)
	}
}

// asRoot makes run believe it was started as root, for the -user socket
// ownership code; the chown it then does (to the current user) is allowed
// unprivileged.
func asRoot(t *testing.T) {
	t.Helper()
	geteuid = func() int { return 0 }
	t.Cleanup(func() { geteuid = os.Geteuid })
}

func TestUserOwnsSocket(t *testing.T) {
	me, err := user.Current()
	if err != nil {
		t.Skip(err)
	}
	if me.Uid == "0" {
		t.Skip("running as root: -user root is refused")
	}
	asRoot(t)
	sock := filepath.Join(sockDir(t), "l.sock")
	stop, errOut := start(t, sock, "-user", me.Username)
	fi, err := os.Lstat(sock)
	if err != nil {
		t.Fatal(err)
	}
	st := fi.Sys().(*syscall.Stat_t)
	if strconv.Itoa(int(st.Uid)) != me.Uid || strconv.Itoa(int(st.Gid)) != me.Gid {
		t.Fatalf("socket owned by %d:%d, want %s:%s", st.Uid, st.Gid, me.Uid, me.Gid)
	}
	if code := stop(); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
}

func TestUnknownUser(t *testing.T) {
	asRoot(t)
	var errOut bytes.Buffer
	code := run(context.Background(), []string{"evrd-links", "-socket", filepath.Join(sockDir(t), "l.sock"), "-user", "no-such-user-evrd"}, io.Discard, &errOut)
	if code != 1 || !strings.Contains(errOut.String(), "msg=user") || !strings.Contains(errOut.String(), "no-such-user-evrd") {
		t.Fatalf("exit %d, stderr %q", code, errOut.String())
	}
}
