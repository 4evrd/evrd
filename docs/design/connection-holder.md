# Design: keeping IRC connections up across evrd restarts

Status: proposed · Owner: evrd core · Related: [OpenBSD](../openbsd.md#upgrading)

## Problem

Every connection evrd keeps to an IRC network lives inside the evrd process.
Restarting evrd — to upgrade it, or for a setting that needs a restart —
closes all of them: every user quits and rejoins every channel, loses their
place in the network for a moment, and can lose a nick to someone faster.
That is the one thing a bouncer exists to prevent.

What already avoids restarts (so this is only about the rest):

- most settings apply live (Admin → Settings);
- a release that only changes the web app goes live without a restart
  (the installer compares the program);
- a renewed certificate is re-read on `SIGHUP` (`rcctl reload evrd`).

## Goal

Upgrading or restarting evrd keeps every user's IRC connections open: no
QUIT, no JOINs, no lost nick, no lost lines. Users' own IRC clients may
reconnect to evrd (they do so automatically); the web app reconnects its
stream by itself already.

Non-goals: surviving a reboot or a crash of the machine; keeping *client*
connections (irssi → evrd) open — only the upstream side matters to the
network; moving connections between machines (that's clustering).

## Idea: a connection holder

Split the part that must never restart from the part we change often:

```
 IRC networks ◄──TLS──► evrd-links ◄──unix socket──► evrd ◄── web app, IRC clients
                       (holder: tiny,              (everything else:
                        rarely updated)             restarts freely)
```

**evrd-links** owns the TCP and TLS connections to the networks. It is
deliberately dumb and small, so it almost never needs a new version:

- dials a server when evrd asks (with the source address and TLS settings
  evrd gives), and keeps the connection;
- passes lines both ways, unchanged;
- while no evrd is attached to a link, it keeps the link alive itself:
  answers `PING` with `PONG`, and **buffers** incoming lines (per link, by
  bytes and lines, oldest dropped first with a count of what was dropped);
- remembers, per link, only what's needed to resume: the registration
  lines (`001`–`005`), the current nick (follows `NICK` for itself), and
  that the link is registered.

TLS can't be handed from one process to another (the session keys live in
memory), which is why the holder terminates it rather than evrd passing
sockets to its next self.

**evrd** keeps all the IRC logic it has today. The seam already exists:
an upstream connection opens its socket through `Options.Dial`
(`internal/upstream`). With a holder, `Dial` returns a stream from the
holder instead of a TCP connection.

## Resuming after a restart

1. evrd starts, connects to the holder's socket, and lists the links it
   holds (`LIST` → link id, network id, registered?, nick, buffered lines).
2. For each of its networks with a live link, evrd *resumes* instead of
   dialling: `ATTACH <link>` gives it the stream, then the holder sends the
   cached registration lines and the buffered lines.
3. The upstream code skips registration (`NICK`/`USER`/`CAP`/SASL) for a
   resumed link, takes the nick and ISUPPORT from the cached lines, and
   rebuilds channel state without anyone noticing: `NAMES` and `TOPIC` for
   each saved channel (replies, not joins), and the server's `JOIN`-less
   view is reconciled against the saved channel list.
4. Buffered lines go through the normal path, so history and mentions
   have everything that arrived while evrd was away.
5. Networks with no live link (new, or one that dropped meanwhile) connect
   as they do today.

evrd tells the holder which links it no longer wants (`CLOSE <link>
<quit message>`), so a network switched off while evrd was down is still
cleaned up.

## Protocol (evrd ↔ holder)

Line-based over a unix socket, one control connection plus one stream per
attached link (or framed multiplexing over one connection — to be decided
in the prototype; framing is simpler to secure and test). Messages:

| From evrd | Meaning |
|---|---|
| `HELLO <version>` | handshake; the holder refuses an incompatible major version |
| `LIST` | links held: id, network, registered, nick, buffered count |
| `DIAL <link> <host> <port> <tls> <bind> <sni> <fingerprints…>` | open a link |
| `ATTACH <link>` | start streaming it; holder sends cached registration + buffer |
| `DETACH <link>` | stop streaming; holder keeps it alive and buffers |
| `CLOSE <link> <quit>` | send QUIT, close |

The holder never interprets IRC beyond `PING`, `001`–`005`, its own `NICK`,
`ERROR` and connection loss (reported as `LOST <link> <reason>`).

## Security

- The socket lives inside evrd's chroot (`/var/evrd/run/links.sock`),
  owned by `_evrd`, mode 0600: only evrd can use it.
- The holder runs as its own user (`_evrdlinks`), chrooted to an empty
  directory, pledged `stdio inet dns unix` (no files at all) and unveiled
  to nothing; evrd's pledge gains `unix`.
- Certificates and SASL credentials stay in evrd: the holder sees SASL
  only as lines passing through, never stores them.

## Deploying it

- Two services: `evrd_links` (rc.d, started first) and `evrd`. Upgrading
  evrd restarts only `evrd`. The installer updates `evrd_links` only when
  the holder itself changed, and says that this one drops connections.
- Off by default at first (`links_holder /var/evrd/run/links.sock` in
  evrd.conf turns it on); without it evrd works exactly as today.
- Cluster: each node has its own holder; nothing changes between nodes.
  A node draining for an upgrade can simply restart against its holder
  instead of handing users to another node.

## Failure modes

| What happens | Result |
|---|---|
| evrd crashes or restarts | links stay up; resumed on start |
| holder restarts | links drop (as today); evrd reconnects them |
| evrd away longer than the buffer | oldest lines dropped; evrd logs and shows "N lines missed" in the buffer |
| network drops a link while evrd is away | holder reports `LOST`; evrd reconnects on resume |
| version mismatch | evrd falls back to dialling directly, and says so in the log and Admin overview |

## Tests

- Unit: resume parsing (registration cache, NAMES/TOPIC rebuild), buffer
  limits and the dropped-lines marker, protocol framing.
- Integration (testirc server): connect, restart evrd, assert the IRC
  server saw no QUIT/JOIN, the nick is kept, lines sent during the gap
  reach history, PINGs were answered.
- e2e: the existing suite run with the holder on.
- OpenBSD VM: pledge/unveil of both processes; `rcctl restart evrd` with
  users connected.

## Plan

1. Holder process and protocol, behind `links_holder`; dial through it.
2. Resume path in `internal/upstream` (skip registration, rebuild state).
3. Buffering, missed-lines marker, `LOST` handling.
4. Installer/rc.d for `evrd_links`; docs; turn on by default once it has
   run on evrd.ircforever.org for a while.

Rough size: the holder is small (a few hundred lines); the resume path and
its tests are the bulk — on the order of a couple of focused days, plus
time running it on evrd.ircforever.org before making it the default.
