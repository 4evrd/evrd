package upstream

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/4evrd/evrd/internal/history"
	"github.com/4evrd/evrd/internal/links"
	"github.com/4evrd/evrd/internal/store"
	"github.com/4evrd/evrd/internal/testirc"
)

func startTestHolder(t *testing.T) *links.Client {
	t.Helper()
	dir, err := os.MkdirTemp("", "hold")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	path := filepath.Join(dir, "l.sock")
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go links.NewHolder(quiet).Serve(ctx, ln)
	return links.NewClient(path)
}

func TestRestartKeepsTheLinkThroughTheHolder(t *testing.T) {
	var log testirc.Log
	addr := testirc.StartLogged(t, &log)
	host, port, _ := net.SplitHostPort(addr)
	var p int
	for _, ch := range port {
		p = p*10 + int(ch-'0')
	}
	n := store.Network{ID: 7, Nick: "alice", Host: host, Port: p, Channels: []string{"#c"}}
	holder := startTestHolder(t)

	var stopping atomic.Bool
	opts := Options{Links: holder, LinkID: "n7", Detaching: stopping.Load}

	// First evrd: registers and joins through the holder.
	c1 := New(n, quiet, opts)
	cancel1 := run(t, c1)
	eventually(t, "registered", func() bool { return c1.Status() == StatusConnected })
	eventually(t, "joined", func() bool { return log.Has("JOIN #c") })

	// evrd stops (restart): detaches, no QUIT.
	stopping.Store(true)
	cancel1()
	time.Sleep(200 * time.Millisecond)
	if log.Has("QUIT") {
		t.Fatal("the server saw a QUIT when evrd restarted")
	}
	// Something arrives while evrd is down.
	log.Send(":bob!b@h PRIVMSG #c :said while you were restarting")

	// Next evrd: resumes the same link.
	stopping.Store(false)
	var got atomic.Value
	n.Nick = "alice" // the saved nick; the holder knows the real one
	opts.Persist = func(m history.Message) {
		if strings.Contains(m.Text, "while you were restarting") {
			got.Store(m.Text)
		}
	}
	c2 := New(n, quiet, opts)
	run(t, c2)
	eventually(t, "connected again", func() bool { return c2.Status() == StatusConnected })
	eventually(t, "the missed message delivered", func() bool { return got.Load() != nil })
	eventually(t, "members rebuilt by NAMES", func() bool { return len(c2.Members("#c")) == 2 })
	if c2.Topic("#c") != "the topic" {
		eventually(t, "topic rebuilt", func() bool { return c2.Topic("#c") == "the topic" })
	}
	if c2.Nick() != "alice" {
		t.Fatalf("nick %q", c2.Nick())
	}

	// The network saw one connection, one registration, no QUIT, no second JOIN.
	if n := log.Connections(); n != 1 {
		t.Fatalf("%d connections to the server", n)
	}
	users, joins := 0, 0
	for _, l := range log.Lines() {
		switch {
		case strings.HasPrefix(l, "USER "):
			users++
		case strings.HasPrefix(l, "JOIN "):
			joins++
		case strings.HasPrefix(l, "QUIT"):
			t.Fatalf("server saw %q", l)
		}
	}
	if users != 1 || joins != 1 {
		t.Fatalf("registrations %d, joins %d — want 1 and 1", users, joins)
	}
}

func TestSwitchedOffQuitsThroughTheHolder(t *testing.T) {
	var log testirc.Log
	addr := testirc.StartLogged(t, &log)
	host, port, _ := net.SplitHostPort(addr)
	var p int
	for _, ch := range port {
		p = p*10 + int(ch-'0')
	}
	n := store.Network{ID: 8, Nick: "bob", Host: host, Port: p, QuitMsg: "switched off"}
	holder := startTestHolder(t)
	c := New(n, quiet, Options{Links: holder, LinkID: "n8", Detaching: func() bool { return false }})
	cancel := run(t, c)
	eventually(t, "registered", func() bool { return c.Status() == StatusConnected })
	cancel() // the user switched the network off
	eventually(t, "QUIT reached the server", func() bool { return log.Has("QUIT :switched off") })
	eventually(t, "the holder let the link go", func() bool {
		ls, _ := holder.List(context.Background())
		return len(ls) == 0
	})
}

func TestNoHolderConnectsDirectly(t *testing.T) {
	addr := testirc.Start(t)
	host, port, _ := net.SplitHostPort(addr)
	var p int
	for _, ch := range port {
		p = p*10 + int(ch-'0')
	}
	n := store.Network{ID: 9, Nick: "carol", Host: host, Port: p}
	c := New(n, quiet, Options{Links: links.NewClient(filepath.Join(t.TempDir(), "none.sock")), LinkID: "n9"})
	run(t, c)
	eventually(t, "connected without a holder", func() bool { return c.Status() == StatusConnected })
}
