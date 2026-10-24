// Package netfile is evrd's networks file: a user's networks, channels and
// playback settings as YAML, to export from the web app and import again
// (on the same server, or another one), or to write by hand.
//
// Secrets (server and services passwords, the services login command,
// channel keys, module settings) never go out as plaintext: an export
// carries them sealed under the server's secret key ("enc:v1:…"), and the
// key itself isn't in the file, so only an evrd with the same key can read
// them back. Without a key set they're left out. A file written by hand may
// give them in plaintext; they're encrypted once imported.
package netfile

import (
	"bytes"
	"errors"
	"fmt"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/4evrd/evrd/internal/store"
)

// Version is the file format's version (File.Format).
const Version = 1

// File is a networks file.
type File struct {
	Format   int       `yaml:"evrd_networks"`
	Networks []Network `yaml:"networks"`
}

// Network is one network. Fields left out keep what an existing network
// has, or evrd's defaults for a new one.
type Network struct {
	Name         string            `yaml:"name"`
	Enabled      *bool             `yaml:"enabled,omitempty"` // connected (when the import connects networks)
	Servers      []Server          `yaml:"servers,omitempty"`
	Nick         string            `yaml:"nick,omitempty"`
	AltNick      string            `yaml:"alt_nick,omitempty"`
	Ident        string            `yaml:"ident,omitempty"`
	Realname     string            `yaml:"realname,omitempty"`
	QuitMessage  string            `yaml:"quit_message,omitempty"`
	BindHost     string            `yaml:"bind_host,omitempty"`
	Login        *Login            `yaml:"login,omitempty"`
	Certificates *Certificates     `yaml:"certificates,omitempty"`
	Encoding     string            `yaml:"encoding,omitempty"`
	Flood        *Flood            `yaml:"flood,omitempty"`
	JoinDelay    *int              `yaml:"join_delay,omitempty"`
	Playback     *Playback         `yaml:"playback,omitempty"` // the network's default
	Channels     []Channel         `yaml:"channels,omitempty"`
	Queries      []Query           `yaml:"queries,omitempty"` // private buffers with their own playback
	Modules      map[string]Module `yaml:"modules,omitempty"`
	Extra        map[string]any    `yaml:",inline"` // unknown keys, reported

	notes []Note
}

// Server is one of a network's servers.
type Server struct {
	Host     string `yaml:"host"`
	Port     int    `yaml:"port,omitempty"`
	TLS      *bool  `yaml:"tls,omitempty"`
	Password string `yaml:"password,omitempty"` // sent as PASS; secret
}

// Login is how evrd logs in to the network's services (store.Network's
// Auth* fields).
type Login struct {
	Method         string `yaml:"method,omitempty"` // auto, sasl, nickserv, command, none
	Account        string `yaml:"account,omitempty"`
	Password       string `yaml:"password,omitempty"` // secret
	ServicesNick   string `yaml:"services_nick,omitempty"`
	IdentifyArgs   string `yaml:"identify_args,omitempty"`
	IdentifyOn     string `yaml:"identify_on,omitempty"`
	Command        string `yaml:"command,omitempty"` // secret
	Ghost          string `yaml:"ghost,omitempty"`
	JoinAfterLogin *bool  `yaml:"join_after_login,omitempty"`
	Timeout        int    `yaml:"timeout,omitempty"`
}

// Certificates is which server certificates are accepted.
type Certificates struct {
	TrustAll     *bool    `yaml:"trust_all,omitempty"`
	TrustPKI     *bool    `yaml:"trust_pki,omitempty"`
	Fingerprints []string `yaml:"fingerprints,omitempty"`
}

// Flood is the network's flood protection.
type Flood struct {
	Rate  *float64 `yaml:"rate,omitempty"`
	Burst *int     `yaml:"burst,omitempty"`
}

// Playback is a buffer's playback settings (nil: the default).
type Playback struct {
	Lines     *int  `yaml:"lines,omitempty"`
	AutoClear *bool `yaml:"auto_clear,omitempty"`
}

