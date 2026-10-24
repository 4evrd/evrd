package upstream

// What evrd knows of each joined channel beyond its members and topic (who
// set the topic and when, the channel's modes, when it was made), so an IRC
// client can be answered without asking the network (#96): an attaching
// client gets its JOIN burst from here, and NAMES, TOPIC and MODE queries
// are answered here once evrd knows the whole of the channel. It's kept up
// to date from the lines evrd reads anyway; evrd asks the network itself
// once when it joins, or picks a held link back up.

import (
	"strconv"
	"strings"
	"time"
)

// chanInfo is a joined channel's state beyond members and topic.
type chanInfo struct {
	topicBy    string    // who set the topic, as the server named them
	topicAt    time.Time // when ("" by and zero at: unknown)
	topicKnown bool      // the topic is known (set or not)
	namesKnown bool      // a NAMES reply ended since evrd joined: the members are whole
	// joining: evrd's own JOIN is being answered (topic, then names): when
	// its NAMES end, the topic was sent if there is one
	joining    bool
	modes      []chanMode // the channel's modes, list modes left out
	modesKnown bool
	created    time.Time
}

// chanMode is one channel mode, with its argument if it takes one.
type chanMode struct {
	mode byte
	arg  string
}

// defaultChanModes are the CHANMODES groups when the server doesn't say:
// lists, always an argument, an argument when set, never one.
var defaultChanModes = [4]string{"beI", "k", "l", "imnpst"}

// modeKind is which CHANMODES group a mode is in ('A' lists, 'B' always an
// argument, 'C' an argument when set, 'D' never one); 'D' when the server
// didn't name it. Holds c.mu.
func (c *Conn) modeKind(m byte) byte {
	for i, group := range c.chanModes {
		if strings.IndexByte(group, m) >= 0 {
			return "ABCD"[i]
		}
	}
	return 'D'
}

// setChanModes reads ISUPPORT CHANMODES=A,B,C,D. Holds c.mu.
func (c *Conn) setChanModes(v string) {
	groups := strings.SplitN(v, ",", 4)
	if len(groups) < 4 {
		return
	}
	copy(c.chanModes[:], groups)
}

// setMode sets or clears one of a channel's modes.
func (ci *chanInfo) setMode(m byte, arg string, on bool) {
	for i, have := range ci.modes {
		if have.mode == m {
			if on {
				ci.modes[i].arg = arg
			} else {
				ci.modes = append(ci.modes[:i], ci.modes[i+1:]...)
			}
			return
		}
	}
	if on {
		ci.modes = append(ci.modes, chanMode{mode: m, arg: arg})
	}
}

// chanModesReply takes RPL_CHANNELMODEIS (324): the channel's modes, whole.
// Holds c.mu.
func (c *Conn) chanModesReply(channel, modes string, args []string) {
	ci := c.info[strings.ToLower(channel)]
	if ci == nil {
		return
	}
	ci.modes = nil
	for i := 0; i < len(modes); i++ {
		m := modes[i]
		if m == '+' || m == '-' {
			continue
		}
		arg := ""
		if kind := c.modeKind(m); kind == 'B' || kind == 'C' {
			if len(args) == 0 {
				continue
			}
			arg, args = args[0], args[1:]
		}
		ci.setMode(m, arg, true)
	}
	ci.modesKnown = true
}

// newChanInfo starts what evrd knows of a channel it's in. joining: from
// its own JOIN, which the server answers with the topic and names.
// Holds c.mu.
func (c *Conn) newChanInfo(channel string, joining bool) {
	c.info[strings.ToLower(channel)] = &chanInfo{joining: joining}
}

// namesEnded takes RPL_ENDOFNAMES (366): the member list is whole, and
// after evrd's own JOIN, so is the topic (a server sends it first, and
// nothing when there's none). Holds c.mu.
func (c *Conn) namesEnded(channel string) {
	ci := c.info[strings.ToLower(channel)]
	if ci == nil {
		return
	}
	ci.namesKnown = true
	if ci.joining {
		ci.joining, ci.topicKnown = false, true
	}
}

