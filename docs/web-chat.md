# A web chat on another site

Another site's web chat (say `https://chat.example.org`) can sign its users in
to evrd and use their bouncer: the user's own always-on connection, with
the nick evrd keeps logged in to services, the channels evrd keeps joined,
the history evrd saved, and the same live feed every other client of theirs
gets. The web chat speaks to evrd's API and chat socket, the same ones
evrd's own web app uses, with a **token** in place of the session cookie
(browsers don't send another site's cookies).

## Setting it up

An admin lists the sites under *Admin → Settings → Sign-in*, or in
`evrd.conf`:

| Setting | Default | |
|---|---|---|
| `web_chat_origins` | empty (none) | Page origins allowed, space-separated: scheme and host (and port), no path, e.g. `https://chat.example.org http://localhost:5180`. |
| `web_chat_network` | empty | The network (by name) the web chat is told to use when it doesn't ask for one. Empty: the user's network when they have just one. |
| `web_chat_days` | `30` | How long a web chat stays signed in unused (1–365). Each use starts the time again. |

Nothing else changes for evrd's own web app. Everything below is under
`/api/` and `/auth/`, which the `evrd-web` image's nginx already passes to
evrd (`/api/` with WebSocket upgrades).

## Signing in

1. The web chat makes a random **state** (16–128 of `A-Z a-z 0-9 . _ ~ -`),
   keeps it (`sessionStorage`), and sends the browser to

   ```
   https://bnc.example.org/auth/web-chat?return=<page to come back to>&state=<state>[&network=<name>]
   ```

   `return` is an absolute URL on one of `web_chat_origins` (any path and
   query). Anything else gets a plain `400` page and goes nowhere: evrd never
   redirects to a site that isn't listed.

2. A browser that isn't signed in to evrd signs in the way it always does:
   the sign-in page (password or single sign-on), or straight to the
   identity provider when single sign-on is the only way in
   (`password_login no`). Then it comes back.

