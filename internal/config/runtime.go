package config

// Runtime settings: the directives an admin can change from the web app.
// Their values live in the database (shared by every node) as directive
// text, layered over the config file:
//
//	defaults < evrd.conf < database (admin UI) < environment
//
// Bootstrap directives (listen addresses, database, cluster identity,
// secret keys…) stay file/env only: evrd needs them before it can read the
// database, and some differ per node.

import (
	"bufio"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Spec describes one editable setting.
type Spec struct {
	Key   string `json:"key"`
	Group string `json:"group"`
	Label string `json:"label"`
	Help  string `json:"help"`
	// Kind is int, bool, string, list (space-separated), duration, float,
	// choice (one of Options), or secret (a string that's never shown back).
	Kind    string   `json:"kind"`
	Options []string `json:"options,omitempty"`
	// Restart is set when a change only takes effect after evrd restarts.
	Restart bool `json:"restart"`
	// Env is the environment variable that overrides it, if any.
	Env string `json:"env,omitempty"`
	get func(*Config) string
	// reset clears a list before the database's value replaces it.
	reset func(*Config)
}

func fmtBool(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

func fmtDur(d time.Duration) string {
	if d == 0 {
		return "0s"
	}
	s := d.String() // 720h0m0s -> 720h, 1h30m0s -> 1h30m, 10s stays
	if strings.HasSuffix(s, "m0s") {
		s = strings.TrimSuffix(s, "0s")
	}
	if strings.HasSuffix(s, "h0m") {
		s = strings.TrimSuffix(s, "0m")
	}
	return s
}

// Specs are the settings the admin UI edits, in display order.
var Specs = []Spec{
	{Key: "irc_host", Group: "General", Label: "IRC host name", Kind: "string",
		Help: "The host name users point their IRC clients at (shown on the IRC clients page).",
		get:  func(c *Config) string { return c.IRCHost }},
	{Key: "public_url", Group: "General", Label: "Public URL", Kind: "string", Restart: true,
		Help: "Where the web app is served, e.g. https://bnc.example.org. Single sign-on redirects back here.",
		get:  func(c *Config) string { return c.PublicURL }},

	{Key: "max_networks", Group: "Limits", Label: "Networks per user", Kind: "int",
		Help: "0 is unlimited. Admins can override it per user.",
		get:  func(c *Config) string { return strconv.Itoa(c.Limits.MaxNetworks) }},
	{Key: "max_clients", Group: "Limits", Label: "IRC clients per user", Kind: "int",
		Help: "Attached at once. 0 is unlimited.",
		get:  func(c *Config) string { return strconv.Itoa(c.Limits.MaxClients) }},
	{Key: "backlog_lines", Group: "Limits", Label: "Backlog per buffer", Kind: "int",
		Help: "Lines kept per channel or query, pruned every 10 minutes. 0 keeps everything.",
		get:  func(c *Config) string { return strconv.Itoa(c.Limits.BacklogLines) }},

	{Key: "connect_rate", Group: "Connections", Label: "Connects per second", Kind: "float",
		Help: "The most new connections per second to one server host, per node, so reconnect storms are spread out. 0 is unlimited.",
		get:  func(c *Config) string { return strconv.FormatFloat(c.Outbound.ConnectRate, 'f', -1, 64) }},
	{Key: "bindhost", Group: "Connections", Label: "Bind hosts", Kind: "list",
		Help:  "Local addresses (or host names) users may choose to connect from. Space-separated.",
		get:   func(c *Config) string { return strings.Join(c.Outbound.BindHosts, " ") },
		reset: func(c *Config) { c.Outbound.BindHosts = nil }},

	{Key: "tls", Group: "HTTPS & TLS", Label: "Certificate", Kind: "choice", Options: []string{"acme", "selfsigned", "files"},
		Help: "For the IRC ports, and for the web app when evrd serves HTTPS itself. Let's Encrypt (acme) gets and renews one automatically, accepting the CA's subscriber agreement.",
		get:  func(c *Config) string { return c.TLSSource() }},
	{Key: "acme_email", Group: "HTTPS & TLS", Label: "Let's Encrypt contact email", Kind: "string",
		Help: "Optional: where the CA writes about expiring certificates or policy changes.",
		get:  func(c *Config) string { return c.TLS.ACMEEmail }},
	{Key: "acme_hosts", Group: "HTTPS & TLS", Label: "Certificate host names", Kind: "list",
		Help:  "Names the certificate covers. Empty: the public URL's host and the IRC host name.",
		get:   func(c *Config) string { return strings.Join(c.TLS.ACMEHosts, " ") },
		reset: func(c *Config) { c.TLS.ACMEHosts = nil }},
	{Key: "acme_ca", Group: "HTTPS & TLS", Label: "ACME directory", Kind: "string",
		Help: "Empty: Let's Encrypt. Their staging directory (https://acme-staging-v02.api.letsencrypt.org/directory) is for trying things out.",
		get:  func(c *Config) string { return c.TLS.ACMECA }},
	{Key: "tls_cert", Group: "HTTPS & TLS", Label: "Certificate file", Kind: "string", Restart: true,
		Help: "PEM, with the chain. Read at startup, before evrd chroots.",
		get:  func(c *Config) string { return c.TLSCert }},
	{Key: "tls_key", Group: "HTTPS & TLS", Label: "Key file", Kind: "string", Restart: true,
		get: func(c *Config) string { return c.TLSKey }},

	{Key: "modules_off", Group: "Modules", Label: "Switched-off modules", Kind: "list",
		Help:  "Modules nobody can use on this server (e.g. autoop autovoice), space-separated. Running ones stop at once.",
		get:   func(c *Config) string { return strings.Join(c.ModulesOff, " ") },
		reset: func(c *Config) { c.ModulesOff = nil }},
	{Key: "password_login", Group: "Sign-in", Label: "Password sign-in", Kind: "bool",
		Help: "Let users sign in to the web app with a password. Turn off to use single sign-on only (set it up first). IRC clients still attach with client passwords.",
		get:  func(c *Config) string { return fmtBool(c.Auth.PasswordLogin) }},
	{Key: "session_ttl", Group: "Sign-in", Label: "Session length", Kind: "duration",
		Help: "How long a web sign-in lasts, e.g. 720h.",
		get:  func(c *Config) string { return fmtDur(c.Auth.SessionTTL) }},
	{Key: "cookie_secure", Group: "Sign-in", Label: "Secure cookies", Kind: "bool",
		Help: "Only send the session cookie over HTTPS. Turn off only for plain-HTTP development.",
		get:  func(c *Config) string { return fmtBool(c.Auth.CookieSecure) }},
	{Key: "oidc_issuer", Group: "Single sign-on", Label: "OIDC issuer", Kind: "string", Restart: true,
		Help: "Your provider's issuer URL, e.g. https://id.example.org/oidc. Empty turns single sign-on off.",
		get:  func(c *Config) string { return c.Auth.OIDCIssuer }},
	{Key: "oidc_client_id", Group: "Single sign-on", Label: "Client ID", Kind: "string", Restart: true,
		get: func(c *Config) string { return c.Auth.OIDCClientID }},
	{Key: "oidc_client_secret", Group: "Single sign-on", Label: "Client secret", Kind: "secret", Restart: true, Env: "EVRD_OIDC_CLIENT_SECRET",
		get: func(c *Config) string { return c.Auth.OIDCClientSecret }},
	{Key: "oidc_label", Group: "Single sign-on", Label: "Button label", Kind: "string",
		Help: "Shown on the sign-in button: “Continue with …”.",
		get:  func(c *Config) string { return c.Auth.OIDCLabel }},
	{Key: "oidc_auto_create", Group: "Single sign-on", Label: "Create accounts on first sign-in", Kind: "bool",
		get: func(c *Config) string { return fmtBool(c.Auth.OIDCAutoCreate) }},
	{Key: "oidc_link_existing", Group: "Single sign-on", Label: "Link existing accounts by username", Kind: "bool",
		Help: "Only if your provider's usernames can be trusted to match evrd's.",
		get:  func(c *Config) string { return fmtBool(c.Auth.OIDCLinkExisting) }},
	{Key: "oidc_admin_role", Group: "Single sign-on", Label: "Admin role", Kind: "string",
		Help: "A provider role that makes the user an evrd admin. Empty: admins are set in evrd.",
		get:  func(c *Config) string { return c.Auth.OIDCAdminRole }},

	{Key: "otel_endpoint", Group: "Metrics", Label: "OTLP endpoint", Kind: "string", Restart: true, Env: "OTEL_EXPORTER_OTLP_METRICS_ENDPOINT",
		Help: "Where metrics are sent, e.g. http://victoriametrics:8428/opentelemetry/v1/metrics. Empty turns export off.",
		get:  func(c *Config) string { return c.Metrics.OTLPEndpoint }},
	{Key: "otel_interval", Group: "Metrics", Label: "Export interval", Kind: "duration", Restart: true,
		get: func(c *Config) string { return fmtDur(c.Metrics.Interval) }},
	{Key: "metrics_url", Group: "Metrics", Label: "Query URL", Kind: "string", Env: "EVRD_METRICS_URL",
		Help: "VictoriaMetrics' (PromQL) URL the dashboard charts read from.",
		get:  func(c *Config) string { return c.Metrics.QueryURL }},
}

// SpecFor returns the spec for an editable key.
func SpecFor(key string) (Spec, bool) {
	for _, s := range Specs {
		if s.Key == key {
			return s, true
		}
	}
	return Spec{}, false
}

// Value is a setting's current value in directive syntax.
func (c *Config) Value(key string) string {
	if s, ok := SpecFor(key); ok {
		return s.get(c)
	}
	return ""
}

// InFile reports whether the config file set key.
func (c *Config) InFile(key string) bool { return c.fromFile[key] }

// clone copies c deeply enough to apply settings to the copy.
func (c *Config) clone() *Config {
	n := *c
	n.Listen = slices.Clone(c.Listen)
	n.Outbound.BindHosts = slices.Clone(c.Outbound.BindHosts)
	n.Outbound.Bind = slices.Clone(c.Outbound.Bind)
	n.Secrets.Old = slices.Clone(c.Secrets.Old)
	n.TLS.ACMEHosts = slices.Clone(c.TLS.ACMEHosts)
	n.fromFile = c.fromFile
	return &n
}

// WithSettings returns base (the file, without env) with the database's
// settings and then the environment applied.
func (base *Config) WithSettings(values map[string]string) (*Config, error) {
	c := base.clone()
	for _, s := range Specs {
		v, ok := values[s.Key]
		if !ok {
			continue
		}
		if s.reset != nil {
			s.reset(c)
		}
		if strings.TrimSpace(v) == "" && (s.Kind == "list" || s.Kind == "string" || s.Kind == "secret") {
			c.setEmpty(s.Key)
			continue
		}
		if err := c.set(s.Key, v); err != nil {
			return nil, fmt.Errorf("%s: %w", s.Key, err)
		}
	}
	if err := c.ApplyEnv(); err != nil {
		return nil, err
	}
	return c, nil
}

// setEmpty clears a string setting (set() can't be given "").
func (c *Config) setEmpty(key string) {
	switch key {
	case "irc_host":
		c.IRCHost = ""
	case "public_url":
		c.PublicURL = ""
	case "oidc_issuer":
		c.Auth.OIDCIssuer = ""
	case "oidc_client_id":
		c.Auth.OIDCClientID = ""
	case "oidc_client_secret":
		c.Auth.OIDCClientSecret = ""
	case "oidc_label":
		c.Auth.OIDCLabel = ""
	case "oidc_admin_role":
		c.Auth.OIDCAdminRole = ""
	case "otel_endpoint":
		c.Metrics.OTLPEndpoint = ""
	case "metrics_url":
		c.Metrics.QueryURL = ""
	case "acme_email":
		c.TLS.ACMEEmail = ""
	case "acme_ca":
		c.TLS.ACMECA = ""
	case "tls_cert":
		c.TLSCert = ""
	case "tls_key":
		c.TLSKey = ""
	}
}

// Check validates a value for an editable key.
func Check(key, value string) error {
	s, ok := SpecFor(key)
	if !ok {
		return fmt.Errorf("%s can't be changed from the web app", key)
	}
	if strings.ContainsAny(value, "\r\n") {
		return fmt.Errorf("%s: one line only", key)
	}
	// A URL's password is shown masked (user:xxxxx@): saving the mask back
	// would replace the real one.
	if u, err := url.Parse(strings.TrimSpace(value)); err == nil && u.User != nil {
		if p, ok := u.User.Password(); ok && p == "xxxxx" {
			return fmt.Errorf("%s: type the password again (it's shown masked)", s.Label)
		}
	}
	if strings.TrimSpace(value) == "" && (s.Kind == "list" || s.Kind == "string" || s.Kind == "secret") {
		return nil
	}
	c := Default()
	if s.reset != nil {
		s.reset(c)
	}
	if err := c.set(key, value); err != nil {
		return fmt.Errorf("%s: %w", s.Label, err)
	}
	if s.Kind == "int" {
		if n, _ := strconv.Atoi(strings.TrimSpace(value)); n < 0 {
			return fmt.Errorf("%s: 0 or more", s.Label)
		}
	}
	return nil
}

// managedHeader marks the block evrd appends to the file.
const managedHeader = "# --- set from the evrd admin UI (Admin → Settings) ---"

// RewriteFile writes settings into a config file, keeping comments and
// everything else: a set key replaces its directive line(s) in place (or is
// appended under a marked block), a nil value removes the directive. The
// file is replaced atomically.
func RewriteFile(path string, values map[string]*string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	var lines []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	f.Close()
	if err := sc.Err(); err != nil {
		return err
	}

	done := map[string]bool{}
	var out []string
	for _, l := range lines {
		t := strings.TrimSpace(l)
		key, _, _ := strings.Cut(t, " ")
		v, managed := values[key]
		if t == "" || strings.HasPrefix(t, "#") || !managed {
			out = append(out, l)
			continue
		}
		if v == nil || done[key] {
			continue // removed, or a repeat (bindhost) folded into one line
		}
		_, rest, _ := strings.Cut(t, " ")
		_, comment := splitComment(rest)
		out = append(out, key+" "+*v+comment) // keeps a trailing # comment
		done[key] = true
	}
	var add []string
	for _, s := range Specs { // stable order
		if v, ok := values[s.Key]; ok && v != nil && !done[s.Key] {
			add = append(add, s.Key+" "+*v)
		}
	}
	if len(add) > 0 {
		if !slices.Contains(out, managedHeader) {
			if len(out) > 0 && strings.TrimSpace(out[len(out)-1]) != "" {
				out = append(out, "")
			}
			out = append(out, managedHeader)
		}
		out = append(out, add...)
	}

	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".evrd.conf.*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	w := bufio.NewWriter(tmp)
	for _, l := range out {
		w.WriteString(l + "\n")
	}
	if err := w.Flush(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(info.Mode()); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
