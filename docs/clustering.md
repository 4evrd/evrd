# Running several nodes

evrd scales out: run any number of nodes against the same Postgres and
ScyllaDB, behind a plain round-robin load balancer for IRC (TCP passthrough)
and the API (HTTP + WebSocket). Each user is owned by one node, which runs
their network connections and serves their clients; any other node forwards
that user's IRC connections and web requests to it. If a node dies, its
users move to the others in about 20 seconds; on shutdown a node hands them
over at once. Design and trade-offs: [scaling.md](scaling.md).

Every node runs the same config, plus its own identity:

```
internal_listen      :8099   # internal HTTP — reachable between nodes only
internal_irc_listen  :6699   # internal IRC — reachable between nodes only
history              scylla://db1,db2/evrd   # history must be shared
```

and in its environment (or as `node_id`, `internal_url`, `internal_irc_addr`,
`cluster_secret` directives):

```
EVRD_NODE_ID=node-a
EVRD_INTERNAL_URL=http://evrd-a.internal:8099
EVRD_INTERNAL_IRC_ADDR=evrd-a.internal:6699
EVRD_CLUSTER_SECRET=<the same long random secret on every node>
```

The dev stack (`make up`) runs two nodes (`evrd`, `evrd2`) behind nginx (`deploy/lb.conf`);
`deploy/traefik/` is a tested Traefik equivalent (needs Traefik ≥ 3.7 for TCP
health checks). A lone node without `internal_listen` still works and owns
every user. `cluster_lease 20s` sets how long a dead node's users wait to
move — the same on every node.

## Source addresses and connect pacing

Large networks throttle and cap connections per source address, and every
user on a node would otherwise share the node's address:

```
connect_rate     5                    # most connects/second to one server, per node
upstream_bind    192.0.2.10 192.0.2.11  # spread users over these (stable per user)
upstream_prefix6 2001:db8:42::/64     # each user's own IPv6 address (IPv6 servers)
```

`upstream_bind` / `upstream_prefix6` are per node (`EVRD_UPSTREAM_BIND`,
`EVRD_UPSTREAM_PREFIX6`). A prefix needs Linux and a local route for it:
`ip -6 route add local 2001:db8:42::/64 dev lo`.

**Bind hosts** (ZNC's BindHost): `bindhost 192.0.2.20 vhost.example.org`
(repeatable) lists local addresses users may pick to connect from, for their
whole account or per network. On Linux an IPv6 bind host may come from a routed prefix
with only a local route for it, like `upstream_prefix6`'s addresses: evrd
binds it with `IPV6_FREEBIND`, so nothing has to be added to an interface
per address. Admins may use any address and can lock a
user's choice (DenySetBindHost).

In a cluster, keep `settings_write_conf` off: settings changed in the web app live in the database, which every node reads (see [configuration](configuration.md#server-settings-admin--settings)).
