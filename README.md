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
`internal/` (`config`, `irc`, `bouncer`, `server`) so both binaries share it.

## Develop

The whole stack — an upstream IRC server, `evrd`, and the web client — runs with
Docker Compose, with **hot reload** on both services (air rebuilds the daemon on
`.go` changes; vite HMR reloads the web client on source changes):

```sh
make up          # dev stack with hot reload
make down        # stop it
```

- web client → http://localhost:5173
- evrd WebSocket → ws://localhost:8097
- evrd IRC → localhost:6697

The upstream `ircd` ([ergo](https://github.com/ergochat/ergo), a stand-in for the
production `ngircd-27`) is internal-only — `evrd` reaches it as `ircd:6667` over
the compose network, so it never collides with a host port.

To run the built images instead (what CI publishes; web served by nginx on
:8080):

```sh
make up-prod
```

### Without Docker

```sh
make build       # builds bin/evrd and bin/evrctl
make web         # builds web/dist
./bin/evrd -version
cd web && pnpm dev
```

## Status

Early scaffold. The daemon, CLI, and web client build and run; the bouncer
engine (upstream dialing, buffers, client attach, persistence) is being filled
in on top of this skeleton.
