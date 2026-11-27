package upstream

// The last nick (Options.LastNick): like any bouncer, evrd keeps the nick
// a user picked. A client's NICK that the server takes is saved (Options.
// OnLastNick), and a new connection — evrd restarted without the links
// holder, the network dropped, a jump — registers with it instead of the
// network's nick. A client attaching to a running connection doesn't
// change the nick at all: it's told the one in use (internal/client).
// Under the nick guard, a last nick found registered to someone else is
// forgotten, so reconnects don't keep landing on it.

import "strings"

// clientAsksNick notes a client's NICK, saved as the last nick once the
// server takes it.
func (c *Conn) clientAsksNick(line string) {
	f := strings.Fields(line)
	if len(f) < 2 || !strings.EqualFold(f[0], "NICK") {
		return
	}
	c.mu.Lock()
	c.askedNick = strings.TrimPrefix(f[1], ":")
	c.mu.Unlock()
}

// lastNickChanged follows our nick: the answer to a client's NICK
// becomes the last nick. Other renames (the guard's, services') don't.
func (c *Conn) lastNickChanged(nu string) {
	c.mu.Lock()
	if c.askedNick == "" || !strings.EqualFold(nu, c.askedNick) {
		c.mu.Unlock()
		return
	}
	c.askedNick = ""
	changed := c.lastNick != nu
	c.lastNick = nu
	save := c.onLastNick
	c.mu.Unlock()
	if changed && save != nil {
		go save(nu)
	}
}

// forgetLastNick drops the last nick when it's nick (found registered to
// someone else). Holds c.mu.
func (c *Conn) forgetLastNick(nick string) {
	if c.lastNick == "" || !strings.EqualFold(c.lastNick, nick) {
		return
	}
	c.lastNick = ""
	if save := c.onLastNick; save != nil {
		go save("")
	}
}

// LastNick is the nick a client last changed to ("" none).
func (c *Conn) LastNick() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lastNick
}
