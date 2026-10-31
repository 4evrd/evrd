package client

// Quiet rejoin (the account's QuietRejoin): as a network's link drops, the
// client keeps its channels open — a notice in each says so — instead of a
// KICK and, once evrd is back in, a JOIN that brings each window forward
// (mIRC, #147). Back in, the client is told only what changed: who left,
// who came, status modes and the topic.

import (
	"fmt"
	"strings"

	"github.com/4evrd/evrd/internal/irc"
	"github.com/4evrd/evrd/internal/upstream"
)

// rejoin is a channel the client kept open while evrd was out of it.
type rejoin struct {
	Was  upstream.LeftChannel `json:"was"`
	Nick string               `json:"nick"` // ours, then
}

// leftQuietly is leftChannels for a client that keeps the channels open.
func (se *session) leftQuietly(line upstream.Line) {
	for _, ch := range line.Left {
		key := strings.ToLower(ch)
		if se.up.ChannelSettings(ch).Detached || !se.open[key] {
			continue
		}
		se.rejoining[key] = rejoin{Was: line.Was[key], Nick: se.unick}
		se.send(fmt.Sprintf(":%s NOTICE %s :%s", serverName, ch, ircText(se.tr("notice.disconnected", "network", se.net.Name))))
	}
}

// kept reports whether the client kept ch open while evrd was out of it.
func (se *session) kept(ch string) bool {
	_, ok := se.rejoining[strings.ToLower(ch)]
	return ok
}

// rejoinedAlready tells the client what changed in the channels it kept
// open that a new connection rejoined before the session caught up with it.
func (se *session) rejoinedAlready() {
	for key := range se.rejoining {
		if st, ok := se.up.ChannelState(key); ok && st.NamesKnown {
			se.rejoined(key)
		}
	}
}

// joinFailed: the network turned evrd away from a channel the client kept
// open, so it's taken out after all.
var joinFailed = map[string]bool{"403": true, "405": true, "471": true, "473": true, "474": true, "475": true, "476": true, "477": true, "489": true}

// notRejoined takes the client out of a channel it kept open that evrd
// couldn't rejoin, reporting whether m said so.
func (se *session) notRejoined(m irc.Message) bool {
	if !joinFailed[m.Command] || len(m.Params) < 2 {
		return false
	}
	key := strings.ToLower(m.Params[1])
	if _, ok := se.rejoining[key]; !ok {
		return false
	}
	delete(se.rejoining, key)
	se.leftChannels([]string{m.Params[1]})
	return true
}

// rejoined tells a client that kept ch open what changed while evrd was
// out of it, as the names of evrd's rejoin end.
func (se *session) rejoined(ch string) {
	key := strings.ToLower(ch)
	rj, ok := se.rejoining[key]
	if !ok {
		return
	}
	delete(se.rejoining, key)
	self := func(nick string) bool { return strings.EqualFold(nick, rj.Nick) || strings.EqualFold(nick, se.unick) }
	before := map[string]upstream.Member{}
	for _, m := range rj.Was.Members {
		before[strings.ToLower(m.Nick)] = m
	}
	now := se.up.Members(ch)
	var mine, wasMine upstream.Member
	for _, m := range rj.Was.Members {
		if self(m.Nick) {
			wasMine = m
		}
	}
	for _, m := range now {
		k := strings.ToLower(m.Nick)
		if self(m.Nick) {
			mine = m
			continue
		}
		old, there := before[k]
		delete(before, k)
		if !there {
			se.send(fmt.Sprintf(":%s JOIN %s", userhost(m), ch))
			old = upstream.Member{Nick: m.Nick}
		}
		se.statusModes(ch, m.Nick, prefixes(old), prefixes(m))
	}
	for _, m := range rj.Was.Members {
		if _, gone := before[strings.ToLower(m.Nick)]; gone && !self(m.Nick) {
			se.send(fmt.Sprintf(":%s PART %s :%s", userhost(m), ch, ircText(se.tr("part.goneWhileAway"))))
		}
	}
	se.statusModes(ch, se.unick, prefixes(wasMine), prefixes(mine))
	if topic := se.up.Topic(ch); topic != rj.Was.Topic {
		se.send(fmt.Sprintf(":%s TOPIC %s :%s", serverName, ch, topic))
	}
	se.send(fmt.Sprintf(":%s NOTICE %s :%s", serverName, ch, ircText(se.tr("notice.rejoined", "network", se.net.Name))))
}

// statusModes sends the MODE that takes nick's status prefixes in ch from
// was to is (+o, -v, …).
func (se *session) statusModes(ch, nick, was, is string) {
	var plus, minus []byte
	for i := 0; i < len(is); i++ {
		if !strings.ContainsRune(was, rune(is[i])) {
			if mode := se.up.PrefixMode(is[i]); mode != 0 {
				plus = append(plus, mode)
			}
		}
	}
	for i := 0; i < len(was); i++ {
		if !strings.ContainsRune(is, rune(was[i])) {
			if mode := se.up.PrefixMode(was[i]); mode != 0 {
				minus = append(minus, mode)
			}
		}
	}
	if len(plus)+len(minus) == 0 {
		return
	}
	modes, args := "", []string{}
	if len(minus) > 0 {
		modes += "-" + string(minus)
		for range minus {
			args = append(args, nick)
		}
	}
	if len(plus) > 0 {
		modes += "+" + string(plus)
		for range plus {
			args = append(args, nick)
		}
	}
	se.send(fmt.Sprintf(":%s MODE %s %s %s", serverName, ch, modes, strings.Join(args, " ")))
}

// prefixes is a member's status prefixes: all of them when the network
// says, else its highest.
func prefixes(m upstream.Member) string {
	if m.Prefixes != "" {
		return m.Prefixes
	}
	return m.Prefix
}

// userhost is a member's nick!user@host, as far as it's known.
func userhost(m upstream.Member) string {
	return m.Nick + "!" + orElse(m.User, m.Nick) + "@" + orElse(m.Host, serverName)
}
