package modules

// autoattach: brings a detached channel back to your IRC clients when
// something said there matches a rule, as ZNC's autoattach does.

import (
	"strings"

	"github.com/4evrd/evrd/internal/irc"
	"github.com/4evrd/evrd/internal/messages"
)

func init() {
	register(Info{
		Name: "autoattach", ZNC: "autoattach", Title: "Auto-attach",
		Description: "Brings a detached channel back to your IRC clients when a message there matches one of your rules.",
		Settings: []Setting{{Key: "rules", Label: "Rules", Kind: "lines",
			Help: "One per line: a channel, then optionally the text to look for and a nick!user@host mask; * and ? match anything. Start a line with ! to never attach on what it matches. Example: #home *evrd* *!*@friend.example.org"}},
		new: func(h Host, s Settings) Module { return &autoattach{h: h, rules: parseAttachRules(s.Lines("rules"))} },
	})
}

// attachRule is one line of rules: ZNC's [!]#channel [search] [host].
type attachRule struct {
	not                  bool
	channel, search, who string
}

func parseAttachRules(lines []string) []attachRule {
	var out []attachRule
	for _, l := range lines {
		f := strings.Fields(l)
		r := attachRule{search: "*", who: "*"}
		r.channel, r.not = strings.CutPrefix(f[0], "!")
		if len(f) > 1 {
			r.search = f[1]
		}
		if len(f) > 2 {
			r.who = f[2]
		}
		if r.channel != "" {
			out = append(out, r)
		}
	}
	return out
}

func (r attachRule) matches(channel, prefix, text string) bool {
	return Match(r.channel, channel) && Match(r.search, text) && Match(r.who, prefix)
}

type autoattach struct {
	h     Host
	rules []attachRule
}

func (a *autoattach) Registered() {}

func (a *autoattach) Line(m irc.Message) {
	if m.Command != "PRIVMSG" && m.Command != "NOTICE" || len(m.Params) < 2 {
		return
	}
	ch, text := m.Params[0], m.Params[len(m.Params)-1]
	if !a.h.Joined(ch) || !a.h.Detached(ch) || strings.EqualFold(nickOf(m.Prefix), a.h.Nick()) {
		return
	}
	if t, ok := strings.CutPrefix(text, "\x01ACTION "); ok { // /me: its words
		text = strings.TrimSuffix(t, "\x01")
	}
	hit := false
	for _, r := range a.rules {
		if r.matches(ch, m.Prefix, text) {
			if r.not {
				return // a ! rule wins
			}
			hit = true
		}
	}
	if hit {
		a.h.Log(messages.T("module.log.autoattach", "channel", ch, "nick", nickOf(m.Prefix)))
		a.h.AttachChannel(ch)
	}
}
