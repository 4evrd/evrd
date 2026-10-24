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
| `modules_off` | — | [modules](modules.md) nobody on this server can use, space-separated |
| `network_directory` (`EVRD_NETWORK_DIRECTORY`) | `https://evrd.sh/api/networks/v1.json` | the [network directory](network-directory.md) users pick from when adding a network: a URL, a local file, or `off` |
| `ident_file` (`EVRD_IDENT_FILE`) | — | an oidentd user config evrd keeps up to date so networks see users' idents, not evrd's system user; see [Idents](openbsd.md#idents-oidentd) |
| `identd_listen` (`EVRD_IDENTD_LISTEN`) | — | evrd answers ident queries (RFC 1413) itself, e.g. `:113`; see [Ident](#ident-identd) |
| `links_socket` (`EVRD_LINKS_SOCKET`) | — | the [connection holder](#connection-holder)'s socket: IRC connections stay up while evrd restarts (one node only) |
| `hold_clients` | `yes` | with the holder, keep IRC clients connected through evrd restarts too ([connection holder](#connection-holder)) |
| `provision_token` (`EVRD_PROVISION_TOKEN`) | — | turns on the [provisioning API](#provisioning-api) for tools that create accounts |
| `db` (`EVRD_DB`) | `sqlite:evrd.db` | accounts and settings: `postgres://user:pass@host/evrd?sslmode=disable` or `sqlite:/path/evrd.db` |
| `history` (`EVRD_HISTORY`) | `pebble:history` | chat history: `pebble:/var/lib/evrd/history` (one node) or `scylla://host1,host2:9042/evrd` (shared) |
| `secret_key` (`EVRD_SECRET_KEY`), `secret_key_file`, `secret_key_old` | — | encrypts saved network passwords and module settings; see [Saved passwords](#saved-passwords) |
| `public_url` | — | the web app's address, e.g. `https://bnc.example.org` (SSO redirects back here) |
| `irc_host` | `localhost` | the host name users give their IRC clients (shown in the web app) |
| `irc_sts` | `24h` | how long IRC clients are told to connect over TLS only (IRCv3 `sts`), or `off`; offered only with an `ircs://` listener and a certificate clients trust for `irc_host` — see [STS](#sts-tls-only-for-irc-clients) |
| `password_login` | `yes` | allow password sign-in to the web app; `no` leaves only single sign-on (Admin → Settings refuses it until SSO is set up) |
| `session_ttl` | `720h` | how long a web sign-in lasts |
| `trusted_proxies` (`EVRD_TRUSTED_PROXIES`) | the local host | the proxies whose `X-Forwarded-For` is believed: addresses or CIDR ranges, space-separated (file or environment only). From anywhere else the connection's own address counts — it's what each user's allowed hosts are checked against. Set it to the proxy's network when the proxy isn't on the same machine (e.g. Docker: `172.16.0.0/12`) |
| `cookie_secure` | `yes` | HTTPS-only session cookie; `no` only for plain-HTTP testing |
| `oidc_issuer`, `oidc_client_id`, `oidc_client_secret` (`EVRD_OIDC_CLIENT_SECRET`) | — | single sign-on with any OpenID Connect provider |
| `oidc_label`, `oidc_auto_create`, `oidc_link_existing`, `oidc_admin_role` | `single sign-on`, `yes`, `no`, — | sign-in button text and account rules for SSO |
| `oidc_roles_claim`, `oidc_scopes` | `roles`, — | where the roles for `oidc_admin_role` are in the ID token (a claim, or a dotted path), and extra scopes to ask for; see [providers](#oidc-providers) |
| `irc_link_signin`, `irc_remember_days` | `yes` (with `public_url`), `30` | IRC clients without a usable password get a link to allow them on the website, remembering the device for that many days; see [signing in by link](irc-clients.md#signing-in-by-link) |
| `znc_password_days` | `90` | how long an account made from a ZNC config keeps its unused ZNC password; see [exporting and importing networks](#exporting-and-importing-networks) |
| `irc_mfa`, `irc_mfa_client_passwords` | `optional`, `no` | a second factor (authenticator app) for IRC sign-ins with a password: `off`, `optional` (each user's choice on *Account → Security*) or `required` (everyone; users can't turn it off); `irc_mfa_client_passwords` asks for it with IRC client passwords too. See [the IRC clients guide](irc-clients.md#a-second-factor) |
| `sso_irc_password` | `off` | let SSO accounts use their provider password in IRC clients: `password_grant`, `ldap` or `logto`, each with settings of its own; see [SSO passwords in IRC clients](#sso-passwords-in-irc-clients) |
| `max_networks`, `max_clients`, `backlog_lines` | `0`, `0`, `2000` | per-user defaults (0 = unlimited); admins override per user |
| `connect_rate` | `5` | most new connections per second to one IRC server host, per node |
| `client_ping_timeout` | `60s` | evrd pings an IRC client that has said nothing for 90 seconds and drops it when it doesn't answer within this long; `0` never pings |
| `prefer_ipv6` | `yes` | connect to IRC servers over IPv6 first, falling back to IPv4; `no` tries IPv4 first. With only IPv4 `upstream_bind` addresses, IPv4 goes first either way |
| `bindhost` | — | local addresses users may choose to connect from (repeatable) |
| `upstream_bind`, `upstream_prefix6` (`EVRD_UPSTREAM_BIND`, `EVRD_UPSTREAM_PREFIX6`) | — | spread users over source addresses; see [clustering](clustering.md#source-addresses-and-connect-pacing) |
| `otel_endpoint` (`OTEL_EXPORTER_OTLP_METRICS_ENDPOINT`), `otel_interval`, `metrics_url` (`EVRD_METRICS_URL`) | —, `15s`, — | metrics; see [Metrics](#metrics) |
| `otel_traces_endpoint` (`OTEL_EXPORTER_OTLP_TRACES_ENDPOINT`) | — | OTLP/HTTP traces URL (e.g. `http://jaeger:4318/v1/traces`): spans for web API requests and the services behind them |
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
`backlog_lines`, `connect_rate`, `client_ping_timeout`, `prefer_ipv6`, `bindhost`, `network_directory`, `tls`, `acme_email`, `acme_hosts`,
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
oidc_roles_claim  roles       # where those roles are in the ID token
# oidc_scopes     groups      # extra scopes (default: roles when oidc_admin_role is set)
```

### OIDC providers

Any OpenID Connect provider works. The redirect URI is always
`<public_url>/auth/callback`, the client is a confidential web app (code flow
with PKCE), and the ID token must be signed with an asymmetric key (RS256,
ES256…).

| Provider | `oidc_issuer` | Scopes and roles | IRC passwords (`sso_irc_password`) |
|---|---|---|---|
| Logto | `https://id.example.org/oidc` | roles come with the `roles` scope (asked for when `oidc_admin_role` is set), in `roles` | `logto` |
| authentik | `https://auth.example.org/application/o/<slug>/` | groups are in `groups` (in the `profile` scope): `oidc_roles_claim groups`. Pick a **signing key** for the provider — without one it signs with HS256, which evrd doesn't accept | `ldap`, through an LDAP outpost (its password grant takes app passwords, not the user's) |
| Keycloak | `https://kc.example.org/realms/<realm>` | realm roles: `oidc_roles_claim realm_access.roles`, with *Add to ID token* on for the *realm roles* mapper (client scope `roles`); client roles: `resource_access.<client>.roles` | `password_grant` (turn on *Direct access grants*), or `ldap` if it's backed by one |
| Others | the provider's issuer, as in its `/.well-known/openid-configuration` | `oidc_roles_claim` names the claim; `oidc_scopes` asks for what puts it in the ID token | see [SSO passwords in IRC clients](#sso-passwords-in-irc-clients) |

The roles claim may hold a list or one space-separated string; a missing one
(or one of another shape) means no roles.

### SSO passwords in IRC clients

An IRC client can't go through the provider's sign-in page, and OpenID Connect
itself can't check a password. The recommended way for SSO accounts is
**[signing in by link](irc-clients.md#signing-in-by-link)** (on by default with
`public_url`): it works with any provider and its MFA, and needs no settings
here. **IRC client passwords** work too. `sso_irc_password` lets it use its provider password too,
checked one of three ways. All of these are in *Admin → Settings → SSO
passwords in IRC* too, and apply at once.

| Your identity provider | `sso_irc_password` | Notes |
|---|---|---|
| Keycloak | `password_grant` | turn on *Direct access grants* for the client |
| Okta | `password_grant` | allow the *Resource Owner Password* grant for the app |
| Auth0 | `password_grant` | allow the *Password* grant and set a default directory |
| Microsoft Entra ID | `password_grant` | ROPC: cloud accounts only, not federated ones or ones MFA is enforced for |
| Dex | `password_grant` | `oauth2.passwordConnector` set |
| Logto | `logto` | no password grant: evrd asks its Management API |
| authentik | `ldap` | its password grant only takes app passwords: use an LDAP outpost |
| FreeIPA, OpenLDAP, Active Directory, LLDAP (behind any OIDC provider) | `ldap` | bind as the user |
| Google, GitHub, Zitadel, … | — | no way to check a password: IRC client passwords |

**`password_grant`** — OAuth2's resource-owner password grant at the OIDC
provider's token endpoint (found from `oidc_issuer`), with `scope=openid`. The
answer's ID token is checked like a web sign-in's, and its subject must be the
account's: someone else's right password doesn't sign in to this account.

```
sso_irc_password           password_grant
# the sign-in client (oidc_client_id) by default; or one of its own:
# sso_password_client_id     evrd-irc
# sso_password_client_secret …   (or EVRD_SSO_PASSWORD_CLIENT_SECRET)
sso_password_login         username    # or email: what the provider knows the user by
```

**`ldap`** — a bind to the directory as the user. TLS is required: `ldaps://`,
or `ldap://` with StartTLS (`ldap_insecure yes` sends passwords in the clear;
only on a trusted local network). The user's DN comes from a template, or a
search:

```
sso_irc_password   ldap
ldap_url           ldaps://ldap.example.org
# a template…
ldap_user_dn       uid={username},ou=people,dc=example,dc=org
# …or a search (as ldap_bind_dn, or anonymous), which must find one entry
# ldap_bind_dn       cn=evrd,ou=services,dc=example,dc=org
# ldap_bind_password …   (or EVRD_LDAP_BIND_PASSWORD)
# ldap_base_dn       ou=people,dc=example,dc=org
# ldap_user_filter   (&(objectClass=person)(uid={username}))   # AD: (sAMAccountName={username})
ldap_login         username   # or subject: {username} is the account's SSO subject
```

`{username}` is escaped (for a DN or a filter), so a user name can't change
what's bound or searched for, and an empty password is refused before it gets
to the directory (it would be an anonymous bind). Active Directory's "account
disabled" and "locked out" are told apart from a wrong password.

**`logto`** — Logto has no password grant, so evrd asks its Management API as a
machine-to-machine app:

```
sso_irc_password         logto
logto_m2m_client_id      <machine-to-machine app id>
# secret: logto_m2m_client_secret, or the EVRD_LOGTO_M2M_CLIENT_SECRET env var
# logto_m2m_resource     https://default.logto.app/api   # only if the default is wrong
```

In Logto, create an application of type **Machine-to-machine** and give it a
role with access to the **Logto Management API** (*API resources → Logto
Management API*; the `all` permission). Its ID and secret go above. The
resource defaults to `https://<tenant>.logto.app/api` for a Logto Cloud issuer
and `https://default.logto.app/api` for a Logto of your own; set
`logto_m2m_resource` if yours is different (a custom domain on Logto Cloud).
evrd looks the user up first: one **suspended in Logto** is refused, and one
with **no password** there (social sign-in only) is told to set one or use a
client password.

However it's checked:

- The order is: IRC client password, then the evrd password, then the SSO
  password. A local account (not linked to SSO) is never sent to the provider.
- A right password is remembered for 10 minutes (only a keyed hash of it, in
  memory), so a client reconnecting doesn't ask the provider each time. A wrong
  one is never remembered.
- Failed sign-ins count toward the same lockout as the web sign-in: 10 within
  15 minutes and the account is refused for a while, over IRC and on the web.
- If the provider can't be reached (or is set up wrong — the server log says
  how) the sign-in is refused, never let through, with "can't check your SSO
  password right now"; client passwords keep working. That doesn't count toward
  the lockout.
- The activity feed says a client signed in with the SSO password, and how it
  was checked.

Keep the secrets (`sso_password_client_secret`, `ldap_bind_password`,
`logto_m2m_client_secret` — Logto's app can read and change every user) in the
environment variables or *Settings*, where they're stored encrypted when
`secret_key` is set, not in a world-readable file.

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
- **Authentication** — how evrd logs in to services: SASL PLAIN, IDENTIFY to
  NickServ (or `NickServ@services.dal.net`…), a command of your own (Undernet's
  X), or none; presets for common networks; GHOST/REGAIN a taken nick; join
  channels only once identified. See [Logging in to services](services.md).
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
what arrived since an IRC client was last on that network
(AutoClearChanBuffer / AutoClearQueryBuffer; `*status` `setbuffer` and
`autoclear` change them per network or per channel); timestamps prepended/appended for clients without
server-time (strftime format, time zone); fixed CTCP replies (an empty reply
blocks the request; with no client attached evrd answers VERSION, PING and
TIME itself); and the addresses allowed to sign in, web and IRC.

`*status` has the same: `listservers`, `addserver`, `delserver`,
`listfingerprints`, `addfingerprint`, `delfingerprint` (and ZNC's
`AddTrustedServerFingerprint` names), `detach`, and `set` for altnick,
sasluser, auth, servicesnick, identifyargs, identifyon, authcommand, ghost,
joinafterauth, authtimeout, bindhost, quitmsg, encoding, trustallcerts,
trustpki, floodrate, floodburst, joindelay.

## Exporting and importing networks

**Networks → Export** downloads your networks as a YAML file: servers,
identity, services login, certificates, channels, modules and each buffer's
playback settings. **Networks → Import** adds networks from one of these, or
from a ZNC config: a whole `znc.conf`, or just the `<User>` or `<Network>`
blocks you paste in. When a ZNC config has several users, you pick whose
networks to bring in, then tick the networks. A network you already have
(same name) is left alone, merged with the file's (its settings kept, the
channels and servers it lacks added, empty fields filled in), or updated
from the file, as you choose; its history stays either way. Imported networks connect only if you ask, so ZNC and evrd
don't fight over your nick while both are up.

Passwords never go out as plaintext. Server and services passwords, the
services login command, channel keys and module settings are written
encrypted with the server's `secret_key` (`enc:v1:…`), and the key isn't in
the file. They come back on the same server, or on another one with the same
key. Anywhere else they're left empty, and the import says which ones to set
again. Without a `secret_key` they're left out of the export.

From ZNC, the config has the servers (and their passwords), nicks, channels
(keys, detached, disabled, buffer sizes), certificate fingerprints, flood
settings and `IRCConnectEnabled`. `keepnick`, `kickrejoin`, `autocycle` and
`ctcpflood` come over as evrd's modules. `sasl` and `nickserv` set the
login method, but ZNC keeps their passwords outside the config (as it does
`perform`'s commands and `autoop`'s list), so the import tells you what to
fill in. For old logs, see `evrctl history import-znc`.

Admins can do the same for any account (Admin → Users → a user →
**Import**, and **Export** beside it), with an admin's say: a bind host
comes over though it isn't on the `bind_hosts` list, and the account's
network limit doesn't apply.

**Admin → Users → Import from ZNC** brings a whole ZNC server over. Each
`<User>` in its config goes to an evrd account: the same name by default
(change it to put a ZNC user somewhere else, or several into one account).
An account that doesn't exist yet is made, and

- it keeps the user's ZNC password. evrd stores ZNC's hash (salted SHA-256
  or MD5, Argon2id, or a plaintext `Pass` hashed on the way in), checks it
  at the first sign-in, web or IRC client, and replaces it with its own
  bcrypt hash then. The hash is never shown or exported. One no one signs
  in with is cleared after `znc_password_days` (90 by default, Admin →
  Settings → Sign-in), so ZNC's weaker hashes don't stay around. A ZNC
  password shorter than evrd's minimum still works, but must be changed at
  the next web sign-in.
- with an email, it's also claimable, as with the
  [provisioning API](#provisioning-api): the owner's first SSO sign-in with
  the same username and email takes it over.

An account that already exists keeps its own password. Then each user's
networks are imported into their account, as above.

A file written by hand works too. Fields left out keep what the network
already has (or the defaults), and passwords may be given in plaintext, to
be encrypted once imported:

```yaml
evrd_networks: 1
networks:
  - name: libera
    servers:
      - host: irc.libera.chat   # port 6697 and TLS unless given
    nick: alice
    login: {method: sasl, account: alice, password: "…"}
    channels:
      - name: "#evrd"
      - name: "#secret"
        key: "…"
        playback: {lines: 500}
```

## HTTPS and certificates

evrd's own TLS ports — the `ircs://` IRC listeners, and `https_listen` when
evrd serves the web app itself — share one certificate source, set with `tls`
(or in **Admin → Settings → HTTPS & TLS**, where it can be switched while evrd
runs):

| `tls` | |
|---|---|
| `acme` | Let's Encrypt: evrd gets a certificate for each name in `acme_hosts` (default: `public_url`'s host and `irc_host`) on first use and renews it 30 days before it expires. Picking it accepts the CA's subscriber agreement. |
| `selfsigned` | made once and kept. Browsers and IRC clients ask you to accept it. |
| `files` | `tls_cert` and `tls_key` (PEM, with the chain), from certbot, acme-client or your own CA. Read at startup, again whenever the file changes, and on `SIGHUP` (`rcctl reload evrd`), with no restart. In a chroot, keep a copy inside it and point these at the copy, as the OpenBSD installer does. |

Certificates evrd gets or makes (and the ACME account) are kept in the
database, encrypted with `secret_key`, so every node of a cluster serves the
same ones and nothing is written to disk.

### STS: TLS only for IRC clients

With an `ircs://` listener and a certificate clients trust for `irc_host`
(Let's Encrypt, or `files` from a public CA — never `selfsigned`), evrd offers
IRC clients the IRCv3 `sts` capability: a client on a plain port is told the
TLS port, and one on TLS how long to keep to it (`irc_sts`, a day by
default). Clients that support it (Goguma, gamja, Halloy, Senpai, WeeChat…)
then connect with TLS only, and refuse a plain connection until that time
runs out.

That's the lock-in: once a client has seen it, taking TLS away — removing the
`ircs://` listener, or a certificate that expires or no longer matches — locks
it out until the duration passes. Keep the duration modest (the default is a
day), and set `irc_sts off` first, and wait that long, before removing TLS.
evrd checks the certificate every few minutes and stops offering `sts` as
soon as it isn't one clients would trust.

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
`stdio rpath wpath cpath fattr flock inet dns` (plus `unix sendfd` with
`links_socket`): no exec, no other files.

Local paths (`db sqlite:…`, `history pebble:…`, `web_root`) must be under the
chroot; write them as seen from outside (`sqlite:/var/evrd/data/evrd.db`) and
evrd translates them. Name resolution inside the chroot needs `etc/resolv.conf`
and `etc/hosts` there (the OpenBSD rc.d script copies them at every start).
`settings_write_conf` is turned off, since the config file is outside.
`evrctl`, run as root with the same config, switches to `user` too, so files
it creates stay evrd's.

The [OpenBSD installer](openbsd.md) sets all of this up.

## Connection holder

```
links_socket /var/evrd/run/links.sock
```

`evrd-links` keeps users' IRC connections open while evrd restarts. Run it
first, as root, with the same chroot and user:

```
evrd-links -socket /var/evrd/run/links.sock -chroot /var/evrd -user _evrd
```

evrd opens each connection to an IRC network itself and hands the socket to
the holder, which does TLS and passes lines both ways. When evrd stops — to
upgrade, or for a setting that needs a restart — the holder keeps the
connections: it answers the servers' pings and buffers what arrives (4 MB per
network; `-buffer` changes it). The next evrd takes each one back without
registering or joining again, and replays the buffered lines into history.
Switching a network off, or deleting it, still quits it.

The holder keeps evrd's IRC clients connected too. evrd hands it the IRC
listeners (`listen irc…://` and `ircs://`, with the certificate for TLS ones)
and the holder accepts the clients, so an evrd restart is invisible to them:
no disconnect, no second welcome, no rejoining. While evrd is away the holder
answers their pings and keeps what they send (64 KiB each); the next evrd
picks each one up where the last left it, and what the networks said
meanwhile reaches them as it's replayed, with its real time. Clients that
connect while evrd is down wait for it. A client evrd isn't back for within
2 minutes is told to reconnect. `hold_clients no` turns this off: evrd
listens itself, and its restarts disconnect clients as before. An older
holder (protocol 1) holds no clients; evrd then listens itself, and
restarting the holder brings it in (`evrd-links -version` says which).

Without the holder (not running, or `links_socket` unset), evrd connects
directly, as before. Restarting the holder itself drops the connections, so it
is built to change rarely; the OpenBSD installer restarts it only when its
program changed. In Docker, run evrd in the holder's network namespace
(`network_mode: "service:links"`) so the connections outlive evrd's container.
The holder is for a single node: in a [cluster](clustering.md), users move
between nodes instead, and `links_socket` is ignored.

## Ident (identd)

Some IRC networks ask each connection's host who it is (ident, TCP port 113),
and a few refuse connections that don't answer (EFnet's "No Ident response").
With `identd_listen`, evrd answers those queries itself, with the ident each
network registers with (its *username*, else the nick):

```
identd_listen :113
```

A server asks while the connection registers, from the address evrd dialed.
evrd matches the query to the connection by that address and the two ports;
when NAT has changed evrd's port, the one connection to that server still
registering answers. If it can't tell connections apart it answers `NO-USER`
rather than guess. Each node of a [cluster](clustering.md) answers for its
own connections.

### In Docker

A host identd can't see a container's connections, so let evrd answer. The
image runs evrd as a normal user, which can't bind port 113: listen on a high
port in the container and map 113 to it on the host:

```
identd_listen :1113
```

```yaml
  links:                # the service that owns the network namespace (or evrd's own)
    ports:
      - "113:1113"
  evrd:
    network_mode: "service:links"
```

Publish the port on the service that owns the network namespace: with the
[connection holder](#connection-holder), evrd shares the holder's
(`network_mode: "service:links"`), so the mapping goes on `links`. Behind a
router, forward public TCP 113 to the host too.

On a host where evrd runs natively, like the OpenBSD install, oidentd with
[`ident_file`](openbsd.md#idents-oidentd) works instead (or `identd_listen
:113`, bound before evrd drops root).

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
`authMethod` and the other [services login](services.md) fields,
`channels`, `channelSettings`, `trustAllCerts`, `fingerprints`, …). The
answer lists each network as `created`, `exists` (that name is already there,
so a call can be repeated) or `failed` with the reason. `connect` connects every
listed network, new or already there — so a migration can create everything
first, take the old bouncer off IRC, then call again with `connect` (no nick
clash in between).

`bindHost` is taken as given, as an admin may set one: a ZNC user's vhost
comes over though it isn't on the `bind_hosts` list. On a network that's
already there, a `bindHost` in the call replaces its bind host (reconnecting
it if it's up), so a repeat can fill one in.

A new account has no password and is **claimable**: the first SSO sign-in
with the same username *and* email links to it — so the provider account must
be the owner's, not a look-alike. After that it's an ordinary SSO account.
Keep the token secret and the API on a local address (or behind your proxy
with the token); anyone with it can create accounts.

## Saved passwords

Network passwords (the SASL password and server passwords), a network's
services login command, its module settings (perform's commands can carry an
`OPER` or `IDENTIFY` password), the values set in Admin → Settings (such as
the SSO client secret) and the keys of the certificates evrd keeps are
encrypted in the database with AES-256-GCM under a key that lives outside it:

```
secret_key      <32 bytes, base64>   # or EVRD_SECRET_KEY, or secret_key_file /path
secret_key_old  <previous key>       # optional: still decrypts while rotating
```

`evrctl genkey` makes a key. Every node needs the same one; keep it out of
database backups. At startup evrd encrypts anything saved before a key was
set and re-encrypts anything under an old key — network passwords, module
settings and settings alike — so rotating is: set the new
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

Admins see every metric under Admin → Metrics. For Grafana there's a
ready-made dashboard, `deploy/grafana/evrd.json` (a VictoriaMetrics or
Prometheus data source with uid `victoriametrics`; change it on import):
traffic, upstream links and lag, clients, sign-ins, users per node and the
versions running.

If the metrics store sits behind a proxy with basic auth, put the
credentials in both URLs (`https://user:pass@vm.example.org/opentelemetry/v1/metrics`,
`https://user:pass@vm.example.org`): evrd sends them as basic auth, and
Admin → Settings shows the password masked.
