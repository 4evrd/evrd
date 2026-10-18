# Scaling evrd to several nodes

Status: **built** (phases 1–3 below). The dev stack runs two nodes behind a
load balancer (`make up`); `internal/clustertest` runs two nodes in one test
process and drives every path through the node that doesn't own the user.

## Where things stand

| state | where it lives | shared across nodes? |
|---|---|---|
| accounts, sessions, client passwords, networks | Postgres | yes |
| chat history | ScyllaDB (`history scylla://…`) | yes |
| who owns which user (`nodes`, `user_leases`) | Postgres | yes |
| live IRC connections to networks ("upstreams") | the user's owner node | no — forwarded to |
| attached IRC clients, web chat sockets | the user's owner node | no — forwarded to |
| fan-out (upstream → clients/web), attached-client counts | the user's owner node | no — summed across nodes for admin pages |

## Design: each user is owned by one node; other nodes forward to it

Shard by **user**, not by network. The owner node runs all of a user's
upstream connections and terminates all of their IRC clients and web chat
sockets. Every other node is just a front door that forwards that user's live
traffic to the owner.

Why per user rather than per network:

- Everything that ties a user's connections together stays in one process,
  unchanged: `jump` between networks, the client limit (counted where the
  clients are), suspension kicking everything, the web chat seeing what an IRC
  client sent.
- Fan-out stays in-process. **No message bus is needed** — the only
  cross-node traffic is "forward this connection to the owner".
- A user's footprint is small (a few networks), so users spread evenly.

### Ownership: leases in Postgres

```
nodes        (id, internal_addr, started_at, heartbeat_at)
user_leases  (user_id PK, node_id, epoch, expires_at)
```

- Each node heartbeats its `nodes` row every 5 s. A node is live if it beat
  within 15 s.
- **Placement** is rendezvous hashing of the user ID over live nodes: every
  node computes the same owner without coordination, and adding or removing a
  node moves only ~1/N of users.
- A node acquires a user with a conditional update
  (`… WHERE user_id = ? AND (expires_at < now() OR node_id = me)`), renews its
  leases in one batched statement per heartbeat, and lease TTL is 20 s.
- **Fencing:** a node that can't renew (lost Postgres) stops its users'
  connections at TTL/2, before anyone else may take over — so two nodes never
  hold the same nick on a network. `epoch` increments on each takeover. Forwarded
  work is only done by the current owner: a node that has let a user go
  answers a forwarded request with "moving — try again in a moment" (HTTP 421,
  or 464 to an IRC client), and the client's retry lands on the new owner.

### Routing

- **IRC clients** connect to any node (plain L4/TCP load balancer). The node
  answers CAP and reads registration (PASS, NICK, USER), looks up the owner,
  and if it isn't the owner it connects to the owner's internal IRC port,
  sends `EVRD-PROXY <secret> <client ip> <caps>`, replays PASS/NICK/USER and
  any bytes the client already sent, and splices the two sockets. The owner
  authenticates the client as usual. IRC has no redirect, so proxying is the
  only option; it costs one in-datacenter hop.
- **Web app** requests hit any node (L7 load balancer, sessions are in
  Postgres). A signed-in user's API requests are handled by their owner —
  reverse-proxied to its internal HTTP port when they land elsewhere — since
  almost every page shows live state (link status, unread counts, the chat
  WebSocket, `*status` from the web). Signing in, `/api/config` and the admin
  pages about everyone are served wherever they land.
- **Admin effects** on another user (`/api/admin/users/{id}…`: suspend,
  delete, password reset, limits, sign-outs) are forwarded to that user's
  owner like any other request about the user, so live connections react
  immediately. Admin pages about everyone (overview, user list) are served
  wherever they land and add up each node's live numbers from its
  `/internal/usage`.
- Changes made straight in the database (evrctl) still take effect: the
  ownership loop treats suspended and deleted users as owned by nobody, so
  their owner stops them within a heartbeat.
- A user nobody owns (suspended, or between owners) has no live state, so
  their requests are served from the database wherever they land.

The internal ports (IRC + HTTP) are on a private network, authenticated with
mTLS or a shared secret, and never exposed.

### Background work

- Backlog pruning: each node prunes its own users' networks. This also suits
  the ScyllaDB backend's dirty-buffer tracking, since writes for a network
  only ever come from its owner.
