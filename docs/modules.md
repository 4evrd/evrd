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
| `autoop`, `autovoice` | autoop, autovoice | Gives ops or voice to the people you list when they join a channel where you have ops. | **Who**: one per line, a `nick!user@host` mask (or a nick) and optionally channels: `*!*@friend.example.org #home,#work`. |
| `alias` | alias | Your own commands for IRC clients: `/op #chan nick` becomes what you define. | **Aliases**: `name = commands`, with `%1`, `%2`… (`%1+` for the rest) and `%nick%`; several commands separated by `;`. |
| `ctcpflood` | ctcpflood | Stops answering CTCP requests, and passing them on, while too many arrive at once. | **Most CTCPs** (4) **in seconds** (2). |
| `q` | q | Logs in to QuakeNet's Q with the network's account and password (Authentication), instead of NickServ, and hides your host. | **Hide my host** (yes). |

Logging in to NickServ, SASL, saving channels, away when nobody's attached,
playback and logs aren't modules: evrd does them itself.

## For admins

`modules_off` (or Admin → Settings → Modules) switches modules off for
everyone, at once:

```
modules_off autoop autovoice
```

## Writing one

A module is Go code in `internal/modules`: an `Info` (name, description,
settings) registered in `init`, and a type with `Registered()` (signed in)
and `Line(irc.Message)` (every line the server sends). It may also implement
`CTCPFilter`, `ClientFilter` (lines from the user's clients) and
`LoginHandler`. It acts through a `Host`: send lines, see channels and
members, set timers, write to the connection log. Calls to one connection's
modules never overlap, so a module needs no locking. Test it against a fake
host, as `modules_test.go` does.
