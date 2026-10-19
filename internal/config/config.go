// Package config loads evrd's runtime configuration.
package config

import (
	"bufio"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/4evrd/evrd/internal/realip"
	"net"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"slices"
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
	// TLS says where the certificate for evrd's TLS listeners (the IRC
	// ports, and HTTPSListen) comes from.
	TLS TLS
	// HTTPSListen, when set (e.g. ":443"), makes evrd serve the web app and
	// API over HTTPS itself; HTTPListen (e.g. ":80") then sends browsers to
	// it and answers Let's Encrypt's challenges. Unset, a proxy in front of
	// evrd handles HTTPS.
	HTTPSListen string
	HTTPListen  string
	// LinksSocket, when set, is the connection holder's socket (evrd-links,
	// internal/links): upstream connections live there and survive evrd
	// restarting. Single node only.
	LinksSocket string
	// ProvisionToken, when set, turns on the provisioning API
	// (/api/provision/…) for tools that create accounts and their networks,
	// like a migration from another bouncer. Callers send it as a bearer token.
	ProvisionToken string
	// WebRoot, when set, is the built web app (web/build) for evrd to serve
	// itself on the API address, so no separate web server is needed.
	WebRoot string

	// Chroot and User confine evrd once it has read its config, keys and
	// certificates: it chroots to Chroot and runs as User from then on (and
	// on OpenBSD also unveils and pledges). Paths under Chroot (db, history,
	// web_root) are used as seen from inside it.
	Chroot string
	User   string

	// PublicURL is where the web app is served, e.g. https://bnc.example.org.
	// The OIDC callback is PublicURL + /auth/callback.
	PublicURL string
	// IRCHost is the hostname shown to users for attaching a native client.
	IRCHost string

	Auth     Auth
	Limits   Limits
	Cluster  Cluster
	Outbound Outbound
	Metrics  Metrics
	// ModulesOff are modules (internal/modules) switched off server-wide.
	ModulesOff []string
	Secrets    Secrets

	// WriteConf (settings_write_conf) makes evrd write settings changed in
	// the admin UI back into this file, on every node.
	WriteConf bool
	// Path is the file this was loaded from ("" for none).
	Path string
	// fromFile records the directives the file set.
	fromFile map[string]bool
}

// TLS configures where certificates come from.
type TLS struct {
	// Source is "files" (TLSCert and TLSKey), "acme" (Let's Encrypt, or
	// another ACME CA) or "selfsigned". Empty: files when TLSCert is set,
	// else selfsigned.
	Source string
	// ACMEEmail is the contact for the CA's expiry and policy mail.
	ACMEEmail string
	// ACMEHosts are the names to get certificates for. Empty: public_url's
	// host and irc_host.
	ACMEHosts []string
	// ACMECA is the CA's ACME directory URL. Empty: Let's Encrypt.
	ACMECA string
}

// TLSSource is the certificate source in effect.
func (c *Config) TLSSource() string {
	if c.TLS.Source != "" {
		return c.TLS.Source
	}
	if c.TLSCert != "" && c.TLSKey != "" {
		return "files"
	}
	return "selfsigned"
}

// CertHosts are the names evrd's certificate must cover: acme_hosts, or
// public_url's host and irc_host.
func (c *Config) CertHosts() []string {
	var out []string
	add := func(h string) {
		h = strings.ToLower(strings.TrimSpace(h))
		if h != "" && !slices.Contains(out, h) {
			out = append(out, h)
		}
	}
	if len(c.TLS.ACMEHosts) > 0 {
		for _, h := range c.TLS.ACMEHosts {
			add(h)
		}
		return out
	}
	if u, err := url.Parse(c.PublicURL); err == nil {
		add(u.Hostname())
	}
	add(c.IRCHost)
	return out
}

// ACMEHosts are the CertHosts a public CA will sign: not IPs, localhost or
// single labels.
func (c *Config) ACMEHosts() []string {
	var out []string
	for _, h := range c.CertHosts() {
		if net.ParseIP(h) != nil || h == "localhost" || !strings.Contains(h, ".") ||
			strings.HasSuffix(h, ".localhost") || strings.HasSuffix(h, ".local") {
			continue
		}
		out = append(out, h)
	}
	return out
}

