package upstream

// Who's in evrd's channels beyond their nick and user@host — realname,
// account, away, the server they're on — so a client's WHO about a channel
// evrd is in can be answered without asking the network (#96). evrd asks
// once itself when it joins a channel (WHOX where the network has it, so
// accounts come too), or picks a held link back up, and keeps the answers
// current from extended-join, away-notify, account-notify and setname. A
// part that can't be kept current (away without away-notify, accounts
// without account-notify) is never answered from here.

import (
	"strconv"
	"strings"
)

// whoToken marks evrd's own WHOX queries, so their replies are told apart
// from a client's (WHOX tokens are up to three digits).
const whoToken = "471"

// whoFields are the WHOX fields evrd asks for: token, channel, user, host,
// server, nick, flags, hop count, account, realname.
const whoFields = "tcuhsnfdar"

// person is what evrd knows of a nick in its channels beyond user@host.
type person struct {
	realname      string
	realnameKnown bool
	account       string // "" when logged out
	accountKnown  bool
	away          bool
	awayKnown     bool
	oper          bool   // from WHO's flags; not kept current
	server        string // as WHO named it; "" when not known
	hops          int
}

// personOf is a nick's entry, made when there's none. Holds c.mu.
func (c *Conn) personOf(nick string) *person {
	key := strings.ToLower(nick)
	p := c.people[key]
	if p == nil {
		p = &person{}
		c.people[key] = p
	}
	return p
}

// forgetPerson drops a nick that's in none of evrd's channels any more.
// Holds c.mu.
func (c *Conn) forgetPerson(nick string) {
	if len(c.channelsOf(nick, false)) == 0 {
		delete(c.people, strings.ToLower(nick))
	}
}

// renamePerson follows a NICK. Holds c.mu.
func (c *Conn) renamePerson(old, nu string) {
	if p := c.people[strings.ToLower(old)]; p != nil {
		delete(c.people, strings.ToLower(old))
		c.people[strings.ToLower(nu)] = p
	}
}

// hasWHOX reports whether the network takes WHOX queries. Holds c.mu.
func (c *Conn) hasWHOX() bool {
	for _, tok := range c.isupport {
		if tok == "WHOX" || strings.HasPrefix(tok, "WHOX=") {
			return true
		}
	}
	return false
}

// askWho asks the network who's in a channel, on this connection, without
// passing the reply to clients: none asked. In the background, as it waits
// its turn at the flood rate.
func (c *Conn) askWho(channel string) {
	key := strings.ToLower(channel)
	c.mu.Lock()
	w := c.w
	line := "WHO " + channel
	if c.hasWHOX() {
		line += " %" + whoFields + "," + whoToken
		c.whoAsked[key] = 'x'
	} else {
		c.whoAsked[key] = 'w'
	}
	c.resync["who "+key] = true
	c.mu.Unlock()
	go func() {
		c.mu.Lock()
		same := c.w == w
		c.mu.Unlock()
		if same {
			c.send(line)
		}
	}()
}

// whoFlags reads WHO's flags (H or G, * for an operator, then prefixes).
func (p *person) whoFlags(flags string) {
	p.away, p.awayKnown = strings.HasPrefix(flags, "G"), true
	p.oper = strings.Contains(flags, "*")
}

// whoReply takes RPL_WHOREPLY (352): me channel user host server nick flags
// :hops realname. Holds c.mu.
func (c *Conn) whoReply(params []string) {
	if len(params) < 8 {
		return
	}
	user, host, server, nick, flags := params[2], params[3], params[4], params[5], params[6]
	hops, real, _ := strings.Cut(params[7], " ")
	c.knownUserHost(nick, user, host)
	p := c.personOf(nick)
	p.server, p.realname, p.realnameKnown = server, real, true
	p.hops, _ = strconv.Atoi(hops)
	p.whoFlags(flags)
}

// whoxReply takes RPL_WHOSPCRPL (354) to evrd's own WHOX: me token channel
// user host server nick flags hops account :realname. Holds c.mu.
func (c *Conn) whoxReply(params []string) {
	if len(params) < 11 || params[1] != whoToken {
		return
	}
	user, host, server, nick, flags, hops, account := params[3], params[4], params[5], params[6], params[7], params[8], params[9]
	c.knownUserHost(nick, user, host)
	p := c.personOf(nick)
	p.server, p.realname, p.realnameKnown = server, params[10], true
	p.hops, _ = strconv.Atoi(hops)
	p.whoFlags(flags)
	if account == "0" {
		account = ""
	}
	p.account, p.accountKnown = account, true
}