// Channel is a saved channel.
type Channel struct {
	Name     string    `yaml:"name"`
	Key      string    `yaml:"key,omitempty"` // secret
	Detached bool      `yaml:"detached,omitempty"`
	Disabled bool      `yaml:"disabled,omitempty"`
	Playback *Playback `yaml:"playback,omitempty"`
}

// Query is a private buffer's playback.
type Query struct {
	Name     string   `yaml:"name"`
	Playback Playback `yaml:"playback"`
}

// Module is one of the network's modules. Its settings are secret (perform's
// commands can hold a password).
type Module struct {
	Enabled  bool              `yaml:"enabled"`
	Settings map[string]string `yaml:"settings,omitempty"`
}

// Note is something an import couldn't take as it was: a message key in
// internal/messages (irc.netfile.*) and its arguments.
type Note struct {
	Key  string
	Args []any
}

// Header is the comment at the top of an export.
const Header = `# evrd networks: import this file from Networks → Import on an evrd server.
# Passwords, channel keys, the services login command and module settings
# are encrypted with the exporting server's secret key, which isn't in this
# file: only an evrd with the same key can read them back. Anywhere else
# they're left empty, to set again.
`

// Export writes networks (with their buffers' playback settings, by
// lowercased buffer name; "" is the network's) as a networks file. Secrets
// are sealed; when the server has no key to seal with they're left out and
// sealed is false.
func Export(nets []store.Network, playback map[int64]map[string]store.BufferPlayback) (out []byte, sealed bool, err error) {
	sealed = store.SecretsEncrypted()
	seal := func(s string) string {
		if s == "" || err != nil {
			return ""
		}
		v, ok, e := store.SealSecret(s)
		if e != nil {
			err = e
		}
		if !ok {
			return ""
		}
		return v
	}
	f := File{Format: Version}
	for _, n := range nets {
		x := Network{
			Name: n.Name, Enabled: ptr(n.Enabled), Nick: n.Nick, AltNick: n.AltNick, Ident: n.Username,
			Realname: n.Realname, QuitMessage: n.QuitMsg, BindHost: n.BindHost, Encoding: n.Encoding,
			Flood:        &Flood{Rate: ptr(n.FloodRate), Burst: ptr(n.FloodBurst)},
			Certificates: &Certificates{TrustAll: ptr(n.TrustAllCerts), TrustPKI: ptr(n.TrustPKI), Fingerprints: n.Fingerprints},
		}
		if n.JoinDelay != 0 {
			x.JoinDelay = ptr(n.JoinDelay)
		}
		for _, sv := range n.ServerList() {
			x.Servers = append(x.Servers, Server{Host: sv.Host, Port: sv.Port, TLS: ptr(sv.TLS), Password: seal(sv.Password)})
		}
		l := Login{
			Method: n.Auth(), Account: n.SASLUser, Password: seal(n.Password), ServicesNick: n.ServicesNick,
			IdentifyArgs: n.IdentifyArgs, IdentifyOn: n.IdentifyOn, Command: seal(n.AuthCommand), Ghost: n.GhostCmd,
			Timeout: n.AuthTimeout,
		}
		if n.JoinAfterAuth {
			l.JoinAfterLogin = ptr(true)
		}
		x.Login = &l
		bps := playback[n.ID]
		x.Playback = playbackOf(bps[""], false)
		chans := map[string]bool{}
		for _, c := range n.Channels {
			cs := n.Channel(c)
			chans[strings.ToLower(c)] = true
			x.Channels = append(x.Channels, Channel{Name: c, Key: seal(cs.Key), Detached: cs.Detached, Disabled: cs.Disabled, Playback: playbackOf(bps[strings.ToLower(c)], true)})
		}
		var qs []string
		for b := range bps {
			if b != "" && !chans[b] && !isChannel(b) {
				qs = append(qs, b)
			}
		}
		slices.Sort(qs)
		for _, b := range qs {
			if p := playbackOf(bps[b], true); p != nil {
				x.Queries = append(x.Queries, Query{Name: b, Playback: *p})
			}
		}
		for name, m := range n.Modules {
			if x.Modules == nil {
				x.Modules = map[string]Module{}
			}
			mm := Module{Enabled: m.Enabled}
			for k, v := range m.Settings {
				if s := seal(v); s != "" {
					if mm.Settings == nil {
						mm.Settings = map[string]string{}
					}
					mm.Settings[k] = s
				}
			}
			x.Modules[name] = mm
		}
		f.Networks = append(f.Networks, x)
	}
	if err != nil {
		return nil, false, err
	}
	var buf bytes.Buffer
	buf.WriteString(Header)
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(f); err != nil {
		return nil, false, err
	}
	return buf.Bytes(), sealed, enc.Close()
}

