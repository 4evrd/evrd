package upstream

// The nick guard's grace (Options.NickGuardGrace): instead of leaving a
// nick the moment services say it's registered, a guarded connection with
// a client attached stays on it for the grace, so the user — who may own
// the nick — can identify with services themselves:
//
//   - The user's clients get one notice from the server (Options.
//     GuardWarning): identify within the grace or be moved to the next
//     nick.
//   - The nick becomes the connection's own once services or the server
//     say it's identified: services' "you are now identified/recognized"
//     notice, the server's registered-nick user mode (+R on ngIRCd, +r
//     elsewhere), RPL_LOGGEDIN (900) or account-notify's ACCOUNT for an
//     account named like the nick. When the grace ends without any of
//     those, a WHOIS of our own nick decides (its replies don't reach
//     clients): RPL_WHOISREGNICK (307), or RPL_WHOISACCOUNT (330) naming
//     an account named like the nick, keeps it; anything else moves on.
//   - A nick identified for is remembered (Options.OnIdentifiedNick) and
//     tried first on later connections while a client is attached
//     (Options.PreferNick): the user identifies again within the grace.
//   - Any change of nick ends the grace; services renaming or killing the
//     connection during it is handled as before (nickguard.go).
//
// A grace of 0 moves off at once, as before. So does a connection with no
// client attached: nobody's there to identify.

import (
	"fmt"
	"strings"
	"time"

	"github.com/4evrd/evrd/internal/irc"
	"github.com/4evrd/evrd/internal/messages"
)

// guardWhoisWait is how long the closing WHOIS may take before it counts
// as not identified (a test can shorten it).
var guardWhoisWait = 10 * time.Second

// guardGraceStart starts the grace for cur, services' nick being from.
// Holds c.mu, and unlocks it.
func (c *Conn) guardGraceStart(cur, from string) {
	c.graceNick = cur
	c.graceSeq++
	seq, gen, grace := c.graceSeq, c.auth.gen, c.guardGrace
	next := c.guardPick(cur)
	warn := c.guardWarning
	c.mu.Unlock()
	services := orElse(from, "NickServ")
	c.log.Info("nick is registered, waiting for the user to identify", "network", c.net.Name, "nick", cur, "grace", grace, "next", next)
	c.noteT(false, messages.T("log.nickGrace", "nick", cur, "grace", graceText(grace), "next", next))
	if warn != "" {
		text := strings.NewReplacer("{nick}", cur, "{services}", services, "{grace}", graceText(grace), "{next}", next).Replace(warn)
		c.broadcastRaw(Line{Text: ":" + c.replyFrom + " NOTICE " + cur + " :" + text})
	}
	time.AfterFunc(grace, func() { c.guardGraceExpired(gen, seq) })
}

// graceText is the grace as the warning says it: "60 seconds", "2 minutes".
func graceText(d time.Duration) string {
	if d >= 2*time.Minute && d%time.Minute == 0 {
		return fmt.Sprintf("%d minutes", int(d/time.Minute))
	}
	s := int((d + time.Second - 1) / time.Second)
	if s == 1 {
		return "1 second"
	}
	return fmt.Sprintf("%d seconds", s)
}

// guardGraceEnd stops a grace that's running (the nick changed, or it's
// identified). Holds c.mu.
func (c *Conn) guardGraceEnd() {
	if c.graceNick != "" || c.whoisNick != "" {
		c.graceNick, c.whoisNick, c.whoisFinal = "", "", false
		c.graceSeq++
		c.whoisSeq++
	}
}

// guardGraceExpired is the grace over: unless the nick was identified for
// meanwhile, a WHOIS of it decides.
func (c *Conn) guardGraceExpired(gen, seq int) {
	c.mu.Lock()
	if c.auth.gen != gen || c.graceSeq != seq || c.graceNick == "" || c.w == nil {
		c.mu.Unlock()
		return
	}
	nick := c.graceNick
	c.mu.Unlock()
	c.guardWhois(nick, true)
}

// guardWhois asks the server whether nick (ours) is identified. final: a
// "no" moves off it (the grace is over); otherwise only a "yes" counts.
func (c *Conn) guardWhois(nick string, final bool) {
	c.mu.Lock()
	if c.whoisNick != "" {
		c.whoisFinal = c.whoisFinal || final
		c.mu.Unlock()
		return
	}
	c.whoisNick, c.whoisFinal, c.whoisReg = nick, final, false
	c.whoisSeq++
	seq, gen := c.whoisSeq, c.auth.gen
	c.mu.Unlock()
	c.send("WHOIS " + nick)
	time.AfterFunc(guardWhoisWait, func() {
		c.mu.Lock()
		if c.auth.gen != gen || c.whoisSeq != seq || c.whoisNick == "" {
			c.mu.Unlock()
			return
		}
		c.mu.Unlock()
		c.guardWhoisDone()
	})
}

// whoisNumerics are WHOIS's replies about a nick (Params[1]).
var whoisNumerics = map[string]bool{
	"301": true, "307": true, "310": true, "311": true, "312": true, "313": true, "317": true, "318": true,
	"319": true, "320": true, "330": true, "335": true, "338": true, "378": true, "379": true, "671": true,
	"276": true, "401": true, "402": true,
}

