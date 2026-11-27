package config

// Managed mode (docs/managed.md): another service (Boson) runs evrd as an
// invisible IRCv3 bridge in front of one IRC server. Every account gets
// that one network and nothing else, accounts come from the managed API,
// IRC clients sign in with the service's OIDC access tokens, and nothing a
// client sees names the bouncer.

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Managed configures managed mode. Off (the zero value) changes nothing.
type Managed struct {
	// On turns managed mode on (managed yes).
	On bool
	// Network is the network's name: each account's one network, and the
	// ISUPPORT NETWORK clients see when the server sends none.
	Network string
	// Host, Port and TLS are the IRC server (managed_server host [+]port).
	Host string
	Port int
	TLS  bool
	// TLSVerify checks the server's certificate chain (default yes);
	// Fingerprints are SHA-256 fingerprints to accept instead.
	TLSVerify    bool
	Fingerprints []string
	// ServerPassword is sent as PASS ("" none).
	ServerPassword string
	// WebIRCPassword, when set, makes every connection start with WEBIRC,
	// so the server shows each user's own address. WebIRCGateway is the
	// gateway name sent with it; WebIRCResolve looks the address's host
	// name up (forward-confirmed) instead of sending the address alone.
	WebIRCPassword string
	WebIRCGateway  string
	WebIRCResolve  bool
	// ServerName is the name clients see the bouncer's own lines come from.
	ServerName string
	// Token is the bearer token of the managed API (/api/managed/…).
	Token string
	// TokenIssuer is the OIDC issuer whose access tokens IRC clients sign
	// in with (default: oidc_issuer); TokenAudience the "aud" they must
	// carry ("*" accepts any); TokenJWKS a JWKS URL to read the keys from
	// instead of the issuer's discovery document.
	TokenIssuer   string
	TokenAudience string
	TokenJWKS     string
	// MOTD is the message of the day, a line per managed_motd ("": none).
	MOTD []string
	// Texts are what clients are told, by name (ManagedTexts): "-" sends
	// nothing where that's possible.
	Texts map[string]string
	// AwayMessage is set on the network while no client is attached (for
	// accounts with auto-away); VersionReply answers CTCP VERSION then
	// ("": no answer); QuitMessage is sent when a connection is switched off.
	AwayMessage  string
	VersionReply string
	QuitMessage  string
	// NickGuard keeps accounts that don't log in to services off nicks
	// registered to someone else (managed_nick_guard, default yes).
	NickGuard bool
	// NickGuardGrace is how long a guarded connection may stay on a nick
	// services say is registered, so the user can identify for it, before
	// it's moved off (managed_nick_guard_grace, default 60s; 0 moves at
	// once).
	NickGuardGrace time.Duration
	// ClientWebIRCPassword lets a web gateway in front of evrd (Boson's
	// webircgateway) pass each client's own address with WEBIRC
	// (managed_client_webirc_password; "" refuses WEBIRC from clients).
	ClientWebIRCPassword string
	// Guests lets clients without credentials in (managed_guests, default
	// no): each gets an ephemeral session of its own on the network.
	Guests bool
	// GuestsMax caps the guests at once (managed_guests_max, default 100;
	// 0 no cap), GuestsPerIP those from one address (managed_guests_per_ip,
	// default 3; 0 no cap).
	GuestsMax, GuestsPerIP int
	// GuestLinger keeps a guest's connection after its client goes, so a
	// page refresh picks it up again (managed_guest_linger, default 30s).
	GuestLinger time.Duration
}

// ManagedTexts are the texts managed_text sets, with their defaults.
// {network} is the network's name, {nick} the user's nick.
var ManagedTexts = map[string]string{
	"welcome":      "Welcome to the {network} IRC Network {nick}",
	"disconnected": "Connection to {network} lost, reconnecting",
	"reconnected":  "Reconnected to {network}",
	"kicked":       "Connection to {network} lost",
	"left":         "Left",
	"restarting":   "Server restarting, please reconnect",
	"unavailable":  "Service unavailable, try again later",
	"denied":       "Sign-in failed",
	// the nick guard's warning on a registered nick ({services}, {grace}
	// and {next} too)
	"registered": "{nick} is registered with {services}. Identify within {grace} (/msg {services} IDENTIFY password) or you'll be moved to {next}",
}

// defaultManaged is managed mode's defaults, when it's off.
func defaultManaged() Managed {
	return Managed{TLSVerify: true, WebIRCGateway: "gateway", WebIRCResolve: true, ServerName: "irc", AwayMessage: "Away", NickGuard: true,
		NickGuardGrace: 60 * time.Second, GuestsMax: 100, GuestsPerIP: 3, GuestLinger: 30 * time.Second}
}

// Text is the named text (ManagedTexts) with {network} and {nick} filled
// in; "" when it's set to "-" (send nothing).
func (m Managed) Text(name, nick string) string {
	t, ok := m.Texts[name]
	if !ok {
		t = ManagedTexts[name]
	}
	if t == "-" {
		return ""
	}
	return strings.NewReplacer("{network}", m.Network, "{nick}", nick).Replace(t)
}

// ManagedIssuer is the issuer of the access tokens IRC clients sign in
// with in managed mode: managed_token_issuer, else oidc_issuer ("": none).
func (c *Config) ManagedIssuer() string {
	if c.Managed.TokenIssuer != "" {
		return c.Managed.TokenIssuer
	}
	return strings.TrimRight(c.Auth.OIDCIssuer, "/")
}