func playbackOf(bp store.BufferPlayback, autoClear bool) *Playback {
	p := Playback{Lines: bp.Lines}
	if autoClear {
		p.AutoClear = bp.AutoClear
	}
	if p.Lines == nil && p.AutoClear == nil {
		return nil
	}
	return &p
}

// ErrNotNetworks means the text isn't a networks file.
var ErrNotNetworks = errors.New("netfile: not an evrd networks file")

// Looks reports whether text looks like a networks file (rather than, say,
// a ZNC config).
func Looks(text []byte) bool {
	for _, l := range strings.Split(string(text), "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "#") || l == "---" {
			continue
		}
		return strings.HasPrefix(l, "evrd_networks:") || strings.HasPrefix(l, "networks:")
	}
	return false
}

// Parse reads a networks file.
func Parse(text []byte) (File, error) {
	var f File
	if err := yaml.Unmarshal(text, &f); err != nil {
		return f, fmt.Errorf("%w: %v", ErrNotNetworks, err)
	}
	if f.Format > Version {
		return f, fmt.Errorf("netfile: written by a newer evrd (format %d)", f.Format)
	}
	if f.Networks == nil {
		return f, ErrNotNetworks
	}
	return f, nil
}

// Notes are the things the network's import couldn't take as they were.
func (x *Network) Notes() []Note { return x.notes }

// AddNote records a note on the network (zncimport's conversion has its own).
// The same note twice (a module's settings, one per setting) is kept once.
func (x *Network) AddNote(key string, args ...any) {
	n := Note{key, args}
	if !slices.ContainsFunc(x.notes, func(o Note) bool { return o.Key == key && fmt.Sprint(o.Args...) == fmt.Sprint(args...) }) {
		x.notes = append(x.notes, n)
	}
}

// Settings is a network's playback settings by lowercased buffer name ("" is
// the network's default), to apply after the network is saved.
type Settings map[string]Playback

