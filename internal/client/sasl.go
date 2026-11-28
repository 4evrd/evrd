package client

// SASL PLAIN, for clients that sign in to evrd that way instead of with
// PASS. The authentication identity is the user name, or user/network to
// pick a network, as with PASS (and soju); the password is the account
// password or an IRC client password.

import (
	"encoding/base64"
	"fmt"
	"slices"
	"strings"

	"github.com/4evrd/evrd/internal/irc"
)

// maxSASL bounds a SASL payload (base64).
const maxSASL = 4096

// authenticate answers AUTHENTICATE during registration.
func (se *session) authenticate(m irc.Message) {
	nick := orElse(se.nick, "*")
	reply := func(code, text string) { se.send(fmt.Sprintf(":%s %s %s %s", se.serverName(), code, nick, text)) }
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
		mech := strings.ToUpper(arg)
		if !slices.Contains(se.saslMechs(), mech) {
			reply("908", strings.Join(se.saslMechs(), ",")+" :are available SASL mechanisms")
			reply("904", ":SASL authentication failed")
			return
		}
		se.saslMech = mech
		se.send("AUTHENTICATE +")
	default:
		if arg != "+" {
			se.saslBuf += arg
		}
		if len(arg) == 400 && len(se.saslBuf) < maxSASL {
			return // more to come
		}
		payload, mech := se.saslBuf, se.saslMech
		se.saslMech, se.saslBuf = "", ""
		raw, err := base64.StdEncoding.DecodeString(payload)
		if mech == "EXTERNAL" && err == nil {
			se.saslExternal(string(raw), reply) // the certificate (certauth.go)
			return
		}
		login, password, ok := saslCredentials(mech, string(raw))
		if err != nil || !ok {
			reply("904", ":SASL authentication failed")
			return
		}
		username, network, _ := strings.Cut(login, "/")
		u, device, otp, code, why := se.signIn(username, network, password)
		if code != "" {
			reply("904", ":"+why)
			return
		}
		se.saslUser, se.saslDevice, se.saslOTP = u, device, otp
		if login == "" {
			login = u.Username // a token says whose it is
		}
		// attach (here, or on the node another one forwards this client
		// to) takes it from PASS, first: the owner registers the client as
		// soon as NICK and USER are in
		se.pass = login + ":" + password
		se.regLines = append([]string{"PASS " + se.pass}, dropPASS(se.regLines)...)
		reply("900", fmt.Sprintf("%s!%s@%s %s :You are now logged in as %s", nick, orElse(se.user, nick), se.serverName(), u.Username, u.Username))
		reply("903", ":SASL authentication successful")
	}
}

// saslMechs are the SASL mechanisms offered: PLAIN; EXTERNAL to a client
// that sent a certificate (certauth.go); and in managed mode with an
// issuer the bearer-token ones.
func (se *session) saslMechs() []string {
	if m := se.srv.managed; m != nil && m.Tokens() != nil {
		return []string{"PLAIN", "OAUTHBEARER", "IRCV3BEARER"}
	}
	if se.certsOn() && se.peerCert().cert != "" {
		return []string{"PLAIN", "EXTERNAL"}
	}
	return []string{"PLAIN"}
}

// saslCredentials reads a mechanism's decoded payload as a login (user or
// user/network, "" when a token alone says whose it is) and a secret.
//
//	PLAIN        authzid NUL authcid NUL password
//	OAUTHBEARER  n,a=authzid, ^A auth=Bearer <token> ^A … ^A^A (RFC 7628)
//	IRCV3BEARER  authzid NUL type NUL token (IRCv3 draft/bearer)
func saslCredentials(mech, raw string) (login, secret string, ok bool) {
	switch mech {
	case "PLAIN":
		parts := strings.Split(raw, "\x00")
		if len(parts) != 3 || parts[1] == "" {
			return "", "", false
		}
		return parts[1], parts[2], true
	case "OAUTHBEARER":
		kvs := strings.Split(raw, "\x01")
		gs2 := strings.Split(kvs[0], ",")
		if len(gs2) < 2 || (gs2[0] != "n" && gs2[0] != "y") {
			return "", "", false
		}
		login = strings.TrimPrefix(gs2[1], "a=")
		for _, kv := range kvs[1:] {
			if v, found := strings.CutPrefix(kv, "auth="); found {
				scheme, token, _ := strings.Cut(v, " ")
				if strings.EqualFold(scheme, "Bearer") && token != "" {
					return login, strings.TrimSpace(token), true
				}
			}
		}
		return "", "", false
	case "IRCV3BEARER":
		parts := strings.SplitN(raw, "\x00", 3)
		if len(parts) != 3 || parts[2] == "" {
			return "", "", false
		}
		return parts[0], parts[2], true
	}
	return "", "", false
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
