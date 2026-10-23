package accounts

import (
	"context"
	"slices"
	"strings"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/htmlindex"

	"github.com/4evrd/evrd/internal/store"
)

// NormalizeNetwork trims and checks a network's settings, filling in ports
// and defaults. Shared by the web API and *status so both apply the same
// rules. Host/Port/TLS are kept equal to the first server.
func NormalizeNetwork(n *store.Network) error {
	n.Name, n.Nick, n.AltNick = strings.TrimSpace(n.Name), strings.TrimSpace(n.Nick), strings.TrimSpace(n.AltNick)
	n.Username, n.Realname = strings.TrimSpace(n.Username), strings.TrimSpace(n.Realname)
	n.QuitMsg, n.BindHost, n.SASLUser = strings.TrimSpace(n.QuitMsg), strings.TrimSpace(n.BindHost), strings.TrimSpace(n.SASLUser)
	n.Encoding = strings.TrimSpace(n.Encoding)
	switch {
	case n.Name == "" || strings.ContainsAny(n.Name, " /:"):
		return invalid("a network needs a name without spaces, / or :")
	case len(n.Name) > 32:
		return invalid("keep network names to 32 characters")
	case n.Nick == "":
		return invalid("a network needs a nick")
	case !validNick(n.Nick):
		return invalid("nicks can't contain spaces or , * ? ! @ # :")
	case n.AltNick != "" && !validNick(n.AltNick):
		return invalid("the alternate nick can't contain spaces or , * ? ! @ # :")
	case strings.ContainsAny(n.Username, " @!:"):
		return invalid("the ident can't contain spaces, @, ! or :")
	case n.FloodRate < 0 || n.FloodRate > 100:
		return invalid("the flood rate is lines per second, 0–100 (0 turns protection off)")
	case n.FloodRate > 0 && (n.FloodBurst < 1 || n.FloodBurst > 1000):
		return invalid("the flood burst is 1–1000 lines")
	case n.JoinDelay < 0 || n.JoinDelay > 600:
		return invalid("the join delay is 0–600 seconds")
	case strings.ContainsAny(n.QuitMsg, "\r\n"):
		return invalid("the quit message must be one line")
	}
	if err := normalizeAuth(n); err != nil {
		return err
	}

	// Servers: the list wins; a bare Host/Port/TLS (older callers, *status)
	// becomes a one-server list.
	servers := n.Servers
	if len(servers) == 0 && strings.TrimSpace(n.Host) != "" {
		servers = []store.Server{{Host: n.Host, Port: n.Port, TLS: n.TLS}}
	}
	var clean []store.Server
	for _, sv := range servers {
		sv.Host = strings.TrimSpace(sv.Host)
		if sv.Host == "" {
			continue
		}
		if strings.ContainsAny(sv.Host, " /") {
			return invalid("%q isn't a server hostname", sv.Host)
		}
		if sv.Port < 0 || sv.Port > 65535 {
			return invalid("ports are 1–65535")
		}
		if sv.Port == 0 {
			sv.Port = 6667
			if sv.TLS {
				sv.Port = 6697
			}
		}
		if strings.ContainsAny(sv.Password, "\r\n") {
			return invalid("server passwords must be one line")
		}
		clean = append(clean, sv)
	}
	if len(clean) == 0 {
		return invalid("a network needs a server hostname")
	}
	if len(clean) > 20 {
		return invalid("keep it to 20 servers")
	}
	n.Servers = clean
	n.Host, n.Port, n.TLS = clean[0].Host, clean[0].Port, clean[0].TLS

	var fps []string
	for _, f := range n.Fingerprints {
		f = NormalizeFingerprint(f)
		if f == "" {
			continue
		}
		if len(f) != 64 || strings.Trim(f, "0123456789abcdef") != "" {
			return invalid("certificate fingerprints are SHA-256: 64 hex digits (colons optional)")
		}
		if !slices.Contains(fps, f) {
			fps = append(fps, f)
		}
	}
	if fps == nil {
		fps = []string{}
	}
	n.Fingerprints = fps

	if n.Encoding != "" {
		if _, err := Charset(n.Encoding); err != nil {
			return err
		}
	}

	var chans []string
	settings := map[string]store.ChannelSettings{}
	for _, ch := range n.Channels {
		if ch = strings.TrimSpace(ch); ch == "" {
			continue
		}
		if !strings.ContainsRune("#&+!", rune(ch[0])) || strings.ContainsAny(ch, " ,\x07") {
			return invalid("%q isn't a channel name — channels start with #", ch)
		}
		if slices.ContainsFunc(chans, func(c string) bool { return strings.EqualFold(c, ch) }) {
			continue
		}
		chans = append(chans, ch)
		if cs, ok := n.ChannelSettings[strings.ToLower(ch)]; ok {
			cs.Key = strings.TrimSpace(cs.Key)
			if strings.ContainsAny(cs.Key, " ,") {
				return invalid("channel keys can't contain spaces or commas")
			}
			if cs != (store.ChannelSettings{}) {
				settings[strings.ToLower(ch)] = cs
			}
		}
	}
	if chans == nil {
		chans = []string{}
	}
	n.Channels, n.ChannelSettings = chans, settings
	return nil
}

