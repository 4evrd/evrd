package cluster

import (
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/4evrd/evrd/internal/store"
)

// fakeClock is shared by every node in a test.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (f *fakeClock) now() time.Time { f.mu.Lock(); defer f.mu.Unlock(); return f.t }
func (f *fakeClock) add(d time.Duration) {
	f.mu.Lock()
	f.t = f.t.Add(d)
	f.mu.Unlock()
}

// node records what its hooks were told.
type node struct {
	*Cluster
	mu      sync.Mutex
	running map[int64]bool
}

func (n *node) has(uid int64) bool { n.mu.Lock(); defer n.mu.Unlock(); return n.running[uid] }

// flakyStore fails every call while down is set, to simulate losing the
// database.
type flakyStore struct {
	store.Store
	down bool
}

func (f *flakyStore) UpsertNode(n store.Node) error {
	if f.down {
		return fmt.Errorf("db down")
	}
	return f.Store.UpsertNode(n)
}

func setup(t *testing.T, users int) (store.Store, *fakeClock) {
	t.Helper()
	st, err := store.Open("sqlite:" + filepath.Join(t.TempDir(), "evrd.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	for i := 0; i < users; i++ {
		st.CreateUser(store.User{Username: fmt.Sprintf("u%d", i)})
	}
	return st, &fakeClock{t: time.Unix(1_800_000_000, 0)}
}

func newNode(st store.Store, clock *fakeClock, id string) *node {
	n := &node{running: map[int64]bool{}}
	n.Cluster = New(st, store.Node{ID: id, HTTPURL: "http://" + id}, Hooks{
		Acquire: func(uid int64) { n.mu.Lock(); n.running[uid] = true; n.mu.Unlock() },
		Lose:    func(uid int64) { n.mu.Lock(); delete(n.running, uid); n.mu.Unlock() },
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	n.now = clock.now
	return n
}

// tick advances time by a heartbeat and ticks every node.
func tick(clock *fakeClock, nodes ...*node) {
	clock.add(Heartbeat)
	for _, n := range nodes {
		n.Tick()
	}
}

// exactlyOne checks that every user runs on exactly one node, and that the
// node running it is the lease holder.
func exactlyOne(t *testing.T, st store.Store, clock *fakeClock, users int, nodes ...*node) map[string]int {
	t.Helper()
	per := map[string]int{}
	for uid := int64(1); uid <= int64(users); uid++ {
		var on []string
		for _, n := range nodes {
			if n.has(uid) {
				on = append(on, n.self.ID)
			}
		}
		if len(on) != 1 {
			t.Fatalf("user %d runs on %v", uid, on)
		}
		owner, err := nodes[0].Owner(uid)
		if err != nil || owner.ID != on[0] {
			t.Fatalf("user %d runs on %s but the lease says %v (%v)", uid, on[0], owner.ID, err)
		}
		per[on[0]]++
	}
	return per
}

func TestSingleNodeOwnsEveryone(t *testing.T) {
	st, clock := setup(t, 20)
	a := newNode(st, clock, "a")
	tick(clock, a)
	if per := exactlyOne(t, st, clock, 20, a); per["a"] != 20 {
		t.Fatalf("got %v", per)
	}
	if !a.Leader() {
		t.Fatal("a lone node should lead")
	}
}

func TestUsersSpreadAndFailOver(t *testing.T) {
	const users = 300
	st, clock := setup(t, users)
	a := newNode(st, clock, "a")
	tick(clock, a)

	// b joins: after it has settled, users move to it in batches.
	b := newNode(st, clock, "b")
	for i := 0; i < 20; i++ {
		tick(clock, a, b)
	}
	per := exactlyOne(t, st, clock, users, a, b)
	if per["a"] < 100 || per["b"] < 100 {
		t.Fatalf("uneven spread: %v", per)
	}
	if a.Leader() == b.Leader() {
		t.Fatal("exactly one node should lead")
	}

	// b dies (stops ticking). Its leases expire and a takes everyone over.
	for i := 0; i < 8; i++ {
		tick(clock, a)
	}
	if per := exactlyOne(t, st, clock, users, a, &node{Cluster: b.Cluster, running: map[int64]bool{}}); per["a"] != users {
		t.Fatalf("after failover: %v", per)
	}
}

func TestDrainHandsOffAtOnce(t *testing.T) {
	st, clock := setup(t, 50)
	a, b := newNode(st, clock, "a"), newNode(st, clock, "b")
	for i := 0; i < 10; i++ {
		tick(clock, a, b)
	}
	b.Drain()
	if len(b.heldIDs()) != 0 || len(b.running) != 0 {
		t.Fatal("drain left users running")
	}
	// b has left the live set: a's next tick owns everyone, without waiting
	// for b's heartbeat to go stale or its leases to expire.
	tick(clock, a)
	if per := exactlyOne(t, st, clock, 50, a, b); per["a"] != 50 {
		t.Fatalf("after drain: %v", per)
	}
}

func TestSuspendedAndDeletedUsersAreStopped(t *testing.T) {
	st, clock := setup(t, 3)
	a := newNode(st, clock, "a")
	tick(clock, a)
	u, _ := st.UserByID(2)
	u.Suspended = true
	st.UpdateUser(u)
	st.DeleteUser(3)
	tick(clock, a)
	if !a.has(1) || a.has(2) || a.has(3) {
		t.Fatalf("running: %v", a.running)
	}
	u.Suspended = false
	st.UpdateUser(u)
	tick(clock, a)
	if !a.has(2) {
		t.Fatal("unsuspended user didn't come back")
	}
}

func TestFencingStopsUsersBeforeLeasesExpire(t *testing.T) {
	st, clock := setup(t, 5)
	flaky := &flakyStore{Store: st}
	a := newNode(flaky, clock, "a")
	tick(clock, a)
	if len(a.heldIDs()) != 5 {
		t.Fatal("setup")
	}
	flaky.down = true
	tick(clock, a) // 5s without renewing: keep running
	if len(a.running) != 5 {
		t.Fatal("stopped too early")
	}
	tick(clock, a) // 10s: half the lease — stop everything
	if len(a.running) != 0 {
		t.Fatalf("still running after fencing: %v", a.running)
	}
	// Another node can take over once the leases expire, never before.
	b := newNode(st, clock, "b")
	b.Tick()
	if len(b.heldIDs()) != 0 {
		t.Fatal("b took leases that hadn't expired")
	}
	clock.add(DefaultLeaseTTL)
	b.Tick()
	if len(b.heldIDs()) != 5 {
		t.Fatalf("b holds %v", b.heldIDs())
	}
}

func TestPlaceIsStable(t *testing.T) {
	nodes := []store.Node{{ID: "a"}, {ID: "b"}, {ID: "c"}}
	moved := 0
	for uid := int64(1); uid <= 1000; uid++ {
		before := Place(uid, nodes)
		after := Place(uid, nodes[:2]) // c leaves
		if before.ID != "c" && before.ID != after.ID {
			t.Fatalf("user %d moved from %s to %s though its node stayed", uid, before.ID, after.ID)
		}
		if before.ID == "c" {
			moved++
		}
	}
	if moved < 250 || moved > 420 {
		t.Fatalf("c owned %d of 1000 — expected about a third", moved)
	}
}

func TestLeaderForgetsLongGoneNodes(t *testing.T) {
	st, clock := setup(t, 1)
	st.UpsertNode(store.Node{ID: "old", HeartbeatAt: clock.now().Add(-2 * time.Hour)})
	a := newNode(st, clock, "a")
	tick(clock, a)
	nodes, _ := st.LiveNodes(clock.now().Add(-3 * time.Hour))
	if len(nodes) != 1 || nodes[0].ID != "a" {
		t.Fatalf("nodes: %v", nodes)
	}
}

func TestShorterLeaseFailsOverFaster(t *testing.T) {
	st, clock := setup(t, 30)
	a, b := newNode(st, clock, "a"), newNode(st, clock, "b")
	a.SetLeaseTTL(8 * time.Second)
	b.SetLeaseTTL(8 * time.Second)
	step := func(nodes ...*node) {
		clock.add(2 * time.Second) // a quarter of the lease
		for _, n := range nodes {
			n.Tick()
		}
	}
	for i := 0; i < 10; i++ {
		step(a, b)
	}
	if len(b.heldIDs()) == 0 {
		t.Fatal("b got no users")
	}
	// b dies: a has everyone within ~one lease (8s), not the default 20s.
	start := clock.now()
	for len(a.heldIDs()) < 30 {
		step(a)
		if clock.now().Sub(start) > 12*time.Second {
			t.Fatalf("a holds %d after %v", len(a.heldIDs()), clock.now().Sub(start))
		}
	}
}