// CheckSignIn refuses settings that leave no way to sign in to the web app:
// password sign-in off with no single sign-on to use instead.
func (c *Config) CheckSignIn() error {
	if !c.Auth.PasswordLogin && !c.Auth.OIDCEnabled() {
		return errors.New("set up single sign-on before turning off password sign-in — otherwise nobody can sign in to the web app")
	}
	return nil
}

// CheckTLS reports a certificate setup that can't work.
func (c *Config) CheckTLS() error {
	switch c.TLSSource() {
	case "acme":
		if len(c.ACMEHosts()) == 0 {
			return errors.New("Let's Encrypt needs a public host name: set the public URL, the IRC host name or the certificate host names")
		}
	case "files":
		if c.TLSCert == "" || c.TLSKey == "" {
			return errors.New("certificate files: set both the certificate and the key file")
		}
	}
	return nil
}

// Secrets configures encryption of saved network passwords. Every node must
// have the same key; keep it out of the database and its backups.
type Secrets struct {
	// Key is 32 bytes, base64 or hex (evrctl genkey makes one). KeyFile
	// reads it from a file instead (EVRD_SECRET_KEY works too).
	Key     string
	KeyFile string
	// Old keys still decrypt (for rotating); evrd re-encrypts everything
	// under Key at startup.
	Old []string
}

// SecretKeys decodes the configured keys. A nil current key means secrets
// are stored unencrypted.
func (c *Config) SecretKeys() (current []byte, old [][]byte, err error) {
	key := c.Secrets.Key
	if key == "" && c.Secrets.KeyFile != "" {
		b, err := os.ReadFile(c.Secrets.KeyFile)
		if err != nil {
			return nil, nil, fmt.Errorf("secret_key_file: %w", err)
		}
		key = strings.TrimSpace(string(b))
	}
	if key != "" {
		if current, err = decodeKey(key); err != nil {
			return nil, nil, fmt.Errorf("secret_key: %w", err)
		}
	}
	for _, o := range c.Secrets.Old {
		k, err := decodeKey(o)
		if err != nil {
			return nil, nil, fmt.Errorf("secret_key_old: %w", err)
		}
		old = append(old, k)
	}
	return current, old, nil
}

func decodeKey(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	for _, dec := range []func(string) ([]byte, error){
		hex.DecodeString, base64.StdEncoding.DecodeString, base64.RawStdEncoding.DecodeString,
		base64.URLEncoding.DecodeString, base64.RawURLEncoding.DecodeString,
	} {
		if b, err := dec(s); err == nil && len(b) == 32 {
			return b, nil
		}
	}
	return nil, errors.New("want 32 bytes, base64 or hex (evrctl genkey makes one)")
}

// Metrics configures OpenTelemetry export and the store dashboards read
// back from (VictoriaMetrics: it accepts OTLP and answers PromQL).
type Metrics struct {
	// OTLPEndpoint is the OTLP/HTTP metrics URL, e.g.
	// http://victoriametrics:8428/opentelemetry/v1/metrics. Empty: no export.
	OTLPEndpoint string
	Interval     time.Duration
	// QueryURL is the Prometheus-compatible query API, e.g.
	// http://victoriametrics:8428. Empty: dashboards show no charts.
	QueryURL string
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
	// BindHosts are the local addresses (or host names for them) users may
	// pick to connect from, like ZNC's BindHost list. Admins may set any.
	BindHosts []string
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
	// TrustedProxies are the proxies whose X-Forwarded-For is believed (nil:
	// the local host only). Set in the file or environment only.
	TrustedProxies []netip.Prefix
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
		Limits:   Limits{MaxNetworks: 0, MaxClients: 0, BacklogLines: 2000},
		Outbound: Outbound{ConnectRate: 5},
		Metrics:  Metrics{Interval: 15 * time.Second},
		fromFile: map[string]bool{},
	}
}

