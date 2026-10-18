# evrd

The IRCForever IRC bouncer — stay connected, forever.

`evrd` keeps your IRC connection alive while you're away, buffers what you miss,
and lets clients (native or web) attach and detach at will.

## Layout

This is a monorepo with three parts:

| Part      | Path                | What it is                                                       |
| --------- | ------------------- | ---------------------------------------------------------------- |
| **core**  | `cmd/evrd`, `internal/` | The bouncer daemon and its engine (IRC, sessions, storage). |
| **cli**   | `cmd/evrctl`        | Command-line admin for a running daemon.                         |
| **spa**   | `web/`              | The Svelte web client (Vite + Svelte 5 + Tailwind).             |

`cmd/evrd` and `cmd/evrctl` are thin entrypoints; the real logic lives in
`internal/`:

- `store` — the account database (users, sessions, IRC client passwords,
  networks) via GORM behind a `Store` interface. Postgres in deployments
  (`db postgres://user:pass@host/evrd`); SQLite for tests and single-file
  installs — chosen by the DSN, no code change.
- `history` — chat history (messages and channel events), never in Postgres.
  Two backends behind one interface, picked by the `history` directive:
  - `scylla://host1,host2:9042/evrd` — **ScyllaDB** (Cassandra-compatible),
    shared by every evrd node. One partition per buffer per month, newest
    first; writes are queued so a slow database never stalls IRC.
  - `pebble:/var/lib/evrd/history` — an embedded
    [Pebble](https://github.com/cockroachdb/pebble) store on local disk, for
    single-node installs (one evrd process per directory).

  `evrctl history copy <from> <to>` moves history between them.
- `accounts` — user rules shared by the API, the IRC listener and evrctl:
  username/password policy, limits, IRC client passwords, last-admin guards
- `auth` — web sign-in: database-backed cookie sessions, local passwords, and
  OpenID Connect (Logto) with accounts created on first SSO sign-in
- `irc` — protocol message parse/encode
- `upstream` — one live IRC connection per network: dial, register, autojoin,
  reconnect, parse + persist + fan out messages, send
- `bouncer` — owns the store and the set of running upstream connections
- `api` — HTTP + WebSocket API consumed by the web app: the signed-in user's
  account and networks, plus `/api/admin/*` user management
- `client` — downstream IRC listener: native clients (Hexchat, irssi, …)
  attach with a server password, get backlog replay, and a full-fidelity live
  feed bridged to/from the upstream

## Develop

The whole stack — an upstream IRC server, `evrd`, and the web client — runs with
Docker Compose, with **hot reload** on both services (air rebuilds the daemon on
`.go` changes; vite HMR reloads the web client on source changes):

```sh
make up          # dev stack with hot reload
make down        # stop it
```

- web app → http://localhost:5173 (vite proxies `/api` and `/auth` to evrd)
- evrd API → http://localhost:8098
- evrd IRC (native client attach) → localhost:6697 (TLS, self-signed) / 6667
- Postgres → internal only (`postgres:5432`) — accounts and settings
- ScyllaDB → internal only (`scylla:9042`) — chat history
- VictoriaMetrics → internal only (`victoriametrics:8428`) — metrics over OTLP

Create an admin, then sign in on the web app and add a network:

```sh
docker compose -f docker-compose.dev.yml exec evrd \
  go run ./cmd/evrctl -config deploy/evrd.dev.conf user add -admin <name> <password>
```

In the dev stack, point a network at the internal upstream: host `ircd`, port
`6667`, TLS off.

### Attaching a native IRC client

Point any IRC client at the bouncer and authenticate with a **server password**
of `<user>:<password>` (or `<user>/<network>:<password>` to pick a network).
The password is the account password or an **IRC client password** made on
the web app's *IRC clients* page — the only option for accounts that sign in
with SSO, and revocable one client at a time:

```
server:   localhost
port:     6697 (TLS, self-signed in dev — enable "accept invalid cert")
          6667 (plaintext)
password: bncuser:bncpass
```

On connect you get the welcome burst, your channels (with names/topic), backlog
replay, and live traffic. `/join` and `/part` update the network's saved
channel list, `/nick` changes your nick upstream, and your own notices and
`/me` actions are saved and shown on your other clients like messages. evrd
offers the `echo-message` and `server-time` capabilities.

Channel events — joins, parts, quits, kicks, nick changes, topics and modes —
are saved with the messages. On replay they come from `*buffextras` as plain
text (`bob is now known as robert`), like ZNC's buffextras module, so they
never confuse a client's nick list.

The web chat understands `/join`, `/part`, `/msg`, `/query`, `/me`, `/notice`,
`/nick`, `/topic`, `/mode`, `/kick`, `/close`, `/quote` and `/help`, and runs
`*status` commands too (`/msg *status help`, or `/status help`).

`/msg *status help` lists the bouncer's own commands (ZNC's names work too):

