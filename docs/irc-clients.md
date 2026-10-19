# Using evrd from an IRC client

Point any IRC client at the bouncer and authenticate with a **server password**
of `<user>:<password>` (or `<user>/<network>:<password>` to pick a network).
The password is the account password or an **IRC client password** made on
the web app's *IRC clients* page — the only option for accounts that sign in
with SSO, and revocable one client at a time:

```
server:   bnc.example.org         (your irc_host)
port:     6697 (TLS)              — accept the certificate if evrd made a self-signed one
password: mira/libera:<password>  (user/network:password)
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
| `addnetwork <id>` / `directory [search]` | add a network from the [network directory](network-directory.md) by its id (`addnetwork libera`); list or search it |
| `delnetwork <network>` | remove a network and its history |
| `show [network]` | a network's settings (default: this one) |
| `set [network] <setting> <value>` | change `name`, `host`, `port` (`+port` = TLS), `tls`, `nick`, `altnick`, `ident`, `realname`, `password`, `sasluser`, `auth`, `servicesnick`, `identifyargs`, `identifyon`, `authcommand`, `ghost`, `joinafterauth`, `authtimeout` ([services login](services.md)), `bindhost`, `quitmsg`, `encoding`, `trustallcerts`, `trustpki`, `floodrate`, `floodburst`, `joindelay` (`-` clears); reconnects when needed |
| `listservers` / `addserver <host> [[+]port] [pass]` / `delserver <host> [port]` | the network's servers, tried in turn |
| `listfingerprints` / `addfingerprint <sha-256>` / `delfingerprint <sha-256>` | trusted server certificates |
| `detach <#chan>` | hide a channel from IRC clients (still joined and logged); `/join` brings it back |
| `channels` | this network's saved channels |
| `playback <channel\|nick> [lines]` | replay a buffer (default 50, up to 500) |
| `info` / `version` | your account and limits / the evrd version |

Anything you send shows up in the web console and on
your other attached clients too — all sessions share one upstream connection.

Upstream networks authenticate with **SASL PLAIN** when the network has a
password and the server supports it, else by identifying to NickServ once
connected — or however the network's Authentication settings say: see
[Logging in to services](services.md).

Playback for clients without `server-time` carries timestamps in the text
(`[12:34:56] hello`); change the format, time zone and how much is played back
under *Account → Bouncer*.

## Troubleshooting

**`464 :Invalid credentials` with an SSO account.** An account that signs in
with SSO has no evrd password, and your SSO provider's password doesn't work
over IRC. Make an IRC client password on the web app's *IRC clients* page
(`<public_url>/clients`) and use that: `<user>:<client password>`. evrd's
reply says so when the account signs in with SSO, and every refused sign-in is
logged (`irc sign-in failed` with the user, network, client IP and reason —
never the password).
