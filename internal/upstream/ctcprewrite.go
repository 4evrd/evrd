package upstream

// Modules that rewrite CTCPs on their way through (modules.CTCPRewriter:
// bouncedcc's DCC offers), both ways.

import (
	"strings"

	"github.com/4evrd/evrd/internal/irc"
	"github.com/4evrd/evrd/internal/modules"
)

// ClientIPs: where the user's IRC clients attached from (Options.ClientIPs).
func (h modHost) ClientIPs() []string {
	if h.c.clientIPs == nil {
		return nil
	}
	return h.c.clientIPs()
}

// serverCTCP lets the modules change a CTCP sent to us before evrd handles
// it and the clients get it: m and its raw line are changed to match.
// False means a module dropped it.
func (c *Conn) serverCTCP(m *irc.Message, line *string) bool {
	text := m.Params[1]
	if len(text) < 2 || text[0] != '\x01' || !strings.EqualFold(m.Params[0], c.Nick()) {
		return true
	}
	c.modMu.Lock()
	out := text
	for _, mod := range c.mods {
		if r, ok := mod.(modules.CTCPRewriter); ok && out != "" {
			out = r.ServerCTCP(m.Prefix, out)
		}
	}
	c.modMu.Unlock()
	if out == "" {
		return false
	}
	if out != text {
		// the text is the line's last parameter, at its end
		if i := strings.LastIndex(*line, text); i >= 0 {
			*line = (*line)[:i] + out + (*line)[i+len(text):]
		}
		m.Params[1] = out
	}
	return true
}

// ClientCTCP lets the modules change a CTCP (a PRIVMSG's text) one of the
// user's IRC clients, at clientIP, sends target. It returns the text to
// send ("" for nothing).
func (c *Conn) ClientCTCP(target, text, clientIP string) string {
	if len(text) < 2 || text[0] != '\x01' {
		return text
	}
	c.modMu.Lock()
	defer c.modMu.Unlock()
	for _, mod := range c.mods {
		if r, ok := mod.(modules.CTCPRewriter); ok && text != "" {
			text = r.ClientCTCP(target, text, clientIP)
		}
	}
	return text
}