// Load reads a simple "directive value" config file. Blank lines and lines
// starting with '#' are ignored. See ApplyEnv for environment overrides.
func Load(path string) (*Config, error) {
	cfg, err := LoadFile(path)
	if err != nil {
		return nil, err
	}
	if err := cfg.ApplyEnv(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// LoadFile reads a config file without environment overrides, remembering
// its path and which directives it set (see WithSettings).
func LoadFile(path string) (*Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	cfg := Default()
	cfg.Listen = nil
	cfg.Path = path

	sc := bufio.NewScanner(f)
	line := 0
	for sc.Scan() {
		line++
		text := strings.TrimSpace(sc.Text())
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		key, val, _ := strings.Cut(text, " ")
		val, _ = splitComment(val)
		if err := cfg.set(key, val); err != nil {
			return nil, fmt.Errorf("%s:%d: %s: %w", path, line, key, err)
		}
		cfg.fromFile[key] = true
	}
	if len(cfg.Listen) == 0 {
		cfg.Listen = Default().Listen
	}
	return cfg, sc.Err()
}

// splitComment splits "value   # comment" into the value and the comment
// (with its leading space). A # only starts a comment after whitespace, so
// values like "#chan" or "pa#ss" are kept.
func splitComment(val string) (string, string) {
	for i := 1; i < len(val); i++ {
		if val[i] == '#' && (val[i-1] == ' ' || val[i-1] == '\t') {
			j := i - 1
			for j > 0 && (val[j-1] == ' ' || val[j-1] == '\t') {
				j--
			}
			return strings.TrimSpace(val[:j]), val[j:]
		}
	}
	return strings.TrimSpace(val), ""
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
	if v := os.Getenv("EVRD_TRUSTED_PROXIES"); v != "" {
		ps, err := realip.ParsePrefixes(strings.Fields(v))
		if err != nil {
			return fmt.Errorf("EVRD_TRUSTED_PROXIES: %w", err)
		}
		c.Auth.TrustedProxies = ps
	}
	if v := os.Getenv("EVRD_UPSTREAM_PREFIX6"); v != "" {
		p, err := netip.ParsePrefix(v)
		if err != nil {
			return fmt.Errorf("EVRD_UPSTREAM_PREFIX6: %w", err)
		}
		c.Outbound.Prefix6 = p
	}
	for env, dst := range map[string]*string{
		"EVRD_DB":                 &c.DB,
		"EVRD_HISTORY":            &c.History,
		"EVRD_OIDC_CLIENT_SECRET": &c.Auth.OIDCClientSecret,
		"EVRD_CLUSTER_SECRET":     &c.Cluster.Secret,
		"EVRD_SECRET_KEY":         &c.Secrets.Key,
		"EVRD_SECRET_KEY_FILE":    &c.Secrets.KeyFile,
		"EVRD_PROVISION_TOKEN":    &c.ProvisionToken,
		"EVRD_LINKS_SOCKET":       &c.LinksSocket,
		"EVRD_NODE_ID":            &c.Cluster.NodeID,
		"EVRD_INTERNAL_URL":       &c.Cluster.InternalURL,
		"EVRD_INTERNAL_IRC_ADDR":  &c.Cluster.InternalIRCAddr,
		// the standard OpenTelemetry variable
		"OTEL_EXPORTER_OTLP_METRICS_ENDPOINT": &c.Metrics.OTLPEndpoint,
		"EVRD_METRICS_URL":                    &c.Metrics.QueryURL,
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
	case "tls":
		switch val {
		case "files", "acme", "selfsigned":
			c.TLS.Source = val
		default:
			err = errors.New("files, acme or selfsigned")
		}
	case "acme_email":
		c.TLS.ACMEEmail = val
	case "acme_hosts": // several per line
		c.TLS.ACMEHosts = append(c.TLS.ACMEHosts, strings.Fields(val)...)
	case "acme_ca":
		c.TLS.ACMECA = val
	case "https_listen":
		c.HTTPSListen = val
	case "http_listen":
		c.HTTPListen = val
	case "links_socket":
		c.LinksSocket = val
	case "provision_token":
		c.ProvisionToken = val
	case "web_root":
		c.WebRoot = val
	case "chroot":
		c.Chroot = val
	case "user":
		c.User = val
	case "public_url":
		c.PublicURL = strings.TrimRight(val, "/")
	case "irc_host":
		c.IRCHost = val
	case "password_login":
		c.Auth.PasswordLogin, err = parseBool(val)
	case "session_ttl":
		c.Auth.SessionTTL, err = time.ParseDuration(val)
	case "trusted_proxies": // repeatable, or several per line
		var ps []netip.Prefix
		if ps, err = realip.ParsePrefixes(strings.Fields(val)); err == nil {
			c.Auth.TrustedProxies = append(c.Auth.TrustedProxies, ps...)
		}
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
	case "modules_off": // repeatable, or several per line
		c.ModulesOff = append(c.ModulesOff, strings.Fields(val)...)
	case "bindhost": // repeatable, or several per line
		c.Outbound.BindHosts = append(c.Outbound.BindHosts, strings.Fields(val)...)
	case "settings_write_conf":
		c.WriteConf, err = parseBool(val)
	case "secret_key":
		c.Secrets.Key = val
	case "secret_key_file":
		c.Secrets.KeyFile = val
	case "secret_key_old":
		c.Secrets.Old = append(c.Secrets.Old, strings.Fields(val)...)
	case "upstream_bind":
		err = c.addBind(val)
	case "upstream_prefix6":
		c.Outbound.Prefix6, err = netip.ParsePrefix(val)
	case "otel_endpoint":
		c.Metrics.OTLPEndpoint = val
	case "otel_interval":
		c.Metrics.Interval, err = time.ParseDuration(val)
	case "metrics_url":
		c.Metrics.QueryURL = strings.TrimRight(val, "/")
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

// Jail rewrites the local paths (an SQLite db, a Pebble history, web_root)
// as seen from inside Chroot: /var/evrd/data/evrd.db becomes /data/evrd.db.
// They must be absolute and under Chroot.
// InChroot is path as seen from inside Chroot, when it's under it
// (/var/evrd/etc/tls/x.crt → /etc/tls/x.crt).
func (c *Config) InChroot(path string) (string, bool) {
	if c.Chroot == "" || !filepath.IsAbs(path) {
		return path, false
	}
	rel, err := filepath.Rel(filepath.Clean(c.Chroot), filepath.Clean(path))
	if err != nil || rel == ".." || strings.HasPrefix(rel, "../") {
		return path, false
	}
	return filepath.Join("/", rel), true
}

func (c *Config) Jail() error {
	if c.Chroot == "" {
		return nil
	}
	root := filepath.Clean(c.Chroot)
	inside := func(name, p string) (string, error) {
		if !filepath.IsAbs(p) {
			return "", fmt.Errorf("%s %s: with chroot, use an absolute path under %s", name, p, root)
		}
		rel, err := filepath.Rel(root, filepath.Clean(p))
		if err != nil || rel == ".." || strings.HasPrefix(rel, "../") {
			return "", fmt.Errorf("%s %s: not under the chroot %s", name, p, root)
		}
		return filepath.Join("/", rel), nil
	}
	for _, d := range []struct {
		name, scheme string
		dst          *string
	}{{"db", "sqlite:", &c.DB}, {"history", "pebble:", &c.History}, {"web_root", "", &c.WebRoot}} {
		v := *d.dst
		if v == "" || strings.Contains(v, "://") {
			continue // a database server: not a path
		}
		v = strings.TrimPrefix(v, d.scheme)
		path, query, _ := strings.Cut(v, "?")
		p, err := inside(d.name, path)
		if err != nil {
			return err
		}
		if query != "" {
			p += "?" + query
		}
		*d.dst = d.scheme + p
	}
	return nil
}
