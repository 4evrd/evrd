package client

// Passing the network's lines on: each client gets what its capabilities
// let it read. Lines a capability brings pass to the clients that asked for
// it; the others get them translated (CHGHOST as QUIT and JOIN, RENAME as
// PART and JOIN, a FAIL as a notice…) or not at all (ACCOUNT, AWAY…), and
// tags only the ones they take.

import (
	"fmt"
	"sort"
	"strings"

	"github.com/4evrd/evrd/internal/history"
	"github.com/4evrd/evrd/internal/irc"
	"github.com/4evrd/evrd/internal/upstream"
)

// relay forwards one raw upstream line, keeping the session's view of its
// nick in step and hiding what only makes sense to the bouncer.
func (se *session) relay(line upstream.Line) {
	if len(line.Left) > 0 {
		se.leftChannels(line.Left)
		return
	}
	m := irc.Parse(line.Text)
	text := line.Text
	if strings.HasPrefix(text, "@") {
		_, text, _ = strings.Cut(text[1:], " ")
	}
	switch m.Command {
	case "PING", "PONG", "CAP", "AUTHENTICATE":
		return
	case "001":
		// The upstream re-registered (it reconnected). Don't replay the
		// welcome; just bring the client's nick along if it changed.
		if len(m.Params) > 0 && m.Params[0] != se.unick {
			se.send(fmt.Sprintf(":%s!%s@%s NICK %s", se.unick, se.unick, serverName, m.Params[0]))
			se.unick = m.Params[0]
		}
		return
	case "002", "003", "004":
		return
	case "NICK":
		if len(m.Params) > 0 && strings.EqualFold(nickFromPrefix(m.Prefix), se.unick) {
			se.unick = m.Params[0]
		}
	}
	if m.Command == "TAGMSG" && !se.caps["message-tags"] {
		return // typing and the like: only for clients that take tags
	}
	if line.Origin == se.id && !se.caps["echo-message"] {
		return // this session's own TAGMSG
	}
	if ch := lineChannel(m); ch != "" && se.up.ChannelSettings(ch).Detached {
		return // detached: kept and logged, not shown
	}
	if !se.tracked(m) {
		return
	}
	if !se.route(m) {
		return
	}
	tags := se.lineTags(line.ID, m.Tags)
	out, keep := se.adapt(m)
	if keep {
		if m.Command == "TAGMSG" && tags == "" {
			return // nothing left to say
		}
		se.send(tags + text)
	} else {
		for _, o := range out {
			se.send(tags + o.String())
		}
	}
	if m.Command == "JOIN" && len(m.Params) > 0 && strings.EqualFold(nickFromPrefix(m.Prefix), se.unick) {
		se.joinedMark(m.Params[0])
	}
}

// route decides whether a line is for this client at all: the network's
// batches go to clients that take that kind of batch, and the lines in a
// batch that doesn't pass go as plain lines.
func (se *session) route(m irc.Message) bool {
	if m.Command != "BATCH" || len(m.Params) == 0 {
		return true
	}
	ref := m.Params[0]
	switch {
	case strings.HasPrefix(ref, "+") && len(m.Params) > 1:
		ok := se.caps["batch"] && se.batchKind(m.Params[1])
		if m.Params[1] == "labeled-response" {
			_, ok = se.ownLabel(m.Tags["label"])
		}
		if parent := m.Tags["batch"]; parent != "" && !se.ubatch[parent] {
			ok = false
		}
		se.ubatch[ref[1:]] = ok
		return ok
	case strings.HasPrefix(ref, "-"):
		ok := se.ubatch[ref[1:]]
		delete(se.ubatch, ref[1:])
		return ok
	}
	return false
}

// batchKind reports whether this client takes a kind of batch.
func (se *session) batchKind(kind string) bool {
	switch kind {
	case "draft/multiline":
		return se.caps["draft/multiline"]
	}
	return true
}

