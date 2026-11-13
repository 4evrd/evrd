# Updates

evrd tells admins when a new release is out, and can install it from the
web app: no `docker compose pull`, no recreating containers.

## The notice

evrd reads the [release feed](#configuration) at start and twice a day. When
there's a newer release, *Admin* says so (with a link to what's new) and
*Admin → Settings → Updates* shows the running version, the latest and when
it was last checked, with *Check now*. The check sends nothing about the
install: it's a plain request for the latest release.

## Installing from the web app

*Install* (an admin, after a confirmation):

1. downloads this platform's release archive (`evrd_<os>_<arch>.tar.gz`, the
   same as for [installing without Docker](../README.md#running-without-docker))
   and the release's `SHA256` file, and refuses an archive whose checksum
   doesn't match;
2. unpacks `evrd`, `evrctl` and the web app into the update directory
   (`update_dir`, `/var/lib/evrd/updates` in the image — the data volume),
   and checks the new `evrd -version` runs and is the release's version;
3. restarts into it. Users on IRC stay connected when the
   [connection holder](configuration.md#connection-holder) runs (it keeps
   the networks and the IRC clients while evrd restarts); the web app
   reconnects by itself and reloads into the new version.

A failed download or check leaves the running version alone, and the panel
says what went wrong.

How evrd restarts depends on how it runs:

| evrd | Restart |
|---|---|
| as the image's user (the shipped compose file), or anything not switching user | in place: evrd stops, then runs the new program in its own process. The container doesn't restart. |
| started as root with `user` (to bind ports below 1024), in Docker | evrd exits (status 75) and Docker's restart policy (`restart: unless-stopped`) starts the container again |
| with `chroot`, or sandboxed on OpenBSD | not offered: upgrade the usual way ([OpenBSD](openbsd.md#upgrading)) |

Every start then runs the installed update instead of the image's program,
so it survives container restarts, and `evrctl` (e.g. `docker compose exec
evrd evrctl …`) is the new version too. The update was installed over a
particular image: start a different one (`docker compose pull` of a newer
release, or a pinned older tag) and that image runs, and the update is
cleared. `EVRD_VERSION` in `.env` still says what the image is; *Admin →
Settings → Updates* says when an update runs over it.

**The web app:** the web image's nginx asks evrd for the web app first
(`/api/webapp/`), so after an update it serves the update's, and its own
copy otherwise (or while evrd restarts). Web images from before this need
pulling once for that.

**evrd-links** isn't updated this way: restarting it would drop every IRC
connection, so it stays the image's until the image is upgraded (it rarely
changes).

**In a cluster**, an install is recorded in the database and every node
installs it within a minute, each restarting as above.

## Configuration

| Directive | Default | |
|---|---|---|
| `update_check` (`EVRD_UPDATE_CHECK`) | `https://api.github.com/repos/4evrd/evrd/releases/latest` | the release feed (GitHub's latest-release JSON); `off` for no check. Editable in *Admin → Settings*. |
| `self_update` (`EVRD_SELF_UPDATE`) | `yes` | admins can install from the web app. `no`: the notice only. Editable in *Admin → Settings*. |
| `update_dir` (`EVRD_UPDATE_DIR`) | `updates` (under the working directory) | where updates are unpacked; must be writable by evrd's user |

To go back to the image's own version: delete the update directory (or its
`current.json`) and restart.

The archive is checked against the release's own checksums, which guards
against a broken or truncated download, but both come from the same place:
it trusts the release feed and GitHub the way `docker pull` trusts the
registry.