// Apply copies x onto n (an existing network, or a new one with evrd's
// defaults), opening sealed secrets. Fields x leaves out stay as they are.
// It returns the buffers' playback settings; notes go to x.Notes.
func (x *Network) Apply(n *store.Network) Settings {
	// open reads a secret; when it was sealed under another server's key
	// it notes that (key, args) and reports false.
	open := func(v, key string, args ...any) (string, bool) {
		if !store.IsSealed(v) {
			return v, true
		}
		p, err := store.OpenSecret(v)
		if err != nil {
			x.AddNote(key, args...)
			return "", false
		}
		return p, true
	}
	set := func(dst *string, v string) {
		if v != "" {
			*dst = v
		}
	}
	n.Name = strings.TrimSpace(x.Name)
	if x.Servers != nil {
		old := n.ServerList()
		var list []store.Server
		for _, sv := range x.Servers {
			s := store.Server{Host: strings.TrimSpace(sv.Host), Port: sv.Port, TLS: sv.TLS == nil || *sv.TLS}
			if s.Port == 0 {
				s.Port = 6667
				if s.TLS {
					s.Port = 6697
				}
			}
			if pw, ok := open(sv.Password, "irc.netfile.serverPassword", "host", s.Host); ok && pw != "" {
				s.Password = pw
			} else {
				for _, o := range old {
					if strings.EqualFold(o.Host, s.Host) && o.Port == s.Port {
						s.Password = o.Password
					}
				}
			}
			list = append(list, s)
		}
		n.Servers = list
	}
	set(&n.Nick, x.Nick)
	set(&n.AltNick, x.AltNick)
	set(&n.Username, x.Ident)
	set(&n.Realname, x.Realname)
	set(&n.QuitMsg, x.QuitMessage)
	set(&n.BindHost, x.BindHost)
	set(&n.Encoding, x.Encoding)
	if l := x.Login; l != nil {
		set(&n.AuthMethod, l.Method)
		set(&n.SASLUser, l.Account)
		if pw, ok := open(l.Password, "irc.netfile.loginPassword"); ok {
			set(&n.Password, pw)
		}
		set(&n.ServicesNick, l.ServicesNick)
		set(&n.IdentifyArgs, l.IdentifyArgs)
		set(&n.IdentifyOn, l.IdentifyOn)
		if c, ok := open(l.Command, "irc.netfile.loginCommand"); ok {
			set(&n.AuthCommand, c)
		}
		set(&n.GhostCmd, l.Ghost)
		if l.JoinAfterLogin != nil {
			n.JoinAfterAuth = *l.JoinAfterLogin
		}
		if l.Timeout != 0 {
			n.AuthTimeout = l.Timeout
		}
	}
	if c := x.Certificates; c != nil {
		if c.TrustAll != nil {
			n.TrustAllCerts = *c.TrustAll
		}
		if c.TrustPKI != nil {
			n.TrustPKI = *c.TrustPKI
		}
		if c.Fingerprints != nil {
			n.Fingerprints = c.Fingerprints
		}
	}
	if f := x.Flood; f != nil {
		if f.Rate != nil {
			n.FloodRate = *f.Rate
		}
		if f.Burst != nil {
			n.FloodBurst = *f.Burst
		}
	}
	if x.JoinDelay != nil {
		n.JoinDelay = *x.JoinDelay
	}
	pb := Settings{}
	if x.Playback != nil {
		pb[""] = Playback{Lines: x.Playback.Lines} // auto-clear is the account's, per kind of buffer
	}
	if x.Channels != nil {
		oldOpts := n.ChannelSettings
		n.Channels, n.ChannelSettings = nil, map[string]store.ChannelSettings{}
		for _, c := range x.Channels {
			name := strings.TrimSpace(c.Name)
			if name == "" {
				continue
			}
			k := strings.ToLower(name)
			cs := store.ChannelSettings{Detached: c.Detached, Disabled: c.Disabled}
			if key, ok := open(c.Key, "irc.netfile.channelKey", "channel", name); ok && key != "" {
				cs.Key = key
			} else {
				cs.Key = oldOpts[k].Key
			}
			n.Channels = append(n.Channels, name)
			if cs != (store.ChannelSettings{}) {
				n.ChannelSettings[k] = cs
			}
			if c.Playback != nil {
				pb[k] = *c.Playback
			}
		}
	}
	for _, q := range x.Queries {
		if name := strings.ToLower(strings.TrimSpace(q.Name)); name != "" {
			pb[name] = q.Playback
		}
	}
	if x.Modules != nil {
		mods := map[string]store.ModuleConfig{}
		for name, m := range x.Modules {
			mc := store.ModuleConfig{Enabled: m.Enabled}
			for k, v := range m.Settings {
				if p, ok := open(v, "irc.netfile.moduleSettings", "module", name); ok {
					if mc.Settings == nil {
						mc.Settings = map[string]string{}
					}
					mc.Settings[k] = p
				} else if old, ok := n.Modules[name].Settings[k]; ok {
					if mc.Settings == nil {
						mc.Settings = map[string]string{}
					}
					mc.Settings[k] = old
				}
			}
			mods[name] = mc
		}
		n.Modules = mods
	}
	extra := make([]string, 0, len(x.Extra))
	for k := range x.Extra {
		extra = append(extra, k)
	}
	slices.Sort(extra)
	for _, k := range extra {
		x.AddNote("irc.netfile.unknownField", "field", k)
	}
	return pb
}

func isChannel(s string) bool { return s != "" && strings.ContainsRune("#&+!", rune(s[0])) }

func ptr[T any](v T) *T { return &v }