- Cluster-wide jobs (dropping orphaned history, expired sessions, leases of
  deleted users) run on the leader: the live node with the smallest ID. No
  lock is needed — the jobs are idempotent, so two leaders for a moment is
  harmless.

### Failure and deploys

- **Node dies:** its leases expire after ≤20 s, the next node in each user's
  rendezvous order takes over and reconnects their networks; their IRC clients
  reconnect through any node and land on the new owner. Measured on the dev
  stack (`docker kill` of a node): a user's network was connected again on
  the other node 19 s later. Users see a disconnect/reconnect on their
  networks — unavoidable, since a TCP connection to an IRC server can't move
  between machines.
- **Rolling deploy:** on SIGTERM a node drains — it stops its users and
  releases their leases — and the others pick them up on their next tick
  (≤5 s) instead of waiting for leases to expire. (In the dev stack, air kills
  the process without SIGTERM, so a reload there waits for expiry.)
- **Load balancer:** give it a short connect timeout and passive health
  checks (see `deploy/lb.conf`), or it keeps trying a dead node for a minute.
- **Postgres down:** nodes keep running what they own until their leases
  can't be renewed, then fence. Prefer a short Postgres outage to split-brain.

### Things to watch

- **Connection limits per source IP.** Every user on a node connects to, say,
  Libera from that node's address. Large networks throttle and cap connections
  per IP; ZNC hosts get exemptions or spread across addresses. Plan per-node
  outbound addresses (IPv6 helps: one per user), and pace reconnects after a
  failover.
- History writes are asynchronous: a line can reach a live client a few
  milliseconds before it's readable from ScyllaDB. The web chat already
  merges fetched history with live lines for this reason.

## Alternatives considered

- **Shard by network** with a message bus (NATS) between nodes: more even
  placement for users with many networks, but every user-wide feature becomes
  a distributed one (jump, limits, suspension, web chat) and a bus becomes
  another dependency on the hot path. Not worth it at the scale of a bouncer
  user's footprint.
- **Sticky load balancing** (route by username at the load balancer): IRC
  clients only reveal the username in `PASS`, after the connection is
  established, so an L4 balancer can't route on it; it would need an
  IRC-aware proxy anyway.

## What was built

1. **Node identity and leases** (`internal/cluster`, `nodes`/`user_leases`):
   rendezvous placement, lease acquire/renew/release, fencing, gradual
   rebalancing (≤50 users per tick, once a new node has been up 10 s).
2. **Forwarding** (`internal/api/cluster.go`, `internal/client/forward.go`):
   the internal HTTP and IRC endpoints, web requests and the chat WebSocket
   reverse-proxied to the owner, IRC clients spliced to it.
3. **Owner-side effects and cluster jobs:** admin actions run on the owner;
   evrctl changes picked up by the loop; admin totals summed across nodes;
   leader-only cluster jobs; drain on shutdown.

4. **Reconnect pacing and source addresses** (`internal/outbound`): connects
   to each IRC server host are spaced to `connect_rate` per second per node
   (default 5), and reconnect backoff is jittered, so a takeover or a
   netsplit recovery reconnects users gradually. Each node can spread users
   over a list of source addresses (`upstream_bind`) or give every user their
   own IPv6 address from a routed prefix (`upstream_prefix6`); either way a
   user always connects from the same address.

## Operating choices

- **Failover time** is the lease length, `cluster_lease` (default 20s, the
  same on every node). Heartbeats are a quarter of it, so shorter leases mean
  proportionally more Postgres writes: at 8s, each node renews its leases
  every 2s.
- **Load balancer:** anything with TCP passthrough for IRC and HTTP with
  WebSocket upgrades, plus health checks so a dead node leaves the rotation.
  Two tested configs: nginx (`deploy/lb.conf`, used by `make up`) and Traefik
  (`deploy/traefik/`). Traefik needs a release with TCP health checks — 3.7
  works; with 3.5, half of IRC connections kept going to a dead node.
- **Outbound addresses:** the per-address connection caps of large networks
  are the first real limit. Give each node its own addresses, or better an
  IPv6 /64 routed to it (`ip -6 route add local <prefix> dev lo`) so every
  user gets a stable address of their own, and talk to the networks you
  care about (Libera, OFTC) about limits for your ranges.
