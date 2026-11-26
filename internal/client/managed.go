package client

// Managed mode (internal/managed): the bouncer is invisible. Its lines come
// from the configured server name, *status and the bouncer commands
// (BOUNCER, ZNC, DETACH) don't exist, clients sign in with the managing
// service's access tokens, every account attaches to the one network, and
// what clients are told is the managed texts — never the bouncer's name.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/4evrd/evrd/internal/accounts"
	"github.com/4evrd/evrd/internal/managed"
	"github.com/4evrd/evrd/internal/store"
)

// SetManaged turns managed mode on (nil: off). Call it before serving.
func (s *Server) SetManaged(m *managed.Mode) { s.managed = m }

// serverName is who the bouncer's own lines come from.
func (se *session) serverName() string {
	if m := se.srv.managed; m != nil {
		return m.ServerName()
	}
	return defaultServerName
}

// isManaged reports whether managed mode is on.
func (se *session) isManaged() bool { return se.srv.managed != nil }

// managedTexts are the irc.* texts a client can be sent outside *status,
// and the managed text (config.ManagedTexts) each is in managed mode.
var managedTexts = map[string]string{
	"kick.disconnected":   "kicked",
	"notice.disconnected": "disconnected",
	"notice.rejoined":     "reconnected",
	"part.goneWhileAway":  "left",
	"part.detached":       "left",
	"part.jumped":         "left",
	"welcome.network":     "welcome",
	"welcome.bouncer":     "welcome",
	"mfa.timedOut":        "denied",
	"mfa.tooManyTries":    "denied",
	"signin.denied":       "denied",
	"signin.expired":      "denied",
	"signin.otherNode":    "unavailable",
	"bouncerConnection":   "unavailable",
}

// managedText is key's text in managed mode, and whether managed mode has
// one for it ("": send nothing).
func (se *session) managedText(key string) (string, bool) {
	m := se.srv.managed
	if m == nil {
		return "", false
	}
	name, ok := managedTexts[key]
	if !ok {
		return "", false
	}
	return m.Text(name, orElse(se.unick, se.nick)), true
}

// channelNotice sends a notice from the server to ch, unless its text is
// one managed mode sends nothing for.
func (se *session) channelNotice(ch, key string, args ...any) {
	text := se.tr(key, args...)
	if text == "" {
		return
	}
	se.send(fmt.Sprintf(":%s NOTICE %s :%s", se.serverName(), ch, ircText(text)))
}

// managedWelcome is the registration burst in managed mode: the network's
// name and the managed MOTD, nothing about a bouncer.
func (se *session) managedWelcome() {
	n, name := se.unick, se.serverName()
	cfg := se.srv.managed.Config()
	se.send(fmt.Sprintf(":%s 001 %s :%s", name, n, ircText(se.tr("welcome.network"))))
	se.send(fmt.Sprintf(":%s 002 %s :Your host is %s", name, n, name))
	se.send(fmt.Sprintf(":%s 003 %s :This server is part of %s", name, n, cfg.Network))
	se.send(fmt.Sprintf(":%s 004 %s %s %s o o", name, n, name, name))
	se.sendISupport()
	if se.quick || len(cfg.MOTD) == 0 {
		se.send(fmt.Sprintf(":%s 422 %s :MOTD File is missing", name, n))
	} else {
		se.send(fmt.Sprintf(":%s 375 %s :- %s Message of the day -", name, n, name))
		for _, l := range cfg.MOTD {
			se.send(fmt.Sprintf(":%s 372 %s :- %s", name, n, ircText(l)))
		}
		se.send(fmt.Sprintf(":%s 376 %s :End of /MOTD command.", name, n))
	}
	if se.nick != "" && se.nick != n {
		se.send(fmt.Sprintf(":%s!%s@%s NICK %s", se.nick, se.nick, name, n))
	}
}

// managedISupport puts the network's name in ISUPPORT when the server
// sends none.
func (se *session) managedISupport(toks []string) []string {
	for _, t := range toks {
		if strings.HasPrefix(t, "NETWORK=") {
			return toks
		}
	}
	return append(toks, "NETWORK="+strings.ReplaceAll(se.srv.managed.NetworkName(), " ", "-"))
}

// managedCommand handles what managed mode takes away from a client: the
// bouncer's commands and nicks. It reports whether it took the line.
func (se *session) managedCommand(cmd string, params []string) bool {
	switch cmd {
	case "BOUNCER", "ZNC", "DETACH":
		se.numeric("421", cmd+" :Unknown command")
		return true
	case "PRIVMSG", "NOTICE", "TAGMSG":
		if len(params) > 0 && isOwnNick(params[0]) {
			if cmd == "PRIVMSG" {
				se.numeric("401", params[0]+" :No such nick/channel")
			}
			return true
		}
	}
	return false
}

// tokenDevice is how a client signed in with an access token shows among
// the account's clients.
const tokenDevice = "token"

// tokenSignIn checks secret as an access token for username ("": whoever
// the token is for), in managed mode. ok is false when it isn't one to
// check (not managed, or not a token's shape).
func (se *session) tokenSignIn(username, secret string) (u store.User, device string, ok bool, err error) {
	m := se.srv.managed
	if m == nil || !managed.LooksLikeToken(secret) {
		return store.User{}, "", false, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	u, err = m.SignIn(ctx, se.srv.acc, secret, username)
	switch {
	case err == nil:
		se.srv.log.Info("irc sign-in with an access token", "user", u.Username, "ip", se.clientIP())
		return u, sharedDevice(tokenDevice, se.clientIP()), true, nil
	case errors.Is(err, accounts.ErrSuspended):
		return store.User{}, "", true, err
	case errors.Is(err, managed.ErrTokensUnavailable):
		se.srv.log.Warn("access token check", "err", err)
		return store.User{}, "", true, errTokensDown
	default:
		se.srv.log.Info("irc sign-in: access token refused", "ip", se.clientIP(), "err", err)
		return store.User{}, "", true, accounts.ErrInvalidCredentials
	}
}

// errTokensDown is the issuer's keys being out of reach: not the user's
// fault, so no lockout.
var errTokensDown = errors.New("access tokens can't be checked right now")

// managedNetwork is the network a managed account's client attaches to.
func (se *session) managedNetwork(u store.User) (store.Network, bool) {
	nets, _ := se.srv.acc.ListNetworks(context.Background(), u.ID)
	for _, n := range nets {
		if se.srv.managed.IsNetwork(n) {
			return n, true
		}
	}
	if len(nets) == 1 {
		return nets[0], true
	}
	return store.Network{}, false
}

// managedRestartText is what a held client is told when it can't be picked
// up after a restart ("" outside managed mode).
func (se *session) managedRestartText() string {
	if m := se.srv.managed; m != nil {
		return orElse(m.Text("restarting", se.unick), "reconnect")
	}
	return ""
}

// movingMsg is what a client is told while its account moves to another
// node.
func (se *session) movingMsg() string {
	if m := se.srv.managed; m != nil {
		return orElse(m.Text("unavailable", se.nick), "Try again in a moment")
	}
	return movingMsg
}

// ownedWait is how long a managed account's client waits for this node to
// take the account on: one just made is taken at the next cluster tick.
var ownedWait = 8 * time.Second

// waitOwned reports whether this node runs userID within ownedWait, in
// managed mode (outside it, the client is refused at once).
func (se *session) waitOwned(userID int64) bool {
	if !se.isManaged() {
		return false
	}
	for end := time.Now().Add(ownedWait); time.Now().Before(end); {
		time.Sleep(100 * time.Millisecond)
		if se.srv.b.Owns(userID) {
			return true
		}
	}
	return false
}
