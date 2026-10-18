# Developing evrd

How the code is laid out, the dev stack, tests, CI and releases. For running evrd, see the [README](../README.md).

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
    first; writes are queued (up to 16k lines) so a slow database doesn't stall IRC.
  - `pebble:/var/lib/evrd/history` — an embedded
    [Pebble](https://github.com/cockroachdb/pebble) store on local disk, for
    single-node installs (one evrd process per directory).

  `evrctl history copy <from> <to>` copies history between them.
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

## The dev stack

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

The upstream `ircd` ([ergo](https://github.com/ergochat/ergo), a stand-in for the
production `ngircd-27`) is internal-only — `evrd` reaches it as `ircd:6667` over
the compose network, so it never collides with a host port.

`make up-prod` builds the production images and runs them as a two-node
cluster demo with the same test ircd (`docker-compose.yml`; web on :8080). To
deploy evrd for real, use [`deploy/docker/`](../deploy/docker) — see the
[README](../README.md).

The README screenshots come from `SHOTS=1 npx playwright test zz-shots`
(in `web/`) against the dev stack.

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
from the production image (`docker-compose.e2e.yml`) instead of air. On
`main` it then releases (see below) and publishes the images. Pushes to
`main` and `v*` tags publish multi-arch images (cross-compiled, no emulated
Go builds) to `ghcr.io/4evrd/evrd` and
`ghcr.io/4evrd/evrd-web`.

### Releases and commit messages

Releases are cut by [semantic-release](https://semantic-release.gitbook.io)
from [Conventional Commits](https://www.conventionalcommits.org). On every push
to `main`, once CI passes, it looks at the commits since the last tag:

| Commit | Release |
|---|---|
| `fix: …`, `perf: …` | patch (0.1.0 → 0.1.1) |
| `feat: …` | minor (0.1.0 → 0.2.0) |
| `feat!: …` or a `BREAKING CHANGE:` footer | major |
| `docs:`, `test:`, `ci:`, `chore:`, `refactor:`, `build:` | none |

It tags `vX.Y.Z`, publishes a GitHub release with notes grouped by type, and
the images are pushed as `ghcr.io/4evrd/evrd:X.Y.Z` and `:X.Y` (plus `latest`
and the commit sha, as on every push to `main`); `evrd -version` reports it.
Config: `.releaserc.json`. Pull requests have their commit messages checked
with commitlint (`commitlint.config.mjs`). Versions started from a `v0.0.0`
baseline, so evrd stays 0.x until a breaking change says otherwise.

### Without Docker

```sh
make build       # builds bin/evrd and bin/evrctl
make web         # builds web/dist
./bin/evrd -version
cd web && pnpm dev
```

## The website

https://4evrd.github.io lives in its own repository,
[4evrd/4evrd.github.io](https://github.com/4evrd/4evrd.github.io) (an
organization Pages site, so it's served from the root). Its docs and install
pages mirror this README and `docs/`: update both when behaviour changes.
