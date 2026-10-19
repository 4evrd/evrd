// Package modules is evrd's take on ZNC's modules: optional behaviours a
// user switches on per network — perform, keepnick, kickrejoin, autocycle,
// autoop/autovoice, alias, ctcpflood, q — each with its own settings.
//
// A module runs inside a network's connection. It sees what the server
// sends, can send lines, set timers and write to the network's connection
// log, and is told when the connection signs in. Modules are compiled in
// (evrd runs pledged, without exec, maybe on several nodes), and describe
// their settings, so the web app and *status show them without knowing each
// one.
package modules

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/4evrd/evrd/internal/irc"
)

// Host is what a module can do and see: one network's connection.
type Host interface {
	// Send sends a raw line to the server.
	Send(line string)
	// SendAsUser sends a line as if the user had typed it in a client: their
	// messages are saved and shown on their other clients.
	SendAsUser(line string)
	// Nick is our current nick; WantedNick the one configured.
	Nick() string
	WantedNick() string
	// Joined reports whether we're in a channel; Members lists its nicks
	// with their prefixes ("@", "+"…) and hosts where known.
	Joined(channel string) bool
	// Channels lists the channels we're in.
	Channels() []string
	Members(channel string) []Member
	// IsOp reports whether we have ops (or better) in a channel.
	IsOp(channel string) bool
	// Account is the network's login account and password (for modules
	// like q that log in themselves).
	Account() (user, password string)
	// After runs f after d, on the connection's goroutine, unless the
	// connection ends first.
	After(d time.Duration, f func())
	// Log writes to the network's connection log.
	Log(text string)
	// Reply sends a notice to the user's attached clients, as the module.
	Reply(text string)
}

// Member is one nick in a channel.
type Member struct {
	Nick, Prefix, Host string // Host is user@host, when known
}

// Module is one running instance, on one network.
type Module interface {
	// Registered is called once the connection has signed in.
	Registered()
	// Line is called with every line the server sends, after evrd has
	// handled it.
	Line(m irc.Message)
}

// CTCPFilter is implemented by modules that can drop CTCP requests (true:
// don't answer, don't pass on).
type CTCPFilter interface {
	CTCP(from, command string) (drop bool)
}

// ClientFilter is implemented by modules that act on lines from the user's
// IRC clients before they go to the server (true: handled, don't send).
type ClientFilter interface {
	ClientLine(line string) (handled bool)
}

// Setting describes one of a module's settings.
type Setting struct {
	Key     string `json:"key"`
	Label   string `json:"label"`
	Help    string `json:"help,omitempty"`
	Kind    string `json:"kind"` // text | lines | number | bool
	Default string `json:"default,omitempty"`
}

// Info describes a module.
type Info struct {
	Name        string    `json:"name"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	Settings    []Setting `json:"settings"`
	// ZNC is the ZNC module it stands in for, if any.
	ZNC string `json:"znc,omitempty"`

	new func(h Host, s Settings) Module
}

// Config is a module as a network has it: on or off, and its settings.
type Config struct {
	Enabled  bool              `json:"enabled"`
	Settings map[string]string `json:"settings,omitempty"`
}

// Settings are a module's settings, with defaults filled in.
type Settings map[string]string

// Lines is a "lines" setting: one item per line, blank lines and
// surrounding space dropped.
func (s Settings) Lines(key string) []string {
	var out []string
	for _, l := range strings.Split(s[key], "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}

// Int is a "number" setting (def if unset or not a number).
func (s Settings) Int(key string, def int) int {
	if n, err := strconv.Atoi(strings.TrimSpace(s[key])); err == nil {
		return n
	}
	return def
}

// Bool is a "bool" setting.
func (s Settings) Bool(key string) bool {
	switch strings.ToLower(strings.TrimSpace(s[key])) {
	case "yes", "true", "on", "1":
		return true
	}
	return false
}

var (
	mu       sync.RWMutex
	registry = map[string]Info{}
)

func register(i Info) {
	mu.Lock()
	defer mu.Unlock()
	registry[i.Name] = i
}

// All lists the modules, by name.
func All() []Info {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]Info, 0, len(registry))
	for _, i := range registry {
		out = append(out, i)
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Name < out[b].Name })
	return out
}

// Get returns a module's description.
func Get(name string) (Info, bool) {
	mu.RLock()
	defer mu.RUnlock()
	i, ok := registry[strings.ToLower(name)]
	return i, ok
}

// Check reports whether a network's module configs are valid: known
// modules, known settings, numbers that are numbers.
func Check(cfgs map[string]Config) error {
	for name, c := range cfgs {
		info, ok := Get(name)
		if !ok {
			return fmt.Errorf("no module %q", name)
		}
		for k, v := range c.Settings {
			var spec *Setting
			for i := range info.Settings {
				if info.Settings[i].Key == k {
					spec = &info.Settings[i]
				}
			}
			if spec == nil {
				return fmt.Errorf("module %s has no setting %q", name, k)
			}
			if spec.Kind == "number" && strings.TrimSpace(v) != "" {
				if _, err := strconv.Atoi(strings.TrimSpace(v)); err != nil {
					return fmt.Errorf("%s: %s must be a whole number", name, spec.Label)
				}
			}
		}
	}
	return nil
}

// Start creates the enabled modules for a network, skipping any in off
// (switched off server-wide by an admin).
func Start(h Host, cfgs map[string]Config, off map[string]bool) []Module {
	names := make([]string, 0, len(cfgs))
	for n := range cfgs {
		names = append(names, n)
	}
	sort.Strings(names) // a stable order: perform before q, say
	var out []Module
	for _, name := range names {
		c := cfgs[name]
		info, ok := Get(name)
		if !ok || !c.Enabled || off[info.Name] {
			continue
		}
		s := Settings{}
		for _, spec := range info.Settings {
			s[spec.Key] = spec.Default
		}
		for k, v := range c.Settings {
			s[k] = v
		}
		out = append(out, info.new(h, s))
	}
	return out
}

// nickOf is the nick part of nick!user@host.
func nickOf(prefix string) string {
	n, _, _ := strings.Cut(prefix, "!")
	return n
}

// hostOf is the user@host part of nick!user@host ("" without one).
func hostOf(prefix string) string {
	_, h, ok := strings.Cut(prefix, "!")
	if !ok {
		return ""
	}
	return h
}

// Match reports whether a nick!user@host matches an IRC mask with * and ?
// (case-insensitively).
func Match(mask, s string) bool {
	return wildcard(strings.ToLower(mask), strings.ToLower(s))
}

func wildcard(p, s string) bool {
	for len(p) > 0 {
		switch p[0] {
		case '*':
			for len(p) > 0 && p[0] == '*' {
				p = p[1:]
			}
			if p == "" {
				return true
			}
			for i := 0; i <= len(s); i++ {
				if wildcard(p, s[i:]) {
					return true
				}
			}
			return false
		case '?':
			if s == "" {
				return false
			}
		default:
			if s == "" || s[0] != p[0] {
				return false
			}
		}
		p, s = p[1:], s[1:]
	}
	return s == ""
}
