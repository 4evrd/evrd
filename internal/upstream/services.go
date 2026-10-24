package upstream

// Logging in to the network's services once connected: an IDENTIFY to
// NickServ (or a line of the user's own, for Undernet's X and the like),
// whose answer is watched for so the connection log can say whether it
// worked. Channel joins can wait for the answer (+r channels), and a nick
// held by a ghost is won back. SASL happens earlier, in handleCap.

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/4evrd/evrd/internal/irc"
	"github.com/4evrd/evrd/internal/messages"
	"github.com/4evrd/evrd/internal/modules"
	"github.com/4evrd/evrd/internal/store"
)

// Waits a test can shorten.
var (
	promptWait = 10 * time.Second // identify anyway when services never ask
	ghostWait  = 3 * time.Second  // take the nick back if services don't say it's free
	authUnit   = time.Second      // AuthTimeout's unit
)

// Health.Auth: how the services login went on this connection.
const (
	AuthPending     = "pending"     // sent, no answer yet
	AuthIdentified  = "identified"  // services (or SASL) said yes
	AuthFailed      = "failed"      // services (or SASL) said no
	AuthUnconfirmed = "unconfirmed" // no answer in time
)

// authState is one connection's services login. Held under c.mu.
type authState struct {
	gen        int      // the connection it belongs to: stale timers check it
	sent       bool     // the login went out
	waiting    bool     // and services haven't answered yet
	prompt     bool     // the login waits for services to ask for it
	afterNick  bool     // the login waits for the nick back (IDENTIFY password)
	held       []string // channel joins waiting for the answer
	holding    bool
	ghosted    bool // the ghost command went out
	nickSent   bool // and NICK after it
	saslFailed bool // SASL was refused (logged once)
}

// What services say. Formatting is stripped first; failures are checked
// before successes.
var (
	servicesFail = regexp.MustCompile(`(?i)(password (is )?(incorrect|mismatch|wrong)|(incorrect|invalid|wrong|bad) password|authentication failed|login (failed|unsuccessful)|failed login|invalid (account )?credentials|is not (a )?registered|isn't registered|not a registered|no such (account|user)|account does not exist|unknown (account|user)|suspended|frozen|too many (failed|invalid)|access denied)`)
	servicesOK   = regexp.MustCompile(`(?i)(you are now (identified|logged in|recognized)|you're now logged in|now identified for|password accepted|authentication successful|successfully (identified|logged in|authenticated)|you('re| are) already (identified|logged in))`)
	// services asking for the password (Atheme, Anope, DALnet)
	servicesPrompt = regexp.MustCompile(`(?i)(nick(name)? is registered|is owned by someone else|registered and protected|please identify|identify via|/msg \S+ identify|type\s+/(msg|ns|nickserv))`)
	// they let a nick go after GHOST, RECOVER or RELEASE
	servicesFreed = regexp.MustCompile(`(?i)(ghost(ed)?|killed|disconnected|recovered|released|has been (freed|regained))`)
	formatting    = regexp.MustCompile("\x03[0-9]{0,2}(,[0-9]{1,2})?|[\x02\x0f\x11\x16\x1d\x1e\x1f]")
)

// servicesTarget is where IDENTIFY goes: NickServ unless set.
func servicesTarget(n store.Network) string { return orElse(n.ServicesNick, "NickServ") }

// servicesLine sends text to services: as a PRIVMSG, or for "/NS" style
// targets as the server's own command (NS IDENTIFY …).
func servicesLine(target, text string) string {
	if cmd, ok := strings.CutPrefix(target, "/"); ok {
		return strings.ToUpper(cmd) + " " + text
	}
	return "PRIVMSG " + target + " :" + text
}

// account is the services account: the SASL account name, else the nick.
func account(n store.Network) string { return orElse(n.SASLUser, n.Nick) }

// identifyLine is the NickServ login: IDENTIFY with the account name
// (Atheme, Anope) or just the password (older services) — by default the
// account only when it isn't the nick in use.
func identifyLine(n store.Network, current string) string {
	acct := account(n)
	withAccount := !strings.EqualFold(acct, current)
	switch n.IdentifyArgs {
	case store.IdentifyAccount:
		withAccount = true
	case store.IdentifyPassword:
		withAccount = false
	}
	if withAccount {
		return servicesLine(servicesTarget(n), "IDENTIFY "+acct+" "+n.Password)
	}
	return servicesLine(servicesTarget(n), "IDENTIFY "+n.Password)
}

// commandLines is AuthCommand's line, filled in.
func commandLines(n store.Network) []string {
	line := strings.NewReplacer("%nick%", n.Nick, "%account%", account(n), "%password%", n.Password).Replace(n.AuthCommand)
	return modules.ClientCommand(line)
}

// commandTarget is who the command line talks to (X@channels.undernet.org),
// or "".
func commandTarget(n store.Network) string {
	for _, l := range commandLines(n) {
		if m := irc.Parse(l); m.Command == "PRIVMSG" && len(m.Params) > 0 {
			return m.Params[0]
		}
	}
	return ""
}

