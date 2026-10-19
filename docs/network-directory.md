# Network directory

When a user adds a network, evrd offers a searchable list of public IRC
networks: picking one fills in its servers (TLS where the network offers it)
and suggested channels, so only the nickname is left to type. *Enter details
manually* is still there for anything not listed. From an IRC client,
`/msg *status directory [search]` lists the same networks and
`/msg *status addnetwork <id>` adds one.

The list is the **network directory**, a JSON document published with the evrd
website at

    https://evrd.sh/api/networks/v1.json

(its JSON Schema: <https://evrd.sh/api/networks/v1.schema.json>; the source is
`api/networks/` in [4evrd.github.io](https://github.com/4evrd/4evrd.github.io) —
pull requests for new networks or changed servers are welcome).

## How evrd uses it

- evrd fetches it at startup and every 12 hours (an hour after a failure),
  sending `If-None-Match` / `If-Modified-Since`, so an unchanged list costs a
  `304`. Each fetch has a 30-second timeout and a 1 MiB cap.
- The last good copy is kept in the database, shared by every node: a restart,
  a new node or an outage of evrd.sh still has a list.
- A document that doesn't parse, has another format version or has no usable
  networks never replaces a good one. Single entries that don't check out
  (a bad host name, a duplicate id…) are left out and reported.
- *Admin → Settings → Network directory* shows the source, when it was last
  fetched, the last error and skipped entries, and has *Check now*.

## Configuration

```
network_directory https://evrd.sh/api/networks/v1.json   # the default
network_directory /etc/evrd/networks.json                # a file of your own
network_directory off                                    # no list
```

`network_directory` (`EVRD_NETWORK_DIRECTORY`) is also editable in *Admin →
Settings*, and a change applies at once on every node. A local file (a path or
`file://` URL) is read the same way and on the same schedule — use it to offer
your own list, e.g. with a private network on it, or to run without fetching
from the internet. With `chroot`, the file must be inside the chroot. HTTPS
fetches work in the OpenBSD sandbox (pledge `inet dns`; the CA certificates and
`/etc/resolv.conf` are read before confining).

## Format (version 1)

```json
{
  "$schema": "https://evrd.sh/api/networks/v1.schema.json",
  "version": 1,
  "updated": "2026-10-19T00:00:00Z",
  "networks": [
    {
      "id": "libera",
      "name": "Libera.Chat",
      "description": "Free and open-source software communities.",
      "homepage": "https://libera.chat",
      "servers": [
        { "host": "irc.libera.chat", "port": 6697, "tls": true },
        { "host": "irc.libera.chat", "port": 6667, "tls": false }
      ],
      "sasl": true,
      "services": "atheme",
      "channels": ["#libera"],
      "tags": ["foss", "community"],
      "region": "global"
    }
  ]
}
```

| Field | | |
|---|---|---|
| `version` | required | `1`. A breaking change gets a new path (`v2.json`), so older evrd keeps reading `v1.json`. |
| `updated` | | when the list last changed (RFC 3339) |
| `networks[].id` | required | short and stable: lowercase letters, digits and `-`, up to 32. It's the name a picked network gets, and what `addnetwork <id>` takes. Unique. |
| `name` | required | display name, up to 64 characters |
| `description`, `homepage` | | one line (up to 500 characters); an `http(s)` URL |
| `servers` | required | 1–16 `{host, port, tls}`, in order of preference. evrd saves the TLS ones when there are any (a plain-text fallback would quietly give up TLS), otherwise all of them. evrd verifies certificates, so list a TLS server only when its certificate is publicly trusted and matches the host name (EFnet's and IRCnet's don't, for their round-robin names). |
| `sasl` | | the network's services accept SASL logins |
| `services` | | which services run there, for login hints: `atheme`, `anope`, `x` (Undernet), `q` (QuakeNet), `ergo`, `none`… |
| `channels` | | channels to suggest joining, e.g. the help channel (up to 16) |
| `tags`, `region` | | for search: `foss`, `gaming`, `anime`…; `global`, `eu`, `us`… |

Fields evrd doesn't know are ignored, so the format can grow (login methods,
say) without breaking older instances: add optional fields, never change the
meaning of existing ones.
