// Package config loads evrd's runtime configuration.
package config

import (
	"bufio"
	"fmt"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config is evrd's runtime configuration.
type Config struct {
	// Listen holds listener URLs, e.g. "ircs://:6697",
	// "irc+insecure://127.0.0.1:6667", "wss://:8097", "unix:///run/evrd.sock".
	Listen []string
	// DB is the storage DSN, e.g. "postgres://evrd:pass@db/evrd" or
	// "sqlite:/var/lib/evrd/evrd.db".
	DB string
	// History is where chat history is kept: "scylla://host1,host2/keyspace"
	// for a shared ScyllaDB (needed to run several evrd nodes), or
	// "pebble:/var/lib/evrd/history" for an embedded store on local disk
	// (one evrd process only).
	History string
	// API is the HTTP/WebSocket API listen address.
	API string
	// TLSCert and TLSKey are the cert/key for ircs:// listeners. If empty, a
	// self-signed cert is generated at startup (fine for dev).
	TLSCert string
	TLSKey  string

	// PublicURL is where the web app is served, e.g. https://bnc.example.org.
	// The OIDC callback is PublicURL + /auth/callback.
	PublicURL string
	// IRCHost is the hostname shown to users for attaching a native client.
	IRCHost string

	Auth     Auth
	Limits   Limits
	Cluster  Cluster
	Outbound Outbound
}

// Cluster configures running several evrd nodes against the same database.
// Each user is served by one node; the others forward that user's web
// requests and IRC clients to it over the internal endpoints, which must be
// reachable between nodes only (never from the internet).
type Cluster struct {
	// NodeID names this node; unique per process (default: the hostname).
	NodeID string
	// InternalListen is the internal HTTP listen address, e.g. ":8099";
	// InternalURL is how other nodes reach it, e.g. "http://evrd-1:8099".
	InternalListen string
	InternalURL    string
	// InternalIRCListen / InternalIRCAddr are the same for forwarded IRC
	// clients, e.g. ":6699" and "evrd-1:6699".
	InternalIRCListen string
	InternalIRCAddr   string
	// Secret authenticates nodes to each other. Required when the internal
	// endpoints are on.
	Secret string
	// LeaseTTL is how long a dead node's users wait before moving (default
	// 20s). The same on every node.
	LeaseTTL time.Duration
}

// Outbound configures connections to IRC networks.
type Outbound struct {
	// ConnectRate is the most connects per second to one server host (0 =
	// unlimited), so failovers and netsplit recoveries reconnect gradually.
	ConnectRate float64
	// Bind lists source addresses to spread users over; Prefix6 gives each
	// user an IPv6 address of their own. Per node (EVRD_UPSTREAM_BIND,
	// EVRD_UPSTREAM_PREFIX6).
	Bind    []netip.Addr
	Prefix6 netip.Prefix
}

// Enabled reports whether the internal endpoints are configured.
func (c Cluster) Enabled() bool { return c.InternalListen != "" }

// Auth configures sign-in.
type Auth struct {
	// PasswordLogin allows signing in to the web with a local password.
	PasswordLogin bool
	// SessionTTL is how long a web session lasts.
	SessionTTL time.Duration
	// CookieSecure marks the session cookie Secure (HTTPS only). Turn it off
	// only for plain-HTTP local development.
	CookieSecure bool

	// OIDC sign-in (Logto, or any OpenID Connect provider). Enabled when
	// Issuer is set. SAML and social logins are configured in the provider.
	OIDCIssuer       string
	OIDCClientID     string
	OIDCClientSecret string
	// OIDCLabel names the provider on the sign-in button.
	OIDCLabel string
	// OIDCAutoCreate creates an evrd account on first sign-in.
	OIDCAutoCreate bool
	// OIDCLinkExisting links a first-time SSO user to an existing local
	// account with the same username. Off by default: only turn it on when
	// usernames in the provider are known to belong to the same people.
	OIDCLinkExisting bool
	// OIDCAdminRole, when set, makes users holding that provider role evrd
	// admins (and demotes them when they lose it), synced at each sign-in.
	OIDCAdminRole string
}

// OIDCEnabled reports whether SSO sign-in is configured.
func (a Auth) OIDCEnabled() bool { return a.OIDCIssuer != "" }

// Limits are the server-wide defaults; 0 means unlimited. A user's own
// override, set by an admin, wins.
type Limits struct {
	MaxNetworks  int
	MaxClients   int
	BacklogLines int
}

// Default returns a config with sensible defaults.
func Default() *Config {
	return &Config{
		Listen:  []string{"irc+insecure://127.0.0.1:6667"},
		DB:      "sqlite:evrd.db",
		History: "pebble:history",
		API:     ":8098",
		IRCHost: "localhost",
		Auth: Auth{
			PasswordLogin:  true,
			SessionTTL:     30 * 24 * time.Hour,
			CookieSecure:   true,
			OIDCLabel:      "single sign-on",
			OIDCAutoCreate: true,
		},
		Limits:   Limits{MaxNetworks: 5, MaxClients: 5, BacklogLines: 2000},
		Outbound: Outbound{ConnectRate: 5},
	}
}

// Load reads a simple "directive value" config file. Blank lines and lines
// starting with '#' are ignored. See ApplyEnv for environment overrides.
func Load(path string) (*Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	cfg := Default()
	cfg.Listen = nil

	sc := bufio.NewScanner(f)
	line := 0
	for sc.Scan() {
		line++
		text := strings.TrimSpace(sc.Text())
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		key, val, _ := strings.Cut(text, " ")
		val = strings.TrimSpace(val)
		if err := cfg.set(key, val); err != nil {
			return nil, fmt.Errorf("%s:%d: %s: %w", path, line, key, err)
		}
	}
	if len(cfg.Listen) == 0 {
		cfg.Listen = Default().Listen
	}
	if err := cfg.ApplyEnv(); err != nil {
		return nil, err
	}
	return cfg, sc.Err()
}

func (c *Config) addBind(val string) error {
	for _, f := range strings.Fields(val) {
		a, err := netip.ParseAddr(f)
		if err != nil {
			return err
		}
		c.Outbound.Bind = append(c.Outbound.Bind, a)
	}
	return nil
}

// ApplyEnv applies settings that may come from the environment: secrets, and
// the per-node cluster and outbound settings, so several nodes can share one
// config file. It returns the first malformed value.
func (c *Config) ApplyEnv() error {
	if v := os.Getenv("EVRD_UPSTREAM_BIND"); v != "" {
		c.Outbound.Bind = nil
		if err := c.addBind(v); err != nil {
			return fmt.Errorf("EVRD_UPSTREAM_BIND: %w", err)
		}
	}
	if v := os.Getenv("EVRD_UPSTREAM_PREFIX6"); v != "" {
		p, err := netip.ParsePrefix(v)
		if err != nil {
			return fmt.Errorf("EVRD_UPSTREAM_PREFIX6: %w", err)
		}
		c.Outbound.Prefix6 = p
	}
	for env, dst := range map[string]*string{
		"EVRD_OIDC_CLIENT_SECRET": &c.Auth.OIDCClientSecret,
		"EVRD_CLUSTER_SECRET":     &c.Cluster.Secret,
		"EVRD_NODE_ID":            &c.Cluster.NodeID,
		"EVRD_INTERNAL_URL":       &c.Cluster.InternalURL,
		"EVRD_INTERNAL_IRC_ADDR":  &c.Cluster.InternalIRCAddr,
	} {
		if v := os.Getenv(env); v != "" {
			*dst = v
		}
	}
	return nil
}

func (c *Config) set(key, val string) error {
	var err error
	switch key {
	case "listen":
		c.Listen = append(c.Listen, val)
	case "db":
		c.DB = val
	case "history":
		c.History = val
	case "history_dir": // older name for a Pebble directory
		c.History = "pebble:" + val
	case "api":
		c.API = val
	case "tls_cert":
		c.TLSCert = val
	case "tls_key":
		c.TLSKey = val
	case "public_url":
		c.PublicURL = strings.TrimRight(val, "/")
	case "irc_host":
		c.IRCHost = val
	case "password_login":
		c.Auth.PasswordLogin, err = parseBool(val)
	case "session_ttl":
		c.Auth.SessionTTL, err = time.ParseDuration(val)
	case "cookie_secure":
		c.Auth.CookieSecure, err = parseBool(val)
	case "oidc_issuer":
		c.Auth.OIDCIssuer = val
	case "oidc_client_id":
		c.Auth.OIDCClientID = val
	case "oidc_client_secret":
		c.Auth.OIDCClientSecret = val
	case "oidc_label":
		c.Auth.OIDCLabel = val
	case "oidc_auto_create":
		c.Auth.OIDCAutoCreate, err = parseBool(val)
	case "oidc_link_existing":
		c.Auth.OIDCLinkExisting, err = parseBool(val)
	case "oidc_admin_role":
		c.Auth.OIDCAdminRole = val
	case "node_id":
		c.Cluster.NodeID = val
	case "internal_listen":
		c.Cluster.InternalListen = val
	case "internal_url":
		c.Cluster.InternalURL = strings.TrimRight(val, "/")
	case "internal_irc_listen":
		c.Cluster.InternalIRCListen = val
	case "internal_irc_addr":
		c.Cluster.InternalIRCAddr = val
	case "cluster_secret":
		c.Cluster.Secret = val
	case "cluster_lease":
		c.Cluster.LeaseTTL, err = time.ParseDuration(val)
	case "connect_rate":
		c.Outbound.ConnectRate, err = strconv.ParseFloat(val, 64)
	case "upstream_bind":
		err = c.addBind(val)
	case "upstream_prefix6":
		c.Outbound.Prefix6, err = netip.ParsePrefix(val)
	case "max_networks":
		c.Limits.MaxNetworks, err = strconv.Atoi(val)
	case "max_clients":
		c.Limits.MaxClients, err = strconv.Atoi(val)
	case "backlog_lines":
		c.Limits.BacklogLines, err = strconv.Atoi(val)
	default:
		return fmt.Errorf("unknown directive")
	}
	return err
}

func parseBool(v string) (bool, error) {
	switch strings.ToLower(v) {
	case "yes", "true", "on", "1":
		return true, nil
	case "no", "false", "off", "0":
		return false, nil
	}
	return false, fmt.Errorf("want yes or no, got %q", v)
}