// lineTags are the tags a relayed line gets: evrd's time and msgid for what
// it recorded, and of the network's tags the ones this client asked for —
// client-only tags (+typing, +draft/reply…) and bot with message-tags,
// account with account-tag, batch for a batch it gets. Replies name lines
// by evrd's msgids.
func (se *session) lineTags(id history.ID, theirs map[string]string) string {
	var extra []string
	for k, v := range theirs {
		ok := false
		switch {
		case strings.HasPrefix(k, "+"):
			ok, v = se.caps["message-tags"] && len(k) > 1, se.localRef(k, v)
		case k == "bot":
			ok = se.caps["message-tags"]
		case k == "account":
			ok = se.caps["account-tag"]
		case k == "batch":
			ok = se.ubatch[v]
		case k == "label":
			v, ok = se.ownLabel(v)
		case k == "draft/multiline-concat":
			ok = se.caps["draft/multiline"]
		}
		if !ok {
			continue
		}
		if v != "" {
			k += "=" + v
		}
		extra = append(extra, k)
	}
	sort.Strings(extra)
	return se.tags(id, "", strings.Join(extra, ";"))
}

// localRef turns a reply's or reaction's msgid from the network's into
// evrd's, which is what clients know the line by.
func (se *session) localRef(tag, v string) string {
	if tag == "+draft/reply" || tag == "+reply" {
		if ours, ok := se.up.LocalMsgID(v); ok {
			return ours
		}
	}
	return v
}

// tracked follows the channels the client has open as the network takes
// it in and out, reporting whether m reaches it: not a JOIN of ours for a
// channel the client has open already (sent its burst by evrd), nor the
// topic and names after it — they'd reach every client.
func (se *session) tracked(m irc.Message) bool {
	p := m.Params
	mine := strings.EqualFold(nickFromPrefix(m.Prefix), se.unick)
	switch {
	case m.Command == "JOIN" && mine && len(p) > 0:
		key := strings.ToLower(p[0])
		if se.open[key] {
			se.dupNames[key] = true
			return false
		}
		se.open[key] = true
	case (m.Command == "PART" && mine || m.Command == "KICK" && len(p) > 1 && strings.EqualFold(p[1], se.unick)) && len(p) > 0:
		delete(se.open, strings.ToLower(p[0]))
	case (m.Command == "332" || m.Command == "333") && len(p) >= 2:
		return !se.dupNames[strings.ToLower(p[1])]
	case m.Command == "353" && len(p) >= 3:
		return !se.dupNames[strings.ToLower(p[2])]
	case m.Command == "366" && len(p) >= 2:
		key := strings.ToLower(p[1])
		if se.dupNames[key] {
			delete(se.dupNames, key)
			return false
		}
	}
	return true
}

// adapt fits a line to this client's capabilities. keep: send it as it
// came; otherwise out is what to send instead (nothing, to drop it).
func (se *session) adapt(m irc.Message) (out []irc.Message, keep bool) {
	p := m.Params
	switch m.Command {
	case "ACK": // labeled-response: for the client whose command it was
		_, mine := se.ownLabel(m.Tags["label"])
		return nil, mine
	case "ACCOUNT":
		return nil, se.caps["account-notify"]
	case "AWAY":
		return nil, se.caps["away-notify"]
	case "SETNAME":
		return nil, se.caps["setname"]
	case "INVITE":
		if len(p) > 0 && !strings.EqualFold(p[0], se.unick) {
			return nil, se.caps["invite-notify"]
		}
	case "JOIN":
		if len(p) >= 3 && !se.caps["extended-join"] {
			m.Params = p[:1]
			return []irc.Message{m}, false
		}
	case "CHGHOST":
		if !se.caps["chghost"] {
			return se.chghost(m), false
		}
	case "RENAME":
		if !se.caps["draft/channel-rename"] {
			return se.rename(m), false
		}
	case "REDACT":
		if !se.caps["draft/message-redaction"] {
			return nil, false
		}
		if len(p) >= 2 {
			if ours, ok := se.up.LocalMsgID(p[1]); ok {
				m.Params[1] = ours
				return []irc.Message{m}, false
			}
		}
	case "FAIL", "WARN", "NOTE":
		if !se.caps["standard-replies"] && len(p) > 0 {
			text := m.Command + " " + strings.Join(p[:len(p)-1], " ") + ": " + p[len(p)-1]
			return []irc.Message{{Prefix: orElse(m.Prefix, serverName), Command: "NOTICE", Params: []string{se.unick, text}}}, false
		}
	case "366": // RPL_ENDOFNAMES: me #chan :End
		if len(p) >= 2 && !se.namesWanted(p[1], true) {
			return nil, false
		}
	case "353": // RPL_NAMREPLY: me = #chan :names
		if len(p) >= 4 && !se.namesWanted(p[2], false) {
			return nil, false
		}
		if len(p) >= 4 && !(se.caps["multi-prefix"] && se.caps["userhost-in-names"]) {
			m.Params[3] = se.names(p[3])
			return []irc.Message{m}, false
		}
	case "352": // RPL_WHOREPLY: me #chan user host server nick flags :hops real
		if len(p) >= 7 && !se.caps["multi-prefix"] {
			m.Params[6] = se.whoFlags(p[6])
			return []irc.Message{m}, false
		}
	}
	return nil, true
}

