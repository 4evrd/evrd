package upstream

// Running a network's modules (internal/modules) inside its connection.

import (
	"context"
	"strings"
	"sync"
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
		f()
		h.c.modMu.Unlock()
		if chans := h.c.takeAttached(); len(chans) > 0 { // on a timer: no line to go with
			h.c.broadcastRaw(Line{Attach: chans})
		}
	})
}

func (h modHost) Reply(text string) {
	h.c.broadcastRaw(Line{Text: ":" + h.c.replyFrom + " NOTICE " + h.c.Nick() + " :" + text})
}

// ClientAttached: the user isn't away (no IRC or web client attached is
// what Idle reports).
func (h modHost) ClientAttached() bool { return h.c.idle == nil || !h.c.idle() }

func (h modHost) Detached(channel string) bool { return h.c.ChannelSettings(channel).Detached }

// AttachChannel attaches a detached channel at once (and saves that).
// Clients hear of it with the line being handled (Line.Attach), which
// they then get in the channel's playback, not twice.
func (h modHost) AttachChannel(channel string) {
	c := h.c
	if !c.ChannelSettings(channel).Detached {
		return
	}
	m := c.SetChannelSettings(channel, func(cs *store.ChannelSettings) { cs.Detached = false })
	if c.onChanSettings != nil {
		c.onChanSettings(m)
	}
	c.mu.Lock()
	c.attached = append(c.attached, channel)
	c.mu.Unlock()
}

// takeAttached returns the channels modules attached since last asked.
func (c *Conn) takeAttached() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	chans := c.attached
	c.attached = nil
	return chans
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
	c.useModules(c.newModules(ctx))
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
	c.useModules(c.newModules(c.modCtx))
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

// useModules puts new module instances in place (holds modMu).
func (c *Conn) useModules(mods []modules.Module) {
	c.mods = mods
	var rw []modules.ClientRewriter
	for _, m := range mods {
		if r, ok := m.(modules.ClientRewriter); ok {
			rw = append(rw, r)
		}
	}
	c.mu.Lock()
	c.rewriters = rw
	c.mu.Unlock()
}

// modsRewrite lets the modules change (or drop) a line from the server
// before evrd handles it; changed says one did.
func (c *Conn) modsRewrite(m *irc.Message) (keep, changed bool) {
	c.modMu.Lock()
	defer c.modMu.Unlock()
	before := ""
	for _, mod := range c.mods {
		r, ok := mod.(modules.LineRewriter)
		if !ok {
			continue
		}
		if before == "" {
			before = m.String()
		}
		if !r.RewriteLine(m) {
			return false, true
		}
	}
	return true, before != "" && m.String() != before
}

// rewriteOut is what a line to the server becomes once the modules have
// had it (encrypted by crypt): the line, several, or none.
func (c *Conn) rewriteOut(line string) []string {
	c.mu.Lock()
	rws := c.rewriters
	c.mu.Unlock()
	lines := []string{line}
	for _, r := range rws {
		var next []string
		for _, l := range lines {
			next = append(next, r.RewriteClientLine(l)...)
		}
		lines = next
	}
	return lines
}

// withTagsOf is text (a line rewritten) with the tags the original line had.
func withTagsOf(orig, text string) string {
	if strings.HasPrefix(orig, "@") {
		tags, _, _ := strings.Cut(orig, " ")
		return tags + " " + text
	}
	return text
}

// SaveSetting saves a setting a module changed itself, without starting
// the modules afresh (crypt keeps the key exchanges it's in the middle of).
func (h modHost) SaveSetting(module, key, value string) {
	c := h.c
	c.mu.Lock()
	cfgs := make(map[string]store.ModuleConfig, len(c.modCfg))
	for k, v := range c.modCfg {
		cfgs[k] = v
	}
	mc := cfgs[module]
	settings := map[string]string{}
	for k, v := range mc.Settings {
		settings[k] = v
	}
	settings[key] = value
	mc.Settings = settings
	cfgs[module] = mc
	c.modCfg = cfgs // so the saved network doesn't look changed (Reconfigure)
	save := c.saveModule
	c.mu.Unlock()
	if save == nil {
		return
	}
	go func() { // (not under modMu: saving may reconfigure)
		// one at a time, each the latest value: an older one never lands last
		saveMu.Lock()
		defer saveMu.Unlock()
		c.mu.Lock()
		value := c.modCfg[module].Settings[key]
		c.mu.Unlock()
		save(module, key, value)
	}()
}

// saveMu keeps the modules' saves of their settings one at a time.
var saveMu sync.Mutex

// Note is something a module tells the user, as from a nick of its own
// (*crypt): each IRC client gets it in the user's language.
type Note struct {
	From string
	Text messages.Text
}

// Notify tells the user's IRC clients something, as from the module.
func (h modHost) Notify(from string, t messages.Text) {
	h.c.broadcastRaw(Line{Note: Note{From: from, Text: t}})
}
