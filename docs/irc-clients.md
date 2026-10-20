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
offers the IRCv3 capabilities below.

Channel events — joins, parts, quits, kicks, nick changes, topics and modes —
are saved with the messages. On replay they come from `*buffextras` as plain
text (`bob is now known as robert`), like ZNC's buffextras module, so they
never confuse a client's nick list. A client that asks for
`draft/event-playback` gets them as the real lines instead (see below).

## IRCv3

evrd offers these capabilities (`CAP LS 302`): `batch`, `cap-notify`,
`draft/chathistory`, `draft/event-playback`, `echo-message`, `message-tags`
and `server-time`. A client that asks for none of them sees exactly what it
always did. What a client gets depends on what it asks for:

| client asks for | on attach | live lines |
|---|---|---|
| nothing | backlog as plain lines, timestamps in the text, events from `*buffextras` | as the server sent them |
| `server-time` | backlog tagged `@time` | your own lines and saved ones tagged `@time` |
| `batch` (+ `server-time`) | each buffer's backlog in a `BATCH +ref chathistory <buffer>` | |
| … + `draft/event-playback` | events in the batch as `JOIN`, `PART`, `QUIT`, `KICK`, `NICK`, `TOPIC`, `MODE` | |
| `message-tags` | every replayed line tagged `@msgid` | every saved line tagged `@msgid` — the same id it has in history, so a client can drop a line it fetched twice |
| `draft/chathistory` | **no backlog**: channels are joined (with names and topic) and the client fetches history itself | |

`echo-message` echoes your own lines, with their `@msgid` too.

**Typing indicators and client tags.** evrd asks the network for
`message-tags` when it offers it. Then client-only tags (`+typing`,
`+draft/reply`, `+draft/react`…) pass both ways for clients with
`message-tags`: `TAGMSG` lines (typing notifications, reactions) and the
`+` tags on messages and notices. Your typing also shows on your other
attached clients, as with soju; `echo-message` sends a client's own back.
`TAGMSG`s are never saved, and the network's other tags (its `msgid`,
`account`…) aren't passed on. A client can't send anything but `+` tags.
On a network without `message-tags`, typing goes nowhere and messages go
without their tags. A link the connection holder kept through a restart asks
the network for its caps again (`CAP LIST`).

**Scrollback (`draft/chathistory`).** Clients page through history with
`CHATHISTORY LATEST|BEFORE|AFTER|AROUND|BETWEEN <buffer> …` (by `msgid=` or
`timestamp=`) and find the buffers with news since they were last here with
`CHATHISTORY TARGETS`; replies come in a `chathistory` (or
`draft/chathistory-targets`) batch, and mistakes as `FAIL CHATHISTORY …`
standard replies. A client sees the network it's attached to and nothing
else. Without `draft/event-playback`, only messages, notices and actions come
back, and events don't count against the limit. A `timestamp=` stands for its
whole millisecond (the precision of `@time`); `msgid=` is exact, so evrd lists
it first.

Clients that negotiate chathistory are sent this ISUPPORT (`005`) line:

```
CHATHISTORY=1000 MSGREFTYPES=msgid,timestamp
```

— at most 1000 lines per request (more is capped, not refused).

The replayed and fetched events carry the nick only — evrd doesn't keep
`user@host` — so their prefix is `nick!nick@<network>`, like replayed
messages. `labeled-response` isn't offered: replies to most commands come
from the network, which evrd doesn't ask to label them.

Clients that use chathistory, as they do with soju: Goguma, gamja, Halloy and
Senpai. Recent WeeChat takes the batched backlog with `server-time`; irssi,
HexChat and mIRC take the backlog as before.

The web chat understands `/join`, `/part`, `/msg`, `/query`, `/me`, `/notice`,
`/nick`, `/topic`, `/mode`, `/kick`, `/close`, `/quote` and `/help`, and runs
`*status` commands too (`/msg *status help`, or `/status help`).

`/msg *status help` lists the bouncer's own commands. ZNC's names work too
(`ListNetworks`, `JumpNetwork`, `AddNetwork`…), and so does ZNC's raw command:
`/znc ListNetworks` (or `/quote znc …`) is the same as `/msg *status ListNetworks`.

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