// namesWanted reports whether a NAMES reply for ch goes to this client: a
// client with no-implicit-names gets only those it asked for (end marks
// the reply's last line).
func (se *session) namesWanted(ch string, end bool) bool {
	if !se.caps["no-implicit-names"] {
		return true
	}
	key := strings.ToLower(ch)
	if se.namesAsked[key] == 0 {
		return false
	}
	if end {
		se.namesAsked[key]--
	}
	return true
}

// isPrefix reports whether c is a nick prefix on this network.
func (se *session) isPrefix(c byte) bool { return se.up.PrefixMode(c) != 0 }

// names fits a NAMES list to the client: the highest prefix only without
// multi-prefix, nicks only without userhost-in-names.
func (se *session) names(list string) string {
	var out []string
	for _, e := range strings.Fields(list) {
		n := 0
		for n < len(e) && se.isPrefix(e[n]) {
			n++
		}
		pfx, rest := e[:n], e[n:]
		if !se.caps["multi-prefix"] && len(pfx) > 1 {
			pfx = pfx[:1]
		}
		if !se.caps["userhost-in-names"] {
			rest = nickFromPrefix(rest)
		}
		out = append(out, pfx+rest)
	}
	return strings.Join(out, " ")
}

// whoFlags keeps a WHO reply's highest prefix only (H@+ -> H@).
func (se *session) whoFlags(flags string) string {
	var b strings.Builder
	seen := false
	for i := 0; i < len(flags); i++ {
		if se.isPrefix(flags[i]) {
			if seen {
				continue
			}
			seen = true
		}
		b.WriteByte(flags[i])
	}
	return b.String()
}

// chghost is a host change for a client without chghost: the nick quits and
// joins each shared channel again, with its modes back — what the chghost
// spec suggests. Our own change needs nothing.
func (se *session) chghost(m irc.Message) []irc.Message {
	nick := nickFromPrefix(m.Prefix)
	if len(m.Params) < 2 || strings.EqualFold(nick, se.unick) {
		return nil
	}
	nu := nick + "!" + m.Params[0] + "@" + m.Params[1]
	var out []irc.Message
	for _, p := range se.up.Presence(nick) {
		if se.up.ChannelSettings(p.Channel).Detached {
			continue
		}
		if len(out) == 0 {
			out = append(out, irc.Message{Prefix: m.Prefix, Command: "QUIT", Params: []string{"Changing host"}})
		}
		out = append(out, irc.Message{Prefix: nu, Command: "JOIN", Params: []string{p.Channel}})
		if p.Prefix != "" {
			if mode := se.up.PrefixMode(p.Prefix[0]); mode != 0 {
				out = append(out, irc.Message{Prefix: serverName, Command: "MODE", Params: []string{p.Channel, "+" + string(mode), nick}})
			}
		}
	}
	return out
}

// rename is a channel rename for a client without draft/channel-rename: it
// leaves the old name and joins the new one, and the network is asked for
// the new one's names and topic.
func (se *session) rename(m irc.Message) []irc.Message {
	if len(m.Params) < 2 {
		return nil
	}
	old, nu := m.Params[0], m.Params[1]
	why := "Renamed to " + nu
	if len(m.Params) > 2 && m.Params[2] != "" {
		why += ": " + m.Params[2]
	}
	me := se.unick + "!" + se.unick + "@" + serverName
	se.up.SendRaw("NAMES " + nu)
	se.up.SendRaw("TOPIC " + nu)
	return []irc.Message{
		{Prefix: me, Command: "PART", Params: []string{old, why}},
		irc.Parse(se.selfJoin(nu)),
	}
}
