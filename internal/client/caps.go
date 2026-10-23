package client

// IRCv3 capabilities. evrd offers its own (ownCaps) to every client, and
// the network's (linkCaps) while the attached link has them: what the
// network sends for those passes through to the clients that asked, and is
// translated or dropped for the others (adapt.go). A client with cap-notify
// hears when the offer changes: the link gained or lost a capability, or
// the client moved to another network.

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/4evrd/evrd/internal/irc"
	"github.com/4evrd/evrd/internal/store"
	"github.com/4evrd/evrd/internal/upstream"
)

// ownCaps are evrd's own capabilities, offered whatever the network.
// standard-replies is evrd's too: a client without it gets the network's
// FAIL, WARN and NOTE as notices.
var ownCaps = []string{
	"batch", "cap-notify", "draft/chathistory", "draft/event-playback", "no-implicit-names",
	"draft/pre-away", "draft/read-marker", "echo-message",
	"message-tags", "sasl", "server-time", "standard-replies",
	"soju.im/bouncer-networks", "soju.im/bouncer-networks-notify",
}

// linkCaps are the network's capabilities, offered while the link has them.
var linkCaps = []string{
	"multi-prefix", "userhost-in-names", "extended-join", "account-notify", "account-tag",
	"away-notify", "chghost", "invite-notify", "setname", "draft/channel-rename",
	"draft/message-redaction", "extended-monitor", "labeled-response", "draft/multiline",
}

// offer is what this client may ask for now, with each one's value.
func (se *session) offer() map[string]string {
	out := map[string]string{}
	for _, c := range ownCaps {
		out[c] = ""
	}
	link := se.up
	if link == nil && !se.registered {
		link = se.regLink()
	}
	out["sasl"] = "PLAIN"
	if v := se.srv.stsValue(se.secure()); v != "" {
		out["sts"] = v
	}
	if link != nil {
		for _, c := range linkCaps {
			if link.HasCap(c) {
				out[c] = link.CapValue(c)
			}
		}
	}
	return out
}

// lsWait is how long a CAP LS answer waits for the client to say which
// network it's for (PASS, NICK and USER usually follow at once).
const lsWait = 1500 * time.Millisecond

// regLink is the link of the network a registering client is headed for, as
// far as it's known yet — from SASL, PASS (the user's first network when it
// names none) or BOUNCER BIND — so its capabilities can be offered before
// registration ends. nil when it isn't known, isn't running on this node,
// or the password doesn't hold.
func (se *session) regLink() *upstream.Conn {
	key := se.pass + "\x00" + se.bindID
	if key == se.regKey {
		return se.regUp
	}
	se.regKey, se.regUp = key, nil
	login, password, _ := strings.Cut(se.pass, ":")
	username, network, _ := strings.Cut(login, "/")
	u := se.saslUser
	if u.ID == 0 {
		var err error
		if password == "" {
			return nil
		}
		if u, _, err = se.srv.acc.IRCLogin(username, password); err != nil {
			return nil
		}
	}
	if !se.srv.b.Owns(u.ID) || network == "" && se.bindID == "" && se.caps["soju.im/bouncer-networks"] {
		return nil // another node's, or a bouncer connection
	}
	nets, _ := se.srv.b.Store().ListNetworks(context.Background(), u.ID)
	var n store.Network
	ok := false
	switch {
	case se.bindID != "":
		n, ok = findNetworkID(nets, se.bindID)
	case network != "":
		n, ok = findNetwork(nets, network)
	case len(nets) > 0:
		n, ok = nets[0], true
	}
	if ok {
		if c, running := se.srv.b.ConnFor(n.ID); running {
			se.regUp = c
		}
	}
	return se.regUp
}

// answerLS answers a CAP LS that waited for the network.
func (se *session) answerLS() {
	se.lsWaiting = false
	se.advertised = se.offer()
	se.sendCapList("LS", se.capWords(se.advertised))
}

