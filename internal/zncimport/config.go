package zncimport

// Reading ZNC's config (znc.conf) into evrd networks: each <User>'s
// <Network>s, with their servers, nicks, channels and the modules evrd has.
// A part of one works too: a <User> block, or just <Network> blocks.
//
// The user's own password is read only as ZNC's hash of it (Pass), for an
// admin bringing the user over (accounts.ZNCPasswordHash). Server passwords
// and channel keys are in the config as plaintext and are taken (evrd
// encrypts them); what modules keep in ZNC's moddata (SASL and NickServ
// passwords, perform's commands) isn't in it, so those come with a note.

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/4evrd/evrd/internal/netfile"
)

// section is a <Tag name> … </Tag> block of the config.
type section struct {
	tag, name string
	vars      [][2]string // in order; a key can repeat (Server, LoadModule)
	children  []*section
}

func (s *section) all(key string) []string {
	var out []string
	for _, kv := range s.vars {
		if strings.EqualFold(kv[0], key) {
			out = append(out, kv[1])
		}
	}
	return out
}

func (s *section) get(key string) (string, bool) {
	v := s.all(key)
	if len(v) == 0 {
		return "", false
	}
	return v[len(v)-1], true
}

func (s *section) str(key string) string { v, _ := s.get(key); return v }

func (s *section) boolean(key string) *bool {
	v, ok := s.get(key)
	if !ok {
		return nil
	}
	b := strings.EqualFold(v, "true") || v == "1" || strings.EqualFold(v, "yes") || strings.EqualFold(v, "on")
	return &b
}

func (s *section) number(key string) *int {
	if n, err := strconv.Atoi(s.str(key)); err == nil {
		return &n
	}
	return nil
}

func (s *section) kids(tag string) []*section {
	var out []*section
	for _, c := range s.children {
		if strings.EqualFold(c.tag, tag) {
			out = append(out, c)
		}
	}
	return out
}

var (
	openRE  = regexp.MustCompile(`^<\s*(\w+)\s+([^>]*?)\s*>$`)
	closeRE = regexp.MustCompile(`^<\s*/\s*(\w+)\s*>$`)
)

// ErrNotConfig means the text has nothing that reads as a ZNC config.
var ErrNotConfig = errors.New("zncimport: not a ZNC config")