// guardWhoisReply takes the replies to guardWhois's WHOIS, reporting
// whether m was one (clients don't see them).
func (c *Conn) guardWhoisReply(m irc.Message) bool {
	if !whoisNumerics[m.Command] || len(m.Params) < 2 {
		return false
	}
	c.mu.Lock()
	nick := c.whoisNick
	if nick == "" || !strings.EqualFold(m.Params[1], nick) {
		c.mu.Unlock()
		return false
	}
	switch m.Command {
	case "307": // RPL_WHOISREGNICK: identified for this nick
		c.whoisReg = true
	case "330": // RPL_WHOISACCOUNT: logged in as Params[2]
		if len(m.Params) > 2 && strings.EqualFold(m.Params[2], nick) {
			c.whoisReg = true
		}
	}
	end := m.Command == "318" || m.Command == "401" || m.Command == "402"
	c.mu.Unlock()
	if end {
		c.guardWhoisDone()
	}
	return true
}

// guardWhoisDone acts on the WHOIS's answer.
func (c *Conn) guardWhoisDone() {
	c.mu.Lock()
	nick, reg, final := c.whoisNick, c.whoisReg, c.whoisFinal
	c.whoisNick, c.whoisFinal = "", false
	c.whoisSeq++
	if nick == "" {
		c.mu.Unlock()
		return
	}
	if reg {
		c.mu.Unlock()
		c.guardIdentified(nick, "whois")
		return
	}
	if !final || !strings.EqualFold(c.nick, nick) || c.ownNicks[strings.ToLower(nick)] {
		c.mu.Unlock()
		return
	}
	c.graceNick = ""
	next := c.guardMove(nick, true)
	c.mu.Unlock()
	c.log.Info("nick is registered and wasn't identified for, moving off it", "network", c.net.Name, "nick", nick, "next", next)
	c.noteT(false, messages.T("log.nickRegistered", "nick", nick, "next", next))
	c.send("NICK " + next)
}

// guardIdentified makes nick ours ("" or not the nick in use: nothing): it
// stays, its grace ends, and a nick services said is registered is
// remembered for later connections. how is what told us, for the log.
func (c *Conn) guardIdentified(nick, how string) {
	c.mu.Lock()
	if !c.guard || c.health.Status != StatusConnected || nick == "" || !strings.EqualFold(nick, c.nick) {
		c.mu.Unlock()
		return
	}
	k := strings.ToLower(nick)
	if c.ownNicks[k] {
		c.mu.Unlock()
		return
	}
	c.ownNicks[k] = true
	delete(c.badNicks, k)
	c.guardGraceEnd()
	registered := c.prompted[k]
	if registered {
		c.preferNick = nick
	}
	save := c.onIdentified
	c.mu.Unlock()
	c.log.Info("identified for the nick, keeping it", "network", c.net.Name, "nick", nick, "by", how)
	c.noteT(false, messages.T("log.nickIdentified", "nick", nick))
	if registered && save != nil {
		go save(nick)
	}
}

// guardAccount follows our services account (900, ACCOUNT): one named
// like the nick makes the nick ours; another, during a grace, asks the
// server (the account may own the nick).
func (c *Conn) guardAccount(account string) {
	if account == "" || account == "*" {
		return
	}
	c.mu.Lock()
	if !c.guard {
		c.mu.Unlock()
		return
	}
	nick, grace := c.nick, c.graceNick
	c.mu.Unlock()
	if strings.EqualFold(account, nick) {
		c.guardIdentified(nick, "account")
		return
	}
	if grace != "" && strings.EqualFold(grace, nick) {
		c.guardWhois(nick, false)
	}
}

// guardUserModes follows our own user modes: the registered-nick mode
// services set on identifying (+R on ngIRCd, where +r restricts; +r on
// UnrealIRCd, InspIRCd and the others) makes the nick ours.
func (c *Conn) guardUserModes(modes string) {
	c.mu.Lock()
	if !c.guard {
		c.mu.Unlock()
		return
	}
	want := byte('r')
	if strings.HasPrefix(strings.ToLower(c.software), "ngircd") {
		want = 'R'
	}
	nick := c.nick
	c.mu.Unlock()
	adding := true
	for i := 0; i < len(modes); i++ {
		switch modes[i] {
		case '+':
			adding = true
		case '-':
			adding = false
		case want:
			if adding {
				c.guardIdentified(nick, "mode")
			}
		}
	}
}

// guardNickLen fits the nick to NICKLEN once the server says it: a nick
// the server cut to fit (ngIRCd: TheLion-windows-), or one fitted to the
// shortest NICKLEN there is after a refusal while registering, gives way
// to the first candidate fitted by parts (TheLion-windows).
func (c *Conn) guardNickLen() {
	c.mu.Lock()
	max := c.isupportNickLen()
	want, cur := c.guardWant, c.nick
	cut := len(want) > max && strings.EqualFold(cur, want[:max])
	if !c.guard || max == 0 || c.health.Status != StatusConnected || c.guardAsked != "" || !(cut || c.guardLen > 0) ||
		c.ownNicks[strings.ToLower(cur)] {
		c.mu.Unlock()
		return
	}
	c.guardLen = 0 // once
	next := c.guardPick("")
	if strings.EqualFold(next, cur) {
		c.mu.Unlock()
		return
	}
	c.guardAsked = next
	c.guardSeq++
	c.mu.Unlock()
	c.send("NICK " + next)
}
