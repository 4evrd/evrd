package client

import (
	"bufio"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/4evrd/evrd/internal/bouncer"
)

func TestQuietClientIsPingedThenDropped(t *testing.T) {
	r := newRig(t)
	r.srv.pingQuiet = 300 * time.Millisecond
	r.srv.SetClientPingTimeout(400 * time.Millisecond)

	// one answers evrd's PING, and stays; its PONG isn't the network's
	answers := r.attach("alice/one:alice-password")
	answers.expect(" 376 ")
	answers.expect("PING :evrd")
	answers.send("PONG :evrd")
	answers.expect("PING :evrd") // quiet again later: asked again, not dropped
	answers.send("QUIT :done")
	answers.closed()

	// the other says nothing: dropped as stopped answering
	quiet := r.attach("alice/one:alice-password")
	quiet.expect(" 376 ")
	if !quiet.closed() {
		t.Fatal("a client that never answered is still connected")
	}
	eventually(t, "recorded", func() bool {
		return strings.Join(r.reasons(), " ") == bouncer.DropQuit+" "+bouncer.DropPingTimeout
	})
	if r.irc.Has("PONG :evrd") {
		t.Fatal("the client's PONG to evrd went to the network")
	}

	// 0 turns it off
	r.srv.SetClientPingTimeout(0)
	off := r.attach("alice/one:alice-password")
	off.expect(" 376 ")
	time.Sleep(time.Second)
	off.send("PING :still")
	off.expect("PONG evrd :still")
}

func TestStalledWriteDropsTheClient(t *testing.T) {
	srv := &Server{writeStall: 50 * time.Millisecond}
	ours, theirs := net.Pipe() // writes block until the other end reads
	defer theirs.Close()
	se := &session{srv: srv, conn: ours, w: bufio.NewWriter(ours)}
	start := time.Now()
	se.send(strings.Repeat("x", 8192)) // more than the buffer: it has to write
	if took := time.Since(start); took > time.Second {
		t.Fatalf("send waited %v", took)
	}
	if se.dropped != bouncer.DropWriteStalled {
		t.Fatalf("reason: %q", se.dropped)
	}
	theirs.SetReadDeadline(time.Now().Add(2 * time.Second))
	if n, err := theirs.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
		t.Fatalf("not closed: read %d, %v", n, err)
	}
}

func TestDefaultPingTimeout(t *testing.T) {
	s := &Server{}
	if s.clientPingTimeout() != defaultPingTimeout {
		t.Fatal(s.clientPingTimeout())
	}
	s.SetClientPingTimeout(0)
	if s.clientPingTimeout() != 0 {
		t.Fatal("0 is off, not the default")
	}
}
