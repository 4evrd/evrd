package modules

// The modules that come with evrd.

import (
	"fmt"
	"strings"
	"time"

	"github.com/4evrd/evrd/internal/irc"
	"github.com/4evrd/evrd/internal/messages"
)

func init() {
	register(Info{
		Name: "perform", ZNC: "perform", Title: "Perform",
		Description: "Commands sent to the server every time evrd signs in: OPER, a MODE, a login to a service the network has.",
		Settings: []Setting{{Key: "commands", Label: "Commands", Kind: "lines",
			Help: "One per line, as you'd type them in a client: /msg NickServ …, /mode %nick% +x, or raw IRC. %nick% is your nick."}},
		new: func(h Host, s Settings) Module { return &perform{h: h, cmds: s.Lines("commands")} },
	})
	register(Info{
		Name: "keepnick", ZNC: "keepnick", Title: "Keep nick",
		Description: "When your nick is taken, wins it back as soon as it frees up (and tries every 30 seconds meanwhile).",
		new:         func(h Host, s Settings) Module { return &keepnick{h: h} },
	})
	register(Info{
		Name: "kickrejoin", ZNC: "kickrejoin", Title: "Rejoin on kick",
		Description: "Rejoins a channel you were kicked from, after a delay.",
		Settings:    []Setting{{Key: "delay", Label: "Delay (seconds)", Kind: "number", Default: "10"}},
		new:         func(h Host, s Settings) Module { return &kickrejoin{h: h, delay: s.Int("delay", 10)} },
	})
	register(Info{
		Name: "autocycle", ZNC: "autocycle", Title: "Autocycle",
		Description: "When you're left alone in a channel without ops, leaves and rejoins it so you get ops.",
		Settings: []Setting{{Key: "channels", Label: "Channels", Kind: "lines", Default: "*",
			Help: "Which channels, one per line; * and ? match anything. Start a line with ! to leave a channel out."}},
		new: func(h Host, s Settings) Module {
			return &autocycle{h: h, chans: s.Lines("channels"), last: map[string]time.Time{}}
		},
	})
	for _, m := range []struct{ name, title, mode, what string }{{"autoop", "Auto-op", "o", "ops"}, {"autovoice", "Auto-voice", "v", "voice"}} {
		register(Info{
			Name: m.name, ZNC: m.name, Title: m.title,
			Description: fmt.Sprintf("Gives %s to the people you list when they join a channel where you have ops.", m.what),
			Settings: []Setting{{Key: "users", Label: "Who", Kind: "lines",
				Help: "One per line: a nick!user@host mask, then optionally the channels (comma-separated, wildcards allowed). Example: *!*@friend.example.org #home,#work"}},
			new: func(h Host, s Settings) Module {
				return &autogive{h: h, mode: m.mode, rules: parseRules(s.Lines("users"))}
			},
		})
	}
	register(Info{
		Name: "alias", ZNC: "alias", Title: "Aliases",
		Description: "Your own commands for IRC clients: /op #chan nick turned into what you define.",
		Settings: []Setting{{Key: "aliases", Label: "Aliases", Kind: "lines",
			Help: "One per line: name = commands. %1, %2… are the words after it, %1+ the rest from there, %nick% your nick; separate several commands with ;. Example: op = /mode %1 +o %2"}},
		new: func(h Host, s Settings) Module { return &alias{h: h, defs: parseAliases(s.Lines("aliases"))} },
	})
	register(Info{
		Name: "ctcpflood", ZNC: "ctcpflood", Title: "CTCP flood protection",
		Description: "Stops answering CTCP requests (and passing them to clients) while too many arrive at once.",
		Settings: []Setting{
			{Key: "limit", Label: "Most CTCPs", Kind: "number", Default: "4"},
			{Key: "seconds", Label: "In seconds", Kind: "number", Default: "2"},
		},
		new: func(h Host, s Settings) Module {
			return &ctcpflood{h: h, limit: max(1, s.Int("limit", 4)), window: time.Duration(max(1, s.Int("seconds", 2))) * time.Second}
		},
	})
	register(Info{
		Name: "q", ZNC: "q", Title: "QuakeNet Q",
		Description: "Logs in to QuakeNet's Q with the network's account and password (under Authentication) instead of NickServ, and hides your host.",
		Settings:    []Setting{{Key: "hidehost", Label: "Hide my host (+x)", Kind: "bool", Default: "yes"}},
		new:         func(h Host, s Settings) Module { return &qauth{h: h, hide: s.Bool("hidehost")} },
	})
}

// --- perform ------------------------------------------------------------------

type perform struct {
	h    Host
	cmds []string
}

