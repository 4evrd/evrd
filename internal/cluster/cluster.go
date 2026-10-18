// Package cluster decides which evrd node owns each user.
//
// A user's live state — their upstream IRC connections, attached clients and
// web chat sockets — exists on exactly one node, the one holding the user's
// lease in the database. Every node runs the same loop every few seconds:
//
//  1. heartbeat its row in the nodes table;
//  2. renew the leases it holds;
//  3. place every active user on a live node by rendezvous hashing, so all
//     nodes agree without talking to each other and a node joining or
//     leaving moves only its share of users;
//  4. take the leases of users placed on it (once free), and hand off users
//     placed elsewhere — stopping them first, then expiring the lease, so two
//     nodes never run the same user.
//
// A node that can't reach the database stops all its users after half a
// lease lifetime, before anyone else could take them over (fencing).
// Suspended and deleted users are owned by nobody, which is also how a
// suspension made with evrctl takes effect.
package cluster

import (
	"context"
	"errors"
	"hash/fnv"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"code.ircforever.org/ircforever/evrd/internal/store"
)

// Timing follows the lease length (how long a dead node's users wait to move):
// heartbeats every quarter of it, so a lease outlives three missed beats; a
// node is live while its last beat is within three quarters; a node that
// can't renew fences itself halfway; a new node must be up for half a lease
// before users move to it.
const (
	DefaultLeaseTTL = 20 * time.Second
	MinLeaseTTL     = 4 * time.Second
	// Heartbeat is the tick at the default lease length.
	Heartbeat = DefaultLeaseTTL / 4
	moveBatch = 50 // most users handed off per tick, so moves are gradual
)

// ErrNoOwner means no live node holds the user's lease right now — it's
// between nodes, or the cluster is starting.
var ErrNoOwner = errors.New("cluster: user has no owner right now")

// Hooks are what the node does when it gains or loses a user.
type Hooks struct {
	Acquire func(userID int64) // start the user's enabled networks
	Lose    func(userID int64) // stop everything the user has here
}

// Cluster is this node's view of ownership.
type Cluster struct {
	st    store.Store
	self  store.Node
	hooks Hooks
	log   *slog.Logger
	now   func() time.Time
	ttl   time.Duration // lease length; the other timings follow from it

	mu        sync.Mutex
	held      map[int64]bool
	nodes     []store.Node // live nodes as of the last tick
	lastRenew time.Time
}

// New creates the cluster member for this node. self.ID must be unique and
// stable per process; HTTPURL and IRCAddr are its internal endpoints.
func New(st store.Store, self store.Node, hooks Hooks, log *slog.Logger) *Cluster {
	return &Cluster{st: st, self: self, hooks: hooks, log: log, now: time.Now, ttl: DefaultLeaseTTL, held: map[int64]bool{}}
}

// SetLeaseTTL sets the lease length (failover time); at least MinLeaseTTL.
// Every node in a cluster should use the same value.
func (c *Cluster) SetLeaseTTL(ttl time.Duration) {
	c.ttl = max(ttl, MinLeaseTTL)
}

func (c *Cluster) heartbeat() time.Duration { return c.ttl / 4 }
func (c *Cluster) liveFor() time.Duration   { return c.ttl * 3 / 4 }

// SetClock replaces the clock — for tests that drive leases through time.
func (c *Cluster) SetClock(now func() time.Time) { c.now = now }

// Self is this node.
func (c *Cluster) Self() store.Node { return c.self }

// Owns reports whether this node holds the user's lease.
func (c *Cluster) Owns(userID int64) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.held[userID]
}

// Owner is the node holding the user's lease.
func (c *Cluster) Owner(userID int64) (store.Node, error) {
	n, _, ok, err := c.st.LeaseOwner(userID, c.now())
	if err != nil {
		return store.Node{}, err
	}
	if !ok {
		return store.Node{}, ErrNoOwner
	}
	return n, nil
}

// WaitOwner is Owner, waiting up to max for a user who is between nodes.
func (c *Cluster) WaitOwner(ctx context.Context, userID int64, max time.Duration) (store.Node, error) {
	deadline := time.Now().Add(max)
	for {
		n, err := c.Owner(userID)
		if !errors.Is(err, ErrNoOwner) || time.Now().After(deadline) {
			return n, err
		}
		select {
		case <-ctx.Done():
			return store.Node{}, ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}

// Nodes are the live nodes as of the last tick (always including this one).
func (c *Cluster) Nodes() []store.Node {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.nodes) == 0 {
		return []store.Node{c.self}
	}
	return append([]store.Node(nil), c.nodes...)
}

// Leader reports whether this node should run cluster-wide jobs: the live
// node with the smallest ID.
func (c *Cluster) Leader() bool {
	nodes := c.Nodes()
	min := nodes[0].ID
	for _, n := range nodes {
		if n.ID < min {
			min = n.ID
		}
	}
	return min == c.self.ID
}

// Run ticks until ctx is done, then hands every user off (a graceful drain:
// other nodes pick them up on their next tick).
func (c *Cluster) Run(ctx context.Context) {
	c.Tick()
	t := time.NewTicker(c.heartbeat())
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			c.Drain()
			return
		case <-t.C:
			c.Tick()
		}
	}
}

