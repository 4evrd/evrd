# Modules

Modules are optional behaviours you switch on per network, like ZNC's
network modules. Switch them on and set them up on the network's page
(Networks → the network → Modules), or from an IRC client with `*status`:

```
/msg *status modules                       which are on, and what each does
/msg *status loadmod keepnick              switch one on for this network
/msg *status unloadmod keepnick            and off
/msg *status modset kickrejoin delay 5     change a setting
/msg *status modset perform commands /mode %nick% +i | /msg NickServ ghost me
```

(`modset` separates the lines of a multi-line setting with ` | `.)

Changes apply when you save, without reconnecting. Perform's commands run at
the next sign-in. What modules do shows in the network's connection log.

| Module | Stands in for (ZNC) | What it does | Settings |
|---|---|---|---|
| `perform` | perform | Sends commands every time evrd signs in to the network. | **Commands**, one per line, as typed in a client (`/msg …`, `/mode %nick% +x`) or raw IRC. `%nick%` is your nick. |
| `keepnick` | keepnick | When your nick is taken, wins it back as soon as it's free, and tries every 30 seconds meanwhile. | — |
| `kickrejoin` | kickrejoin | Rejoins a channel you were kicked from. | **Delay** in seconds (10). |
| `autocycle` | autocycle | When you're left alone in a channel without ops, leaves and rejoins it so you get ops. | **Channels**: patterns with `*` and `?`, `!` to leave one out (`*`). |
| `autoreply` | autoreply | While no client (IRC or web) is attached, answers private messages with a notice — once per person per interval. Not channels, CTCP, services or servers. | **Reply** (`I am currently away, try again later`); `%nick%` is your nick, `%sender%` theirs. **Interval** in minutes (2). |
| `autoattach` | autoattach | When a message in a [detached](#detached-channels) channel matches a rule, attaches it again: your IRC clients get its JOIN and what was said, as with `*status attach`. | **Rules**, one per line, ZNC's `[!]#channel [search] [host]`: `#home *evrd* *!*@friend.example.org`. `*` and `?` match anything; the search matches the message's text, the host `nick!user@host`; a `!` rule never attaches. |
| `autoop`, `autovoice` | autoop, autovoice | Gives ops or voice to the people you list when they join a channel where you have ops. | **Who**: one per line, a `nick!user@host` mask (or a nick) and optionally channels: `*!*@friend.example.org #home,#work`. |
| `alias` | alias | Your own commands for IRC clients: `/op #chan nick` becomes what you define. | **Aliases**: `name = commands`, with `%1`, `%2`… (`%1+` for the rest) and `%nick%`; several commands separated by `;`. |
| `ctcpflood` | ctcpflood | Stops answering CTCP requests, and passing them on, while too many arrive at once. | **Most CTCPs** (4) **in seconds** (2). |
| `q` | q | Logs in to QuakeNet's Q with the network's account and password (Authentication), instead of NickServ, and hides your host. The QuakeNet preset under Authentication switches it on. | **Hide my host** (yes). |
| `crypt` | crypt | FiSH encryption: what you send to a channel or nick you have a key for goes out encrypted, what they send is decrypted. See [crypt](#crypt). | **Keys**, one per line: `#chan key` or `nick cbc:key`. **Mark decrypted messages with** (🔒). |
| `bouncedcc` | bouncedcc | Relays DCC chats and file transfers (DCC CHAT, SEND, RESUME) through evrd, both ways: the other side is given evrd's address, never yours, and your client needn't be reachable. Passive (reverse) DCCs work too. Needs an admin to set `dcc_public` (below). | — |

Logging in to NickServ, SASL ([services](services.md)), saving channels, away when nobody's attached
(with your own away message, like simple_away), clientnotify, notes,
playback and logs aren't modules: evrd does them itself.

A ZNC config's `LoadModule = autoreply <text>` carries the reply over, and
`LoadModule = autoattach #a !#b …` its channels, one rule each (rules ZNC
keeps in its own data have to be added again).

### Detached channels

`/msg *status detach #chan` (or `/detach #chan`) hides a channel from your IRC
clients while evrd stays in it and logs it; `/msg *status attach #chan` or
`/join #chan` brings it back with what you missed. `autoattach` does that for
you when something you care about is said there.

## crypt

`crypt` speaks FiSH (as FiSH, mircryption, HexChat's fishlim, weechat's
fish.py and ZNC's crypt do), so people with any of those can talk to you
encrypted while your own clients, the web app and history see plain text.
Messages, notices, actions and topics you send to a target with a key go
out as `+OK …` (Blowfish ECB) or, for a key written `cbc:…`, `+OK *…`
(CBC, newer clients), split so each line fits. What arrives as `+OK …` or
`mcps …` from a target with a key (messages, notices, actions, topics) is
decrypted before evrd saves or shows it, and marked with the **Mark**
setting (`🔒 ` by default, before the text, so nick lists and history
stay right; empty for none). Text that doesn't decrypt is shown as it
came. Lines that are already `+OK …` (a client doing its own FiSH) and
CTCPs other than actions go as they are. A channel's key is used for
the channel; a nick's for private messages both ways.

From an IRC client, talk to `*crypt`:

```
/msg *crypt setkey #secret correct horse     ECB (what every FiSH client understands)
/msg *crypt setkey bob cbc:another key       CBC
/msg *crypt delkey bob
/msg *crypt listkeys
/msg *crypt keyx bob                         a DH1080 key exchange (keyx bob ecb for old clients)
```

`keyx` sends bob `DH1080_INIT`; when his client answers, the key is set and
saved, as it is when someone starts an exchange with you (evrd answers
with `DH1080_FINISH`, CBC when they ask for it). Keys set this way are
saved with the network's module settings (encrypted, like passwords) and
show under Modules. Importing from ZNC doesn't bring crypt's keys (ZNC
keeps them outside its config): set them again.

## For admins

`modules_off` (or Admin → Settings → Modules) switches modules off for
everyone, at once:

```
modules_off autoop autovoice
```

bouncedcc relays nothing until an admin gives it the address to offer
(Admin → Settings → Modules, or in `evrd.conf`):

```
dcc_public 203.0.113.5 2001:db8::5   # the IPv4 and/or IPv6 address DCC offers point at
dcc_ports 50000-50100                # ports to listen on: open them in the firewall
dcc_timeout 60s                      # to be connected, and for a transfer to move
dcc_max_mb 0                         # the largest file relayed (0: any size)
dcc_private no                       # follow offers to loopback and private addresses
```

(`dcc_listen` picks the local address listeners bind; unset, all of them.)
A listener takes one connection and closes, or closes when nobody
connects in time. One meant for your client takes only the addresses your
IRC clients attached from; evrd connects to your client where it attached
from, not where its offer says. Offers to addresses inside evrd's own
network (loopback, private, link-local) aren't followed unless
`dcc_private` is on, nor ever ones to ports below 1024, so a stranger's
offer can't make evrd connect somewhere it shouldn't. Each connection
relays at most 16 DCCs at once. In a cluster, set `EVRD_DCC_PUBLIC` (and `EVRD_DCC_PORTS`) on each
node: a network's DCCs listen on the node it's connected from.

## Writing one

A module is Go code in `internal/modules`: an `Info` (name, description,
settings) registered in `init`, and a type with `Registered()` (signed in)
and `Line(irc.Message)` (every line the server sends). It may also implement
`CTCPFilter`, `ClientFilter` (lines from the user's clients) and
`LoginHandler`. It acts through a `Host`: send lines, see channels and
members, set timers, write to the connection log, tell whether a client is
attached, attach a detached channel. Calls to one connection's
modules never overlap, so a module needs no locking. Test it against a fake
host, as `modules_test.go` does.

To change lines rather than only act on them, as `crypt` does, a module
implements `LineRewriter` (lines from the server, before evrd handles,
saves or passes them on; it can drop one) or `ClientRewriter` (lines going
to the server, after evrd has saved and shown them; one can become
several). `ClientRewriter` is called outside the lock that keeps the other
calls apart, so that module locks what it shares. A host may also be a
`SettingsSaver` (save a setting the module changed) and a `Notifier`
(tell the user's clients something, as `*name`, in their language);
a `ClientFilter` gets the user's `/msg *name …` lines.