func (p *perform) Registered() {
	n := 0
	for _, c := range p.cmds {
		for _, l := range ClientCommand(strings.ReplaceAll(c, "%nick%", p.h.Nick())) {
			p.h.Send(l)
			n++
		}
	}
	if n > 0 {
		p.h.Log(messages.T("module.log.perform", "count", n))
	}
}

func (p *perform) Line(irc.Message) {}

// ClientCommand turns a command as typed in a client ("/msg x hi", "/mode
// …") into IRC lines; anything else is sent as it is.
func ClientCommand(c string) []string {
	c = strings.TrimSpace(c)
	if c == "" {
		return nil
	}
	if !strings.HasPrefix(c, "/") {
		return []string{c}
	}
	cmd, rest, _ := strings.Cut(c[1:], " ")
	rest = strings.TrimSpace(rest)
	target, text, _ := strings.Cut(rest, " ")
	switch strings.ToLower(cmd) {
	case "msg", "privmsg", "query":
		return []string{"PRIVMSG " + target + " :" + text}
	case "notice":
		return []string{"NOTICE " + target + " :" + text}
	case "me":
		return nil // needs a target; meaningless here
	case "quote", "raw":
		return []string{rest}
	}
	if rest == "" {
		return []string{strings.ToUpper(cmd)}
	}
	return []string{strings.ToUpper(cmd) + " " + rest}
}

// --- keepnick -----------------------------------------------------------------

type keepnick struct {
	h       Host
	waiting bool
}

func (k *keepnick) Registered() { k.check() }

// Started is called when it's switched on while connected.
func (k *keepnick) Started() { k.check() }

func (k *keepnick) check() {
	if k.waiting || strings.EqualFold(k.h.Nick(), k.h.WantedNick()) {
		return
	}
	k.h.Log(messages.T("module.log.keepnick", "nick", k.h.WantedNick()))
	k.waiting = true
	var try func()
	try = func() {
		if strings.EqualFold(k.h.Nick(), k.h.WantedNick()) {
			k.waiting = false
			return
		}
		k.h.Send("NICK " + k.h.WantedNick())
		k.h.After(30*time.Second, try)
	}
	k.h.After(30*time.Second, try)
}

func (k *keepnick) Line(m irc.Message) {
	want := k.h.WantedNick()
	if strings.EqualFold(k.h.Nick(), want) {
		return
	}
	switch m.Command {
	case "QUIT", "NICK": // whoever had it left or changed nick
		if strings.EqualFold(nickOf(m.Prefix), want) {
			k.h.Send("NICK " + want)
		}
	}
}

// --- kickrejoin ---------------------------------------------------------------

type kickrejoin struct {
	h     Host
	delay int
}

func (k *kickrejoin) Registered() {}

func (k *kickrejoin) Line(m irc.Message) {
	if m.Command != "KICK" || len(m.Params) < 2 || !strings.EqualFold(m.Params[1], k.h.Nick()) {
		return
	}
	ch := m.Params[0]
	k.h.Log(messages.T("module.log.kickrejoin", "channel", ch, "by", nickOf(m.Prefix), "seconds", k.delay))
	k.h.After(time.Duration(k.delay)*time.Second, func() {
		if !k.h.Joined(ch) {
			k.h.Send("JOIN " + ch)
		}
	})
}

// --- autocycle ----------------------------------------------------------------

type autocycle struct {
	h     Host
	chans []string
	last  map[string]time.Time
}

func (a *autocycle) Registered() {}

func (a *autocycle) Line(m irc.Message) {
	var chans []string
	switch m.Command {
	case "PART", "KICK":
		if len(m.Params) > 0 {
			chans = []string{m.Params[0]}
		}
	case "QUIT":
		chans = a.h.Channels() // evrd has already taken them out of each
	default:
		return
	}
	for _, ch := range chans {
		if !a.h.Joined(ch) || a.h.IsOp(ch) || len(a.h.Members(ch)) != 1 || !matchChannel(a.chans, ch) {
			continue
		}
		key := strings.ToLower(ch)
		if time.Since(a.last[key]) < time.Minute {
			continue // don't loop if it doesn't work
		}
		a.last[key] = time.Now()
		a.h.Log(messages.T("module.log.autocycle", "channel", ch))
		a.h.Send("PART " + ch)
		a.h.Send("JOIN " + ch)
	}
}

// matchChannel: patterns with * and ?, "!" to exclude; no patterns = all.
func matchChannel(patterns []string, ch string) bool {
	if len(patterns) == 0 {
		return true
	}
	in := false
	for _, p := range patterns {
		if strings.HasPrefix(p, "!") {
			if Match(p[1:], ch) {
				return false
			}
			continue
		}
		if Match(p, ch) {
			in = true
		}
	}
	return in
}

// --- autoop / autovoice -------------------------------------------------------

type rule struct {
	mask  string
	chans []string
}

