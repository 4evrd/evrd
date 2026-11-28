package bouncer

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/4evrd/evrd/internal/store"
)

// Managed mode: connections open with WEBIRC for the latest client's
// address, and say what the config says when away or asked their version.
func TestManagedConnections(t *testing.T) {
	r := newLiveRig(t)
	ips := make(chan string, 4)
	r.b.SetManaged(Managed{AwayMessage: "Away for now", QuitMessage: "bye", ReplyFrom: "irc.example",
		WebIRC: func(ctx context.Context, ip, ident string) string {
			ips <- ip
			if ip == "" {
				return ""
			}
			return "WEBIRC pw gateway " + ip + " " + ip
		}})
	alice := r.user("alice", func(u *store.User) { u.AutoAway, u.AutoAwayDelay = true, new(0) })
	n := r.network(alice, "net", false)
	r.b.Start(r.ctx)

	// a client from 192.0.2.7 attaches: the connection says so
	att, err := r.b.AttachClient(alice.ID, 0, ClientInfo{IP: "192.0.2.7"}, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.b.Connect(n.ID); err != nil {
		t.Fatal(err)
	}
	r.connected(n.ID)
	if ip := <-ips; ip != "192.0.2.7" {
		t.Fatalf("WEBIRC for %q", ip)
	}
	eventually(t, "WEBIRC sent", func() bool { return r.irc.Has("WEBIRC pw gateway 192.0.2.7 192.0.2.7") })
	att.Release()
	eventually(t, "the managed away message", func() bool { return r.irc.Has("AWAY :Away for now") })
	r.b.Disconnect(n.ID)
	eventually(t, "the managed quit", func() bool { return r.irc.Has("QUIT :bye") })
}

// userIP falls back to the last good IRC sign-in on record.
func TestUserIPFromSignIns(t *testing.T) {
	r := newLiveRig(t)
	alice := r.user("alice", nil)
	if ip := r.b.userIP(alice.ID); ip != "" {
		t.Fatalf("an address from nowhere: %q", ip)
	}
	ctx := context.Background()
	r.st.AddIRCSignIn(ctx, store.IRCSignIn{UserID: alice.ID, At: time.Now().Add(-time.Minute), IP: "192.0.2.1", Result: "ok"})
	r.st.AddIRCSignIn(ctx, store.IRCSignIn{UserID: alice.ID, At: time.Now(), IP: "192.0.2.9", Result: "invalid"})
	if ip := r.b.userIP(alice.ID); ip != "192.0.2.1" {
		t.Errorf("from the sign-ins: %q", ip)
	}
	r.b.noteIP(alice.ID, "")
	r.b.noteIP(alice.ID, "192.0.2.2")
	if ip := r.b.userIP(alice.ID); ip != "192.0.2.2" {
		t.Errorf("the latest client's: %q", ip)
	}
	if r.b.webIRC(alice.ID, "alice") != nil {
		t.Error("WEBIRC outside managed mode")
	}
	if !strings.Contains(r.b.awayMessage(store.User{}, time.Now()), "evrd") {
		t.Error("the stock away message changed")
	}
}

// The nick guard in managed mode: a registered nick the user identifies
// for during the grace is kept and saved, and tried first next time.
func TestManagedNickGuardSavesTheIdentifiedNick(t *testing.T) {
	r := newLiveRig(t)
	r.b.SetManaged(Managed{ReplyFrom: "irc.example", NickGuard: true,
		NickGuardGrace: func() time.Duration { return time.Minute }, GuardWarning: "{nick}: identify within {grace}"})
	alice := r.user("alice", nil)
	n, err := r.st.CreateNetwork(context.Background(), store.Network{UserID: alice.ID, Name: "net", Host: r.host, Port: r.port,
		Nick: "lion", AuthMethod: store.AuthNone})
	if err != nil {
		t.Fatal(err)
	}
	r.b.Start(r.ctx)
	att, err := r.b.AttachClient(alice.ID, 0, ClientInfo{IP: "192.0.2.7"}, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	defer att.Release()
	if err := r.b.Connect(n.ID); err != nil {
		t.Fatal(err)
	}
	r.connected(n.ID)
	r.irc.Send(":NickServ!s@services NOTICE lion :This nickname is registered and protected. If it is your nick, type /msg NickServ IDENTIFY password.")
	time.Sleep(100 * time.Millisecond)
	r.irc.Send(":NickServ!s@services NOTICE lion :Password accepted - you are now recognized.")
	eventually(t, "the identified nick saved", func() bool {
		got, _ := r.st.NetworkByID(context.Background(), n.ID)
		return got.IdentifiedNick == "lion"
	})
	if c, _ := r.b.ConnFor(n.ID); c.Nick() != "lion" {
		t.Errorf("moved: %s", c.Nick())
	}
}
