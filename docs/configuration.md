# Configuration

evrd reads one config file (`-config /path/evrd.conf`, or `EVRD_CONFIG`): one
directive per line, `#` starts a comment (also after a value). Some directives
can also come from the environment, which wins over the file. Most server
settings can be changed later in the web app (*Admin → Settings*); those values
are stored in the database and win over the file (see below).

## Directives

| Directive | Default | What it does |
|---|---|---|
| `listen` | `irc+insecure://127.0.0.1:6667` | an IRC listener for clients; repeat for several. `ircs://:6697` (TLS) or `irc+insecure://:6667` |
| `api` | `:8098` | HTTP + WebSocket API the web app talks to |
| `tls` | `files` if `tls_cert` is set, else `selfsigned` | where certificates come from: `acme` (Let's Encrypt), `selfsigned` or `files`; see [HTTPS and certificates](#https-and-certificates) |
| `tls_cert`, `tls_key` | — | PEM files for `tls files` |
| `acme_email`, `acme_hosts`, `acme_ca` | —, `public_url`'s host and `irc_host`, Let's Encrypt | Let's Encrypt's contact address, the names to cover, and the ACME directory |
| `https_listen`, `http_listen` | — | evrd serves the web app over HTTPS itself (e.g. `:443`), and redirects plain HTTP (e.g. `:80`) |
| `web_root` | — | the built web app (`web/build`) for evrd to serve on `api` and `https_listen`, without nginx |
| `chroot`, `user` | — | confine evrd after startup; see [Confinement](#confinement-chroot-and-user) |
| `provision_token` (`EVRD_PROVISION_TOKEN`) | — | turns on the [provisioning API](#provisioning-api) for tools that create accounts |
| `db` (`EVRD_DB`) | `sqlite:evrd.db` | accounts and settings: `postgres://user:pass@host/evrd?sslmode=disable` or `sqlite:/path/evrd.db` |
| `history` (`EVRD_HISTORY`) | `pebble:history` | chat history: `pebble:/var/lib/evrd/history` (one node) or `scylla://host1,host2:9042/evrd` (shared) |
| `secret_key` (`EVRD_SECRET_KEY`), `secret_key_file`, `secret_key_old` | — | encrypts saved network passwords; see [Saved passwords](#saved-passwords) |
| `public_url` | — | the web app's address, e.g. `https://bnc.example.org` (SSO redirects back here) |
| `irc_host` | `localhost` | the host name users give their IRC clients (shown in the web app) |
| `password_login` | `yes` | allow password sign-in to the web app |
| `session_ttl` | `720h` | how long a web sign-in lasts |
| `cookie_secure` | `yes` | HTTPS-only session cookie; `no` only for plain-HTTP testing |
| `oidc_issuer`, `oidc_client_id`, `oidc_client_secret` (`EVRD_OIDC_CLIENT_SECRET`) | — | single sign-on with any OpenID Connect provider |
| `oidc_label`, `oidc_auto_create`, `oidc_link_existing`, `oidc_admin_role` | `single sign-on`, `yes`, `no`, — | sign-in button text and account rules for SSO |
| `max_networks`, `max_clients`, `backlog_lines` | `0`, `0`, `2000` | per-user defaults (0 = unlimited); admins override per user |
| `connect_rate` | `5` | most new connections per second to one IRC server host, per node |
| `bindhost` | — | local addresses users may choose to connect from (repeatable) |
| `upstream_bind`, `upstream_prefix6` (`EVRD_UPSTREAM_BIND`, `EVRD_UPSTREAM_PREFIX6`) | — | spread users over source addresses; see [clustering](clustering.md#source-addresses-and-connect-pacing) |
| `otel_endpoint` (`OTEL_EXPORTER_OTLP_METRICS_ENDPOINT`), `otel_interval`, `metrics_url` (`EVRD_METRICS_URL`) | —, `15s`, — | metrics; see [Metrics](#metrics) |
| `settings_write_conf` | `no` | write settings changed in the web app back into this file |
| `node_id`, `internal_listen`, `internal_url`, `internal_irc_listen`, `internal_irc_addr`, `cluster_secret`, `cluster_lease` | — | running several nodes; see [clustering](clustering.md) |

## Server settings (Admin → Settings)

Admins can change the general, limits, connection, sign-in, SSO and metrics
settings of `evrd.conf` from the web app (the list is below). Each value comes from,
in order (later wins):

1. built-in defaults
2. `evrd.conf`
3. **the database** — values set on the Settings page, shared by every node
4. the environment (`EVRD_…`, `OTEL_EXPORTER_OTLP_METRICS_ENDPOINT`) on that node

The page shows where each value comes from (Default, evrd.conf, Set here, or
Environment — those are locked in the UI). **Reset** removes a value set here,
falling back to evrd.conf or the default.

**Editable:** `irc_host`, `public_url`, `max_networks`, `max_clients`,
`backlog_lines`, `connect_rate`, `bindhost`, `tls`, `acme_email`, `acme_hosts`,
`acme_ca`, `tls_cert`, `tls_key`, `password_login`, `session_ttl`,
`cookie_secure`, the `oidc_*` directives, `otel_endpoint`, `otel_interval`,
`metrics_url`. Most apply live on every node within a few seconds; `public_url`,
the SSO provider (`oidc_issuer`, `oidc_client_id`, `oidc_client_secret`) and
metrics export (`otel_*`) and the certificate files (`tls_cert`, `tls_key`)
are marked *Restart needed* until evrd restarts. The
client secret is stored encrypted (with `secret_key`) and never shown back.

**Startup settings stay in the file/environment** and are only shown: `listen`,
`api`, `https_listen`, `http_listen`, `web_root`, `chroot`, `user`, `db`,
`history`, cluster identity (`node_id`,
`internal_*`, `cluster_secret`), `upstream_bind` / `upstream_prefix6` and the
`secret_key*` directives. evrd needs them before it can reach the database, and
some differ per node.

## Writing changes back to evrd.conf

Off by default. With

```
settings_write_conf yes
```

each node also writes the values set in the UI into **its own** config file:
a changed directive is replaced in place, new ones go under a
`# --- set from the evrd admin UI ---` block, and Reset removes the directive
(so the default applies). Comments and everything else are kept, and the file
is replaced atomically with the same permissions. A node that was down
catches up when it starts. The Settings page lists each node's last write and
any error (e.g. the evrd user can't write the file).

Use it for a **single node** where you want `evrd.conf` to stay the readable,
complete record of the configuration.

**In cluster mode, leave it off** and let the database be the one place these
settings live: every node already reads them from there, nodes' files may
differ on purpose (or be read-only, baked into an image, or managed by config
management), and having several nodes rewrite files gains nothing. evrd logs a
warning if a clustered node has it on.

## Accounts and sign-in

Every user has an evrd account with a role (`user` or `admin`), per-user
limits, and optional suspension. Admins manage accounts on the web app's
*Users* page; `evrctl user …` does the same from a shell (bootstrap the first
admin with `evrctl user add -admin`).

**Signing in to the web app.** Sessions are stored in the database behind an
HttpOnly cookie, so they survive restarts; users can see and revoke them on
their *Account* page. Two methods, either or both:

- **Password** — `password_login yes` (default).
- **SSO via OpenID Connect** — Logto, or any OIDC provider. SAML, social and
  enterprise logins are connectors in the provider; evrd only speaks OIDC.
  Accounts are created on first sign-in and linked by the token's subject.

```
public_url        https://bnc.example.org   # callback: <public_url>/auth/callback
oidc_issuer       https://id.example.org/oidc
oidc_client_id    <app id>
# secret: oidc_client_secret, or the EVRD_OIDC_CLIENT_SECRET env var
oidc_label        IRCforever account        # text on the sign-in button
oidc_auto_create  yes   # create accounts on first sign-in
oidc_link_existing no   # link to an existing local account with the same username
oidc_admin_role   evrd-admin  # optional: provider role that makes an evrd admin
```

**Limits** (server defaults; admins override per user, 0 = unlimited):
`max_networks 0` (unlimited by default), `max_clients 0` (attached IRC clients at once; unlimited by default),
`backlog_lines 2000` (kept per channel, pruned every 10 minutes).

**Suspending** a user signs them out everywhere, drops their IRC clients and
disconnects their networks; lifting it reconnects them. The last active admin
can't be demoted, suspended or deleted.

Passwords need at least 10 characters. A password an admin sets — or the
one-time password **Reset** generates on the Users page — is temporary: the
user picks their own at next sign-in.

Other directives: `session_ttl 720h`, `cookie_secure yes` (turn off only for
plain-HTTP dev), `irc_host` (the hostname shown to users for attaching).

## Network and account settings (ZNC-style)

Each network has its own page in the web app (`/networks/<id>`), with what
ZNC offers without modules:

- **Servers** — several, each with host, port, TLS and an optional server
  password (PASS). evrd connects to the first and moves on to the next each
  time the link drops.
- **Identity** — nick, alternate nick, ident, real name, quit message, bind host
  (empty fields use the account defaults).
- **Authentication** — SASL PLAIN account and password (sent as PASS if the
  network has no SASL).
- **Certificates** — verify with the system CAs (TrustPKI), trust everything
  (TrustAllCerts), and trusted SHA-256 fingerprints. A refused certificate's
  fingerprint is shown with a button to trust it.
- **Channels** — key, detached (joined and logged, hidden from IRC clients
  until they `/join`; `/detach #chan` from a client), join-on-connect.
- **Advanced** — encoding for non-UTF-8 networks (`^charset`: only decode
  invalid UTF-8; `charset`: decode and send in it), flood protection (lines
  per second and burst), join delay.

Per-user settings (Account → Bouncer): default ident, quit message and bind
host; playback lines and whether channels / private messages only play back
what arrived since you were last attached (AutoClearChanBuffer /
AutoClearQueryBuffer); timestamps prepended/appended for clients without
server-time (strftime format, time zone); fixed CTCP replies (an empty reply
blocks the request; with no client attached evrd answers VERSION, PING and
TIME itself); and the addresses allowed to sign in, web and IRC.

`*status` has the same: `listservers`, `addserver`, `delserver`,
`listfingerprints`, `addfingerprint`, `delfingerprint` (and ZNC's
`AddTrustedServerFingerprint` names), `detach`, and `set` for altnick,
sasluser, bindhost, quitmsg, encoding, trustallcerts, trustpki, floodrate,
floodburst, joindelay.

## HTTPS and certificates

evrd's own TLS ports — the `ircs://` IRC listeners, and `https_listen` when
evrd serves the web app itself — share one certificate source, set with `tls`
(or in **Admin → Settings → HTTPS & TLS**, where it can be switched while evrd
runs):

| `tls` | |
|---|---|
| `acme` | Let's Encrypt: evrd gets a certificate for each name in `acme_hosts` (default: `public_url`'s host and `irc_host`) on first use and renews it 30 days before it expires. Picking it accepts the CA's subscriber agreement. |
| `selfsigned` | made once and kept. Browsers and IRC clients ask you to accept it. |
| `files` | `tls_cert` and `tls_key` (PEM, with the chain), from certbot, acme-client or your own CA. Read at startup and again whenever the file changes (not from inside a chroot: restart evrd after renewing there). |

Certificates evrd gets or makes (and the ACME account) are kept in the
database, encrypted with `secret_key`, so every node of a cluster serves the
same ones and nothing is written to disk.

**Who serves HTTPS** for the web app is your choice:

- **evrd itself** — set `https_listen :443` and `http_listen :80`, and
  `web_root` to the built web app. No proxy or web server is needed; port 80
  redirects to HTTPS and answers Let's Encrypt's `http-01` challenge (443
  answers `tls-alpn-01`). For Let's Encrypt, the names must resolve to this
  machine and both ports must be reachable.

  ```
  public_url   https://bnc.example.org
  irc_host     bnc.example.org
  https_listen :443
  http_listen  :80
  web_root     /var/lib/evrd/web
  tls          acme
  acme_email   ops@example.org
  ```

- **a proxy in front** (nginx, Caddy, Traefik, relayd, a cloud load balancer)
  — leave `https_listen` unset; the proxy terminates TLS and forwards to
  `api`. evrd's certificate then only matters for the IRC TLS ports.

The web app has no inline scripts, so a proxy that sets a strict
Content-Security-Policy on every site (`script-src 'self'`, no
`'unsafe-inline'`) doesn't break it; it only needs `connect-src 'self'` for
its API and WebSocket.

The settings page says which of the two is in use and shows each
certificate's issuer, names and expiry (or why getting one failed).

## Confinement (chroot and user)

```
chroot /var/evrd
user   _evrd
```

Started as root, evrd binds its ports (so 80 and 443 work), reads its config,
`secret_key_file` and `tls_cert`, then chroots into `chroot`, switches to
`user`, and from then on runs unprivileged. On OpenBSD it also `unveil`s only
its data, the web app and name resolution, and `pledge`s
`stdio rpath wpath cpath fattr flock inet dns`: no exec, no other files.

Local paths (`db sqlite:…`, `history pebble:…`, `web_root`) must be under the
chroot; write them as seen from outside (`sqlite:/var/evrd/data/evrd.db`) and
evrd translates them. Name resolution inside the chroot needs `etc/resolv.conf`
and `etc/hosts` there (the OpenBSD rc.d script copies them at every start).
`settings_write_conf` is turned off, since the config file is outside.
`evrctl`, run as root with the same config, switches to `user` too, so files
it creates stay evrd's.

The [OpenBSD installer](openbsd.md) sets all of this up.

## Provisioning API

For tools that bring users in from elsewhere (IRCForever's managebot moves
ZNC users over with it), evrd can create an account and its networks before
the owner ever signs in. It's off until `provision_token` is set; every call
sends that token:

```sh
curl -X PUT http://127.0.0.1:8098/api/provision/users/alice \
  -H "Authorization: Bearer $EVRD_PROVISION_TOKEN" \
  -d '{"email": "alice@example.org", "connect": true,
       "networks": [{"name": "libera", "host": "irc.libera.chat", "port": 6697, "tls": true,
                     "nick": "alice", "channels": ["#evrd"], "saslUser": "alice", "password": "…"}]}'
```

Networks take the same fields as the web app's network editor (`servers`,
`nick`, `altNick`, `username`, `realname`, `saslUser`, `password`,
`channels`, `channelSettings`, `trustAllCerts`, `fingerprints`, …). The
answer lists each network as `created`, `exists` (that name is already there,
so a call can be repeated) or `failed` with the reason. `connect` connects every
listed network, new or already there — so a migration can create everything
first, take the old bouncer off IRC, then call again with `connect` (no nick
clash in between).

A new account has no password and is **claimable**: the first SSO sign-in
with the same username *and* email links to it — so the provider account must
be the owner's, not a look-alike. After that it's an ordinary SSO account.
Keep the token secret and the API on a local address (or behind your proxy
with the token); anyone with it can create accounts.

## Saved passwords

Network passwords (the SASL password and server passwords), the values
set in Admin → Settings (such as the SSO client secret) and the keys of the
certificates evrd keeps are encrypted in the
database with AES-256-GCM under a key that lives outside it:

```
secret_key      <32 bytes, base64>   # or EVRD_SECRET_KEY, or secret_key_file /path
secret_key_old  <previous key>       # optional: still decrypts while rotating
```

`evrctl genkey` makes a key. Every node needs the same one; keep it out of
database backups. At startup evrd encrypts anything saved before a key was
set and re-encrypts anything under an old key — network passwords and
settings alike — so rotating is: set the new
`secret_key`, move the old one to `secret_key_old`, restart, then drop it.
Without a key evrd still runs and warns that these are stored in plaintext.
Account passwords and client passwords are bcrypt hashes either way.

## Metrics

evrd exports OpenTelemetry metrics over OTLP/HTTP and reads them back from
VictoriaMetrics for the dashboard's traffic chart and health numbers:

```
otel_endpoint http://victoriametrics:8428/opentelemetry/v1/metrics
otel_interval 15s
metrics_url   http://victoriametrics:8428   # PromQL API the dashboard queries
```

(`OTEL_EXPORTER_OTLP_METRICS_ENDPOINT` and `EVRD_METRICS_URL` work too.) Run
VictoriaMetrics with `-opentelemetry.usePrometheusNaming` so names come out as
`evrd_irc_lines_total{user,network,direction}`, `evrd_upstream_up`,
`evrd_upstream_lag_seconds`, `evrd_upstream_disconnects_total`,
`evrd_irc_clients`, `evrd_web_streams`, `evrd_cluster_users`,
`evrd_history_queue` and friends (`user`/`network` are ids). Without
`metrics_url` the dashboard still works; the chart says metrics are off.
