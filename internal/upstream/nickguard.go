package upstream

// The nick guard (Options.NickGuard): for a connection that never logs in
// to the network's services (managed mode's accounts with the services
// login "none", such as Boson users from another server). Such a
// connection must never keep a nick registered to someone else: services
// would rename or kill it, and other users could take it for the nick's
// owner. So:
//
//   - When services say the nick is registered (NickServ's "This nickname
//     is registered…" notice), when the connection is renamed without
//     asking (services' SVSNICK to Guest12345) or killed by services, the
//     nick is remembered as registered and the connection moves to the
//     next nick: the alternate nick, then the alternate nick with a
//     number (cut to the server's NICKLEN). It never identifies.
//   - The nicks it learned stay skipped on later connections, so a kill
//     doesn't become a reconnect loop on the same nick.
//   - After registering, channel joins (saved ones and clients' JOINs) wait
//     until the nick has settled (guardSettle after its last change), so
//     a registered nick is never seen in a channel.
//
// Clients follow along: they get the server's NICK line like any other.

import (
	"crypto/rand"
	"math/big"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/4evrd/evrd/internal/irc"
	"github.com/4evrd/evrd/internal/messages"
)

// guardSettle is how long joins wait after the nick last changed (a test
// can shorten it).
var guardSettle = 2 * time.Second

// guardNumbered is how many numbered alternate nicks are tried before a
// random one.
const guardNumbered = 20

// servicesKilled is a kill or closing reason that is services enforcing a
// registered nick (Anope, Atheme, DALnet…).
var servicesKilled = regexp.MustCompile(`(?i)(nick(name)?\s*(kill|enforce|protect|collision)|enforce|nickserv|services?)`)

// guardFirst is the nick to register with: the first not known to be
// registered (prefer, the nick the user last identified for, first).
// Holds c.mu.
func (c *Conn) guardFirst(prefer bool) string {
	if !c.guard {
		return orElse(c.lastNick, c.net.Nick)
	}
	c.guardPrefer = prefer
	return c.guardPick("")
}

// guardCandidates are the nicks to use, in order: the nick the user last
// identified for (while a client is there to identify again), the nick a
// client last changed to (not that registered one when nobody's there),
// the nick, the alternate nick, then the alternate nick numbered. Each is fitted to
// the server's NICKLEN, dropping whole trailing parts first. Holds c.mu.
func (c *Conn) guardCandidates() []string {
	base := strings.TrimSpace(c.net.Nick)
	max := c.nickLen()
	var out []string
	if p := strings.TrimSpace(c.preferNick); p != "" && c.guardPrefer && (max == 0 || len(p) <= max) {
		out = append(out, p)
	}
	if l := c.lastNick; l != "" && (max == 0 || len(l) <= max) && (c.guardPrefer || !strings.EqualFold(l, c.preferNick)) {
		out = append(out, l)
	}
	alt := orElse(c.altNick, base+"_")
	out = append(out, fitNick(base, "", max), fitNick(alt, "", max))
	for i := 2; i <= guardNumbered; i++ {
		out = append(out, fitNick(alt, strconv.Itoa(i), max))
	}
	return out
}

// guardPick is the first candidate that isn't known registered (or
// refused), isn't taken on this connection and isn't skip; a random one
// when none is left. Holds c.mu.
func (c *Conn) guardPick(skip string) string {
	for _, n := range c.guardCandidates() {
		k := strings.ToLower(n)
		if n == "" || c.badNicks[k] || c.guardTaken[k] || strings.EqualFold(n, skip) {
			continue
		}
		return n
	}
	alt := orElse(c.altNick, c.net.Nick+"_")
	for {
		r, err := rand.Int(rand.Reader, big.NewInt(90000))
		if err != nil {
			return fitNick(alt, "1", c.nickLen())
		}
		n := fitNick(alt, strconv.Itoa(10000+int(r.Int64())), c.nickLen())
		if !c.badNicks[strings.ToLower(n)] && !strings.EqualFold(n, skip) {
			return n
		}
	}
}