func parseConfig(text []byte) (*section, error) {
	root := &section{}
	stack := []*section{root}
	sc := bufio.NewScanner(bytes.NewReader(text))
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	inComment, n := false, 0
	for sc.Scan() {
		n++
		l := strings.TrimSpace(sc.Text())
		if inComment {
			if _, after, ok := strings.Cut(l, "*/"); ok {
				inComment, l = false, strings.TrimSpace(after)
			} else {
				continue
			}
		}
		if strings.HasPrefix(l, "/*") {
			if !strings.Contains(l[2:], "*/") {
				inComment = true
			}
			continue
		}
		if l == "" || strings.HasPrefix(l, "//") {
			continue
		}
		top := stack[len(stack)-1]
		if m := closeRE.FindStringSubmatch(l); m != nil {
			if len(stack) == 1 || !strings.EqualFold(top.tag, m[1]) {
				return nil, fmt.Errorf("zncimport: line %d: </%s> doesn't close anything", n, m[1])
			}
			stack = stack[:len(stack)-1]
			continue
		}
		if m := openRE.FindStringSubmatch(l); m != nil {
			s := &section{tag: m[1], name: m[2]}
			top.children = append(top.children, s)
			stack = append(stack, s)
			continue
		}
		k, v, ok := strings.Cut(l, "=")
		if !ok {
			return nil, fmt.Errorf("zncimport: line %d isn't a ZNC config line", n)
		}
		top.vars = append(top.vars, [2]string{strings.TrimSpace(k), strings.TrimSpace(v)})
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if len(stack) > 1 {
		return nil, fmt.Errorf("zncimport: <%s %s> isn't closed", stack[len(stack)-1].tag, stack[len(stack)-1].name)
	}
	return root, nil
}

// User is a ZNC user from a config, and its networks as evrd's. Name is ""
// for networks pasted without their <User>.
type User struct {
	Name     string
	Networks []netfile.Network
	// Pass is the user's password as ZNC keeps it (nil: none in the config).
	// Never sent anywhere: it's only for accounts.ZNCPasswordHash.
	Pass *Pass
}

// Pass is a ZNC user's password: Method is md5, sha256, argon2id or plain
// (a password ZNC kept as it is, in Hash).
type Pass struct {
	Method, Hash, Salt string
}

// userPass reads a <User>'s password, in any of ZNC's forms:
//
//	<Pass password> Method = sha256, Hash = …, Salt = … </Pass>  (1.7 on)
//	Pass = sha256#hash#salt#   Pass = md5#hash   Pass = plain#password
//	Pass = <md5 hash> -        Pass = password
func userPass(u *section) *Pass {
	for _, p := range u.kids("Pass") {
		if strings.EqualFold(p.name, "password") {
			m := strings.ToLower(p.str("Method"))
			if m == "" {
				m = "md5"
			}
			return &Pass{Method: m, Hash: p.str("Hash"), Salt: p.str("Salt")}
		}
	}
	v, ok := u.get("Pass")
	if !ok || v == "" {
		return nil
	}
	if h, ok := strings.CutSuffix(v, "-"); ok {
		return &Pass{Method: "md5", Hash: strings.TrimSpace(h)}
	}
	method, rest, _ := strings.Cut(v, "#")
	switch strings.ToLower(method) {
	case "md5", "sha256":
		hash, salt, _ := strings.Cut(rest, "#")
		return &Pass{Method: strings.ToLower(method), Hash: hash, Salt: strings.TrimSuffix(salt, "#")}
	case "plain":
		return &Pass{Method: "plain", Hash: rest}
	}
	return &Pass{Method: "plain", Hash: v}
}

// LooksLikeConfig reports whether text looks like (part of) a ZNC config.
func LooksLikeConfig(text []byte) bool {
	s := strings.ToLower(string(text))
	return strings.Contains(s, "<user ") || strings.Contains(s, "<network ") || strings.Contains(s, "<chan ")
}

// ParseConfig reads a ZNC config, whole or in part, into its users'
// networks. Notes on each network say what didn't carry over.
func ParseConfig(text []byte) ([]User, error) {
	root, err := parseConfig(text)
	if err != nil {
		return nil, err
	}
	var users []User
	for _, u := range root.kids("User") {
		users = append(users, User{Name: u.name, Networks: userNetworks(u), Pass: userPass(u)})
	}
	// <Network>s (or one network's <Chan>s and Servers) pasted on their own
	if nets := userNetworks(root); len(nets) > 0 {
		users = append(users, User{Networks: nets})
	}
	if len(users) == 0 {
		return nil, ErrNotConfig
	}
	return users, nil
}

// userNetworks are a <User>'s networks; settings on the user are the
// defaults its networks inherit, as in ZNC.
func userNetworks(u *section) []netfile.Network {
	var out []netfile.Network
	for _, n := range u.kids("Network") {
		out = append(out, network(u, n, n.name))
	}
	// ZNC before 1.0 had one network per user, its servers and channels on
	// the user; 1.0 named it "default" when it moved them
	if len(u.all("Server")) > 0 || len(u.kids("Chan")) > 0 {
		out = append(out, network(&section{}, u, "default"))
	}
	return out
}

func network(u, n *section, name string) netfile.Network {
	inherit := func(key string) string {
		if v, ok := n.get(key); ok {
			return v
		}
		return u.str(key)
	}
	x := netfile.Network{
		Name: name, Nick: inherit("Nick"), AltNick: inherit("AltNick"), Ident: inherit("Ident"),
		Realname: inherit("RealName"), QuitMessage: inherit("QuitMsg"), BindHost: inherit("BindHost"),
		Encoding: n.str("Encoding"), JoinDelay: n.number("JoinDelay"),
	}
	enabled := true
	if b := n.boolean("IRCConnectEnabled"); b != nil {
		enabled = *b
	}
	x.Enabled = &enabled
	for _, line := range n.all("Server") {
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		sv := netfile.Server{Host: f[0], Port: 6667}
		tls := false
		if len(f) > 1 {
			p := f[1]
			if strings.HasPrefix(p, "+") {
				tls, p = true, p[1:]
			}
			if port, err := strconv.Atoi(p); err == nil {
				sv.Port = port
			}
		}
		sv.TLS = &tls
		if len(f) > 2 {
			sv.Password = strings.Join(f[2:], " ")
		}
		x.Servers = append(x.Servers, sv)
	}
	c := netfile.Certificates{TrustAll: n.boolean("TrustAllCerts"), TrustPKI: n.boolean("TrustPKI")}
	for _, fp := range n.all("TrustedServerFingerprint") {
		c.Fingerprints = append(c.Fingerprints, strings.ToLower(strings.ReplaceAll(fp, ":", "")))
	}
	if c.TrustAll != nil || c.TrustPKI != nil || c.Fingerprints != nil {
		x.Certificates = &c
	}
	if r, err := strconv.ParseFloat(n.str("FloodRate"), 64); err == nil {
		x.Flood = &netfile.Flood{Rate: &r}
	}
	if b := n.number("FloodBurst"); b != nil {
		if x.Flood == nil {
			x.Flood = &netfile.Flood{}
		}
		x.Flood.Burst = b
	}
	// ZNC's buffer size is per user (Buffer before 1.7, then ChanBufferSize)
	lines := u.number("ChanBufferSize")
	if lines == nil {
		lines = u.number("Buffer")
	}
	if lines != nil {
		x.Playback = &netfile.Playback{Lines: lines}
	}
	x.Channels = []netfile.Channel{}
	for _, ch := range n.kids("Chan") {
		cc := netfile.Channel{Name: ch.name, Key: ch.str("Key")}
		if b := ch.boolean("Detached"); b != nil {
			cc.Detached = *b
		}
		if b := ch.boolean("Disabled"); b != nil {
			cc.Disabled = *b
		}
		if l, ac := ch.number("Buffer"), ch.boolean("AutoClearChanBuffer"); l != nil || ac != nil {
			cc.Playback = &netfile.Playback{Lines: l, AutoClear: ac}
		}
		x.Channels = append(x.Channels, cc)
	}
	modules(&x, n)
	return x
}

// ignored are ZNC modules evrd needn't carry over: what they do, it does
// itself (or has no use for).
var ignored = []string{
	"buffextras", "chansaver", "clientbuffer", "controlpanel", "log", "playback",
	"route_replies", "savebuff", "simple_away", "webadmin", "identfile", "cert",
}

// modules turns a network's LoadModule lines into evrd's modules and login
// method, noting the ones whose settings ZNC keeps elsewhere.
func modules(x *netfile.Network, n *section) {
	var skipped []string
	for _, line := range n.all("LoadModule") {
		name, args, _ := strings.Cut(strings.TrimSpace(line), " ")
		name, args = strings.ToLower(name), strings.TrimSpace(args)
		set := func(settings map[string]string) {
			if x.Modules == nil {
				x.Modules = map[string]netfile.Module{}
			}
			x.Modules[name] = netfile.Module{Enabled: true, Settings: settings}
		}
		switch name {
		case "sasl", "nickserv":
			x.Login = &netfile.Login{Method: name}
			x.AddNote("irc.znc.loginPassword", "module", name)
		case "keepnick", "ctcpflood":
			set(nil)
		case "kickrejoin":
			if _, err := strconv.Atoi(args); err == nil {
				set(map[string]string{"delay": args})
			} else {
				set(nil)
			}
		case "autocycle":
			if f := strings.Fields(args); len(f) > 0 {
				set(map[string]string{"channels": strings.Join(f, "\n")})
			} else {
				set(nil)
			}
		case "perform", "autoop", "autovoice", "alias", "q":
			x.AddNote("irc.znc.moduleData", "module", name)
		default:
			if !slices.Contains(ignored, name) {
				skipped = append(skipped, name)
			}
		}
	}
	if len(skipped) > 0 {
		x.AddNote("irc.znc.modulesSkipped", "modules", strings.Join(skipped, ", "))
	}
}
