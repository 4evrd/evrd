package modules

// crypt: FiSH-style encryption for the channels and people the user has a
// key for, like ZNC's crypt. Messages, notices, actions and topics the user
// sends there go out encrypted (fish.go), and theirs are decrypted before
// evrd saves them or passes them on, marked so the user can tell. Keys are
// set in the module's settings, or from an IRC client as *crypt (setkey,
// delkey, listkeys), and keyx swaps one with someone by DH1080 (dh1080.go).

import (
	"sort"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/4evrd/evrd/internal/irc"
	"github.com/4evrd/evrd/internal/messages"
)

// cryptNick is the nick the user talks to the module as.
const cryptNick = "*crypt"

func init() {
	register(Info{
		Name: "crypt", ZNC: "crypt", Title: "Encryption (FiSH)",
		Description: "Encrypts what you send to the channels and people you have a key for, the FiSH way (Blowfish, \"+OK …\"), and decrypts what they send. Keys can be swapped with DH1080. From an IRC client: /msg *crypt help.",
		Settings: []Setting{
			{Key: "keys", Label: "Keys", Kind: "lines",
				Help: "One per line: a channel or nick, then its key. Start the key with cbc: for CBC mode (newer FiSH clients), else it's ECB. Example: #secret cbc:correct horse battery"},
			{Key: "mark", Label: "Mark decrypted messages with", Kind: "text", Default: "🔒",
				Help: "Put before each message that arrived encrypted, so you can tell. Empty for no mark."},
		},
		new: func(h Host, s Settings) Module {
			c := &crypt{h: h, keys: map[string]cryptKey{}, dh: map[string]dhPair{}}
			if c.mark = strings.TrimSpace(s["mark"]); c.mark != "" {
				c.mark += " "
			}
			for _, l := range s.Lines("keys") {
				target, key, _ := strings.Cut(l, " ")
				if k, ok := parseCryptKey(target, key); ok {
					c.keys[strings.ToLower(target)] = k
				}
			}
			return c
		},
	})
}

// cryptKey is a channel's or nick's key.
type cryptKey struct {
	target, key string
	cbc         bool
}

// parseCryptKey reads "key", "cbc:key" or "ecb:key" (ECB is the default:
// what every FiSH client understands).
func parseCryptKey(target, s string) (cryptKey, bool) {
	k := cryptKey{target: target}
	s = strings.TrimSpace(s)
	if rest, ok := strings.CutPrefix(s, "cbc:"); ok {
		k.cbc, s = true, rest
	} else {
		s = strings.TrimPrefix(s, "ecb:")
	}
	k.key = s
	return k, target != "" && s != "" && len(s) <= maxKeyLen
}

// setting is the key as a line of the keys setting.
func (k cryptKey) setting() string {
	if k.cbc {
		return k.target + " cbc:" + k.key
	}
	return k.target + " " + k.key
}

func (k cryptKey) modeName() string {
	if k.cbc {
		return "CBC"
	}
	return "ECB"
}

type crypt struct {
	h    Host
	mark string
	// mu guards keys: RewriteClientLine runs alongside the other calls
	mu   sync.Mutex
	keys map[string]cryptKey // by lower-case target
	dh   map[string]dhPair   // key exchanges we started, by lower-case nick
}

func (c *crypt) Registered()      {}
func (c *crypt) Line(irc.Message) {}

func (c *crypt) key(target string) (cryptKey, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	k, ok := c.keys[strings.ToLower(target)]
	return k, ok
}

// setKey sets (or, with an empty key, deletes) a target's key, and saves
// the keys.
func (c *crypt) setKey(target string, k *cryptKey) {
	c.mu.Lock()
	if k == nil {
		delete(c.keys, strings.ToLower(target))
	} else {
		c.keys[strings.ToLower(target)] = *k
	}
	lines := make([]string, 0, len(c.keys))
	for _, k := range c.keys {
		lines = append(lines, k.setting())
	}
	c.mu.Unlock()
	sort.Strings(lines)
	if s, ok := c.h.(SettingsSaver); ok {
		s.SaveSetting("crypt", "keys", strings.Join(lines, "\n"))
	}
}

// say tells the user's clients something, as *crypt.
func (c *crypt) say(t messages.Text) {
	if n, ok := c.h.(Notifier); ok {
		n.Notify(cryptNick, t)
		return
	}
	c.h.Reply(t.English())
}

// isChannel: modules don't see the network's CHANTYPES; these are the
// usual ones.
func isChannel(target string) bool {
	return target != "" && strings.ContainsRune("#&!+", rune(target[0]))
}

// --- *crypt commands ----------------------------------------------------------

