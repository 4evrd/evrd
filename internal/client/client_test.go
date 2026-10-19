package client

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/4evrd/evrd/internal/accounts"
	"github.com/4evrd/evrd/internal/bouncer"
	"github.com/4evrd/evrd/internal/config"
	"github.com/4evrd/evrd/internal/history"
	"github.com/4evrd/evrd/internal/store"
	"github.com/4evrd/evrd/internal/testirc"
)

type rig struct {
	t    *testing.T
	st   store.Store
	hist history.Store
	addr string // evrd's IRC listener
	b    *bouncer.Bouncer
	acc  *accounts.Service
	srv  *Server
	ctx  context.Context
	user store.User
}

// scratchDir is a temp dir removed when the test ends, best effort: the
// bouncer's goroutines may still be finishing writes as the test returns,
// which would make t.TempDir's cleanup fail the test.
func scratchDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "evrd-client-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

func newRig(t *testing.T) *rig {
	t.Helper()
	data := scratchDir(t) // registered first, so removed last
	st, err := store.Open("sqlite:" + filepath.Join(data, "evrd.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	acc := accounts.New(st, config.Limits{MaxNetworks: 3, MaxClients: 5})
	hist, err := history.Open(filepath.Join(data, "history"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { hist.Close() })
	b := bouncer.New(st, hist, log)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	b.Start(ctx)

	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := ln.Addr().String()
	ln.Close()
	srv := NewServer(b, acc, log, "", "")
	go srv.ListenAndServe(ctx, []string{"irc+insecure://" + addr})

	u, err := acc.Create(accounts.CreateInput{Username: "alice", Password: "alice-password"})
	if err != nil {
		t.Fatal(err)
	}
	host, port, _ := net.SplitHostPort(testirc.Start(t))
	var p int
	fmt.Sscan(port, &p)
	for _, name := range []string{"one", "two"} {
		st.CreateNetwork(store.Network{UserID: u.ID, Name: name, Host: host, Port: p, Nick: "alice_" + name, Channels: []string{"#" + name}})
	}
	for i := 0; i < 50; i++ { // wait for the listener
		if c, err := net.Dial("tcp", addr); err == nil {
			c.Close()
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	return &rig{t: t, st: st, hist: hist, addr: addr, b: b, acc: acc, srv: srv, ctx: ctx, user: u}
}

type ircClient struct {
	t  *testing.T
	c  net.Conn
	sc *bufio.Scanner
}

func (r *rig) attach(pass string, caps ...string) *ircClient {
	r.t.Helper()
	c, err := net.Dial("tcp", r.addr)
	if err != nil {
		r.t.Fatal(err)
	}
	r.t.Cleanup(func() { c.Close() })
	ic := &ircClient{t: r.t, c: c, sc: bufio.NewScanner(c)}
	if len(caps) > 0 {
		ic.send("CAP LS 302")
		ic.send("CAP REQ :" + strings.Join(caps, " "))
		ic.send("CAP END")
	}
	ic.send("PASS " + pass)
	ic.send("NICK alice")
	ic.send("USER alice 0 * :alice")
	return ic
}

func (ic *ircClient) send(line string) { fmt.Fprintf(ic.c, "%s\r\n", line) }

// expect reads until a line contains want, failing after a timeout.
func (ic *ircClient) expect(want string) string {
	ic.t.Helper()
	ic.c.SetReadDeadline(time.Now().Add(3 * time.Second))
	var seen []string
	for ic.sc.Scan() {
		line := ic.sc.Text()
		if strings.Contains(line, want) {
			return line
		}
		seen = append(seen, line)
	}
	ic.t.Fatalf("never got %q; saw:\n%s", want, strings.Join(seen, "\n"))
	return ""
}

func (r *rig) network(name string) store.Network {
	nets, _ := r.st.ListNetworks(1)
	n, _ := findNetwork(nets, name)
	return n
}

// eventually retries cond for a moment: the store is written asynchronously.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for i := 0; i < 100; i++ {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("never: %s", what)
}

// chat is a buffer's messages without channel events.
func chat(h history.Store, netID int64, target string) []history.Message {
	all, _ := h.Recent(netID, target, 50)
	var out []history.Message
	for _, m := range all {
		if isChat(m.Kind) {
			out = append(out, m)
		}
	}
	return out
}

func TestStatusHelpAndNetworks(t *testing.T) {
	r := newRig(t)
	ic := r.attach("alice/one:alice-password")
	ic.expect(" 001 ")
	ic.expect("JOIN #one")
	ic.send("PRIVMSG *status :help")
	ic.expect(":*status!status@evrd PRIVMSG alice_one :evrd commands")
	ic.send("PRIVMSG *status :listnetworks") // ZNC alias
	ic.expect("* one")
	ic.expect("2 networks.")
	ic.send("PRIVMSG *status :bogus")
	ic.expect(`Unknown command "bogus"`)
}

func TestJoinPartAreSaved(t *testing.T) {
	r := newRig(t)
	ic := r.attach("alice/one:alice-password")
	ic.expect("JOIN #one")
	ic.send("JOIN #new")
	ic.expect("JOIN #new")
	eventually(t, "#new saved", func() bool { return strings.Join(r.network("one").Channels, " ") == "#one #new" })
	ic.send("PART #one")
	ic.expect("PART #one")
	eventually(t, "#one forgotten", func() bool { return strings.Join(r.network("one").Channels, " ") == "#new" })
}

func TestNickChangeFollowsTheServer(t *testing.T) {
	r := newRig(t)
	ic := r.attach("alice/one:alice-password")
	ic.expect("NICK alice_one") // client nick -> upstream nick
	ic.send("NICK renamed")
	ic.expect(":alice_one!u@h NICK renamed")
	ic.send("PRIVMSG *status :info")
	ic.expect("PRIVMSG renamed :Account alice")
}

func TestNoticesAndActionsAreRecorded(t *testing.T) {
	r := newRig(t)
	ic := r.attach("alice/one:alice-password")
	ic.expect("JOIN #one")
	ic.send("NOTICE #one :heads up")
	ic.send("PRIVMSG #one :\x01ACTION waves\x01")
	ic.send("PRIVMSG bob :\x01VERSION\x01")
	n := r.network("one")
	eventually(t, "notice and action saved", func() bool {
		msgs := chat(r.hist, n.ID, "#one")
		return len(msgs) == 2 && msgs[0].Kind == "notice" && msgs[1].Kind == "action" && msgs[1].Text == "waves"
	})
	if msgs, _ := r.hist.Recent(n.ID, "bob", 10); len(msgs) != 0 {
		t.Fatalf("a CTCP VERSION was saved: %+v", msgs)
	}
}

func TestCapReqIsAllOrNothing(t *testing.T) {
	r := newRig(t)
	ic := r.attach("alice/one:alice-password", "server-time", "sasl")
	ic.expect("CAP * NAK :server-time sasl")
	ic.expect(" 001 ")
	ic.send("CAP REQ :echo-message")
	ic.expect("ACK :echo-message")
	ic.send("CAP LIST")
	ic.expect("LIST :echo-message")
}

func TestJumpAndNetworkManagement(t *testing.T) {
	r := newRig(t)
	ic := r.attach("alice/one:alice-password")
	ic.expect("JOIN #one")

	ic.send("PRIVMSG *status :jump two")
	ic.expect("PART #one :jumped to two")
	ic.expect("NICK alice_two")
	ic.expect("Jumped from one to two")
	ic.expect("JOIN #two")
	// Traffic now goes to network two.
	ic.send("PRIVMSG #two :hi two")
	n := r.network("two")
	eventually(t, "message on two", func() bool {
		msgs := chat(r.hist, n.ID, "#two")
		return len(msgs) == 1 && msgs[0].Text == "hi two"
	})

	ic.send("PRIVMSG *status :delnetwork two")
	ic.expect("You're on two")
	ic.send("PRIVMSG *status :addnetwork three irc.example.org 6667")
	ic.expect("Added three (irc.example.org:6667, nick alice)")
	ic.send("PRIVMSG *status :addnetwork four irc.example.org")
	ic.expect("limit of 3 networks")
	ic.send("PRIVMSG *status :delnetwork three")
	ic.expect("Removed three")

	// the message and the server's JOIN, once both are saved
	eventually(t, "#two history saved", func() bool { m, _ := r.hist.Recent(n.ID, "#two", 10); return len(m) == 2 })
	ic.send("PRIVMSG *status :playback #two")
	ic.expect("] hi two") // playback carries a timestamp for clients without server-time
	ic.expect("Replayed 2 line(s) of #two")
}

func TestChannelEventsAreSavedAndReplayed(t *testing.T) {
	r := newRig(t)
	ic := r.attach("alice/one:alice-password", "server-time")
	ic.expect("JOIN #one")
	for _, l := range []string{
		":ann!u@h JOIN #one",
		":fake 353 alice_one = #one :@alice_one +bob",
		":bob!u@h NICK robert",
		":ann!u@h TOPIC #one :new topic",
		":robert!u@h QUIT :gone",
		":ann!u@h MODE #one +o ann",
		":ann!u@h KICK #one alice_one :bye",
	} {
		ic.send("SIMULATE " + l) // forwarded raw to the fake server, which echoes it
	}
	n := r.network("one")
	want := []string{
		"alice_one joined", "ann joined", "bob is now known as robert", "ann changed the topic to: new topic",
		"robert quit (gone)", "ann set mode +o ann", "ann kicked alice_one (bye)",
	}
	eventually(t, "events saved", func() bool {
		msgs, _ := r.hist.Recent(n.ID, "#one", 50)
		return len(msgs) == len(want)
	})
	msgs, _ := r.hist.Recent(n.ID, "#one", 50)
	for i, m := range msgs {
		if got := describe(m); got != want[i] {
			t.Errorf("event %d: got %q, want %q", i, got, want[i])
		}
	}
	// Kicked: still saved, so a reconnect rejoins.
	if ch := r.network("one").Channels; len(ch) != 1 || ch[0] != "#one" {
		t.Fatalf("kick dropped the saved channel: %v", ch)
	}

	again := r.attach("alice/one:alice-password", "server-time")
	again.expect("@time=")
	again.expect(":*buffextras!buffextras@evrd PRIVMSG #one :bob is now known as robert")
}

func TestStatusShowAndSet(t *testing.T) {
	r := newRig(t)
	ic := r.attach("alice/one:alice-password")
	ic.expect("JOIN #one")

	ic.send("PRIVMSG *status :set realname Alice Liddell")
	ic.expect("Saved realname = Alice Liddell on one. Reconnecting")
	ic.send("PRIVMSG *status :set two password hunter22")
	ic.expect("Saved password = (hidden) on two.")
	ic.send("PRIVMSG *status :set two name one")
	ic.expect("You already have a network called one.")
	ic.send("PRIVMSG *status :set port +7000")
	ic.expect("Saved port = +7000")
	ic.send("PRIVMSG *status :show")
	ic.expect("server 1: 127.0.0.1:7000 (TLS)")
	ic.expect("realname Alice Liddell")
	ic.send("PRIVMSG *status :set nick bad nick!")
	ic.expect("Nicks can't contain")

	if n := r.network("two"); n.Password != "hunter22" {
		t.Fatalf("password not saved: %+v", n)
	}
	ic.send("PRIVMSG *status :set two password -")
	ic.expect("(cleared)")
	eventually(t, "password cleared", func() bool { return r.network("two").Password == "" })
}