// nickSeps split a nick into parts: thelion-windows-bosonchat.
const nickSeps = "-_.|"

// fitNick is base+suffix cut to max (0: no limit), keeping the suffix. A
// base with parts loses whole trailing parts first, as long as two are
// left (thelion-windows-bosonchat → thelion-windows: the home server's
// suffix goes before the name is cut), and is cut in the middle only when
// that isn't enough.
func fitNick(base, suffix string, max int) string {
	if max <= 0 || len(base)+len(suffix) <= max {
		return base + suffix
	}
	keep := max - len(suffix)
	if keep <= 0 {
		return suffix[len(suffix)-max:]
	}
	b := strings.TrimRight(base, nickSeps)
	for len(b) > keep {
		i := strings.LastIndexAny(b, nickSeps)
		if i <= 0 {
			break
		}
		head := strings.TrimRight(b[:i], nickSeps)
		if head == "" || !strings.ContainsAny(strings.Trim(head, nickSeps), nickSeps) {
			break // one part left: it would read as someone else's nick
		}
		b = head
	}
	if len(b) > keep {
		b = base[:keep]
		if t := strings.TrimRight(b, nickSeps); t != "" && suffix == "" {
			b = t
		}
	}
	return b + suffix
}

// minNickLen is the nick length every server takes (RFC 1459), used once
// a server refused a nick while registering, before its NICKLEN is known.
const minNickLen = 9

// nickLen is the server's ISUPPORT NICKLEN, else the length to fit
// while it's unknown (0: none). Holds c.mu.
func (c *Conn) nickLen() int {
	if n := c.isupportNickLen(); n > 0 {
		return n
	}
	return c.guardLen
}

// isupportNickLen is the server's ISUPPORT NICKLEN (0: not said). Holds
// c.mu.
func (c *Conn) isupportNickLen() int {
	for _, t := range c.isupport {
		if v, ok := strings.CutPrefix(t, "NICKLEN="); ok {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				return n
			}
		}
	}
	return 0
}

// guardMove leaves the nick in use for another one, after marking it as
// registered (bad) or only taken on this connection. Holds c.mu; the
// caller sends the returned line (and notes why).
func (c *Conn) guardMove(cur string, bad bool) (next string) {
	k := strings.ToLower(cur)
	if bad {
		c.badNicks[k] = true
		c.forgetLastNick(cur)
	} else {
		c.guardTaken[k] = true
	}
	next = c.guardPick(cur)
	c.guardAsked = next
	c.guardSeq++
	return next
}

// guardNotice handles a notice from services to target while guarded:
// their "this nick is registered" starts the grace (guardGraceStart) or
// moves us off the nick, and their "you're identified" makes the nick
// ours (guardIdentified). It reports whether the notice was the prompt
// (then it isn't looked at as a login answer).
func (c *Conn) guardNotice(from, target, text string) bool {
	if c.guard && servicesOK.MatchString(text) {
		c.guardIdentified(target, "services")
		return false
	}
	if !servicesPrompt.MatchString(text) {
		return false
	}
	idle := c.idle != nil && c.idle()
	c.mu.Lock()
	if !c.guard || c.health.Status != StatusConnected {
		c.mu.Unlock()
		return false
	}
	cur := c.nick
	k := strings.ToLower(cur)
	switch {
	case c.guardAsked != "":
		// about the nick we're leaving already
		c.mu.Unlock()
		return true
	case target != "" && !strings.EqualFold(target, cur):
		// about a nick we were on before
		c.mu.Unlock()
		return true
	case c.ownNicks[k], strings.EqualFold(c.graceNick, cur):
		// identified for it, or the grace for it runs already
		c.mu.Unlock()
		return true
	}
	c.prompted[k] = true
	if c.guardGrace > 0 && !idle {
		c.guardGraceStart(cur, from)
		return true // (unlocked)
	}
	next := c.guardMove(cur, true)
	c.mu.Unlock()
	c.log.Info("nick is registered to someone else, moving off it", "network", c.net.Name, "nick", cur, "next", next)
	c.noteT(false, messages.T("log.nickRegistered", "nick", cur, "next", next))
	c.send("NICK " + next)
	return true
}

