package upstream

// What the network offers beyond plain IRC: the capabilities evrd asks for,
// its ISUPPORT tokens, and the network's msgids. Attached IRC clients get
// the capabilities the link has (internal/client), so they're asked for here
// whether or not evrd itself uses them.

import (
	"strings"
)

// wantCaps are the capabilities evrd asks a network for when it offers them
// (sasl is asked for apart, when there's a password to log in with).
var wantCaps = []string{
	"message-tags", "batch", "cap-notify", "labeled-response", "draft/multiline",
	"multi-prefix", "userhost-in-names", "extended-join",
	"account-notify", "account-tag", "away-notify", "chghost", "invite-notify", "setname",
	"standard-replies", "draft/channel-rename", "draft/message-redaction", "extended-monitor",
}

// HasCap reports whether the network acknowledged a capability on this
// connection.
func (c *Conn) HasCap(name string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.caps[name]
}

// CapValue is the value the network offered a capability with
// ("max-bytes=4096" for draft/multiline), "" for none.
func (c *Conn) CapValue(name string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.capsOffered[name]
}

// ISupport is the network's ISUPPORT tokens (KEY or KEY=value), in the order
// it sent them.
func (c *Conn) ISupport() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.isupport...)
}

// mergeISupport applies a 005 line's tokens: a new value replaces the old,
// and -KEY drops one.
func mergeISupport(have, tokens []string) []string {
	for _, tok := range tokens {
		key, _, _ := strings.Cut(strings.TrimPrefix(tok, "-"), "=")
		kept := have[:0]
		for _, h := range have {
			if k, _, _ := strings.Cut(h, "="); k != key {
				kept = append(kept, h)
			}
		}
		have = kept
		if !strings.HasPrefix(tok, "-") {
			have = append(have, tok)
		}
	}
	return have
}

// Account is the services account the link is logged in to, "" when unknown.
func (c *Conn) Account() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.account
}

// Presence is a channel a nick is in, with its highest prefix there.
type Presence struct {
	Channel, Prefix string
}

// Presence lists the joined channels nick is in.
func (c *Conn) Presence(nick string) []Presence {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []Presence
	for _, ch := range c.channelsOf(nick, false) {
		p := ""
		if m := c.members[strings.ToLower(ch)][strings.ToLower(nick)]; m != nil {
			p = m.Prefix
		}
		out = append(out, Presence{Channel: ch, Prefix: p})
	}
	return out
}

// PrefixMode is the channel mode a nick prefix stands for ('@' -> 'o'), 0
// for none.
func (c *Conn) PrefixMode(prefix byte) byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	if i := strings.IndexByte(c.prefixChars, prefix); i >= 0 {
		return c.prefixModes[i]
	}
	return 0
}

// msgids pairs the network's msgids with evrd's for recent lines, so a
// client's reply, reaction or redaction can name a line either way.
type msgids struct {
	theirs, ours map[string]string // their id -> ours, ours -> theirs
	order        []string          // their ids, oldest first
}

// maxMsgids is how many recent lines' msgids a link remembers.
const maxMsgids = 8192

func (x *msgids) add(theirs, ours string) {
	if x.theirs == nil {
		x.theirs, x.ours = map[string]string{}, map[string]string{}
	}
	if len(x.order) >= maxMsgids {
		old := x.order[0]
		x.order = x.order[1:]
		delete(x.ours, x.theirs[old])
		delete(x.theirs, old)
	}
	x.theirs[theirs], x.ours[ours] = ours, theirs
	x.order = append(x.order, theirs)
}

// LocalMsgID is evrd's msgid for a line the network gave id.
func (c *Conn) LocalMsgID(id string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	ours, ok := c.ids.theirs[id]
	return ours, ok
}

// NetworkMsgID is the network's msgid for a line evrd gave id.
func (c *Conn) NetworkMsgID(id string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	theirs, ok := c.ids.ours[id]
	return theirs, ok
}

// renamed follows a channel the network renamed (draft/channel-rename):
// joined, saved, members and topic move to the new name.
func (c *Conn) renamed(old, nu string) {
	c.mu.Lock()
	changed := false
	for k := range c.channels {
		if strings.EqualFold(k, old) {
			delete(c.channels, k)
			c.channels[nu] = true
		}
	}
	for i, s := range c.saved {
		if strings.EqualFold(s, old) {
			c.saved[i], changed = nu, true
		}
	}
	lo, ln := strings.ToLower(old), strings.ToLower(nu)
	if m, ok := c.members[lo]; ok {
		delete(c.members, lo)
		c.members[ln] = m
	}
	if t, ok := c.topics[lo]; ok {
		delete(c.topics, lo)
		c.topics[ln] = t
	}
	saved := append([]string(nil), c.saved...)
	c.mu.Unlock()
	if changed && c.onChannels != nil {
		c.onChannels(saved)
	}
}