// CheckManaged reports a managed setup that can't work.
func (c *Config) CheckManaged() error {
	m := c.Managed
	if !m.On {
		return nil
	}
	switch {
	case m.Network == "":
		return errors.New("managed: set managed_network, the network's name")
	case m.Host == "" || m.Port == 0:
		return errors.New("managed: set managed_server <host> [+]<port>")
	case m.Token == "":
		return errors.New("managed: set managed_token (or EVRD_MANAGED_TOKEN), the managed API's token")
	case c.ManagedIssuer() == "":
		return errors.New("managed: set managed_token_issuer (or oidc_issuer), whose access tokens IRC clients sign in with")
	case m.TokenAudience == "":
		return errors.New(`managed: set managed_token_audience, the "aud" of the access tokens clients sign in with ("*" for any)`)
	case strings.ContainsAny(m.ServerName, " :!@"):
		return errors.New("managed: managed_server_name is one word, without : ! or @")
	case strings.ContainsAny(m.WebIRCGateway, " "):
		return errors.New("managed: managed_webirc_gateway is one word")
	}
	return nil
}

// setManaged sets a managed_* directive, reporting whether key is one.
func (c *Config) setManaged(key, val string) (bool, error) {
	m := &c.Managed
	var err error
	switch key {
	case "managed":
		m.On, err = parseBool(val)
	case "managed_network":
		m.Network = val
	case "managed_server":
		m.Host, m.Port, m.TLS, err = parseServer(val)
	case "managed_tls_verify":
		m.TLSVerify, err = parseBool(val)
	case "managed_fingerprint": // repeatable, or several per line
		for _, f := range strings.Fields(val) {
			m.Fingerprints = append(m.Fingerprints, strings.ToLower(strings.ReplaceAll(f, ":", "")))
		}
	case "managed_server_password":
		m.ServerPassword = val
	case "managed_webirc_password":
		m.WebIRCPassword = val
	case "managed_webirc_gateway":
		m.WebIRCGateway = val
	case "managed_webirc_resolve":
		m.WebIRCResolve, err = parseBool(val)
	case "managed_server_name":
		m.ServerName = val
	case "managed_token":
		m.Token = val
	case "managed_token_issuer":
		m.TokenIssuer = strings.TrimRight(val, "/")
	case "managed_token_audience":
		m.TokenAudience = val
	case "managed_token_jwks":
		m.TokenJWKS = val
	case "managed_motd": // repeatable: a line each
		m.MOTD = append(m.MOTD, val)
	case "managed_text": // managed_text <name> <text>
		name, text, _ := strings.Cut(val, " ")
		if _, ok := ManagedTexts[name]; !ok {
			return true, fmt.Errorf("%q isn't a managed text (%s)", name, strings.Join(managedTextNames(), ", "))
		}
		if m.Texts == nil {
			m.Texts = map[string]string{}
		}
		m.Texts[name] = strings.TrimSpace(text)
	case "managed_away":
		m.AwayMessage = val
	case "managed_version":
		m.VersionReply = val
	case "managed_quit":
		m.QuitMessage = val
	case "managed_nick_guard":
		m.NickGuard, err = parseBool(val)
	case "managed_nick_guard_grace":
		m.NickGuardGrace, err = parseSeconds(val)
	case "managed_client_webirc_password":
		m.ClientWebIRCPassword = val
	case "managed_guests":
		m.Guests, err = parseBool(val)
	case "managed_guests_max":
		m.GuestsMax, err = parseCount(val)
	case "managed_guests_per_ip":
		m.GuestsPerIP, err = parseCount(val)
	case "managed_guest_linger":
		m.GuestLinger, err = parseSeconds(val)
	default:
		return false, nil
	}
	return true, err
}

// managedTextNames are ManagedTexts' names, sorted.
func managedTextNames() []string {
	var out []string
	for k := range ManagedTexts {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// parseServer reads "host [+]port" ("+": TLS); the port defaults to 6697
// with TLS ("host +") and 6667 without.
func parseServer(val string) (host string, port int, tls bool, err error) {
	f := strings.Fields(val)
	if len(f) == 0 || len(f) > 2 {
		return "", 0, false, errors.New("want <host> [+]<port>")
	}
	host, port = f[0], 6667
	if len(f) == 2 {
		p := f[1]
		if tls = strings.HasPrefix(p, "+"); tls {
			p, port = p[1:], 6697
		}
		if p != "" {
			if port, err = strconv.Atoi(p); err != nil || port < 1 || port > 65535 {
				return "", 0, false, fmt.Errorf("%q isn't a port", f[1])
			}
		}
	}
	return host, port, tls, nil
}

// parseSeconds is a duration ("60s", "2m") or a number of seconds ("60"),
// not negative.
func parseSeconds(val string) (time.Duration, error) {
	if n, err := strconv.Atoi(val); err == nil {
		val = strconv.Itoa(n) + "s"
	}
	d, err := time.ParseDuration(val)
	if err == nil && d < 0 {
		err = errors.New("must not be negative")
	}
	return d, err
}

// parseCount is a number, 0 or more.
func parseCount(val string) (int, error) {
	n, err := strconv.Atoi(val)
	if err == nil && n < 0 {
		err = errors.New("must not be negative")
	}
	return n, err
}