// knownUserHost fills in a member's user@host where evrd lacks it.
// Holds c.mu.
func (c *Conn) knownUserHost(nick, user, host string) {
	for _, set := range c.members {
		if mem := set[strings.ToLower(nick)]; mem != nil && mem.Host == "" {
			mem.User, mem.Host = user, host
		}
	}
}

// whoEnded takes RPL_ENDOFWHO (315): when it ends evrd's own WHO about a
// channel, what it knows of who's there is whole. Holds c.mu.
func (c *Conn) whoEnded(mask string) {
	key := strings.ToLower(mask)
	if _, ok := c.whoAsked[key]; !ok {
		return
	}
	delete(c.whoAsked, key)
	if ci := c.info[key]; ci != nil {
		ci.whoKnown = true
	}
}

// joinedBy records what a JOIN says of who joined: with extended-join their
// account and realname; with away-notify, that they're back (the server
// sends an AWAY right after if they aren't). Holds c.mu.
func (c *Conn) joinedBy(nick string, params []string) {
	p := c.personOf(nick)
	if c.caps["extended-join"] && len(params) >= 3 {
		account := params[1]
		if account == "*" {
			account = ""
		}
		p.account, p.accountKnown = account, true
		p.realname, p.realnameKnown = params[2], true
	}
	if c.caps["away-notify"] && !p.awayKnown {
		p.away, p.awayKnown = false, true
	}
}

// Who is what evrd knows of one member for a WHO answer.
type Who struct {
	Member
	Realname string
	Account  string // "" when logged out
	Away     bool
	Oper     bool
	Server   string
	Hops     int
}

// WhoState is a channel's members with what WHO says of them, and whether
// evrd can answer a WHO about it: every member's realname, user@host and
// away known and kept current (away-notify), and accounts too for Accounts
// (account-notify).
func (c *Conn) WhoState(channel string) (who []Who, ok, accounts bool) {
	st, in := c.ChannelState(channel)
	c.mu.Lock()
	defer c.mu.Unlock()
	ci := c.info[strings.ToLower(channel)]
	if !in || ci == nil || !ci.whoKnown || !st.NamesKnown || !c.caps["away-notify"] {
		return nil, false, false
	}
	accounts = c.caps["account-notify"]
	server := c.serverName
	for _, m := range st.Members {
		p := c.people[strings.ToLower(m.Nick)]
		if p == nil || !p.realnameKnown || !p.awayKnown || m.Host == "" {
			return nil, false, false
		}
		if !p.accountKnown {
			accounts = false
		}
		w := Who{Member: m, Realname: p.realname, Account: p.account, Away: p.away, Oper: p.oper, Server: p.server, Hops: p.hops}
		if w.Server == "" {
			w.Server = orElse(server, "*")
		}
		who = append(who, w)
	}
	return who, true, accounts
}

// WhoNick is what evrd knows of a nick for WHO <nick>: answerable when the
// nick is in a channel of evrd's whose WHO is known (see WhoState).
func (c *Conn) WhoNick(nick string) (w Who, channel string, ok, accounts bool) {
	c.mu.Lock()
	chans := c.channelsOf(nick, false)
	c.mu.Unlock()
	for _, ch := range chans {
		all, ok, accounts := c.WhoState(ch)
		if !ok {
			continue
		}
		for _, w := range all {
			if strings.EqualFold(w.Nick, nick) {
				return w, ch, true, accounts
			}
		}
	}
	return Who{}, "", false, false
}

// awayChanged takes away-notify's AWAY. Holds c.mu.
func (c *Conn) awayChanged(nick string, away bool) {
	if p := c.people[strings.ToLower(nick)]; p != nil {
		p.away, p.awayKnown = away, true
	}
}

// accountChanged takes account-notify's ACCOUNT ("*": logged out).
// Holds c.mu.
func (c *Conn) accountChanged(nick, account string) {
	if account == "*" {
		account = ""
	}
	if p := c.people[strings.ToLower(nick)]; p != nil {
		p.account, p.accountKnown = account, true
	}
}

// realnameChanged takes setname's SETNAME. Holds c.mu.
func (c *Conn) realnameChanged(nick, realname string) {
	if p := c.people[strings.ToLower(nick)]; p != nil {
		p.realname, p.realnameKnown = realname, true
	}
}