func parseRules(lines []string) []rule {
	var out []rule
	for _, l := range lines {
		f := strings.Fields(l)
		r := rule{mask: f[0]}
		if !strings.Contains(r.mask, "!") {
			r.mask += "!*@*" // a bare nick
		}
		if len(f) > 1 {
			r.chans = strings.Split(f[1], ",")
		}
		out = append(out, r)
	}
	return out
}

type autogive struct {
	h     Host
	mode  string
	rules []rule
}

func (a *autogive) Registered() {}

func (a *autogive) Line(m irc.Message) {
	if m.Command != "JOIN" || len(m.Params) == 0 {
		return
	}
	nick, ch := nickOf(m.Prefix), m.Params[0]
	if strings.EqualFold(nick, a.h.Nick()) {
		return
	}
	for _, r := range a.rules {
		if Match(r.mask, m.Prefix) && matchChannel(r.chans, ch) {
			// a moment, so services and other ops go first
			a.h.After(2*time.Second, func() {
				if !a.h.IsOp(ch) {
					return
				}
				for _, mem := range a.h.Members(ch) {
					if strings.EqualFold(mem.Nick, nick) && !strings.Contains(mem.Prefix, map[string]string{"o": "@", "v": "+"}[a.mode]) {
						a.h.Send("MODE " + ch + " +" + a.mode + " " + nick)
						return
					}
				}
			})
			return
		}
	}
}

// --- alias --------------------------------------------------------------------

func parseAliases(lines []string) map[string]string {
	out := map[string]string{}
	for _, l := range lines {
		name, def, ok := strings.Cut(l, "=")
		name = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(name), "/"))
		if ok && name != "" && !strings.ContainsAny(name, " \t") {
			out[name] = strings.TrimSpace(def)
		}
	}
	return out
}

type alias struct {
	h    Host
	defs map[string]string
}

func (a *alias) Registered()      {}
func (a *alias) Line(irc.Message) {}

func (a *alias) ClientLine(line string) bool {
	cmd, rest, _ := strings.Cut(line, " ")
	def, ok := a.defs[strings.ToLower(cmd)]
	if !ok {
		return false
	}
	args := strings.Fields(rest)
	expanded := def
	for i := 9; i >= 1; i-- { // %10 before %1, and %1+ before %1
		val, plus := "", ""
		if i <= len(args) {
			val, plus = args[i-1], strings.Join(args[i-1:], " ")
		}
		expanded = strings.ReplaceAll(expanded, fmt.Sprintf("%%%d+", i), plus)
		expanded = strings.ReplaceAll(expanded, fmt.Sprintf("%%%d", i), val)
	}
	expanded = strings.ReplaceAll(expanded, "%nick%", a.h.Nick())
	for _, c := range strings.Split(expanded, ";") {
		for _, l := range ClientCommand(c) {
			a.h.SendAsUser(l)
		}
	}
	return true
}

// --- ctcpflood ----------------------------------------------------------------

type ctcpflood struct {
	h        Host
	limit    int
	window   time.Duration
	recent   []time.Time
	flooding bool
}

func (c *ctcpflood) Registered()      {}
func (c *ctcpflood) Line(irc.Message) {}

func (c *ctcpflood) CTCP(from, command string) bool {
	now := time.Now()
	keep := c.recent[:0]
	for _, t := range c.recent {
		if now.Sub(t) < c.window {
			keep = append(keep, t)
		}
	}
	c.recent = append(keep, now)
	if len(c.recent) > c.limit {
		if !c.flooding {
			c.flooding = true
			c.h.Log(messages.T("module.log.ctcpflood", "limit", c.limit, "window", c.window, "command", command, "from", from))
		}
		return true
	}
	c.flooding = false
	return false
}

// --- q ------------------------------------------------------------------------

type qauth struct {
	h    Host
	hide bool
}

// HandlesLogin tells the connection not to identify to NickServ: Q does it.
func (q *qauth) HandlesLogin() bool { return true }

func (q *qauth) Registered() {
	if user, pass := q.h.Account(); pass != "" {
		if user == "" {
			user = q.h.Nick()
		}
		q.h.Send("PRIVMSG Q@CServe.quakenet.org :AUTH " + user + " " + pass)
		q.h.Log(messages.T("module.log.qLogin", "account", user))
	}
	if q.hide {
		q.h.Send("MODE " + q.h.Nick() + " +x")
	}
}

func (q *qauth) Line(m irc.Message) {
	if m.Command == "NOTICE" && strings.HasPrefix(strings.ToLower(m.Prefix), "q!") && len(m.Params) > 1 {
		q.h.Log(messages.T("module.log.qSaid", "said", m.Params[len(m.Params)-1]))
	}
}

// LoginHandler is implemented by modules that log in themselves (so evrd
// doesn't identify to NickServ as well).
type LoginHandler interface{ HandlesLogin() bool }