// ClientLine takes the user's /msg *crypt commands.
func (c *crypt) ClientLine(line string) bool {
	m := irc.Parse(line)
	if m.Command != "PRIVMSG" || len(m.Params) != 2 || !strings.EqualFold(m.Params[0], cryptNick) {
		return false
	}
	args := strings.Fields(m.Params[1])
	if len(args) == 0 {
		args = []string{"help"}
	}
	switch cmd := strings.ToLower(args[0]); cmd {
	case "setkey":
		if len(args) < 3 {
			c.say(messages.T("module.say.cryptHelpSetkey"))
			break
		}
		// the key is the rest of the line: it may have spaces
		_, rest, _ := strings.Cut(strings.TrimSpace(m.Params[1]), " ")
		_, key, _ := strings.Cut(strings.TrimSpace(rest), " ")
		k, ok := parseCryptKey(args[1], key)
		if !ok {
			c.say(messages.T("module.say.cryptBadKey", "most", maxKeyLen))
			break
		}
		c.setKey(args[1], &k)
		c.say(messages.T("module.say.cryptKeySet", "target", args[1], "mode", k.modeName()))
	case "delkey":
		if len(args) != 2 {
			c.say(messages.T("module.say.cryptHelpDelkey"))
			break
		}
		if _, ok := c.key(args[1]); !ok {
			c.say(messages.T("module.say.cryptNoKey", "target", args[1]))
			break
		}
		c.setKey(args[1], nil)
		c.say(messages.T("module.say.cryptKeyDeleted", "target", args[1]))
	case "listkeys":
		c.mu.Lock()
		keys := make([]cryptKey, 0, len(c.keys))
		for _, k := range c.keys {
			keys = append(keys, k)
		}
		c.mu.Unlock()
		sort.Slice(keys, func(a, b int) bool { return strings.ToLower(keys[a].target) < strings.ToLower(keys[b].target) })
		if len(keys) == 0 {
			c.say(messages.T("module.say.cryptNoKeys"))
		}
		for _, k := range keys {
			c.say(messages.T("module.say.cryptListKey", "target", k.target, "mode", k.modeName(), "key", k.key))
		}
	case "keyx":
		if len(args) < 2 || len(args) > 3 || isChannel(args[1]) {
			c.say(messages.T("module.say.cryptHelpKeyx"))
			break
		}
		c.keyx(args[1], len(args) < 3 || !strings.EqualFold(args[2], "ecb"))
	case "help":
		c.say(messages.T("module.say.cryptHelpSetkey"))
		c.say(messages.T("module.say.cryptHelpDelkey"))
		c.say(messages.T("module.say.cryptHelpListkeys"))
		c.say(messages.T("module.say.cryptHelpKeyx"))
	default:
		c.say(messages.T("module.say.cryptUnknown", "command", cmd))
	}
	return true
}

// --- DH1080 -------------------------------------------------------------------

// keyx starts a key exchange with nick (asking for CBC, or ECB for old
// clients).
func (c *crypt) keyx(nick string, cbc bool) {
	p, err := newDH()
	if err != nil {
		return
	}
	c.dh[strings.ToLower(nick)] = p
	suffix := ""
	if cbc {
		suffix = " CBC"
	}
	c.h.Send("NOTICE " + nick + " :DH1080_INIT " + p.Public() + suffix)
	c.say(messages.T("module.say.cryptKeyxSent", "nick", nick))
}

// exchange answers a DH1080_INIT or takes a DH1080_FINISH: true when text
// was one (it isn't shown).
func (c *crypt) exchange(nick, text string) bool {
	f := strings.Fields(text)
	if len(f) < 2 || !strings.HasPrefix(f[0], "DH1080_") {
		return false
	}
	cbc := f[0] == "DH1080_INIT_CBC" || (len(f) > 2 && f[2] == "CBC")
	var key string
	var err error
	switch f[0] {
	case "DH1080_INIT", "DH1080_INIT_CBC":
		var p dhPair
		if p, err = newDH(); err == nil {
			if key, err = p.Key(f[1]); err == nil {
				suffix := ""
				if cbc {
					suffix = " CBC"
				}
				c.h.Send("NOTICE " + nick + " :DH1080_FINISH " + p.Public() + suffix)
			}
		}
	case "DH1080_FINISH":
		p, ok := c.dh[strings.ToLower(nick)]
		if !ok {
			return false // not ours: let the user see it
		}
		delete(c.dh, strings.ToLower(nick))
		key, err = p.Key(f[1])
	default:
		return false
	}
	if err != nil {
		c.say(messages.T("module.say.cryptKeyxFailed", "nick", nick))
		return true
	}
	k := cryptKey{target: nick, key: key, cbc: cbc}
	c.setKey(nick, &k)
	c.h.Log(messages.T("module.log.cryptKeyx", "nick", nick, "mode", k.modeName()))
	if f[0] == "DH1080_FINISH" {
		c.say(messages.T("module.say.cryptKeyxDone", "nick", nick, "mode", k.modeName()))
	} else {
		c.say(messages.T("module.say.cryptKeyxAnswered", "nick", nick, "mode", k.modeName()))
	}
	return true
}

// --- decrypting what arrives --------------------------------------------------