| command | does |
|---|---|
| `networks` | your networks, their status, and which one this client is on |
| `jump <network>` | switch this client to another network |
| `connect [network]` / `disconnect [network]` | bring a network up or down (default: this one) |
| `addnetwork <name> <host> [[+]port] [nick]` | add a network; `+port` is TLS (default `+6697`) |
| `delnetwork <network>` | remove a network and its history |
| `show [network]` | a network's settings (default: this one) |
| `set [network] <setting> <value>` | change `name`, `host`, `port` (`+port` = TLS), `tls on\|off`, `nick`, `ident`, `realname` or `password` (`-` clears); reconnects when needed |
| `channels` | this network's saved channels |
| `playback <channel\|nick> [lines]` | replay a buffer (default 50, up to 500) |
| `info` / `version` | your account and limits / the evrd version | Anything you send shows up in the web console and on
your other attached clients too — all sessions share one upstream connection.

For a real TLS cert, set `tls_cert` / `tls_key` in the config. Upstream networks
authenticate with **SASL PLAIN** when the network has a password and the server
supports it (falling back to a server `PASS`).

The upstream `ircd` ([ergo](https://github.com/ergochat/ergo), a stand-in for the
production `ngircd-27`) is internal-only — `evrd` reaches it as `ircd:6667` over
the compose network, so it never collides with a host port.

To run the built images instead (what CI publishes; web served by nginx on
:8080):

```sh
make up-prod
```

### ScyllaDB tests

`dev/ci-scylla.sh` starts a ScyllaDB in Docker and runs the history and
cluster tests against it (CI runs it too). Against an existing one:
`EVRD_TEST_SCYLLA=host:9042 go test ./internal/history`.

### End-to-end tests

Playwright specs in `web/e2e` drive every page against the real stack: sign-in
and forced password changes, networks, chat (unread counts, mentions, members,
topics, commands, `*status`), IRC clients (attaching a native client with a
client password, backlog playback, web ↔ client sync, limits), the account
page, admin users and the bouncer overview, the network editor, real
connections to the dev ergo (SASL against a registered account, its self-signed
TLS refused then trusted by fingerprint, trust-all, failover across servers,
detach), and the traffic chart read back
from VictoriaMetrics. `e2e/irc.js` is a raw IRC client that attaches to evrd
like irssi would, so tests can play other people in a channel. Each test makes
its own throwaway account.

```sh
make e2e-setup   # one-time: install web deps + the Playwright browser
make e2e         # brings up the dev stack and runs the specs
```

### CI

GitHub Actions (`.github/workflows/ci.yml`) runs on every push and pull
request: gofmt, vet, build and Go tests; the ScyllaDB suite; svelte-check and
the web build; and the Playwright suite against the full dev stack, with evrd
from the production image (`docker-compose.e2e.yml`) instead of air. Pushes to
`main` and `v*` tags publish multi-arch images (cross-compiled, no emulated
Go builds) to `ghcr.io/4evrd/evrd` and
`ghcr.io/4evrd/evrd-web`.

### Without Docker

```sh
make build       # builds bin/evrd and bin/evrctl
make web         # builds web/dist
./bin/evrd -version
cd web && pnpm dev
```

## Running several nodes

evrd scales out: run any number of nodes against the same Postgres and
ScyllaDB, behind a plain round-robin load balancer for IRC (TCP passthrough)
and the API (HTTP + WebSocket). Each user is owned by one node, which runs
their network connections and serves their clients; any other node forwards
that user's IRC connections and web requests to it. If a node dies, its
users move to the others in about 20 seconds; on shutdown a node hands them
over at once. Design and trade-offs: [docs/scaling.md](docs/scaling.md).

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

`make up` runs two nodes (`evrd`, `evrd2`) behind nginx (`deploy/lb.conf`);
`deploy/traefik/` is a tested Traefik equivalent (needs Traefik ≥ 3.7 for TCP
health checks). A lone node without `internal_listen` still works and owns
every user. `cluster_lease 20s` sets how long a dead node's users wait to
move — the same on every node.

### Connections to IRC networks

Large networks throttle and cap connections per source address, and every
user on a node would otherwise share the node's address:

```
connect_rate     5                    # most connects/second to one server, per node
upstream_bind    192.0.2.10 192.0.2.11  # spread users over these (stable per user)
upstream_prefix6 2001:db8:42::/64     # or: an IPv6 address of their own for every user
```

`upstream_bind` / `upstream_prefix6` are per node (`EVRD_UPSTREAM_BIND`,
`EVRD_UPSTREAM_PREFIX6`). A prefix needs Linux and a local route for it:
`ip -6 route add local 2001:db8:42::/64 dev lo`.

**Bind hosts** (ZNC's BindHost): `bindhost 192.0.2.20 vhost.example.org`
(repeatable) lists local addresses users may pick to connect from, for their
whole account or per network. Admins may use any address and can lock a
user's choice (DenySetBindHost).

### Network settings (ZNC-style)

Each network has its own page in the web app (`/networks/<id>`), with what
ZNC offers without modules:

- **Servers** — several, each with host, port, TLS and an optional server
  password (PASS). evrd connects to the first and moves on to the next each
  time the link drops.
- **Identity** — nick, alternate nick, ident, real name, quit message, bind host
  (empty fields use the account defaults).
- **Authentication** — SASL PLAIN account and password (sent as PASS if the
  network has no SASL).
- **Certificates** — verify with the system CAs (TrustPKI), trust everything
  (TrustAllCerts), and trusted SHA-256 fingerprints. A refused certificate's
  fingerprint is shown with a button to trust it.
- **Channels** — key, detached (joined and logged, hidden from IRC clients
  until they `/join`; `/detach #chan` from a client), join-on-connect.
- **Advanced** — encoding for non-UTF-8 networks (`^charset`: only decode
  invalid UTF-8; `charset`: decode and send in it), flood protection (lines
  per second and burst), join delay.

Per-user settings (Account → Bouncer): default ident, quit message and bind
host; playback lines and whether channels / private messages only play back
what arrived since you were last attached (AutoClearChanBuffer /
AutoClearQueryBuffer); timestamps prepended/appended for clients without
server-time (strftime format, time zone); fixed CTCP replies (an empty reply
blocks the request; with no client attached evrd answers VERSION, PING and
TIME itself); and the addresses allowed to sign in, web and IRC.

`*status` has the same: `listservers`, `addserver`, `delserver`,
`listfingerprints`, `addfingerprint`, `delfingerprint` (and ZNC's
`AddTrustedServerFingerprint` names), `detach`, and `set` for altnick,
sasluser, bindhost, quitmsg, encoding, trustallcerts, trustpki, floodrate,
floodburst, joindelay.

### Saved passwords

Network passwords (SASL/NickServ and server passwords) are encrypted in the
database with AES-256-GCM under a key that lives outside it:

```
secret_key      <32 bytes, base64>   # or EVRD_SECRET_KEY, or secret_key_file /path
secret_key_old  <previous key>       # optional: still decrypts while rotating
```

`evrctl genkey` makes a key. Every node needs the same one; keep it out of
database backups. At startup evrd encrypts anything saved before a key was
set and re-encrypts anything under an old key, so rotating is: set the new
`secret_key`, move the old one to `secret_key_old`, restart, then drop it.
Without a key evrd still runs and warns that these are stored in plaintext.
Account passwords and client passwords are bcrypt hashes either way.

## Accounts and sign-in

Every user has an evrd account with a role (`user` or `admin`), per-user
limits, and optional suspension. Admins manage accounts on the web app's
*Users* page; `evrctl user …` does the same from a shell (bootstrap the first
admin with `evrctl user add -admin`).

**Signing in to the web app.** Sessions are stored in the database behind an
HttpOnly cookie, so they survive restarts; users can see and revoke them on
their *Account* page. Two methods, either or both:

- **Password** — `password_login yes` (default).
- **SSO via OpenID Connect** — Logto, or any OIDC provider. SAML, social and
  enterprise logins are connectors in the provider; evrd only speaks OIDC.
  Accounts are created on first sign-in and linked by the token's subject.

```
public_url        https://bnc.example.org   # callback: <public_url>/auth/callback
oidc_issuer       https://id.example.org/oidc
oidc_client_id    <app id>
# secret: oidc_client_secret, or the EVRD_OIDC_CLIENT_SECRET env var
oidc_label        IRCforever account        # text on the sign-in button
oidc_auto_create  yes   # create accounts on first sign-in
oidc_link_existing no   # link to an existing local account with the same username
oidc_admin_role   evrd-admin  # optional: provider role that makes an evrd admin
```

**Limits** (server defaults; admins override per user, 0 = unlimited):
`max_networks 0` (unlimited by default), `max_clients 5` (attached IRC clients at once),
`backlog_lines 2000` (kept per channel, pruned every 10 minutes).

**Suspending** a user signs them out everywhere, drops their IRC clients and
disconnects their networks; lifting it reconnects them. The last active admin
can't be demoted, suspended or deleted.

Passwords need at least 10 characters. A password an admin sets — or the
one-time password **Reset** generates on the Users page — is temporary: the
user picks their own at next sign-in.

Other directives: `session_ttl 720h`, `cookie_secure yes` (turn off only for
plain-HTTP dev), `irc_host` (the hostname shown to users for attaching).

## Metrics

evrd exports OpenTelemetry metrics over OTLP/HTTP and reads them back from
VictoriaMetrics for the dashboard's traffic chart and health numbers:

```
otel_endpoint http://victoriametrics:8428/opentelemetry/v1/metrics
otel_interval 15s
metrics_url   http://victoriametrics:8428   # PromQL API the dashboard queries
```

(`OTEL_EXPORTER_OTLP_METRICS_ENDPOINT` and `EVRD_METRICS_URL` work too.) Run
VictoriaMetrics with `-opentelemetry.usePrometheusNaming` so names come out as
`evrd_irc_lines_total{user,network,direction}`, `evrd_upstream_up`,
`evrd_upstream_lag_seconds`, `evrd_upstream_disconnects_total`,
`evrd_irc_clients`, `evrd_web_streams`, `evrd_cluster_users`,
`evrd_history_queue` and friends (`user`/`network` are ids). Without
`metrics_url` the dashboard still works; the chart says metrics are off.

## Status

Working bouncer. Create an account, add a network, and it really connects: the
daemon dials the upstream, registers, autojoins, stays connected (reconnecting
with backoff), and buffers history. Two ways to use it, sharing one upstream:

- **Web client** — login, manage networks, live console.
- **Native IRC client** — attach on 6697 (TLS) or 6667 with a server password;
  get backlog + a full-fidelity live feed (names, joins, topics, everyone's
  messages). Multiple clients + the web stay in sync over one upstream.

Upstreams authenticate via SASL PLAIN (or server PASS). The IRC listener does
TLS (self-signed in dev, or your own cert).
