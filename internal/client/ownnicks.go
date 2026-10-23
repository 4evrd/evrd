package client

// evrd's own nicks (*status) start with "*", which no IRC nick
// can. A client asking the network about them (mIRC's whois on query, its
// notify list's ISON, a /msg to a ZNC module evrd doesn't have) would get
// the network's "No such nick/channel", which the client shows in the
// *status query. evrd answers these itself instead.

import (
	"fmt"
	"strings"

	"github.com/4evrd/evrd/internal/irc"
)

// isOwnNick reports whether name is one of evrd's names rather than the
// network's: anything starting with "*".
func isOwnNick(name string) bool { return len(name) > 1 && name[0] == '*' }

// knownNick reports whether evrd has a user by that name.
func knownNick(name string) bool { return strings.EqualFold(name, statusNick) }

// ownNickLine answers a client's line about evrd's own nicks, reporting
// whether it took the line. Lines that also name the network's nicks go on
// with only those.
func (se *session) ownNickLine(m irc.Message) bool {
	switch m.Command {
	case "WHOIS": // WHOIS [server] nick
		if len(m.Params) == 0 || !isOwnNick(m.Params[len(m.Params)-1]) {
			return false
		}
		nick := m.Params[len(m.Params)-1]
		if knownNick(nick) {
			user := strings.TrimPrefix(nick, "*")
			se.numeric("311", fmt.Sprintf("%s %s %s * :evrd", nick, user, serverName))
			se.numeric("312", fmt.Sprintf("%s %s :evrd bouncer", nick, serverName))
		} else {
			se.numeric("401", nick+" :No such nick/channel")
		}
		se.numeric("318", nick+" :End of /WHOIS list")
		return true
	case "WHO":
		if len(m.Params) == 0 || !isOwnNick(m.Params[0]) {
			return false
		}
		if nick := m.Params[0]; knownNick(nick) {
			se.numeric("352", fmt.Sprintf("* %s %s %s %s H :0 evrd", strings.TrimPrefix(nick, "*"), serverName, serverName, nick))
		}
		se.numeric("315", m.Params[0]+" :End of /WHO list")
		return true
	case "MODE":
		if len(m.Params) == 0 || !isOwnNick(m.Params[0]) {
			return false
		}
		se.numeric("502", ":Can't change mode for other users")
		return true
	case "USERHOST", "ISON":
		var ours, theirs []string
		for _, p := range m.Params {
			for _, nick := range strings.Fields(p) {
				if isOwnNick(nick) {
					ours = append(ours, nick)
				} else {
					theirs = append(theirs, nick)
				}
			}
		}
		if len(ours) == 0 {
			return false
		}
		var found []string
		for _, nick := range ours {
			if !knownNick(nick) {
				continue
			}
			if m.Command == "USERHOST" {
				nick += "=+" + strings.TrimPrefix(nick, "*") + "@" + serverName
			}
			found = append(found, nick)
		}
		code := map[string]string{"USERHOST": "302", "ISON": "303"}[m.Command]
		se.numeric(code, ":"+strings.Join(found, " "))
		if len(theirs) > 0 {
			se.passOn(irc.Message{Command: m.Command, Params: theirs})
		}
		return true
	case "PRIVMSG", "NOTICE", "TAGMSG":
		// *status is handled by dispatch; other "*" names never reach
		// the network, and only a PRIVMSG gets an answer.
		if len(m.Params) == 0 || !isOwnNick(m.Params[0]) || strings.EqualFold(m.Params[0], statusNick) {
			return false
		}
		if m.Command == "PRIVMSG" {
			se.numeric("401", m.Params[0]+" :No such nick/channel (evrd's commands are on "+statusNick+")")
		}
		return true
	}
	return false
}
