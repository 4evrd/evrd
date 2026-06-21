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

- `store` — SQLite persistence (users, networks, buffered history)
- `irc` — protocol message parse/encode
- `upstream` — one live IRC connection per network: dial, register, autojoin,
  reconnect, parse + persist + fan out messages, send
- `bouncer` — owns the store and the set of running upstream connections
- `api` — HTTP + WebSocket admin/client API consumed by the web app

## Develop

The whole stack — an upstream IRC server, `evrd`, and the web client — runs with
Docker Compose, with **hot reload** on both services (air rebuilds the daemon on
`.go` changes; vite HMR reloads the web client on source changes):

```sh
make up          # dev stack with hot reload
make down        # stop it
```

- web client → http://localhost:5173
- evrd admin/client API → http://localhost:8098
- evrd IRC (native client attach, WIP) → localhost:6697

Create an account, then sign in on the web client and add a network:

```sh
docker compose -f docker-compose.dev.yml exec evrd \
  go run ./cmd/evrctl -db sqlite:/var/lib/evrd/evrd.db user add <name> <password>
```

In the dev stack, point a network at the internal upstream: host `ircd`, port
`6667`, TLS off.

The upstream `ircd` ([ergo](https://github.com/ergochat/ergo), a stand-in for the
production `ngircd-27`) is internal-only — `evrd` reaches it as `ircd:6667` over
the compose network, so it never collides with a host port.

To run the built images instead (what CI publishes; web served by nginx on
:8080):

```sh
make up-prod
```

### End-to-end tests

Playwright specs in `web/e2e` drive the UI against the real daemon (login, add
+ connect a network, send a message in the console):

```sh
make e2e-setup   # one-time: install web deps + the Playwright browser
make e2e         # brings up the dev stack and runs the specs
```

### Without Docker

```sh
make build       # builds bin/evrd and bin/evrctl
make web         # builds web/dist
./bin/evrd -version
cd web && pnpm dev
```

## Status

Working MVP. You can create an account, add a network, and it really connects:
the daemon dials the upstream, registers, autojoins channels, stays connected
(reconnecting with backoff), buffers history, and the web console streams live
messages both ways.

Next: native IRC-client attach (so Hexchat/irssi can connect to evrd on 6697),
SASL, and per-channel presence/nick lists.