// fromServices reports whether a message comes from the services the login
// talks to (NickServ, the nick set, or the command's target).
func (c *Conn) fromServices(prefix string) bool {
	nick := nickOf(prefix)
	if nick == "" || strings.Contains(nick, ".") {
		return false // a server
	}
	targets := []string{"NickServ", c.net.ServicesNick}
	if c.net.Auth() == store.AuthCommand {
		targets = append(targets, commandTarget(c.net))
	}
	for _, t := range targets {
		t = strings.TrimPrefix(t, "/")
		t, _, _ = strings.Cut(t, "@")
		if strings.EqualFold(t, "NS") {
			t = "NickServ"
		}
		if t != "" && strings.EqualFold(t, nick) {
			return true
		}
	}
	return false
}

// authTimeout is how long services get to answer.
func (c *Conn) authTimeout() time.Duration {
	s := c.net.AuthTimeout
	if s <= 0 {
		s = store.DefaultAuthTimeout
	}
	return time.Duration(s) * authUnit
}

// login starts the services login once registered (001), holding joins
// for it when the network asks for that; otherwise they're sent at once.
func (c *Conn) login(moduleLogin bool, joins []string) {
	n := c.net
	c.mu.Lock()
	saslOK, nick, gen := c.saslOK, c.nick, c.auth.gen
	c.mu.Unlock()
	method := n.Auth()
	switch {
	case moduleLogin || method == store.AuthNone:
		c.sendJoins(joins)
		return
	case saslOK:
		c.ghost()
		c.sendJoins(joins)
		return
	case method == store.AuthSASL, method == store.AuthAuto && n.Password == "":
		c.sendJoins(joins) // SASL failed (and said so) or nothing to log in with
		return
	case method == store.AuthNickServ && n.Password == "":
		c.noteT(true, messages.T("log.noPassword", "services", servicesTarget(n)))
		c.sendJoins(joins)
		return
	}
	c.mu.Lock()
	if n.JoinAfterAuth && len(joins) > 0 {
		c.auth.held, c.auth.holding = joins, true
	}
	switch {
	case method != store.AuthCommand && n.IdentifyOn == store.IdentifyOnPrompt:
		c.auth.prompt = true
	case method != store.AuthCommand && n.IdentifyArgs == store.IdentifyPassword && n.GhostCmd != "" && !strings.EqualFold(nick, n.Nick):
		// IDENTIFY password logs in to the nick in use: get ours back first
		c.auth.afterNick = true
	}
	prompt, afterNick, holding := c.auth.prompt, c.auth.afterNick, c.auth.holding
	c.mu.Unlock()
	if holding {
		c.noteT(false, messages.T("log.joinAfterLogin"))
	} else {
		defer c.sendJoins(joins) // after IDENTIFY, at least
	}
	switch {
	case prompt:
		c.noteT(false, messages.T("log.waitingForPrompt", "services", servicesTarget(n)))
		time.AfterFunc(promptWait, func() { c.identify(gen) })
	case afterNick:
		c.ghost()
		time.AfterFunc(2*ghostWait, func() { c.identify(gen) })
	default:
		c.identify(gen)
	}
}

// identify sends the login (once per connection).
func (c *Conn) identify(gen int) {
	c.mu.Lock()
	if c.auth.gen != gen || c.auth.sent || c.w == nil {
		c.mu.Unlock()
		return
	}
	c.auth.sent, c.auth.waiting, c.auth.prompt, c.auth.afterNick = true, true, false, false
	c.health.Auth = AuthPending
	nick := c.nick
	c.mu.Unlock()
	n := c.net
	if n.Auth() == store.AuthCommand {
		for _, l := range commandLines(n) {
			c.send(l)
		}
		if t := commandTarget(n); t != "" {
			c.noteT(false, messages.T("log.sentLoginCommandTo", "target", t))
		} else {
			c.noteT(false, messages.T("log.sentLoginCommand"))
		}
	} else {
		c.send(identifyLine(n, nick))
		c.noteT(false, messages.T("log.identifying", "services", servicesTarget(n), "account", account(n)))
	}
	time.AfterFunc(c.authTimeout(), func() { c.authExpired(gen) })
}

// authExpired gives up waiting for services: the channels are joined anyway.
func (c *Conn) authExpired(gen int) {
	c.mu.Lock()
	if c.auth.gen != gen || !c.auth.waiting {
		c.mu.Unlock()
		return
	}
	c.auth.waiting = false
	c.health.Auth = AuthUnconfirmed
	held := c.takeHeld()
	c.mu.Unlock()
	c.noteT(true, messages.T("log.servicesSilent", "wait", durText(c.authTimeout())))
	c.noteT(false, HintText("services didn't answer the login"))
	c.sendJoins(held)
}