// guardNick follows a NICK of ours (old → nu) while guarded: the answer to
// the guard's or a client's NICK, or a rename nobody here asked for
// (services enforcing a registered nick), which moves us on again.
func (c *Conn) guardNick(old, nu string) {
	c.mu.Lock()
	if !c.guard {
		c.mu.Unlock()
		return
	}
	asked := strings.EqualFold(nu, c.guardAsked) || strings.EqualFold(nu, c.clientNick)
	c.guardGraceEnd() // a grace is for the nick we left
	if asked {
		c.guardAsked, c.clientNick = "", ""
		c.guardSeq++
		seq, gen := c.guardSeq, c.auth.gen
		c.mu.Unlock()
		c.guardSettleLater(gen, seq)
		return
	}
	c.guardAsked = ""
	next := c.guardMove(old, true)
	c.mu.Unlock()
	c.log.Info("renamed by the network off a registered nick, moving on", "network", c.net.Name, "nick", old, "renamed", nu, "next", next)
	c.noteT(false, messages.T("log.nickRegistered", "nick", old, "next", next))
	c.send("NICK " + next)
}

// guardRefused handles the server refusing a nick (433 in use, 432
// erroneous, 437 held, 435/447 can't change) while guarded. While
// registering it picks the next nick; once connected it only acts on the
// guard's own NICK. It reports whether it sent a NICK.
func (c *Conn) guardRefused(m irc.Message) bool {
	if len(m.Params) < 2 {
		return false
	}
	nick := m.Params[1]
	c.mu.Lock()
	if !c.guard {
		c.mu.Unlock()
		return false
	}
	registering := c.health.Status != StatusConnected
	if !registering && (c.guardAsked == "" || !strings.EqualFold(nick, c.guardAsked)) {
		c.mu.Unlock()
		return false
	}
	if registering && m.Command == "432" && c.nickLen() == 0 {
		c.guardLen = minNickLen // maybe too long: the shortest NICKLEN there is
	}
	// in use: maybe free later; erroneous or held: not this nick
	next := c.guardMove(nick, m.Command != "433")
	if registering {
		c.nick, c.guardAsked = next, ""
	}
	c.mu.Unlock()
	c.send("NICK " + next)
	return true
}

// guardKilled remembers the nick in use as registered when services kill
// the connection over it (KILL, or an ERROR naming services or nick
// enforcement), so the next connection starts on another nick.
func (c *Conn) guardKilled(reason string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.guard || c.nick == "" || !servicesKilled.MatchString(reason) {
		return
	}
	c.badNicks[strings.ToLower(c.nick)] = true
	c.forgetLastNick(c.nick)
}

// guardSettleLater lets the held joins go once the nick has been the same
// for guardSettle.
func (c *Conn) guardSettleLater(gen, seq int) {
	time.AfterFunc(guardSettle, func() {
		c.mu.Lock()
		if c.auth.gen != gen || c.guardSeq != seq || c.guardAsked != "" || !c.auth.guarding || c.w == nil {
			c.mu.Unlock()
			return
		}
		c.auth.guarding = false
		held := c.takeHeld()
		c.mu.Unlock()
		c.sendJoins(held)
	})
}

// guardHoldJoin keeps a client's JOIN while the nick settles; it reports
// whether it did.
func (c *Conn) guardHoldJoin(line string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.guard || !c.auth.guarding {
		return false
	}
	c.auth.held = append(c.auth.held, line)
	return true
}

// guardClientNick notes a client's NICK, so its answer isn't taken for a
// rename by services.
func (c *Conn) guardClientNick(line string) {
	f := strings.Fields(line)
	if len(f) < 2 || !strings.EqualFold(f[0], "NICK") {
		return
	}
	c.mu.Lock()
	if c.guard {
		c.clientNick = strings.TrimPrefix(f[1], ":")
	}
	c.mu.Unlock()
}
