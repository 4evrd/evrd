package cluster

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/4evrd/evrd/internal/store"
)

// brokenStore fails the calls a test picks.
type brokenStore struct {
	store.Store
	owner, release, acquire, deleteNodes, upsert error
}

func (b *brokenStore) LeaseOwner(ctx context.Context, uid int64, now time.Time) (store.Node, store.UserLease, bool, error) {
	if b.owner != nil {
		return store.Node{}, store.UserLease{}, false, b.owner
	}
	return b.Store.LeaseOwner(ctx, uid, now)
}

func (b *brokenStore) ReleaseLease(ctx context.Context, uid int64, node string, now time.Time) error {
	if b.release != nil {
		return b.release
	}
	return b.Store.ReleaseLease(ctx, uid, node, now)
}

func (b *brokenStore) AcquireLease(ctx context.Context, uid int64, node string, until, now time.Time) (bool, error) {
	if b.acquire != nil {
		return false, b.acquire
	}
	return b.Store.AcquireLease(ctx, uid, node, until, now)
}

func (b *brokenStore) DeleteNodesBefore(ctx context.Context, t time.Time) error {
	if b.deleteNodes != nil {
		return b.deleteNodes
	}
	return b.Store.DeleteNodesBefore(ctx, t)
}

func (b *brokenStore) UpsertNode(ctx context.Context, n store.Node) error {
	if b.upsert != nil {
		return b.upsert
	}
	return b.Store.UpsertNode(ctx, n)
}

func TestAccessors(t *testing.T) {
	st, clock := setup(t, 4)
	a := newNode(st, clock, "a")
	if a.Self().ID != "a" || a.Self().HTTPURL != "http://a" {
		t.Fatalf("Self: %+v", a.Self())
	}
	if n := a.Nodes(); len(n) != 1 || n[0].ID != "a" {
		t.Fatalf("before the first tick, Nodes is just this one: %v", n)
	}
	if !a.Leader() {
		t.Fatal("alone, a leads")
	}
	if a.HeldCount() != 0 {
		t.Fatal("holds users before ticking")
	}
	tick(clock, a)
	if a.HeldCount() != 4 || !a.Owns(1) {
		t.Fatalf("held %d", a.HeldCount())
	}
	a.SetClock(func() time.Time { return clock.now().Add(time.Hour) })
	if a.now().Sub(clock.now()) != time.Hour {
		t.Fatal("SetClock")
	}
	a.SetLeaseTTL(time.Second)
	if a.ttl != MinLeaseTTL || a.heartbeat() != MinLeaseTTL/4 || a.liveFor() != MinLeaseTTL*3/4 {
		t.Fatalf("the lease is at least MinLeaseTTL: %v", a.ttl)
	}
}

func TestLeaderIsTheSmallestID(t *testing.T) {
	st, clock := setup(t, 1)
	b, a := newNode(st, clock, "b"), newNode(st, clock, "a")
	tick(clock, b, a)
	tick(clock, b, a)
	if b.Leader() || !a.Leader() {
		t.Fatalf("leaders: a %v, b %v", a.Leader(), b.Leader())
	}
}

func TestOwnerErrors(t *testing.T) {
	st, clock := setup(t, 1)
	bs := &brokenStore{Store: st}
	a := newNode(bs, clock, "a")
	if _, err := a.Owner(1); !errors.Is(err, ErrNoOwner) {
		t.Fatalf("before anyone owns the user: %v", err)
	}
	bs.owner = errors.New("db down")
	if _, err := a.Owner(1); err == nil || errors.Is(err, ErrNoOwner) {
		t.Fatalf("a database error: %v", err)
	}
	if _, err := a.WaitOwner(context.Background(), 1, time.Second); err == nil || errors.Is(err, ErrNoOwner) {
		t.Fatalf("WaitOwner returns a database error at once: %v", err)
	}
}

func TestWaitOwner(t *testing.T) {
	st, clock := setup(t, 1)
	a := newNode(st, clock, "a")
	start := time.Now()
	if _, err := a.WaitOwner(context.Background(), 1, 600*time.Millisecond); !errors.Is(err, ErrNoOwner) {
		t.Fatalf("nobody owns the user: %v", err)
	}
	if time.Since(start) < 500*time.Millisecond {
		t.Fatal("didn't wait")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := a.WaitOwner(ctx, 1, time.Minute); !errors.Is(err, context.Canceled) {
		t.Fatalf("a cancelled wait: %v", err)
	}

	// The user gets an owner while we wait.
	go func() {
		time.Sleep(100 * time.Millisecond)
		a.Tick()
	}()
	n, err := a.WaitOwner(context.Background(), 1, 5*time.Second)
	if err != nil || n.ID != "a" {
		t.Fatalf("owner %v, %v", n.ID, err)
	}
}

func TestRunTicksAndDrainsOnStop(t *testing.T) {
	st, _ := setup(t, 3)
	a := New(st, store.Node{ID: "a"}, Hooks{Acquire: func(int64) {}, Lose: func(int64) {}}, quietLog())
	a.SetLeaseTTL(MinLeaseTTL)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { a.Run(ctx); close(done) }()
	deadline := time.Now().Add(5 * time.Second)
	for a.HeldCount() != 3 {
		if time.Now().After(deadline) {
			t.Fatal("Run didn't tick")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run didn't return")
	}
	if a.HeldCount() != 0 {
		t.Fatal("stopping didn't drain")
	}
	if _, err := a.Owner(1); !errors.Is(err, ErrNoOwner) {
		t.Fatalf("leases kept after draining: %v", err)
	}
}

func TestALostLeaseStopsTheUser(t *testing.T) {
	st, clock := setup(t, 2)
	a := newNode(st, clock, "a")
	tick(clock, a)
	// Someone expires a's lease behind its back (a manual edit, say).
	st.ReleaseLease(t.Context(), 1, "a", clock.now().Add(-time.Second))
	if ok, _ := st.AcquireLease(t.Context(), 1, "b", clock.now().Add(time.Hour), clock.now()); !ok {
		t.Fatal("setup: b couldn't take the lease")
	}
	tick(clock, a)
	if a.has(1) || !a.has(2) {
		t.Fatalf("a still runs a user whose lease it lost: %v", a.running)
	}
}

func TestStoreErrorsAreSurvived(t *testing.T) {
	st, clock := setup(t, 2)
	bs := &brokenStore{Store: st, acquire: errors.New("busy"), deleteNodes: errors.New("locked")}
	a := newNode(bs, clock, "a")
	tick(clock, a)
	if a.HeldCount() != 0 {
		t.Fatal("holds users it couldn't acquire")
	}
	bs.acquire = nil
	tick(clock, a)
	if a.HeldCount() != 2 {
		t.Fatalf("held %d once acquiring works", a.HeldCount())
	}
	// Releasing and leaving fail on the way out: users still stop here.
	bs.release, bs.upsert = errors.New("db down"), errors.New("db down")
	a.Drain()
	if a.HeldCount() != 0 || len(a.running) != 0 {
		t.Fatal("drain kept users running")
	}
}

func TestLoseWithAFailingRelease(t *testing.T) {
	st, clock := setup(t, 1)
	bs := &brokenStore{Store: st}
	a := newNode(bs, clock, "a")
	tick(clock, a)
	bs.release = errors.New("db down")
	u, _ := st.UserByID(t.Context(), 1)
	u.Suspended = true
	st.UpdateUser(t.Context(), u)
	tick(clock, a)
	if a.has(1) {
		t.Fatal("a suspended user still runs when the release fails")
	}
}

func quietLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }
