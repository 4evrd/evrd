// Package managed is evrd's managed mode (docs/managed.md): a service such
// as Boson runs evrd as an invisible IRCv3 bridge in front of one IRC
// server that lacks IRCv3. The service creates the accounts through the
// managed API, every account has exactly the one configured network,
// clients sign in with the service's access tokens, connections reach the
// server with WEBIRC so each user shows their own address, and nothing a
// client is told names the bouncer.
package managed

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"slices"
	"strings"
	"time"

	"github.com/4evrd/evrd/internal/accounts"
	"github.com/4evrd/evrd/internal/config"
	"github.com/4evrd/evrd/internal/store"
)

// Mode is managed mode as configured. A nil *Mode is managed mode off.
type Mode struct {
	cfg    config.Managed
	tokens *Verifier
	// lookup is the resolver for WEBIRC host names (tests stand in).
	lookupAddr func(ctx context.Context, addr string) ([]string, error)
	lookupIP   func(ctx context.Context, host string) ([]net.IP, error)
	// live is what the managed API changed over cfg (live.go).
	live live
}

// New is c's managed mode, nil when it's off.
func New(c *config.Config) *Mode {
	if !c.Managed.On {
		return nil
	}
	m := &Mode{cfg: c.Managed, lookupAddr: net.DefaultResolver.LookupAddr, lookupIP: func(ctx context.Context, host string) ([]net.IP, error) {
		return net.DefaultResolver.LookupIP(ctx, "ip", host)
	}}
	if iss := c.ManagedIssuer(); iss != "" {
		m.tokens = NewVerifier(iss, c.Managed.TokenAudience, c.Managed.TokenJWKS)
	}
	return m
}

// On reports whether managed mode is on.
func (m *Mode) On() bool { return m != nil }

// Config is the managed settings in effect: the config's, with what the
// managed API changed over them (live.go).
func (m *Mode) Config() config.Managed {
	c := m.cfg
	m.live.mu.Lock()
	m.live.in.apply(&c)
	m.live.mu.Unlock()
	return c
}

// ServerName is the name the bouncer's own lines come from.
func (m *Mode) ServerName() string { return m.cfg.ServerName }

// NetworkName is the network's name.
func (m *Mode) NetworkName() string { return m.cfg.Network }

// Text is a client-facing text (config.ManagedTexts) for nick: "" to send
// nothing.
func (m *Mode) Text(name, nick string) string { return m.cfg.Text(name, nick) }

// Tokens checks the access tokens clients sign in with; nil when no issuer
// is set.
func (m *Mode) Tokens() *Verifier { return m.tokens }

// SetTokens replaces the token checker (tests).
func (m *Mode) SetTokens(v *Verifier) { m.tokens = v }

// IsNetwork reports whether n is the managed network.
func (m *Mode) IsNetwork(n store.Network) bool { return strings.EqualFold(n.Name, m.cfg.Network) }

// Apply sets the server side of n from the config: name, server, TLS and
// certificate checks. A user's own settings (nick, login) are left alone.
func (m *Mode) Apply(n *store.Network) {
	c := m.cfg
	n.Name, n.Host, n.Port, n.TLS = c.Network, c.Host, c.Port, c.TLS
	n.Servers = []store.Server{{Host: c.Host, Port: c.Port, TLS: c.TLS, Password: c.ServerPassword}}
	n.TrustPKI, n.TrustAllCerts = c.TLSVerify, false
	n.Fingerprints = slices.Clone(c.Fingerprints)
	n.BindHost = ""
	n.Modules = nil
}

// ErrNoAccount is a valid token whose user has no account here.
var ErrNoAccount = errors.New("no account for this token")

// SignIn is the account token signs in, checked with the issuer: the one
// linked to its subject — or, the first time, the account named after its
// user (when that one isn't linked to anyone yet), which is then linked.
// username, when not "", must be the account's.
func (m *Mode) SignIn(ctx context.Context, acc accounts.AccountServiceImpl, token, username string) (store.User, error) {
	if m.tokens == nil {
		return store.User{}, ErrTokensUnavailable
	}
	cl, err := m.tokens.Verify(ctx, token)
	if err != nil {
		return store.User{}, err
	}
	u, err := acc.UserBySubject(ctx, cl.Subject)
	if errors.Is(err, store.ErrNotFound) && cl.Name != "" {
		name, nerr := accounts.NormalizeUsername(cl.Name)
		if nerr != nil {
			return store.User{}, ErrNoAccount
		}
		u, err = acc.UserByUsername(ctx, name)
		if err == nil && u.OIDCSubject != nil {
			return store.User{}, ErrNoAccount // someone else's
		}
		if err == nil {
			sub := cl.Subject
			u.OIDCSubject = &sub
			if err = acc.UpdateUser(ctx, u); err != nil {
				return store.User{}, err
			}
		}
	}
	if errors.Is(err, store.ErrNotFound) {
		return store.User{}, ErrNoAccount
	}
	if err != nil {
		return store.User{}, err
	}
	if username != "" && !strings.EqualFold(username, u.Username) {
		return store.User{}, ErrBadToken
	}
	if u.Suspended {
		return store.User{}, accounts.ErrSuspended
	}
	return u, nil
}

// WebIRC is the WEBIRC line that gives the server ip as the user's address
// (and its host name, forward-confirmed, when managed_webirc_resolve is
// on); "" when WEBIRC is off or ip isn't an address. A gateway of
// "{ident}" sends the user's ident in its place: ngircd takes WEBIRC's
// second parameter as the user name.
func (m *Mode) WebIRC(ctx context.Context, ip, ident string) string {
	if m == nil || m.cfg.WebIRCPassword == "" {
		return ""
	}
	a, err := netip.ParseAddr(ip)
	if err != nil {
		return ""
	}
	a = a.Unmap()
	addr := a.String()
	host := addr
	if m.cfg.WebIRCResolve {
		host = m.resolve(ctx, a)
	}
	gw := m.cfg.WebIRCGateway
	if gw == IdentGateway {
		gw = strings.TrimPrefix(ident, "~")
		if gw == "" || strings.ContainsAny(gw, " :") {
			gw = "user"
		}
	}
	return "WEBIRC " + m.cfg.WebIRCPassword + " " + gw + " " + colonSafe(host) + " " + colonSafe(addr)
}

// IdentGateway, as managed_webirc_gateway, sends each user's ident as
// WEBIRC's gateway (for ngircd).
const IdentGateway = "{ident}"

// colonSafe keeps an IPv6 address from reading as a trailing parameter.
func colonSafe(s string) string {
	if strings.HasPrefix(s, ":") {
		return "0" + s
	}
	return s
}

// resolve is a's host name when its reverse lookup resolves back to it,
// else the address itself.
func (m *Mode) resolve(ctx context.Context, a netip.Addr) string {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	names, err := m.lookupAddr(ctx, a.String())
	if err != nil {
		return a.String()
	}
	for _, name := range names {
		name = strings.TrimSuffix(name, ".")
		if name == "" || strings.ContainsAny(name, " :") {
			continue
		}
		ips, err := m.lookupIP(ctx, name)
		if err != nil {
			continue
		}
		for _, ip := range ips {
			if b, ok := netip.AddrFromSlice(ip); ok && b.Unmap() == a {
				return name
			}
		}
	}
	return a.String()
}
