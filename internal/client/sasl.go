package client

// SASL PLAIN, for clients that sign in to evrd that way instead of with
// PASS. The authentication identity is the user name, or user/network to
// pick a network, as with PASS (and soju); the password is the account
// password or an IRC client password.

import (
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/4evrd/evrd/internal/irc"
)

// maxSASL bounds a SASL payload (base64).
const maxSASL = 4096

// authenticate answers AUTHENTICATE during registration.
func (se *session) authenticate(m irc.Message) {
	nick := orElse(se.nick, "*")
	reply := func(code, text string) { se.send(fmt.Sprintf(":%s %s %s %s", serverName, code, nick, text)) }
	if len(m.Params) == 0 {
		return
	}
	arg := m.Params[0]
	switch {
	case !se.caps["sasl"]:
		reply("904", ":SASL authentication failed")
	case se.saslUser.ID != 0:
		reply("907", ":You have already authenticated")
	case arg == "*":
		se.saslMech, se.saslBuf = "", ""
		reply("906", ":SASL authentication aborted")
	case se.saslMech == "":
		if !strings.EqualFold(arg, "PLAIN") {
			reply("908", "PLAIN :are available SASL mechanisms")
			reply("904", ":SASL authentication failed")
			return
		}
		se.saslMech = "PLAIN"
		se.send("AUTHENTICATE +")
	default:
		if arg != "+" {
			se.saslBuf += arg
		}
		if len(arg) == 400 && len(se.saslBuf) < maxSASL {
			return // more to come
		}
		payload := se.saslBuf
		se.saslMech, se.saslBuf = "", ""
		raw, err := base64.StdEncoding.DecodeString(payload)
		parts := strings.Split(string(raw), "\x00")
		if err != nil || len(parts) != 3 || parts[1] == "" {
			reply("904", ":SASL authentication failed")
			return
		}
		login, password := parts[1], parts[2]
		username, network, _ := strings.Cut(login, "/")
		u, device, code, why := se.signIn(username, network, password)
		if code != "" {
			reply("904", ":"+why)
			return
		}
		se.saslUser, se.saslDevice = u, device
		// attach (here, or on the node another one forwards this client
		// to) takes it from PASS, first: the owner registers the client as
		// soon as NICK and USER are in
		se.pass = login + ":" + password
		se.regLines = append([]string{"PASS " + se.pass}, dropPASS(se.regLines)...)
		reply("900", fmt.Sprintf("%s!%s@%s %s :You are now logged in as %s", nick, orElse(se.user, nick), serverName, u.Username, u.Username))
		reply("903", ":SASL authentication successful")
	}
}

// dropPASS is registration lines without PASS.
func dropPASS(lines []string) []string {
	var out []string
	for _, l := range lines {
		if !strings.HasPrefix(strings.ToUpper(l), "PASS ") {
			out = append(out, l)
		}
	}
	return out
}