// RewriteLine decrypts messages, notices, actions and topics from targets
// with a key, and takes DH1080 notices.
func (c *crypt) RewriteLine(m *irc.Message) bool {
	switch m.Command {
	case "PRIVMSG", "NOTICE":
		if len(m.Params) != 2 {
			return true
		}
		from, to := nickOf(m.Prefix), m.Params[0]
		target := to
		if !isChannel(to) && !strings.EqualFold(from, c.h.Nick()) {
			target = from // a private message: their key
		}
		if m.Command == "NOTICE" && !isChannel(to) && strings.HasPrefix(m.Params[1], "DH1080_") && c.exchange(from, m.Params[1]) {
			return false
		}
		if k, ok := c.key(target); ok {
			m.Params[1] = c.decrypt(k, m.Params[1], true)
		}
	case "TOPIC": // TOPIC #chan :text
		if len(m.Params) == 2 {
			if k, ok := c.key(m.Params[0]); ok {
				m.Params[1] = c.decrypt(k, m.Params[1], false)
			}
		}
	case "332": // RPL_TOPIC me #chan :text
		if len(m.Params) == 3 {
			if k, ok := c.key(m.Params[1]); ok {
				m.Params[2] = c.decrypt(k, m.Params[2], false)
			}
		}
	}
	return true
}

// decrypt decrypts text (or an action's), marking a message; text that
// isn't encrypted, or doesn't decrypt, stays as it is.
func (c *crypt) decrypt(k cryptKey, text string, mark bool) string {
	inner, action := text, false
	if s, ok := strings.CutPrefix(text, "\x01ACTION "); ok {
		inner, action = strings.TrimSuffix(s, "\x01"), true
	}
	plain, _, ok := fishDecrypt(k.key, inner)
	if !ok {
		return text
	}
	plain = cleanText(plain)
	if mark {
		plain = c.mark + plain
	}
	if action {
		return "\x01ACTION " + plain + "\x01"
	}
	return plain
}

// cleanText makes decrypted text safe to put in a line: no line breaks or
// zero bytes, and UTF-8 (text that isn't is taken as Latin-1, what older
// clients send).
func cleanText(s string) string {
	s = strings.NewReplacer("\r", "", "\n", " ", "\x00", "").Replace(s)
	if utf8.ValidString(s) {
		return s
	}
	r := make([]rune, len(s))
	for i := 0; i < len(s); i++ {
		r[i] = rune(s[i])
	}
	return string(r)
}

// --- encrypting what the user sends -------------------------------------------

// relayRoom is about the most the server adds when it passes a line on
// (":nick!user@host "): the rest of IRC's 512 bytes is the room there is.
const relayRoom = 100

// RewriteClientLine encrypts the user's messages, notices, actions and
// topics to targets with a key, split so each fits in a line.
func (c *crypt) RewriteClientLine(line string) []string {
	tags := ""
	if strings.HasPrefix(line, "@") {
		tags, _, _ = strings.Cut(line, " ")
		tags += " "
	}
	m := irc.Parse(line)
	if len(m.Params) != 2 || m.Params[1] == "" || (m.Command != "PRIVMSG" && m.Command != "NOTICE" && m.Command != "TOPIC") {
		return []string{line}
	}
	target, text := m.Params[0], m.Params[1]
	k, ok := c.key(target)
	if !ok || strings.HasPrefix(text, "+OK ") || strings.HasPrefix(text, "mcps ") || strings.HasPrefix(text, "DH1080_") {
		return []string{line} // no key, or already encrypted (by the client's own FiSH)
	}
	head, tail := "", ""
	if strings.HasPrefix(text, "\x01") {
		inner, ok := strings.CutPrefix(text, "\x01ACTION ")
		if !ok {
			return []string{line} // other CTCPs go as they are
		}
		head, tail, text = "\x01ACTION ", "\x01", strings.TrimSuffix(inner, "\x01")
	}
	start := m.Command + " " + target + " :"
	room := 510 - relayRoom - len(start) - len(head) - len(tail)
	pieces := []string{text}
	if m.Command != "TOPIC" { // (a topic is one line however long)
		pieces = splitText(text, fishMaxPlain(room, k.cbc))
	}
	var out []string
	for _, p := range pieces {
		enc, err := fishEncrypt(k.key, k.cbc, p)
		if err != nil {
			return nil // never send it unencrypted
		}
		out = append(out, tags+start+head+enc+tail)
	}
	return out
}

// splitText cuts s into pieces of at most n bytes, at spaces where it can,
// never inside a character.
func splitText(s string, n int) []string {
	if n < 8 {
		n = 8
	}
	var out []string
	for len(s) > n {
		cut := n
		for cut > 0 && !utf8.RuneStart(s[cut]) {
			cut--
		}
		if sp := strings.LastIndexByte(s[:cut], ' '); sp > cut/2 {
			cut = sp + 1
		}
		out = append(out, s[:cut])
		s = s[cut:]
	}
	return append(out, s)
}
