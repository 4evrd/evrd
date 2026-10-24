package upstream

// Running a network's modules (internal/modules) inside its connection.

import (
	"context"
	"strings"
	"time"

	"github.com/4evrd/evrd/internal/irc"
	"github.com/4evrd/evrd/internal/messages"
	"github.com/4evrd/evrd/internal/modules"
	"github.com/4evrd/evrd/internal/store"
)

// modHost is what modules see of the connection.
type modHost struct {
	c   *Conn
	ctx context.Context // this connection: timers stop when it ends
}

func (h modHost) Send(line string) { h.c.send(line) }

// SendAsUser: messages and notices go the way a client's do (saved, and
// shown on the user's other clients); anything else as a user's line.
func (h modHost) SendAsUser(line string) {
	m := irc.Parse(line)
	if (m.Command == "PRIVMSG" || m.Command == "NOTICE") && len(m.Params) == 2 {
		h.c.SendFrom("", m.Command, m.Params[0], m.Params[1])
		return
	}
	h.c.sendUser(line)
}
func (h modHost) Nick() string               { return h.c.Nick() }
func (h modHost) WantedNick() string         { return h.c.net.Nick }
func (h modHost) Joined(channel string) bool { return h.c.Joined(channel) }
func (h modHost) Channels() []string         { return h.c.Channels() }
func (h modHost) Log(t messages.Text)        { h.c.noteT(false, t) }
func (h modHost) Account() (string, string)  { return h.c.net.SASLUser, h.c.net.Password }

func (h modHost) Members(channel string) []modules.Member {
	var out []modules.Member
	for _, m := range h.c.Members(channel) {
		out = append(out, modules.Member{Nick: m.Nick, Prefix: m.Prefix})
	}
	return out
}

// IsOp: halfop or better (@, &, ~, %).
func (h modHost) IsOp(channel string) bool {
	me := h.c.Nick()
	for _, m := range h.c.Members(channel) {
		if strings.EqualFold(m.Nick, me) {
			return strings.ContainsAny(m.Prefix, "~&@%")
		}
	}
	return false
}

func (h modHost) After(d time.Duration, f func()) {
	time.AfterFunc(d, func() {
		if h.ctx.Err() != nil {
			return
		}
		h.c.modMu.Lock()
		defer h.c.modMu.Unlock()
		f()
	})
}

func (h modHost) Reply(text string) {
	h.c.broadcastRaw(Line{Text: ":*evrd!evrd@evrd NOTICE " + h.c.Nick() + " :" + text})
}

func toModuleConfigs(in map[string]store.ModuleConfig) map[string]modules.Config {
	out := map[string]modules.Config{}
	for k, v := range in {
		out[k] = modules.Config{Enabled: v.Enabled, Settings: v.Settings}
	}
	return out
}

// startModules creates the network's modules for a new connection.
func (c *Conn) startModules(ctx context.Context) {
	c.modMu.Lock()
	defer c.modMu.Unlock()
	c.modCtx = ctx
	c.mods = c.newModules(ctx)
}

func (c *Conn) newModules(ctx context.Context) []modules.Module {
	c.mu.Lock()
	cfgs := c.modCfg
	c.mu.Unlock()
	var off map[string]bool
	if c.modsOff != nil {
		off = c.modsOff()
	}
	return modules.Start(modHost{c: c, ctx: ctx}, toModuleConfigs(cfgs), off)
}

// setModules changes the modules while connected: new instances, started
// at once (no reconnect; perform's commands wait for the next sign-in).
func (c *Conn) setModules(cfgs map[string]store.ModuleConfig) {
	c.mu.Lock()
	c.modCfg = cfgs
	c.mu.Unlock()
	c.modMu.Lock()
	defer c.modMu.Unlock()
	if c.modCtx == nil {
		return // not connected: they start with the next connection
	}
	c.mods = c.newModules(c.modCtx)
	if c.Status() == StatusConnected {
		for _, m := range c.mods {
			if s, ok := m.(interface{ Started() }); ok {
				s.Started()
			}
		}
	}
}

// RestartModules starts the modules afresh (the server-wide off list
// changed).
func (c *Conn) RestartModules() {
	c.mu.Lock()
	cfgs := c.modCfg
	c.mu.Unlock()
	c.setModules(cfgs)
}

func (c *Conn) modsRegistered() {
	c.modMu.Lock()
	defer c.modMu.Unlock()
	for _, m := range c.mods {
		m.Registered()
	}
}

func (c *Conn) modsLine(m irc.Message) {
	c.modMu.Lock()
	defer c.modMu.Unlock()
	for _, mod := range c.mods {
		mod.Line(m)
	}
}

// modsDropCTCP reports whether a module (ctcpflood) drops a CTCP request.
func (c *Conn) modsDropCTCP(from, command string) bool {
	c.modMu.Lock()
	defer c.modMu.Unlock()
	for _, m := range c.mods {
		if f, ok := m.(modules.CTCPFilter); ok && f.CTCP(from, command) {
			return true
		}
	}
	return false
}

// modsLogIn reports whether a module (q) logs in itself.
func (c *Conn) modsLogIn() bool {
	c.modMu.Lock()
	defer c.modMu.Unlock()
	for _, m := range c.mods {
		if l, ok := m.(modules.LoginHandler); ok && l.HandlesLogin() {
			return true
		}
	}
	return false
}

// ClientLine lets the modules act on a line from one of the user's IRC
// clients (alias); true means one did, and the line isn't sent on.
func (c *Conn) ClientLine(line string) bool {
	c.modMu.Lock()
	defer c.modMu.Unlock()
	for _, m := range c.mods {
		if f, ok := m.(modules.ClientFilter); ok && f.ClientLine(line) {
			return true
		}
	}
	return false
}