// normalizeAuth checks the services login settings (docs/services.md).
func normalizeAuth(n *store.Network) error {
	n.AuthMethod = strings.ToLower(strings.TrimSpace(n.AuthMethod))
	n.ServicesNick = strings.TrimSpace(n.ServicesNick)
	n.IdentifyArgs = strings.ToLower(strings.TrimSpace(n.IdentifyArgs))
	n.IdentifyOn = strings.ToLower(strings.TrimSpace(n.IdentifyOn))
	n.GhostCmd = strings.ToLower(strings.TrimSpace(n.GhostCmd))
	n.AuthCommand = strings.TrimSpace(n.AuthCommand)
	if n.AuthMethod == "" {
		n.AuthMethod = store.AuthAuto
	}
	if n.IdentifyOn == "connect" {
		n.IdentifyOn = ""
	}
	switch {
	case !slices.Contains([]string{store.AuthAuto, store.AuthSASL, store.AuthNickServ, store.AuthCommand, store.AuthNone}, n.AuthMethod):
		return invalid("the login method is auto, sasl, nickserv, command or none")
	case strings.ContainsAny(n.ServicesNick, " ,:!\r\n") || len(n.ServicesNick) > 64:
		return invalid("the services nick is one word, like NickServ or NickServ@services.dal.net")
	case !slices.Contains([]string{"", store.IdentifyAccount, store.IdentifyPassword}, n.IdentifyArgs):
		return invalid("IDENTIFY takes the account and password, just the password, or (empty) whichever fits")
	case !slices.Contains([]string{"", store.IdentifyOnPrompt}, n.IdentifyOn):
		return invalid("identify on connect or on prompt")
	case !slices.Contains([]string{"", "ghost", "regain", "recover"}, n.GhostCmd):
		return invalid("free a taken nick with ghost, regain or recover (or not at all)")
	case strings.ContainsAny(n.AuthCommand, "\r\n") || len(n.AuthCommand) > 400:
		return invalid("the login command is one line")
	case n.AuthMethod == store.AuthCommand && n.AuthCommand == "":
		return invalid("the command login needs a command, like PRIVMSG X@channels.undernet.org :LOGIN %%account%% %%password%%")
	case n.AuthTimeout < 0 || n.AuthTimeout > 300:
		return invalid("the login timeout is 0–300 seconds")
	}
	return nil
}

func validNick(nick string) bool { return !strings.ContainsAny(nick, " ,*?!@#:") }

// NormalizeFingerprint lowercases a fingerprint and drops colons and spaces.
func NormalizeFingerprint(f string) string {
	return strings.ToLower(strings.NewReplacer(":", "", " ", "").Replace(strings.TrimSpace(f)))
}

// Charset resolves an Encoding value ("name" or "^name") to its charset.
func Charset(enc string) (encoding.Encoding, error) {
	name := strings.TrimPrefix(enc, "^")
	e, err := htmlindex.Get(name)
	if err != nil {
		return nil, invalid("%q isn't an encoding evrd knows (try ISO-8859-1, windows-1252, KOI8-R…)", name)
	}
	return e, nil
}

// CheckNetworkName refuses a name another of the user's networks already has
// (names pick the network in PASS user/network:secret, so they must differ).
func (s *Service) CheckNetworkName(userID int64, name string, exceptID int64) error {
	nets, err := s.ListNetworks(context.Background(), userID)
	if err != nil {
		return err
	}
	for _, n := range nets {
		if n.ID != exceptID && strings.EqualFold(n.Name, name) {
			return invalid("you already have a network called %s", n.Name)
		}
	}
	return nil
}

// NeedsReconnect reports whether a settings change only applies on a new
// connection. Names, channels, quit message and flood settings apply live.
func NeedsReconnect(a, b store.Network) bool {
	return !slices.Equal(a.ServerList(), b.ServerList()) || a.Nick != b.Nick || a.Username != b.Username ||
		a.Realname != b.Realname || a.Password != b.Password || a.SASLUser != b.SASLUser ||
		a.BindHost != b.BindHost || a.TrustAllCerts != b.TrustAllCerts || a.TrustPKI != b.TrustPKI ||
		!slices.Equal(a.Fingerprints, b.Fingerprints) || a.Encoding != b.Encoding ||
		a.Auth() != b.Auth() || a.ServicesNick != b.ServicesNick || a.IdentifyArgs != b.IdentifyArgs ||
		a.IdentifyOn != b.IdentifyOn || a.AuthCommand != b.AuthCommand || a.GhostCmd != b.GhostCmd ||
		a.JoinAfterAuth != b.JoinAfterAuth || a.AuthTimeout != b.AuthTimeout
}
