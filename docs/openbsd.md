# Installing on OpenBSD

evrd runs natively on OpenBSD (amd64 and arm64), without Docker. One script
installs it as a confined system daemon: chrooted, running as its own user,
unveiled and pledged, with SQLite and an embedded history store, so there's
no database server to run.

## Install

As root, on a current OpenBSD (tested on 7.9):

```sh
ftp https://raw.githubusercontent.com/4evrd/evrd/main/deploy/openbsd/install.sh
sh install.sh
```

It downloads the latest release for your machine, checks it against the
release's `SHA256` file, and asks:

| Question | Default | |
|---|---|---|
| Host name for the web app | the machine's name | e.g. `bnc.example.org` |
| Certificate | `letsencrypt` for a public name, else `selfsigned` | `letsencrypt` (automatic, renewed), `selfsigned`, or `none` (plain HTTP, for trying it out) |
| Who serves HTTPS | `evrd` | `evrd` itself on 443 (and 80), or `relayd` in front |
| HTTPS port | `443` | with `evrd` |
| Let's Encrypt email | — | optional, for expiry notices |
| Host name IRC clients connect to | the web host | |
| IRC port (TLS) | `6697` | |
| Plain-text IRC port | none | e.g. `6667` |
| Admin username and password | — | the first admin account |

Then it starts evrd and prints the web app's address. Open the ports it lists
in pf — `/usr/local/share/examples/evrd/pf.conf` has the rules:

```
pass in on egress proto tcp to port { 80 443 }	# the web app
pass in on egress proto tcp to port { 6697 }	# IRC clients
```

For Let's Encrypt, the host names must already resolve to this machine and
ports 80 and 443 must be reachable from the internet. Until the first
certificate arrives, its status (or the CA's error) shows in the web app
under **Admin → Settings → HTTPS & TLS**.

### Without questions

Every answer has an option, and `-y` takes the defaults for the rest — for
provisioning scripts:

```sh
EVRD_ADMIN_PASSWORD='a-long-password' sh install.sh -y \
    -d bnc.example.org -c letsencrypt -w evrd -e ops@example.org -a yourname
```

| Option | |
|---|---|
| `-d host` | the web app's host name |
| `-c letsencrypt\|selfsigned\|none` | the certificate |
| `-w evrd\|relayd` | who serves HTTPS |
| `-s port` | evrd's HTTPS port (default 443) |
| `-p port` | evrd's plain-HTTP port: the web app with `-c none`, else local only (default 8098) |
| `-e email` | Let's Encrypt contact |
| `-i host`, `-t port`, `-P port` | IRC host name, TLS port, plain port |
| `-a name` | create an admin (password from `$EVRD_ADMIN_PASSWORD`, or asked) |
| `-v version` | a specific release instead of the latest |
| `-f archive` | install from a downloaded `evrd_openbsd_<arch>.tar.gz` (with its `SHA256` next to it) |
| `-r` | write a new config (the old one is kept as `evrd.conf.bak`) |
| `-n` | install but don't start |

## What it sets up

| | |
|---|---|
| `/usr/local/bin/evrd`, `evrctl` | the daemon and the admin tool |
| `/etc/evrd/evrd.conf` | the config (`root:_evrd`, 0640) |
| `/etc/evrd/secret.key` | encrypts saved network passwords and certificate keys — **back it up**, apart from the data |
| `/var/evrd/` | the chroot: `data/` (database and history, `_evrd` only), `web/` (the web app, read-only), `etc/` (resolv.conf and hosts) |
| `/etc/rc.d/evrd` | `rcctl start\|stop\|restart evrd`; logs go to `/var/log/daemon` |
| `_evrd` | the user evrd runs as (no shell, no home) |
| `/usr/local/share/examples/evrd/` | relayd, httpd, acme-client and pf examples for your host names |

### How evrd is confined

rc.d starts evrd as root. It binds its ports, reads `evrd.conf`, the secret
key and any certificate files, then:

1. **chroots** to `/var/evrd` — the rest of the filesystem is gone;
2. becomes **`_evrd`** — no root, and the chroot itself and everything but
   `data/` and `tmp/` belong to root, so evrd can't change its own web app or
   name resolution;
3. **unveils** only `data/`, `tmp/`, `web/` (read-only) and `etc/resolv.conf`,
   `etc/hosts` (read-only);
4. **pledges** `stdio rpath wpath cpath fattr flock inet dns` — it can't
   execute anything, change permissions beyond its files, or open other
   kinds of sockets.

Check it with `ps -o user,pledge -p $(pgrep -x evrd)` and
`fstat -p $(pgrep -x evrd)` (its root directory is `/var/evrd`).

## HTTPS: evrd or relayd

**evrd** (the default) serves the web app on 443 and redirects port 80, and
gets its Let's Encrypt certificate itself — no other daemon. The certificate
can later be switched (Let's Encrypt, self-signed, or files) in the web app.

**relayd** suits a machine that serves other sites on 443 as well. The
installer then:

- writes `/etc/httpd.conf` for Let's Encrypt's challenges on port 80,
  `/etc/acme-client.conf` and `/etc/relayd.conf` — only where none exists
  (otherwise it says what to merge from the examples);
- runs `acme-client` and adds renewal to root's crontab:
  `acme-client <host> && rcctl reload relayd && rcctl restart evrd`;
- points evrd's IRC port at the same certificate (`tls_cert`).

relayd terminates TLS and forwards everything, including the WebSocket
(`http websockets`), to evrd on 127.0.0.1:8098.

## Day to day

```sh
rcctl restart evrd                       # after editing evrd.conf
tail -f /var/log/daemon | grep evrd      # logs
evrctl -config /etc/evrd/evrd.conf user add -admin alice -   # password on stdin
evrctl -config /etc/evrd/evrd.conf user list
```

`evrctl` run as root switches to `_evrd` before touching the database.

## Upgrading

Run the installer again. It keeps `evrd.conf` and the key (it asks; `-y`
keeps them), replaces the programs and the web app, and restarts evrd.
Database changes are applied at startup. Pin a version with `-v 0.4.0`.

## Backups

Stop evrd (`rcctl stop evrd`), copy `/var/evrd/data/`, start it again. Keep
`/etc/evrd/secret.key` somewhere else: without it the saved network passwords
in a backup can't be read — which is the point.

## Uninstalling

```sh
rcctl stop evrd && rcctl disable evrd
rm -rf /usr/local/bin/evrd /usr/local/bin/evrctl /etc/rc.d/evrd \
    /usr/local/share/examples/evrd
rm -rf /var/evrd /etc/evrd          # the data and the key: only if you're sure
userdel _evrd && groupdel _evrd
```