// topicSet records a channel's topic and who set it when ("" and zero when
// not known). Holds c.mu.
func (c *Conn) topicSet(channel, topic, by string, at time.Time) {
	key := strings.ToLower(channel)
	c.topics[key] = topic
	if ci := c.info[key]; ci != nil {
		ci.topicKnown, ci.topicBy, ci.topicAt = true, by, at
	}
}

// topicWho takes RPL_TOPICWHOTIME (333): who set the topic and when.
// Holds c.mu.
func (c *Conn) topicWho(channel, by, unix string) {
	ci := c.info[strings.ToLower(channel)]
	if ci == nil {
		return
	}
	ci.topicBy = by
	if n, err := strconv.ParseInt(unix, 10, 64); err == nil {
		ci.topicAt = time.Unix(n, 0)
	}
}

// chanCreated takes RPL_CREATIONTIME (329). Holds c.mu.
func (c *Conn) chanCreated(channel, unix string) {
	if ci := c.info[strings.ToLower(channel)]; ci != nil {
		if n, err := strconv.ParseInt(unix, 10, 64); err == nil {
			ci.created = time.Unix(n, 0)
		}
	}
}

// askModes asks the network for a channel's modes on this connection
// (not a later one), without passing the reply to clients: none asked.
// In the background, as it waits its turn at the flood rate.
func (c *Conn) askModes(channel string) {
	c.mu.Lock()
	w := c.w
	c.resync["mode "+strings.ToLower(channel)] = true
	c.mu.Unlock()
	go func() {
		c.mu.Lock()
		same := c.w == w
		c.mu.Unlock()
		if same {
			c.send("MODE " + channel)
		}
	}()
}

// ChannelState is a joined channel as evrd knows it, to answer IRC clients
// without asking the network. Each part says whether it's known: a part
// that isn't (evrd just joined, or picked a held link back up) is the
// network's to answer.
type ChannelState struct {
	Name string // as joined

	Topic      string
	TopicBy    string    // "" when not known
	TopicAt    time.Time // zero when not known
	TopicKnown bool

	Members    []Member // highest status first, then by nick
	NamesKnown bool

	Modes      string   // "+nt", list modes left out ("+" when none)
	ModeArgs   []string // the arguments of Modes, in order
	ModesKnown bool
	Created    time.Time // zero when not known
}

// Secret and Private report the channel's +s and +p, for NAMES' channel
// type.
func (s ChannelState) Secret() bool  { return strings.IndexByte(s.Modes, 's') >= 0 }
func (s ChannelState) Private() bool { return strings.IndexByte(s.Modes, 'p') >= 0 }

// ChannelState is what evrd knows of a channel it's in; false when it isn't.
func (c *Conn) ChannelState(channel string) (ChannelState, bool) {
	c.mu.Lock()
	name := ""
	for ch := range c.channels {
		if strings.EqualFold(ch, channel) {
			name = ch
		}
	}
	key := strings.ToLower(channel)
	ci := c.info[key]
	if name == "" || ci == nil {
		c.mu.Unlock()
		return ChannelState{}, false
	}
	st := ChannelState{
		Name: name, Topic: c.topics[key], TopicBy: ci.topicBy, TopicAt: ci.topicAt, TopicKnown: ci.topicKnown,
		NamesKnown: ci.namesKnown, ModesKnown: ci.modesKnown, Created: ci.created,
	}
	var b strings.Builder
	b.WriteByte('+')
	for _, m := range ci.modes {
		b.WriteByte(m.mode)
		if m.arg != "" {
			st.ModeArgs = append(st.ModeArgs, m.arg)
		}
	}
	st.Modes = b.String()
	c.mu.Unlock()
	st.Members = c.Members(channel)
	return st, true
}
