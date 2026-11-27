package client

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/4evrd/evrd/internal/config"
	"github.com/4evrd/evrd/internal/managed"
	"github.com/4evrd/evrd/internal/store"
)

// guestRig is the managed rig with guests, and WEBIRC from the web
// gateway (password "gw-secret").
func guestRig(t *testing.T) (*managedRig, *managed.Guests) {
	t.Helper()
	r := newManagedRig(t)
	c := config.Default()
	c.Managed = r.mode.Config()
	c.Managed.ClientWebIRCPassword = "gw-secret"
	m := managed.New(c)
	m.SetTokens(r.mode.Tokens())
	r.mode = m
	r.srv.SetManaged(m)
	g := &managed.Guests{Mode: m, Accounts: r.acc, Bouncer: r.b, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	r.srv.SetGuests(g)
	r.prov = &managed.Provisioner{Mode: m, Accounts: r.acc, Bouncer: r.b, Guests: g, Store: r.st}
	return r, g
}

// guest connects a client without credentials, through the gateway (ip)
// or straight ("").
func (r *managedRig) guest(ip, nick string) *recClient {
	c := r.dial()
	c.send("CAP LS 302")
	if ip != "" {
		c.send("WEBIRC gw-secret webircgateway " + ip + " " + ip)
	}
	c.send("NICK " + nick)
	c.send("USER web 0 * :A visitor")
	c.send("CAP REQ :message-tags server-time echo-message batch draft/chathistory")
	c.send("CAP END")
	return c
}

func TestManagedGuests(t *testing.T) {
	r, g := guestRig(t)
	ctx := context.Background()

	// off: a client without credentials is refused, as before
	c := r.guest("203.0.113.7", "visitor")
	c.expect("464 visitor :Password required")

	// on, live (the managed API's PUT /api/managed/settings)
	if l, err := r.prov.SetLive(ctx, managed.LiveInput{Guests: new(true), GuestLingerSeconds: new(1)}); err != nil || !l.Guests || l.GuestLingerSeconds != 1 {
		t.Fatalf("live %+v %v", l, err)
	}
	c = r.guest("203.0.113.7", "visitor")
	c.expect(":irc.example.org 001 visitor :Welcome to the ExampleNet IRC Network visitor")
	eventually(t, "WEBIRC with the guest's own address", func() bool { return r.irc.Has("WEBIRC webirc-secret boson 203.0.113.7 203.0.113.7") })
	c.send("JOIN #lobby")
	c.expect("JOIN #lobby")
	c.send("PRIVMSG #lobby :hi from a guest")
	if l := c.expect("PRIVMSG #lobby :hi from a guest"); !strings.Contains(l, "msgid=") {
		t.Errorf("no echo-message with a msgid for a guest: %q", l)
	}
	c.send("CHATHISTORY LATEST #lobby * 10")
	c.expect("BATCH +")
	c.expect("hi from a guest")
	if g.Count() != 1 {
		t.Fatalf("guests %d", g.Count())
	}
	us, _ := r.prov.List(ctx)
	if len(us) != 0 {
		t.Errorf("a guest listed among the accounts: %+v", us)
	}
	h, _ := r.prov.Health(ctx)
	if !h.Guests.Enabled || h.Guests.Active != 1 || h.Users.Total != 0 {
		t.Errorf("health %+v", h.Guests)
	}
	conns := r.irc.Connections()

	// a refresh: back within the linger, the same session (no new
	// connection, nobody quit)
	c.c.Close()
	time.Sleep(100 * time.Millisecond)
	c = r.guest("203.0.113.7", "visitor")
	c.expect(" 001 visitor ")
	c.expect("JOIN #lobby")
	if r.irc.Connections() != conns || r.irc.Has("QUIT") {
		t.Errorf("the refresh reconnected (%d → %d) or quit", conns, r.irc.Connections())
	}

	// gone: after the linger the connection quits and the account goes
	all, _ := r.acc.ListUsers(ctx)
	var id int64
	for _, u := range all {
		if u.Guest {
			id = u.ID
		}
	}
	c.c.Close()
	eventually(t, "the guest's session ended", func() bool {
		_, err := r.acc.UserByID(ctx, id)
		return errors.Is(err, store.ErrNotFound) && g.Count() == 0
	})
	eventually(t, "quit", func() bool { return r.irc.Has("QUIT") })

	// the limit per address, and a WEBIRC with the wrong password counts
	// the gateway's own address
	if _, err := r.prov.SetLive(ctx, managed.LiveInput{GuestsPerIP: new(1)}); err != nil {
		t.Fatal(err)
	}
	a := r.guest("198.51.100.1", "one")
	a.expect(" 001 one ")
	b := r.guest("198.51.100.1", "two")
	b.expect("464 two :Too many guests from your address")
	w := r.dial()
	w.send("WEBIRC wrong gw 198.51.100.9 198.51.100.9")
	w.send("NICK three")
	w.send("USER web 0 * :x")
	w.expect(" 001 three ")
	eventually(t, "WEBIRC with the gateway's address", func() bool { return r.irc.Has("WEBIRC webirc-secret boson 127.0.0.1 127.0.0.1") })

	// off again: new guests are refused
	if _, err := r.prov.ResetLive(ctx); err != nil {
		t.Fatal(err)
	}
	r.guest("192.0.2.50", "late").expect("464 late :Password required")

	r.mu.Lock()
	defer r.mu.Unlock()
	for _, l := range r.seen {
		if strings.Contains(strings.ToLower(l), "evrd") {
			t.Errorf("a guest was sent %q", l)
		}
	}
}