// authDone records services' answer: the held joins go out either way.
func (c *Conn) authDone(gen int, ok bool, said string) {
	c.mu.Lock()
	if c.auth.gen != gen || !c.auth.waiting {
		c.mu.Unlock()
		return
	}
	c.auth.waiting = false
	c.health.Auth = map[bool]string{true: AuthIdentified, false: AuthFailed}[ok]
	held := c.takeHeld()
	c.mu.Unlock()
	if ok {
		c.noteT(false, messages.T("log.identified", "said", said))
		c.ghost()
	} else {
		c.noteT(true, messages.T("log.loginRefused", "said", said))
		if h := HintText(said); !h.IsZero() {
			c.noteT(false, h)
		} else {
			c.noteT(false, HintText("password incorrect"))
		}
	}
	c.sendJoins(held)
}

// takeHeld hands over the held joins. Holds c.mu.
func (c *Conn) takeHeld() []string {
	held := c.auth.held
	c.auth.held, c.auth.holding = nil, false
	return held
}

// servicesSaid looks at a notice or message from services: the prompt to
// identify, the login's answer, or a ghost gone.
func (c *Conn) servicesSaid(m irc.Message) {
	text := strings.TrimSpace(formatting.ReplaceAllString(lastParam(m), ""))
	c.mu.Lock()
	st := c.auth
	c.mu.Unlock()
	switch {
	case st.prompt && servicesPrompt.MatchString(text):
		c.identify(st.gen)
	case st.waiting && servicesFail.MatchString(text):
		c.authDone(st.gen, false, text)
	case st.waiting && servicesOK.MatchString(text):
		c.authDone(st.gen, true, text)
	case st.ghosted && servicesFreed.MatchString(text):
		c.takeNick(st.gen)
	}
}

// servicesMissing handles "no such nick" (401) or "services are down"
// (440) for the services target while a login waits.
func (c *Conn) servicesMissing(m irc.Message) {
	c.mu.Lock()
	gen, waiting := c.auth.gen, c.auth.waiting
	c.mu.Unlock()
	if !waiting || len(m.Params) < 2 {
		return
	}
	target := m.Params[1]
	t := strings.TrimPrefix(servicesTarget(c.net), "/")
	if c.net.Auth() == store.AuthCommand {
		t = commandTarget(c.net)
	}
	if m.Command == "401" && !strings.EqualFold(target, t) {
		return
	}
	c.authDone(gen, false, fmt.Sprintf("no services nick %s on this network (%s)", target, lastParam(m)))
}

// ghost asks services to free the nick when evrd is on another one.
func (c *Conn) ghost() {
	n := c.net
	if n.GhostCmd == "" {
		return
	}
	c.mu.Lock()
	if c.auth.ghosted || strings.EqualFold(c.nick, n.Nick) {
		c.mu.Unlock()
		return
	}
	c.auth.ghosted = true
	gen := c.auth.gen
	c.mu.Unlock()
	text := strings.ToUpper(n.GhostCmd) + " " + n.Nick
	if n.Password != "" {
		text += " " + n.Password
	}
	c.send(servicesLine(servicesTarget(n), text))
	c.noteT(false, messages.T("log.ghosting", "nick", n.Nick, "services", servicesTarget(n), "command", n.GhostCmd))
	time.AfterFunc(ghostWait, func() { c.takeNick(gen) })
}

// takeNick switches to the network's nick after a ghost (once).
func (c *Conn) takeNick(gen int) {
	c.mu.Lock()
	ok := c.auth.gen == gen && c.auth.ghosted && !c.auth.nickSent && c.w != nil && !strings.EqualFold(c.nick, c.net.Nick)
	if ok {
		c.auth.nickSent = true
	}
	c.mu.Unlock()
	if ok {
		c.send("NICK " + c.net.Nick)
	}
}

// nickChanged follows our own nick: back on the network's nick, a login
// waiting for it goes out.
func (c *Conn) nickChanged(nick string) {
	c.mu.Lock()
	gen, after := c.auth.gen, c.auth.afterNick
	c.mu.Unlock()
	if after && strings.EqualFold(nick, c.net.Nick) {
		c.identify(gen)
	}
}

// saslFailed logs a refused or impossible SASL login (once).
func (c *Conn) saslFailed(why messages.Text) {
	method := c.net.Auth()
	c.mu.Lock()
	if c.auth.saslFailed {
		c.mu.Unlock()
		return
	}
	c.auth.saslFailed = true
	if method == store.AuthSASL {
		c.health.Auth = AuthFailed
	}
	c.mu.Unlock()
	c.noteT(true, messages.T("log.saslFailed", "reason", why))
	if method == store.AuthAuto {
		c.noteT(false, messages.T("log.identifyInstead", "services", servicesTarget(c.net)))
	} else if h := HintText(why.English()); !h.IsZero() {
		c.noteT(false, h)
	}
}

// sendJoins joins channels, after the join delay if there is one.
func (c *Conn) sendJoins(joins []string) {
	if len(joins) == 0 {
		return
	}
	c.mu.Lock()
	delay := c.joinDelay
	c.mu.Unlock()
	if delay <= 0 {
		for _, l := range joins {
			c.send(l)
		}
		return
	}
	go func() {
		time.Sleep(delay)
		for _, l := range joins {
			c.send(l)
		}
	}()
}
