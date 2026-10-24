# Using evrd from an IRC client

Point any IRC client at the bouncer and authenticate with a **server password**
of `<user>:<password>` (or `<user>/<network>:<password>` to pick a network).
The password is the account password or an **IRC client password** made on
the web app's *IRC clients* page — the only option for accounts that sign in
with SSO, and revocable one client at a time. Clients that do SASL can sign in
with SASL PLAIN instead (user name `<user>` or `<user>/<network>`):

```
server:   bnc.example.org         (your irc_host)
port:     6697 (TLS)              — accept the certificate if evrd made a self-signed one
password: mira/libera:<password>  (user/network:password)
```

On connect you get the welcome burst, your channels (with names/topic), backlog
replay, and live traffic. `/join` and `/part` update the network's saved
channel list, `/nick` changes your nick upstream, and your own notices and
`/me` actions are saved and shown on your other clients like messages. evrd
offers the [IRCv3](#ircv3) capabilities below.

Channel events — joins, parts, quits, kicks, nick changes, topics and modes —
are saved with the messages (the web chat shows them). Backlog replays them
only to a client that asks for `draft/event-playback`, as the real lines in a
history batch (see [IRCv3](#ircv3)); other clients get just the chat, so
replay never confuses their nick lists. Either way the backlog's line count
counts only what's replayed.

The web chat understands `/join`, `/part`, `/msg`, `/query`, `/me`, `/notice`,
`/nick`, `/topic`, `/mode`, `/kick`, `/close`, `/quote` and `/help`, and runs
`*status` commands too (`/msg *status help`, or `/status help`).

`/msg *status help` lists the bouncer's own commands. ZNC's names work too
(`ListNetworks`, `JumpNetwork`, `AddNetwork`…), and so does ZNC's raw command:
`/znc ListNetworks` (or `/quote znc …`) is the same as `/msg *status ListNetworks`.
Commands are case-insensitive; the web chat takes `/znc …` too. `*status`
answers in your account's language (*Account → Profile → Language*); the
command names stay the same in every language. See
[translations](translations.md#irc-messages).

| command | does |
|---|---|
| `networks` | your networks, their status, and which one this client is on |
| `jumpnetwork <network>` | switch this client to another network |
| `jump [server]` | reconnect to the network's next server, or the one named (`jump <network>` still switches network) |
| `connect [network]` / `disconnect [network]` | bring a network up or down (default: this one) |
| `addnetwork <name> <host> [[+]port] [nick]` | add a network; `+port` is TLS (default `+6697`) |
| `addnetwork <id>` / `directory [search]` | add a network from the [network directory](network-directory.md) by its id (`addnetwork libera`); list or search it |
| `delnetwork <network>` | remove a network and its history |
| `show [network]` | a network's settings (default: this one) |
| `set [network] <setting> <value>` | change `name`, `host`, `port` (`+port` = TLS), `tls`, `nick`, `altnick`, `ident`, `realname`, `password`, `sasluser`, `auth`, `servicesnick`, `identifyargs`, `identifyon`, `authcommand`, `ghost`, `joinafterauth`, `authtimeout` ([services login](services.md)), `bindhost`, `quitmsg`, `encoding`, `trustallcerts`, `trustpki`, `floodrate`, `floodburst`, `joindelay` (`-` clears); reconnects when needed |
| `listservers` / `addserver <host> [[+]port] [pass]` / `delserver <host> [port]` | the network's servers, tried in turn |
| `listfingerprints` / `addfingerprint <sha-256>` / `delfingerprint <sha-256>` | trusted server certificates |
| `detach <#chan>` / `attach <#chan>` | hide a channel from IRC clients (still joined and logged) / show it again with what you missed (`/join` does too) |
| `disablechan <#chan>` / `enablechan <#chan>` | leave a channel but keep it saved, with its key / join it again |
| `channels` / `showchan <#chan>` | this network's saved channels / one channel's state, settings and buffer |
| `listnicks <#chan>` / `topics` | who's in a channel / your channels' topics |
| `buffers` | this network's buffers: lines played back, auto-clear, what's waiting |
| `setbuffer <buffer\|*> [lines\|-]` | how many lines a buffer plays back (0–500; `*` is the network's default) |
| `autoclear <buffer> on\|off\|-` | play back only what's new, or the last lines every time (`-`: your account's setting) |
| `clearbuffer <buffer>` / `clearallbuffers` / `clearallchannelbuffers` / `clearallquerybuffers` | start playback over from now (history keeps the lines) |
| `playbuffer <buffer>` / `playback <buffer> [lines]` | play a buffer back as on attach / replay its last lines from history, cleared or not |
| `setbindhost <host>` / `clearbindhost` / `showbindhost` / `setuserbindhost <host>` / `clearuserbindhost` | the network's and your account's bind host |
| `listclients` / `uptime` / `traffic` | attached clients / how long evrd has run / where the network's traffic is |
| `listusers` / `listallusernetworks` | admins: every user / every user's networks |
| `info` / `version` | your account and limits / the evrd version |

Buffer names take wildcards (`clearbuffer #evrd*`) and lists (`#a,#b`).

### Playback buffers

Each channel and private message has a buffer, per network. When an IRC
client attaches it gets each buffer's last lines (*Account → Bouncer*:
playback lines; `setbuffer` for one network or one buffer). A buffer that
**auto-clears** (*Account → Bouncer* for channels and for private messages;
`autoclear` for one buffer) plays back only what came since an IRC client was
last on that network — nothing while another client is on it, as with ZNC.
`clearbuffer` starts a buffer over. The network's page in the web app has
the same settings under **Playback**: lines per buffer for the network, each
buffer's lines and what it plays back, what's waiting, and clearing. The lines stay in history either way: the
web chat, `playback` and `CHATHISTORY` still have them. A client with
`draft/chathistory` gets no playback; it asks for what it wants.

Anything you send shows up in the web console and on
your other attached clients too — all sessions share one upstream connection.

Upstream networks authenticate with **SASL PLAIN** when the network has a
password and the server supports it, else by identifying to NickServ once
connected — or however the network's Authentication settings say: see
[Logging in to services](services.md).

Playback for clients without `server-time` carries timestamps in the text
(`[12:34:56] hello`); change the format, time zone and how much is played back
under *Account → Bouncer*.

## IRCv3

A client that asks for no capabilities gets what IRC clients always got from
evrd. A client that asks gets IRCv3: evrd's own capabilities, offered on every
network, and the network's, passed through while the network has them. evrd
asks the network for each one it passes through when the network offers it.

A client picks its network with PASS, SASL or BOUNCER BIND. Clients send
`CAP LS` first, usually with PASS, NICK and USER right behind, so evrd's answer
waits for those (at most a second and a half) and then offers the network's
capabilities too, which the client can ask for before `CAP END`. When the
network isn't known by then (SASL comes after `CAP LS`), they follow as
`CAP NEW` once the client is attached. `CAP NEW` and `CAP DEL` also come when
the link gains or loses one or the client jumps networks (`cap-notify`,
implied by `CAP LS 302`). A `CAP REQ` is ACKed or NAKed whole, as the spec
says: one that asks for a capability that isn't offered (yet) is NAKed. Lines a
capability brings reach the clients that asked for it; the others get them
translated, or not at all.

| capability | kind | a client without it gets |
|---|---|---|
| `batch`, `server-time`, `message-tags`, `echo-message` | evrd's | plain lines; timestamps in replayed text |
| `cap-notify` | evrd's | the offer as it was at `CAP LS` |
| `sasl` (`PLAIN`) | evrd's | sign in with PASS |
| `standard-replies` | evrd's | the network's `FAIL`/`WARN`/`NOTE` as notices |
| `draft/chathistory`, `draft/event-playback` | evrd's | backlog pushed on attach |
| `draft/read-marker` | evrd's | — |
| `soju.im/bouncer-networks`, `soju.im/bouncer-networks-notify` | evrd's | `*status` network commands |
| `no-implicit-names` | evrd's | every NAMES reply |
| `draft/pre-away` | evrd's | — |
| `sts` | evrd's, with a trusted certificate | — |
| `multi-prefix` | the network's | the highest prefix only (NAMES, WHO) |
| `userhost-in-names` | the network's | nicks only in NAMES |
| `extended-join` | the network's | plain JOINs |
| `account-notify`, `away-notify`, `setname` | the network's | no ACCOUNT, AWAY, SETNAME |
| `account-tag` | the network's | no `@account` |
| `chghost` | the network's | QUIT, then JOIN (and the modes back) in each shared channel |
| `invite-notify` | the network's | only invitations for you |
| `draft/channel-rename` | the network's | PART the old name, JOIN the new one |
| `draft/message-redaction` | the network's | no REDACT |
| `extended-monitor` | the network's | MONITOR replies only |
| `labeled-response` | the network's (with `batch`) | unlabeled replies |
| `draft/multiline` | the network's (with `batch`), with its limits | each line on its own |

Also passed through: the network's ISUPPORT (`005`) — `PREFIX`, `CHANTYPES`,
`MONITOR`, `BOT`… — with evrd's own tokens; MONITOR itself (one list per
network, shared by your clients); and bot mode (`BOT=`, `+B` and the `@bot`
tag, for clients with `message-tags`). Not supported: `draft/metadata-2`
(evrd would have to keep metadata per user and few networks have it).

### Backlog and live lines

| client asks for | on attach | live lines |
|---|---|---|
| nothing | backlog as plain lines, timestamps in the text, without channel events | as the server sent them, without its tags |
| `server-time` | backlog tagged `@time` | your own lines and saved ones tagged `@time` |
| `batch` (+ `server-time`) | each buffer's backlog in a `BATCH +ref chathistory <buffer>` | the network's batches (netsplits…) |
| … + `draft/event-playback` | events in the batch as `JOIN`, `PART`, `QUIT`, `KICK`, `NICK`, `TOPIC`, `MODE` | |
| `message-tags` | every replayed line tagged `@msgid` | every saved line tagged `@msgid` — the same id it has in history, so a client can drop a line it fetched twice |
| `draft/chathistory` | **no backlog**: channels are joined (with names and topic) and the client fetches history itself | |

`echo-message` echoes your own lines, with their `@msgid` too.

**Typing indicators and client tags.** Client-only tags (`+typing`,
`+draft/reply`, `+draft/react`…) pass both ways for clients with
`message-tags`, when the network has `message-tags`: `TAGMSG` lines (typing
notifications, reactions) and the `+` tags on messages and notices. Your
typing also shows on your other attached clients, as with soju;
`echo-message` sends a client's own back. `TAGMSG`s are never saved. Of the
network's own tags only `@account` (with `account-tag`) and `@bot` pass; a
client can't send anything but `+` tags. On a network without
`message-tags`, typing goes nowhere and messages go without their tags.

**msgids.** Clients know lines by evrd's msgids. evrd remembers the network's
msgid for the last few thousand lines of each network, so replies
(`+draft/reply`), reactions and redactions (`REDACT`) name the right line both
ways. evrd doesn't learn the network's msgid for what you send (it doesn't ask
the network to echo), so redacting your own line works only where the network
accepts it otherwise. A redaction doesn't remove the line from evrd's history.

**Scrollback (`draft/chathistory`).** Clients page through history with
`CHATHISTORY LATEST|BEFORE|AFTER|AROUND|BETWEEN <buffer> …` (by `msgid=` or
`timestamp=`) and find the buffers with news since they were last here with
`CHATHISTORY TARGETS`; replies come in a `chathistory` (or
`draft/chathistory-targets`) batch, and mistakes as `FAIL CHATHISTORY …`
standard replies. A client sees the network it's attached to and nothing
else. Without `draft/event-playback`, only messages, notices and actions come
back, and events don't count against the limit. A `timestamp=` stands for its
whole millisecond (the precision of `@time`); `msgid=` is exact, so evrd lists
it first: clients that negotiate chathistory get `CHATHISTORY=1000
MSGREFTYPES=msgid,timestamp` in ISUPPORT — at most 1000 lines per request
(more is capped, not refused).

The replayed and fetched events carry the nick only — evrd doesn't keep
`user@host` — so their prefix is `nick!nick@<network>`, like replayed
messages. AWAY, ACCOUNT, CHGHOST and SETNAME aren't kept in history.

### Networks, sign-in and read markers

**SASL.** Instead of PASS, a client can sign in with SASL PLAIN: the user name
(or `user/network`, to pick a network) and the account password or an IRC
client password. Accounts that sign in with SSO need a client password, as with
PASS, and a refused sign-in is logged the same way.

**Networks (`soju.im/bouncer-networks`).** Goguma and gamja list, add, change
and remove your networks from the client (`BOUNCER LISTNETWORKS`,
`ADDNETWORK`, `CHANGENETWORK`, `DELNETWORK`, with soju's attributes: `name`,
`host`, `port`, `tls`, `nickname`, `username`, `realname`, `pass`, and
read-only `state` and `error`), within your network limit. A client that asks
for the capability and names no network gets a *bouncer connection* on no
network; it opens one connection per network with `BOUNCER BIND <id>`
(ISUPPORT `BOUNCER_NETID`). Each connection counts against your client limit.
With `soju.im/bouncer-networks-notify` it hears when networks come, change
(their state too) and go, within a few seconds.

**Read markers (`draft/read-marker`).** `MARKREAD` reads and moves how far a
buffer is read — the same markers the web app uses, so reading on one client
or in the web app shows as read on the others within a few seconds. A marker
only moves forward. A client gets a channel's marker after its JOIN.

### Labels, multiline, STS

**`labeled-response`** is offered only while the network has it. What evrd
answers itself (`*status`, CHATHISTORY, BOUNCER, MARKREAD, echoes) evrd
labels; other commands go to the network with the label, and the network's
labeled reply goes back to the client that sent it (your other clients get the
same lines without the label). Without the network's help evrd couldn't tell
which reply belongs to which command, and labeling only some would break the
spec, so then it isn't offered.

**`draft/multiline`** is offered while the network has it, with the network's
`max-bytes` and `max-lines`. A multiline message goes to the network as one
batch and is saved a line at a time (a `draft/multiline-concat` line joined to
the one before); each saved line has its own msgid.

**`sts`** tells clients to use TLS only: see
[STS](configuration.md#sts-tls-only-for-irc-clients). It's offered only with a
TLS listener and a certificate clients trust.

**`no-implicit-names`**: only the NAMES replies the client asked for.
**`draft/pre-away`**: an AWAY sent while registering is set once attached
(`AWAY *` is left to auto-away).

### Clients

Clients that use chathistory, read markers and bouncer networks, as they do
with soju: Goguma, gamja, Halloy and Senpai. Recent WeeChat takes the batched
backlog with `server-time`; irssi, HexChat and mIRC take the backlog as
before.

## Troubleshooting

**`464 :Invalid credentials` with an SSO account.** An account that signs in
with SSO has no evrd password, and your SSO provider's password doesn't work
over IRC. Make an IRC client password on the web app's *IRC clients* page
(`<public_url>/clients`) and use that: `<user>:<client password>`. evrd's
reply says so when the account signs in with SSO, and every refused sign-in is
logged (`irc sign-in failed` with the user, network, client IP and reason —
never the password).