// Drain stops and releases every user this node holds.
func (c *Cluster) Drain() {
	c.mu.Lock()
	held := c.held
	c.held = map[int64]bool{}
	c.mu.Unlock()
	now := c.now()
	for uid := range held {
		c.hooks.Lose(uid)
		if err := c.st.ReleaseLease(uid, c.self.ID, now); err != nil {
			c.log.Warn("release lease", "user", uid, "err", err)
		}
	}
	if len(held) > 0 {
		c.log.Info("handed off users", "count", len(held))
	}
}

// Tick runs one round of the loop.
func (c *Cluster) Tick() {
	now := c.now()
	if c.self.StartedAt.IsZero() {
		c.self.StartedAt = now // first tick: when other nodes start counting our uptime
	}
	self := c.self
	self.HeartbeatAt = now
	err := c.st.UpsertNode(self)
	var nodes []store.Node
	var users []store.User
	if err == nil {
		nodes, err = c.st.LiveNodes(now.Add(-c.liveFor()))
	}
	if err == nil {
		users, err = c.st.ListUsers()
	}
	if err == nil {
		err = c.st.RenewLeases(self.ID, now.Add(c.ttl), now)
	}
	var stillHeld []int64
	if err == nil {
		stillHeld, err = c.st.HeldLeases(self.ID, now)
	}
	if err != nil {
		c.fence(now, err)
		return
	}

	c.mu.Lock()
	c.lastRenew = now
	c.nodes = nodes
	c.mu.Unlock()

	// Leases lost without our say (clock trouble, manual edits): stop those users.
	lease := map[int64]bool{}
	for _, uid := range stillHeld {
		lease[uid] = true
	}
	for _, uid := range c.heldIDs() {
		if !lease[uid] {
			c.log.Warn("lost a lease", "user", uid)
			c.lose(uid, false)
		}
	}

	active := map[int64]bool{}
	moved := 0
	for _, u := range users {
		if u.Suspended {
			continue
		}
		active[u.ID] = true
		owner := Place(u.ID, nodes)
		mine := c.Owns(u.ID)
		switch {
		case owner.ID == self.ID && !mine:
			ok, err := c.st.AcquireLease(u.ID, self.ID, now.Add(c.ttl), now)
			if err != nil {
				c.log.Warn("acquire lease", "user", u.Username, "err", err)
				continue
			}
			if ok {
				c.mu.Lock()
				c.held[u.ID] = true
				c.mu.Unlock()
				c.hooks.Acquire(u.ID)
			}
		case mine && owner.ID != self.ID && now.Sub(owner.StartedAt) >= c.ttl/2 && moved < moveBatch:
			// Placed on another node that has been up a while: hand off.
			c.lose(u.ID, true)
			moved++
		}
	}
	if moved > 0 {
		c.log.Info("handing users to other nodes", "count", moved)
	}
	if c.Leader() {
		// Forget nodes gone for an hour (old deploys), so the table stays small.
		if err := c.st.DeleteNodesBefore(now.Add(-time.Hour)); err != nil {
			c.log.Warn("drop old nodes", "err", err)
		}
	}
	// Suspended or deleted users: nobody runs them.
	for _, uid := range c.heldIDs() {
		if !active[uid] {
			c.lose(uid, true)
		}
	}
}

// lose stops a user here and, if release, frees the lease for the next owner.
func (c *Cluster) lose(uid int64, release bool) {
	c.mu.Lock()
	delete(c.held, uid)
	c.mu.Unlock()
	c.hooks.Lose(uid) // stop first, so the next owner never overlaps us
	if release {
		if err := c.st.ReleaseLease(uid, c.self.ID, c.now()); err != nil {
			c.log.Warn("release lease", "user", uid, "err", err)
		}
	}
}

// fence stops every user once the database has been unreachable for half a
// lease lifetime: by then our leases may be about to expire, and another node
// must not find us still connected.
func (c *Cluster) fence(now time.Time, err error) {
	c.mu.Lock()
	since := now.Sub(c.lastRenew)
	n := len(c.held)
	c.mu.Unlock()
	c.log.Warn("cluster tick failed", "err", err, "since_last_renew", since.Round(time.Second))
	if since < c.ttl/2 || n == 0 {
		return
	}
	c.log.Error("can't renew leases — stopping all users on this node", "users", n)
	for _, uid := range c.heldIDs() {
		c.lose(uid, false)
	}
}

func (c *Cluster) heldIDs() []int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]int64, 0, len(c.held))
	for uid := range c.held {
		out = append(out, uid)
	}
	return out
}

// Place picks a user's node by rendezvous (highest random weight) hashing:
// deterministic everywhere, and removing a node only moves that node's users.
func Place(userID int64, nodes []store.Node) store.Node {
	var best store.Node
	var bestScore uint64
	key := strconv.FormatInt(userID, 10)
	for _, n := range nodes {
		h := fnv.New64a()
		h.Write([]byte(n.ID))
		h.Write([]byte{0})
		h.Write([]byte(key))
		if s := mix(h.Sum64()); best.ID == "" || s > bestScore {
			best, bestScore = n, s
		}
	}
	return best
}

// mix is the splitmix64 finalizer: FNV alone mixes short keys poorly, which
// skews placement toward some nodes.
func mix(x uint64) uint64 {
	x ^= x >> 30
	x *= 0xbf58476d1ce4e5b9
	x ^= x >> 27
	x *= 0x94d049bb133111eb
	return x ^ x>>31
}