3. evrd makes a token for this browser and sends it back to `return`, with
   everything in the URL **fragment** (never the query, so it isn't sent to
   the web chat's server or in a `Referer`):

   ```
   https://chat.example.org/chat/#token=…&state=…&user=mira&network=7&api=https%3A%2F%2Fbnc.example.org&expires=1795000000
   ```

   | | |
   |---|---|
   | `token` | the token (43 characters, base64url) |
   | `state` | the state the web chat sent: **check it** and ignore an answer whose state isn't the one you kept, or someone could sign the user in to *their* account |
   | `user` | the evrd user name |
   | `network` | the network to use, by id: the one asked for with `network=` (by name, any case), else `web_chat_network`, else the user's only network; empty when none fits (list them with `GET /api/networks`) |
   | `api` | where evrd's API is (`public_url`) |
   | `expires` | when the token ends if it isn't used (unix seconds); each use moves it on |

   If evrd couldn't make the token, the fragment is `#error=server&state=…`.

   Take the fragment out of the address bar straight away
   (`history.replaceState(null, '', location.pathname + location.search)`)
   and keep the token where the page can get it back after a reload
   (`localStorage`).

Each sign-in makes a new token, one per browser. They're listed with the
user's other sessions under *Account → Sessions* ("web chat on
chat.example.org"), and signing one out there, or *Sign out other sessions*,
ends it. An account keeps its 10 newest web chat tokens; an eleventh signs
out the one used least recently. The token is never logged.

A token works only on the web chat's routes below. It never works as a
cookie, and it can't change the account, its passwords, its networks'
settings or anything an admin does.

## Using the API

Send the token as a bearer token. From an allowed site the responses carry
CORS headers (`Access-Control-Allow-Origin: <the site>`, no credentials):

```
GET /api/networks/7/buffers
Authorization: Bearer <token>
```

| Route | |
|---|---|
| `GET /api/me` | the account (user name, language…) |
| `GET /api/networks` | the user's networks: `id`, `name`, `currentNick`, `status` (`connected`…), `channels` |
| `GET /api/networks/{id}` | one of them |
| `GET /api/networks/{id}/buffers` | its channels and private chats: `name`, `unread`, `mentions`, `lastAt`, `readAt`, and for channels `joined`, `members` (a count), `topic` |
| `GET /api/networks/{id}/channel?target=%23chan` | a channel's `topic`, `members` (`[{nick, prefix}]`, highest status first) and `joined` |
| `GET /api/networks/{id}/messages?target=%23chan` | a buffer's last 200 lines, oldest first: `{id, target, sender, kind, text, extra, time}` |
| `POST /api/networks/{id}/read` | `{"target": "#chan"}`: mark it read (on every client of theirs); no body: the whole network |
| `GET /api/networks/{id}/stream` | the chat socket (below) |
| `POST /auth/web-chat/revoke` | sign this token out (`204` whatever it was) |

An unknown, expired or signed-out token gets `401 {"error": "sign in first"}`:
sign in again. A network that isn't connected gets `409` on the socket.

## The chat socket

A browser can't put headers on a WebSocket, so the token goes as a
subprotocol, next to `evrd`, which evrd answers with:

```js
const ws = new WebSocket(api.replace(/^http/, 'ws') + `/api/networks/${network}/stream?raw=1`,
                         ['evrd', 'evrd.token.' + token])
```

The socket is refused (`403`) from a site that isn't listed, and from a
listed site with the cookie alone. Behind a proxy, evrd sees the browser's
address the usual way (`trusted_proxies`); the socket shows on the user's
*Clients* page as e.g. "Safari on macOS via chat.example.org".

### What comes

Every frame is a JSON object, the first `{"type": "ready"}`: from then on
nothing is missed. With `?raw=1` it also says where things stand:

```json
{"type":"ready","nick":"mira","channels":["#ircforever"],"isupport":["CHANTYPES=#","PREFIX=(ov)@+","…"],"connected":true}
```

Then, as they happen:

| Frame | |
|---|---|
| `{target, sender, kind, text, extra, time, id}` | a saved line: `kind` is `message`, `notice` or `action`, or a channel event (`join`, `part`, `quit`, `kick`, `nick`, `topic`, `mode`). Your own lines come this way too, from any of your clients — the network doesn't echo them. `id` is its msgid. |
| `{type:"typing", target, nick, state}` | someone's typing (`active`, `paused`, `done`) |
| `{type:"raw", line, id?}` (`?raw=1`) | a line exactly as the network sent it, tags and all: numerics (`WHOIS`, `LIST`, `NAMES`, `MODE`…), `JOIN`, `PRIVMSG`… The network's `PING`s come too: evrd answers them, so ignore them. CTCP requests evrd answers itself (`VERSION`…) and the replies to evrd's own `NAMES`/`TOPIC`/`WHO` after a reconnect don't come. `id` is set when evrd saved the line: the same line also comes as the saved-line frame with that `id`, so drop one of the two. |
| `{type:"left", channels}` (`?raw=1`) | the link to the network ended: evrd is out of these channels until it reconnects and rejoins them (their `JOIN`s come as raw lines) |

A client that speaks IRC itself can take everything from the raw lines,
and its own lines (and those of the user's other clients) from the
saved-line frames with a `sender` that's the user's nick.

### What to send

| Frame | |
|---|---|
| `{"target": "#chan", "text": "hi"}` | a message; `"kind": "action"` for `/me`, `"notice"` for a notice |
| `{"raw": "WHOIS carol"}` | any IRC line (`JOIN`, `PART`, `NICK`, `TOPIC`, `MODE`, `NAMES`, `LIST`…). Lines with line breaks are dropped. |
| `{"status": "help"}` | a `*status` command; its answers come as lines from `*status` |
| `{"target": "#chan", "typing": "active"}` | typing (`active`, `paused`, `done`) |

On attach, fill the screen with `GET /api/networks/{id}/buffers` and each
buffer's `messages`, or send `NAMES #chan` and `TOPIC #chan` as raw lines
and read the answers. The socket closes when the account is suspended or
moves to another node; open it again (with a short backoff).

## Signing out

Forget the token, and tell evrd so it's gone there too:

```js
fetch(api + '/auth/web-chat/revoke', { method: 'POST', headers: { Authorization: 'Bearer ' + token } })
```

That signs out this web chat only; evrd's own web app and the user's IRC
clients stay as they are. The user can also sign it out under *Account →
Sessions*.

## Developing against it

Add your dev server's origin, e.g. `http://localhost:5180`, to
`web_chat_origins` next to the real site. evrd compares origins exactly
(scheme, host and port), case aside.
