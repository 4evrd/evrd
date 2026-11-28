package managed

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/4evrd/evrd/internal/accounts"
	"github.com/4evrd/evrd/internal/store"
)

// guestBouncer deletes accounts for real and counts clients as told.
type guestBouncer struct {
	acc     *accounts.Service
	mu      sync.Mutex
	clients map[int64]int
	deleted []int64
	notOurs map[int64]bool
}

func (b *guestBouncer) DeleteUser(id int64) error {
	err := b.acc.DeleteUser(context.Background(), id)
	// noted once it's done, so a test that sees it gone finds no account
	b.mu.Lock()
	b.deleted = append(b.deleted, id)
	b.mu.Unlock()
	return err
}

func (b *guestBouncer) ClientsByUser() map[int64]int {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := map[int64]int{}
	for k, v := range b.clients {
		out[k] = v
	}
	return out
}

func (b *guestBouncer) Owns(id int64) bool { return !b.notOurs[id] }

func (b *guestBouncer) setClients(id int64, n int) {
	b.mu.Lock()
	b.clients[id] = n
	b.mu.Unlock()
}

func (b *guestBouncer) gone(id int64) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, d := range b.deleted {
		if d == id {
			return true
		}
	}
	return false
}

func testGuests(t *testing.T, set func(c *Guests)) (*Guests, *guestBouncer, *accounts.Service) {
	t.Helper()
	acc, _ := testAccounts(t)
	cfg := testConfig(nil)
	cfg.Managed.Guests = true
	cfg.Managed.GuestLinger = 50 * time.Millisecond
	gb := &guestBouncer{acc: acc, clients: map[int64]int{}, notOurs: map[int64]bool{}}
	g := &Guests{Mode: New(cfg), Accounts: acc, Bouncer: gb, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if set != nil {
		set(g)
	}
	return g, gb, acc
}

func TestGuestSignInMakesAnEphemeralAccount(t *testing.T) {
	g, gb, acc := testGuests(t, nil)
	ctx := context.Background()
	u, resumed, err := g.SignIn(ctx, GuestInput{IP: "192.0.2.1", Nick: "visitor", User: "~web.user!", Realname: "A visitor"})
	if err != nil || resumed {
		t.Fatal(err, resumed)
	}
	if !u.Guest || !IsGuestName(u.Username) || u.MaxClients == nil || *u.MaxClients != 1 {
		t.Errorf("account %+v", u)
	}
	nets, _ := acc.ListNetworks(ctx, u.ID)
	if len(nets) != 1 || nets[0].Nick != "visitor" || nets[0].Username != "webuser" || nets[0].Realname != "A visitor" ||
		nets[0].Auth() != store.AuthNone || nets[0].Host != "irc.example.org" || nets[0].Name != "ExampleNet" {
		t.Fatalf("network %+v", nets)
	}
	if !g.IsGuest(u.ID) || g.Count() != 1 {
		t.Error("not counted")
	}

	// the client goes: the session lingers, then ends with the account
	gb.setClients(u.ID, 1)
	g.Left(u.ID) // a client still there: nothing
	if g.Count() != 1 {
		t.Fatal("ended with a client attached")
	}
	gb.setClients(u.ID, 0)
	g.Left(u.ID)
	deadline := time.Now().Add(2 * time.Second)
	for !gb.gone(u.ID) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !gb.gone(u.ID) || g.Count() != 0 {
		t.Fatal("the session didn't end")
	}
	if _, err := acc.UserByID(ctx, u.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("the account stayed: %v", err)
	}
}

func TestGuestRefreshPicksTheSessionUp(t *testing.T) {
	g, gb, _ := testGuests(t, nil)
	g.Mode.cfg.GuestLinger = time.Hour
	ctx := context.Background()
	u, _, err := g.SignIn(ctx, GuestInput{IP: "192.0.2.1", Nick: "visitor"})
	if err != nil {
		t.Fatal(err)
	}
	g.Track(u.ID, func() string { return "visitor_" }) // the guard moved it
	g.Left(u.ID)
	// another address on the same nick: a new guest
	o, resumed, err := g.SignIn(ctx, GuestInput{IP: "192.0.2.2", Nick: "visitor"})
	if err != nil || resumed || o.ID == u.ID {
		t.Fatalf("another address took the session: %v %v", resumed, err)
	}
	// the same address, on the nick it has now
	back, resumed, err := g.SignIn(ctx, GuestInput{IP: "192.0.2.1", Nick: "VISITOR_"})
	if err != nil || !resumed || back.ID != u.ID {
		t.Fatalf("not picked up: %v %v %d", resumed, err, back.ID)
	}
	if gb.gone(u.ID) {
		t.Fatal("ended")
	}
	// gone from the store meanwhile: a new one
	g.Left(u.ID)
	gb.acc.DeleteUser(ctx, u.ID)
	again, resumed, err := g.SignIn(ctx, GuestInput{IP: "192.0.2.1", Nick: "visitor"})
	if err != nil || resumed || again.ID == u.ID {
		t.Fatalf("a deleted session came back: %v %v", resumed, err)
	}
}

func TestGuestLimits(t *testing.T) {
	ctx := context.Background()
	g, _, _ := testGuests(t, nil)
	g.Mode.cfg.GuestsPerIP, g.Mode.cfg.GuestsMax = 2, 3
	for i := range 2 {
		if _, _, err := g.SignIn(ctx, GuestInput{IP: "192.0.2.1", Nick: "a"}); err != nil {
			t.Fatal(i, err)
		}
	}
	if _, _, err := g.SignIn(ctx, GuestInput{IP: "192.0.2.1", Nick: "a"}); !errors.Is(err, ErrGuestsPerIP) {
		t.Errorf("per address: %v", err)
	}
	if _, _, err := g.SignIn(ctx, GuestInput{IP: "192.0.2.2", Nick: "b"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := g.SignIn(ctx, GuestInput{IP: "192.0.2.3", Nick: "c"}); !errors.Is(err, ErrGuestsFull) {
		t.Errorf("max: %v", err)
	}

	// the rate: guestRate new ones a minute from one address
	g, _, _ = testGuests(t, nil)
	g.Mode.cfg.GuestsPerIP, g.Mode.cfg.GuestsMax, g.Mode.cfg.GuestLinger = 0, 0, 0
	now := time.Now()
	g.now = func() time.Time { return now }
	for i := range guestRate {
		u, _, err := g.SignIn(ctx, GuestInput{IP: "192.0.2.9", Nick: "r"})
		if err != nil {
			t.Fatal(i, err)
		}
		g.Left(u.ID)
	}
	if _, _, err := g.SignIn(ctx, GuestInput{IP: "192.0.2.9", Nick: "r"}); !errors.Is(err, ErrGuestsRate) {
		t.Errorf("rate: %v", err)
	}
	now = now.Add(61 * time.Second)
	if _, _, err := g.SignIn(ctx, GuestInput{IP: "192.0.2.9", Nick: "r"}); err != nil {
		t.Errorf("a minute on: %v", err)
	}

	// off: refused
	g.Mode.cfg.Guests = false
	if _, _, err := g.SignIn(ctx, GuestInput{IP: "192.0.2.10", Nick: "x"}); !errors.Is(err, ErrGuestsOff) {
		t.Errorf("off: %v", err)
	}
	var none *Guests
	if none.On() || none.IsGuest(1) || none.Count() != 0 {
		t.Error("nil guests")
	}
	none.Left(1)
}

func TestGuestSweepRemovesLeftovers(t *testing.T) {
	ctx := context.Background()
	g, gb, acc := testGuests(t, nil)
	live, _, err := g.SignIn(ctx, GuestInput{IP: "192.0.2.1", Nick: "a"})
	if err != nil {
		t.Fatal(err)
	}
	old, _ := acc.Create(accounts.CreateInput{Username: "guest-0000000001", Guest: true})
	used, _ := acc.Create(accounts.CreateInput{Username: "guest-0000000002", Guest: true})
	other, _ := acc.Create(accounts.CreateInput{Username: "guest-0000000003", Guest: true})
	member, _ := acc.Create(accounts.CreateInput{Username: "guest-like-member"})
	gb.setClients(used.ID, 1)
	gb.notOurs[other.ID] = true
	n, err := g.Sweep(ctx)
	if err != nil || n != 1 {
		t.Fatalf("removed %d (%v)", n, err)
	}
	for _, id := range []int64{live.ID, used.ID, other.ID, member.ID} {
		if _, err := acc.UserByID(ctx, id); err != nil {
			t.Errorf("%d removed", id)
		}
	}
	if _, err := acc.UserByID(ctx, old.ID); err == nil {
		t.Error("the leftover stayed")
	}
	rctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { g.Run(rctx, 5*time.Millisecond); close(done) }()
	time.Sleep(30 * time.Millisecond)
	cancel()
	<-done
}

func TestGuestInputCleaned(t *testing.T) {
	for in, want := range map[string]string{"": "guest", "ok": "ok", "a b": "guest", "#chan": "guest", "1abc": "guest", "Zoë": "Zoë"} {
		if got := guestNick(in); got != want {
			t.Errorf("guestNick(%q) = %q", in, got)
		}
	}
	for in, want := range map[string]string{"": "guest", "~x": "x", "a.b-c": "abc", "abcdefghijklmno": "abcdefghij", "!!": "guest"} {
		if got := guestIdent(in); got != want {
			t.Errorf("guestIdent(%q) = %q", in, got)
		}
	}
	if guestRealname(" ", "n") != "n" || guestRealname("a\nb", "n") != "a b" || len(guestRealname(strings.Repeat("a", 300), "n")) != 100 {
		t.Error("realname")
	}
}

func TestProvisionerLeavesGuestsAlone(t *testing.T) {
	p, _, acc := testProvisioner(t)
	ctx := context.Background()
	if _, err := acc.Create(accounts.CreateInput{Username: "guest-abc", Guest: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Put(ctx, "alice", UserInput{}); err != nil {
		t.Fatal(err)
	}
	us, _ := p.List(ctx)
	if len(us) != 1 || us[0].Username != "alice" {
		t.Errorf("listed %+v", us)
	}
	if _, err := p.Get(ctx, "guest-abc"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("get: %v", err)
	}
	if _, err := p.Put(ctx, "guest-abc", UserInput{}); !errors.Is(err, ErrGuestAccount) {
		t.Errorf("put: %v", err)
	}
	if err := p.Delete(ctx, "guest-abc"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("delete: %v", err)
	}
	h, err := p.Health(ctx)
	if err != nil || h.Users.Total != 1 || h.Guests.Enabled || h.Guests.Max != 100 {
		t.Errorf("health %+v %v", h, err)
	}
}
