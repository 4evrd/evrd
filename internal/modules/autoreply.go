package modules

// autoreply: while you're away (nothing attached), answers private messages
// with a notice, as ZNC's autoreply does.

import (
	"strings"
	"time"

	"github.com/4evrd/evrd/internal/irc"
	"github.com/4evrd/evrd/internal/messages"
)

// DefaultAutoReply is ZNC's reply.
const DefaultAutoReply = "I am currently away, try again later"

func init() {
	register(Info{
		Name: "autoreply", ZNC: "autoreply", Title: "Auto-reply",
		Description: "While no client is attached, answers private messages with a notice saying you're away (once per person per interval).",
		Settings: []Setting{
			{Key: "reply", Label: "Reply", Kind: "text", Default: DefaultAutoReply,
				Help: "The notice they get. %nick% is your nick, %sender% theirs."},
			{Key: "interval", Label: "Interval (minutes)", Kind: "number", Default: "2",
				Help: "Answers the same person at most once in this many minutes."},
		},
		new: func(h Host, s Settings) Module {
			reply := strings.TrimSpace(s["reply"])
			if reply == "" {
				reply = DefaultAutoReply
			}
			return &autoreply{h: h, reply: reply, every: time.Duration(max(0, s.Int("interval", 2))) * time.Minute, last: map[string]time.Time{}}
		},
	})
}

type autoreply struct {
	h     Host
	reply string
	every time.Duration
	last  map[string]time.Time // lowercased nick -> when we last answered
	now   func() time.Time     // for tests
}

func (a *autoreply) Registered() {}

func (a *autoreply) Line(m irc.Message) {
	if m.Command != "PRIVMSG" || len(m.Params) < 2 || !strings.EqualFold(m.Params[0], a.h.Nick()) {
		return // channels, and everything else
	}
	nick, text := nickOf(m.Prefix), m.Params[len(m.Params)-1]
	switch {
	case strings.HasPrefix(text, "\x01"): // CTCP
		return
	case hostOf(m.Prefix) == "" || isService(nick): // a server, or services
		return
	case strings.EqualFold(nick, a.h.Nick()):
		return
	case a.h.ClientAttached():
		return
	}
	now := time.Now()
	if a.now != nil {
		now = a.now()
	}
	key := strings.ToLower(nick)
	if t, ok := a.last[key]; ok && now.Sub(t) < a.every {
		return
	}
	for k, t := range a.last { // forget the ones whose interval is over
		if now.Sub(t) >= a.every {
			delete(a.last, k)
		}
	}
	a.last[key] = now
	r := strings.NewReplacer("%nick%", a.h.Nick(), "%sender%", nick).Replace(a.reply)
	a.h.Send("NOTICE " + nick + " :" + r)
	a.h.Log(messages.T("module.log.autoreply", "nick", nick))
}

// isService reports whether a nick looks like a network service (NickServ,
// ChanServ, Q, *status…), which mustn't be answered.
func isService(nick string) bool {
	l := strings.ToLower(nick)
	return strings.HasSuffix(l, "serv") || l == "q" || strings.HasPrefix(l, "*") || strings.Contains(l, ".")
}
