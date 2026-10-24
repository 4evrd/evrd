package client

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"testing"
	"time"

	"github.com/4evrd/evrd/internal/bouncer"
)

// reasons is the user's recorded disconnects' reasons, oldest first.
func (r *rig) reasons() []string {
	ds, _ := r.st.ClientDisconnects(r.ctx, r.user.ID, time.Now().Add(-time.Hour))
	out := make([]string, len(ds))
	for i, d := range ds {
		out[len(ds)-1-i] = d.Reason
	}
	return out
}

func TestDisconnectReasons(t *testing.T) {
	r := newRig(t)
	want := func(reasons ...string) {
		t.Helper()
		eventually(t, fmt.Sprint("recorded ", reasons), func() bool { return fmt.Sprint(r.reasons()) == fmt.Sprint(reasons) })
	}

	quits := r.attach("alice/one:alice-password")
	quits.expect(" 376 ")
	quits.send("QUIT :bye")
	want(bouncer.DropQuit)

	hangs := r.attach("alice/one:alice-password")
	hangs.expect(" 376 ")
	hangs.c.(*net.TCPConn).CloseWrite() // hangs up (a plain Close with unread lines resets)
	want(bouncer.DropQuit, bouncer.DropClosed)

	// evrd closing it (the account stopped): its reason, not the read error
	kicked := r.attach("alice/one:alice-password")
	kicked.expect(" 376 ")
	r.b.StopUser(r.user.ID)
	if !kicked.closed() {
		t.Fatal("StopUser didn't close the client")
	}
	want(bouncer.DropQuit, bouncer.DropClosed, bouncer.DropRemoved)

	// refused at the client limit
	one := 1
	u := r.user
	u.MaxClients = &one
	r.st.UpdateUser(r.ctx, u)
	r.attach("alice/one:alice-password").expect(" 376 ")
	r.attach("alice/one:alice-password").expect("Too many clients attached")
	want(bouncer.DropQuit, bouncer.DropClosed, bouncer.DropRemoved, bouncer.DropClientLimit)

	// the device and network come along
	ds, _ := r.st.ClientDisconnects(r.ctx, r.user.ID, time.Now().Add(-time.Hour))
	if ds[len(ds)-1].Network != "one" || ds[len(ds)-1].DeviceKey == "" {
		t.Fatalf("first: %+v", ds[len(ds)-1])
	}
}

func TestReadDrop(t *testing.T) {
	for err, want := range map[error]string{
		io.EOF:                         bouncer.DropClosed,
		net.ErrClosed:                  "",
		os.ErrDeadlineExceeded:         bouncer.DropPingTimeout,
		errors.New("connection reset"): bouncer.DropReset,
	} {
		if got := readDrop(err); got != want {
			t.Errorf("%v: %q, want %q", err, got, want)
		}
	}
}