// capWords is an offer as CAP LS words, sorted, with values for CAP 302.
func (se *session) capWords(offer map[string]string) []string {
	var out []string
	for c, v := range offer {
		if v != "" && se.capVersion >= 302 {
			c += "=" + v
		}
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}

// sendCapList sends CAP <sub> with words, over several lines when they
// don't fit one (CAP 302 marks all but the last with *).
func (se *session) sendCapList(sub string, words []string) {
	nick := orElse(se.unick, orElse(se.nick, "*"))
	for {
		n, size := 0, 0
		for n < len(words) && (n == 0 || size+len(words[n]) < 400) {
			size += len(words[n]) + 1
			n++
		}
		more := ""
		if n < len(words) && se.capVersion >= 302 {
			more = " *"
		}
		se.send(fmt.Sprintf(":%s CAP %s %s%s :%s", serverName, nick, sub, more, strings.Join(words[:n], " ")))
		words = words[n:]
		if len(words) == 0 {
			return
		}
	}
}

// handleCap answers CAP during and after registration. A REQ is all or
// nothing: ACK only when every capability in it is offered.
func (se *session) handleCap(m irc.Message) {
	if len(m.Params) == 0 {
		return
	}
	nick := orElse(se.unick, orElse(se.nick, "*"))
	switch strings.ToUpper(m.Params[0]) {
	case "LS":
		if se.up == nil && !se.registered {
			se.capNegotiating = true
		}
		if len(m.Params) > 1 {
			if v, _ := strconv.Atoi(m.Params[1]); v >= 302 {
				se.capVersion = v
				se.caps["cap-notify"] = true // implied by CAP 302
			}
		}
		if !se.registered && se.pass == "" && se.bindID == "" && se.lsSince.IsZero() {
			// which network isn't known yet: wait a moment for PASS
			se.lsWaiting, se.lsSince, se.lsLine = true, time.Now(), strings.TrimSpace("CAP LS "+strings.Join(m.Params[1:], " "))
			return
		}
		se.answerLS()
	case "LIST":
		var on []string
		for c := range se.caps {
			on = append(on, c)
		}
		sort.Strings(on)
		se.sendCapList("LIST", on)
	case "REQ":
		if len(m.Params) < 2 {
			return
		}
		req := strings.Fields(m.Params[1])
		offer := se.offer()
		batch := se.caps["batch"] || slices.Contains(req, "batch")
		for _, c := range req {
			if _, ok := offer[strings.TrimPrefix(c, "-")]; !ok || !requestable(c) || needsBatch[c] && !batch {
				se.send(fmt.Sprintf(":%s CAP %s NAK :%s", serverName, nick, m.Params[1]))
				return
			}
		}
		for _, c := range req {
			if name, off := strings.CutPrefix(c, "-"); off {
				delete(se.caps, name)
			} else {
				se.caps[c] = true
			}
		}
		se.send(fmt.Sprintf(":%s CAP %s ACK :%s", serverName, nick, m.Params[1]))
	case "END":
		se.capNegotiating = false
	}
}

// needsBatch are the capabilities a client needs batch for.
var needsBatch = map[string]bool{"labeled-response": true, "draft/multiline": true}

// requestable reports whether a client may ask for a capability (sts only
// informs).
func requestable(c string) bool { return strings.TrimPrefix(c, "-") != "sts" }

// syncCaps tells a client with cap-notify what changed in the offer since
// it last heard (CAP DEL, then CAP NEW). A capability taken away is switched
// off for it.
func (se *session) syncCaps() {
	now := se.offer()
	if se.advertised == nil || !se.caps["cap-notify"] {
		return
	}
	var added, gone []string
	for c, v := range now {
		if old, ok := se.advertised[c]; !ok || old != v {
			added = append(added, c)
		}
	}
	for c := range se.advertised {
		if _, ok := now[c]; !ok {
			gone = append(gone, c)
			delete(se.caps, c)
		}
	}
	se.advertised = now
	if len(gone) > 0 {
		sort.Strings(gone)
		se.sendCapList("DEL", gone)
	}
	if len(added) > 0 {
		sub := map[string]string{}
		for _, c := range added {
			sub[c] = now[c]
		}
		se.sendCapList("NEW", se.capWords(sub))
	}
}

// isupport is the ISUPPORT (005) tokens this client gets: evrd's own, then
// the network's.
func (se *session) isupport() []string {
	var toks []string
	if se.caps["draft/chathistory"] {
		toks = append(toks, fmt.Sprintf("CHATHISTORY=%d", chathistoryMax), "MSGREFTYPES=msgid,timestamp")
	}
	if se.caps["soju.im/bouncer-networks"] {
		if se.up != nil {
			toks = append(toks, fmt.Sprintf("BOUNCER_NETID=%d", se.net.ID))
		} else {
			toks = append(toks, "BOUNCER_NETID")
		}
	}
	if se.up != nil {
		toks = append(toks, se.up.ISupport()...)
	}
	return toks
}

// sendISupport sends the ISUPPORT tokens, at most 13 to a line.
func (se *session) sendISupport() {
	toks := se.isupport()
	for len(toks) > 0 {
		n := min(len(toks), 13)
		se.send(fmt.Sprintf(":%s 005 %s %s :are supported by this server", serverName, se.unick, strings.Join(toks[:n], " ")))
		toks = toks[n:]
	}
}

// orDefault is d, or def when d is unset.
func orDefault(d, def time.Duration) time.Duration {
	if d <= 0 {
		return def
	}
	return d
}
