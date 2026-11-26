# Managed mode

Managed mode lets another service run evrd as an **invisible IRCv3
bridge** in front of an IRC server that doesn't speak IRCv3 (ngircd, older
UnrealIRCd or InspIRCd). [Boson](https://github.com/boson-chat) uses it to
add its app to an existing IRC network: Boson users connect to evrd, evrd
connects to the IRC server for them, and they get message tags, server-time,
msgids, `echo-message`, `draft/chathistory`, read markers and the rest from
evrd. Classic IRC users keep connecting to the IRC server directly.

In managed mode:

- **One network.** The config names the IRC server. Every account has
  exactly that network. Nobody can add, edit or remove networks: there's no
  web app, no `*status`, no `BOUNCER`/`ZNC`/`DETACH` commands, and no
  `soju.im/bouncer-networks`.
- **Accounts come from the managing service** through the [managed
  API](#the-managed-api). Nobody can sign up on their own.
- **Clients sign in with the service's OIDC access token**: SASL
  `OAUTHBEARER`, SASL `IRCV3BEARER`, or the token as the password (SASL
  `PLAIN` or `PASS`). No evrd password is involved.
- **Nothing a client sees names evrd.** Its lines come from
  `managed_server_name`. The welcome, MOTD, ISUPPORT `NETWORK`, notices about
  the link, kick and part reasons, error replies, CTCP VERSION answers, the
  away message and the quit message are all neutral or set in the config. A
  test (`TestManagedClientsNeverSeeEvrd`) collects every line sent to clients
  while they connect, the link drops and comes back, sign-ins are refused,
  the lockout and the client limit kick in, and the bouncer's commands are
  tried. It fails if any line says "evrd".
- **Each user shows with their own address** on the IRC server: every
  connection starts with `WEBIRC`, passing the address of the user's latest
  client.

It's off by default. Without `managed yes`, evrd works exactly as before.

## Configuration

```
managed                 yes
managed_network         ExampleNet                # the network's name
managed_server          irc.example.org +6697     # host [+]port, + is TLS
managed_server_name     irc.example.org           # who evrd's own lines come from
managed_token           …                         # or EVRD_MANAGED_TOKEN: the managed API's bearer token
managed_token_issuer    https://auth.example.org  # the issuer of the access tokens (default: oidc_issuer)
managed_token_audience  boson                     # their "aud" ("*" accepts any)
managed_webirc_password …                         # or EVRD_MANAGED_WEBIRC_PASSWORD

# optional
# managed_token_jwks      https://auth.example.org/.well-known/jwks.json  # instead of discovery
# managed_tls_verify      yes          # check the server's certificate chain
# managed_fingerprint     ab12…        # or accept this SHA-256 fingerprint (repeatable)
# managed_server_password …            # sent as PASS (or EVRD_MANAGED_SERVER_PASSWORD)
# managed_webirc_gateway  gateway      # the gateway name WEBIRC sends; {ident}: each user's ident (ngircd)
# managed_webirc_resolve  yes          # send the address's host name (forward-confirmed)
# managed_motd            Welcome!     # a line of the MOTD (repeatable); none: 422
# managed_away            Away         # set while no client is attached (accounts with auto-away)
# managed_version         Boson        # CTCP VERSION answer while no client is attached; none: no answer
# managed_quit            …            # quit message when a connection is switched off
# managed_text <name> <text>           # see below
```

Use `managed_token_issuer` rather than `oidc_issuer`. `oidc_issuer` also
turns on web sign-in, which evrd then checks at startup. evrd starts even
if the token issuer is unreachable. It fetches the keys when the first
token arrives, and sign-ins fail with "Service unavailable" (and no lockout)
until the issuer is back.

`managed_text` sets what clients are told. `{network}` is the network's
name and `{nick}` the user's nick. `-` sends nothing where that's possible:

| Name | Default | When |
|---|---|---|
| `welcome` | `Welcome to the {network} IRC Network {nick}` | 001 |
| `disconnected` | `Connection to {network} lost, reconnecting` | notice in each channel as the link drops (channels stay open) |
| `reconnected` | `Reconnected to {network}` | notice in each channel once rejoined |
| `kicked` | `Connection to {network} lost` | the KICK reason for accounts with quiet rejoin off |
| `left` | `Left` | the PART reason for someone who left while the link was down |
| `restarting` | `Server restarting, please reconnect` | ERROR to a client that can't be picked up after a restart |
| `unavailable` | `Service unavailable, try again later` | the issuer is down, or the account has no network or is moving between nodes |
| `denied` | `Sign-in failed` | other refused sign-ins |

Also turned off in managed mode: second factors for IRC sign-ins, sign-in
by link, the network directory, and the whole web API (the web app, `/auth`,
`/api/*` and the provisioning API). The admin CLI (`evrctl`) still works.

Use a real certificate for evrd's TLS listener (`tls acme` or `tls files`).
The self-signed fallback names evrd as its organization.

## Signing in

The Boson app signs in with its access token. In order of preference:

1. **SASL OAUTHBEARER** (RFC 7628):
   `AUTHENTICATE OAUTHBEARER`, then base64 of
   `n,,\x01auth=Bearer <access_token>\x01\x01`.
2. **SASL IRCV3BEARER** (IRCv3 `draft/bearer`):
   `AUTHENTICATE IRCV3BEARER`, then base64 of `\x00jwt\x00<access_token>`.
3. **SASL PLAIN** with the username and the token as the password, or
   `PASS <access_token>` (or `PASS <username>:<access_token>`).

`CAP LS` offers `sasl=PLAIN,OAUTHBEARER,IRCV3BEARER`. evrd checks the token's
signature against the issuer's JWKS (EdDSA, RS*, ES* and PS* are accepted),
its `iss`, `aud` and expiry. It then finds the account whose subject is the
token's `sub`. The first time, a token whose `name` (or
`preferred_username`) is an account that has no subject yet links that
account. The token only matters when the client signs in: a connection
stays up after the token expires. Failed sign-ins count toward the usual
lockout. Token-only sign-ins are counted per address.

A client attaches to the account's one network, whatever network it
names. The first client connects the account's network, so the IRC server
sees the user arrive with that client's address. After that the
connection stays up (it's a bouncer) and comes back on restarts.

## The managed API

Every call carries `Authorization: Bearer <managed_token>`. Without a
`managed_token`, every call is a 404. With a wrong token it's a 401.

```
GET    /api/managed/health
GET    /api/managed/users
GET    /api/managed/users/{username}
PUT    /api/managed/users/{username}
DELETE /api/managed/users/{username}
```

`{username}` is evrd's account name: 2–32 characters, starting with a
letter, then letters, digits, `-` and `_`, case-insensitive. The IRC nick is
set separately, so a Boson username that doesn't fit can be mapped.

### PUT /api/managed/users/{username}

Creates the account (201) or changes it (200). Fields left out stay as they
are, or take their defaults on a new account:

```json
{
  "subject":  "usr_01J…",           // the token's "sub"
  "email":    "alice@example.org",
  "nick":     "alice",              // default: the username
  "altNick":  "alice_",             // tried when the nick is taken
  "ident":    "alice",              // the user part of nick!user@host (default: the username)
  "realname": "Alice",              // default: the nick
  "disabled": false,                // true cuts the account off (its clients and its connection go)
  "auth": {                         // the login to the network's services
    "method":   "sasl",             // sasl | nickserv | auto | none
    "account":  "alice",            // default: the nick
    "password": "…"                 // "" keeps the saved one; method none clears it
  },
  "channels": ["#general"]          // joined on connect (replaces the list)
}
```

The answer is the account:

```json
{
  "username": "alice", "id": 7, "subject": "usr_01J…", "email": "alice@example.org",
  "disabled": false, "nick": "alice", "altNick": "alice_", "ident": "alice", "realname": "Alice",
  "auth": {"method": "sasl", "account": "alice", "hasPassword": true},
  "channels": ["#general"], "status": "connected", "clients": 1, "created": true
}
```

`status` is the connection's state: `disconnected` (before the first
client, or while disabled), `connecting`, `connected` or `reconnecting`. If a
connected account's nick, ident, real name or services login changes, it
reconnects. A change to its channels joins and parts them live.

Errors: 422 for an invalid username or field (`{"error": "…"}`), 409 for a
`subject` another account has, and 400 for a body that isn't JSON.

### GET, DELETE

`GET /api/managed/users/{username}` returns the account as above (404 if
there's none). `GET /api/managed/users` returns `{"users": [ … ]}`.
`DELETE /api/managed/users/{username}` removes the account, its network
connection and its history.

### GET /api/managed/health

```json
{
  "ok": true,
  "network": {"name": "ExampleNet", "host": "irc.example.org", "port": 6697, "tls": true},
  "users": {"total": 120, "disabled": 2, "attached": 31},
  "clients": 40,
  "connections": {"connected": 97, "connecting": 0, "reconnecting": 0, "disconnected": 23},
  "tokens": true,
  "webirc": true
}
```

`ok` means token sign-in is configured and the network isn't failing
everyone: some connection is up, or none is reconnecting. A setup flow can
poll this after creating its first account and connecting.

## Setting up the IRC server

evrd needs two things on the IRC server:

1. a **WEBIRC block** for evrd's address, with `managed_webirc_password`,
   so each user shows their own address;
2. **no per-address connection limit for evrd's address**, because every
   managed user connects from it. Exempt it from throttling and from
   connect-flood protection too.

Without WEBIRC, managed users show with evrd's host. In that case leave
`managed_webirc_password` empty.

### ngircd

```
[Options]
	WebIRCPassword = <managed_webirc_password>

[Limits]
	MaxConnectionsIP = 0   ; ngircd can't exempt one address: this lifts the limit for everyone
```

and in evrd.conf:

```
managed_webirc_gateway {ident}
```

ngircd reads WEBIRC's second parameter as the user name, so with the
default (`gateway`) every managed user would show as `gateway@…`. ngircd
accepts WEBIRC from any host that knows the password, so keep it secret. ngircd takes the host name WEBIRC sends as it is, so leave
`managed_webirc_resolve` on to show host names rather than addresses.
`dev/ngircd/ngircd.conf` is the configuration the end-to-end test uses.

### UnrealIRCd (5 and 6)

```
webirc {
	mask 192.0.2.10;                 // evrd's address
	password "<managed_webirc_password>";
};

allow {
	mask 192.0.2.10;
	class clients;
	maxperip 10000;
};

except throttle { mask 192.0.2.10; };
except ban { mask 192.0.2.10; type { connect-flood; handshake-data-flood; }; };
```

### InspIRCd

InspIRCd 3 (`m_cgiirc`):

```
<module name="cgiirc">
<cgihost type="webirc" password="<managed_webirc_password>" mask="192.0.2.10">
<connect allow="192.0.2.10" localmax="10000" globalmax="10000" limit="10000" useident="no">
```

InspIRCd 4 (`m_gateway`):

```
<module name="gateway">
<webirc password="<managed_webirc_password>" mask="192.0.2.10">
<connect allow="192.0.2.10" localmax="10000" globalmax="10000" limit="10000">
```

The `<connect>` block for evrd's address must come before the general one.
Also exempt evrd's address from `connflood` and DNSBL checks if those
modules are loaded.

### Services

Each account logs in to the network's services (Anope, Atheme…) as the
managed API's `auth` says: SASL `PLAIN` when the server offers SASL, or
`IDENTIFY` to NickServ once connected. Most servers that lack IRCv3 have no
SASL either, so use `nickserv` or `auto` there.

## Testing

- Unit tests: `go test ./internal/managed/... ./internal/client ./internal/config`.
- `make e2e-managed` (`dev/e2e-managed.sh`) runs evrd in managed mode in
  front of a real ngircd in Docker. It checks WEBIRC (a classic user's WHOIS
  shows the managed user's own address), IRCv3 from a server without it
  (server-time, msgids, echo-message, chathistory), and that nothing names
  evrd. CI runs it on every push.

## Not yet

- Users adding networks of their own. Managed mode refuses this
  everywhere, but the one-network rule lives in the managed paths only, so a
  later version can allow it.
- Guests (Boson users without an account).
- Dropping a user's connection when their last client leaves (a pure
  bridge, without the bouncer's always-on connection).
- A new WEBIRC address when a user's address changes: the connection keeps
  the address it was made with until it reconnects.
