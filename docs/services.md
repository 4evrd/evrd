# Logging in to services (NickServ, SASL, X, Q)

Most networks keep accounts in *services*: NickServ on Atheme and Anope
networks (Libera.Chat, Rizon…), on DALnet a NickServ at
`NickServ@services.dal.net`, X on Undernet, Q on QuakeNet. evrd logs in to
yours every time it connects. Set it up on the network's page, under
**Authentication**, or with `*status set` (below).

## Methods

| Method | What evrd does |
|---|---|
| **Automatic** (`auto`, the default) | SASL PLAIN while connecting when the network offers it. Without SASL — or when SASL is refused — it sends the password as `PASS` and, once connected, `IDENTIFY`s to NickServ. Networks saved before there was a choice use this. |
| **SASL only** (`sasl`) | SASL PLAIN, nothing else. If the network has no SASL or refuses the login, the connection log says so and evrd stays logged out. |
| **IDENTIFY to NickServ** (`nickserv`) | No SASL: once connected, `PRIVMSG NickServ :IDENTIFY …` to the services nick. |
| **A command of my own** (`command`) | Once connected, sends your line: raw IRC or `/msg target text`, with `%account%`, `%password%` and `%nick%` filled in. For services that aren't NickServ, like Undernet's X. |
| **Don't log in** (`none`) | Nothing — or the [q module](modules.md) logs in to QuakeNet's Q. |

SASL EXTERNAL (logging in with a client certificate) isn't offered: evrd
doesn't present client certificates to networks.

The **account name** is your services account (empty: your ident for SASL,
your nick for NickServ). The **password** is stored encrypted and never
sent back to the browser.

## Options

- **Services nick** — where `IDENTIFY` goes: `NickServ` (empty), or
  `NickServ@services.dal.net` on DALnet. `/NS` (or `/NICKSERV`) sends the
  server's own command instead (`NS IDENTIFY …`), for networks that only
  accept that.
- **IDENTIFY sends** — `account`: `IDENTIFY account password` (Atheme,
  Anope 2); `password`: just `IDENTIFY password`, for older services that
  identify you to the nick you're on; empty: the account only when it
  isn't the nick in use (on your alternate nick, say).
- **Identify** — as soon as evrd is connected, or when NickServ asks
  ("This nickname is registered…"). When it never asks, evrd identifies
  after 10 seconds anyway.
- **When the nick is taken** — evrd connects on the alternate nick, logs
  in, then asks services to `GHOST`, `REGAIN` or `RECOVER` the nick (with
  the password) and switches to it. With *IDENTIFY sends: just the
  password* it frees the nick first and identifies once it's back.
- **Join channels after identifying** — holds the channel joins until
  services confirm the login, so channels only logged-in users may join
  (`+r`, `+R`) let evrd in. If services don't answer within **Wait for
  services** seconds (default 15) evrd joins anyway.

## Presets

The *Network services* menu fills the options in:

| Preset | Method | Services nick | IDENTIFY sends | Nick taken |
|---|---|---|---|---|
| Automatic | auto | NickServ | — | — |
| SASL only (Libera.Chat, ergo…) | sasl | — | — | — |
| NickServ — Atheme or Anope | nickserv | NickServ | account and password | REGAIN |
| DALnet | nickserv | `NickServ@services.dal.net` | just the password | GHOST |
| Undernet (X) | command: `PRIVMSG X@channels.undernet.org :LOGIN %account% %password%` | — | — | — |
| QuakeNet (Q) | none, and the q module on | — | — | — |
| Custom | whatever you set | | | |

## Did it work?

The network's connection log (on its page) says what evrd did and what
services answered: *Identified to services: You are now identified for
alice*, or *Services refused the login: Invalid password for alice* with
what to do about it. evrd reads the usual answers of Atheme, Anope, DALnet,
Undernet's X, QuakeNet's Q and ergo, and the `900` numeric
(`RPL_LOGGEDIN`); SASL's `903`/`904`/`905`. No answer within the wait is
logged too, as is a services nick that doesn't exist (wrong services nick
or method) and a channel that turned you away for not being logged in
(`477`). The network's status in the API carries the outcome as `auth`:
`pending`, `identified`, `failed` or `unconfirmed`.

What evrd sends is never logged, and the password is blanked out of
anything services repeat.

## From an IRC client

```
/msg *status set auth nickserv
/msg *status set servicesnick NickServ@services.dal.net
/msg *status set identifyargs password       (account, password or auto)
/msg *status set identifyon prompt           (connect or prompt)
/msg *status set ghost ghost                 (ghost, regain, recover or off)
/msg *status set joinafterauth on
/msg *status set authtimeout 30
/msg *status set authcommand PRIVMSG X@channels.undernet.org :LOGIN %account% %password%
/msg *status set auth command                (set the command first)
/msg *status set sasluser alice
/msg *status set password …
```

`*status show` lists them. In the API (and provisioning) they're
`authMethod`, `servicesNick`, `identifyArgs`, `identifyOn`, `ghost`,
`authCommand`, `joinAfterAuth` and `authTimeout`, next to `saslUser` and
`password`.
