package client

// Channels from evrd's own state (upstream.ChannelState): an attaching
// client's JOIN burst, and NAMES, TOPIC and MODE queries about a channel
// evrd is in, are answered here instead of asking the network (#96) — no
// round trip per channel, and no replies a real join never brings (a "No
// topic is set" for every channel). What evrd doesn't know whole yet (it
// just joined, or picked a held link back up) still goes to the network.

import (
	"fmt"
	"strings"

	"github.com/4evrd/evrd/internal/upstream"
)

// channelBurst follows a self-JOIN with what a server sends on a join: the
// topic when there is one, then the names (not to a client with
// no-implicit-names). When evrd doesn't know the channel whole yet, the
// network is asked, as a client would see from a real join.
func (se *session) channelBurst(ch string) {
	st, ok := se.up.ChannelState(ch)
	if !ok || !st.TopicKnown || !st.NamesKnown {
		se.up.SendRaw("NAMES " + ch)
		se.up.SendRaw("TOPIC " + ch)
		return
	}
	se.sendTopic(st, false)
	if !se.caps["no-implicit-names"] {
		se.sendNames(st)
	}
}

// answerLocally answers a client's NAMES, TOPIC or MODE about one channel
// from evrd's state, reporting whether it did. Changes (a new topic, modes
// to set) and lists (MODE #chan b) are the network's.
func (se *session) answerLocally(command string, params []string) bool {
	if len(params) != 1 || strings.Contains(params[0], ",") {
		return false
	}
	st, ok := se.up.ChannelState(params[0])
	if !ok {
		return false
	}
	switch {
	case command == "NAMES" && st.NamesKnown:
		se.sendNames(st)
	case command == "TOPIC" && st.TopicKnown:
		se.sendTopic(st, true)
	case command == "MODE" && st.ModesKnown:
		se.sendModes(st)
	default:
		return false
	}
	return true
}

// reply sends one numeric reply from evrd as the server.
func (se *session) reply(code string, params ...string) {
	se.send(fmt.Sprintf(":%s %s %s %s", serverName, code, se.unick, strings.Join(params, " ")))
}

// sendTopic sends a channel's topic and who set it when: as on a join,
// nothing when there's none; as a TOPIC answer (asked), 331.
func (se *session) sendTopic(st upstream.ChannelState, asked bool) {
	if st.Topic == "" {
		if asked {
			se.reply("331", st.Name, ":No topic is set")
		}
		return
	}
	se.reply("332", st.Name, ":"+st.Topic)
	if st.TopicBy != "" && !st.TopicAt.IsZero() {
		se.reply("333", st.Name, st.TopicBy, fmt.Sprint(st.TopicAt.Unix()))
	}
}

// maxNamesLine keeps a 353 within IRC's 512 bytes (with CRLF), leaving room
// for the client's nick in its prefix being longer than ours.
const maxNamesLine = 400

// sendNames sends a channel's members as RPL_NAMREPLY lines and the end of
// the list, fitted to the client: every prefix only with multi-prefix,
// user@host only with userhost-in-names (when evrd knows it).
func (se *session) sendNames(st upstream.ChannelState) {
	kind := "="
	switch {
	case st.Secret():
		kind = "@"
	case st.Private():
		kind = "*"
	}
	head := fmt.Sprintf(":%s 353 %s %s %s :", serverName, se.unick, kind, st.Name)
	var line strings.Builder
	flush := func() {
		if line.Len() > 0 {
			se.send(head + line.String())
			line.Reset()
		}
	}
	for _, m := range st.Members {
		e := m.Prefix
		if se.caps["multi-prefix"] && m.Prefixes != "" {
			e = m.Prefixes
		}
		e += m.Nick
		if se.caps["userhost-in-names"] && m.User != "" && m.Host != "" {
			e += "!" + m.User + "@" + m.Host
		}
		if line.Len() > 0 && len(head)+line.Len()+1+len(e) > maxNamesLine {
			flush()
		}
		if line.Len() > 0 {
			line.WriteByte(' ')
		}
		line.WriteString(e)
	}
	flush()
	se.reply("366", st.Name, ":End of /NAMES list")
}

// sendModes answers MODE #chan: the channel's modes, and when it was made
// if evrd knows.
func (se *session) sendModes(st upstream.ChannelState) {
	se.reply("324", append([]string{st.Name, st.Modes}, st.ModeArgs...)...)
	if !st.Created.IsZero() {
		se.reply("329", st.Name, fmt.Sprint(st.Created.Unix()))
	}
}
