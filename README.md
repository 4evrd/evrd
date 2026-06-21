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
Docker Compose:

```sh
make up          # docker compose up --build
```

- web client → http://localhost:8080
- evrd WebSocket → ws://localhost:8097
- evrd IRC → localhost:6697
- upstream ircd → localhost:6667

The upstream uses [ergo](https://github.com/ergochat/ergo) locally as a stand-in
for the production `ngircd-27`.

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
