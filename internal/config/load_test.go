package config

import (
	"encoding/base64"
	"encoding/hex"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func writeConf(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "evrd.conf")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadFileEveryDirective(t *testing.T) {
	path := writeConf(t, `# full config
listen ircs://:6697
listen wss://:8097
db postgres://evrd@db/evrd
history scylla://a,b/evrd
api :9000
tls_cert /etc/x.crt
tls_key /etc/x.key
tls acme
acme_email ops@example.org
acme_hosts a.example.org b.example.org
acme_hosts c.example.org
acme_ca https://ca.example/dir
https_listen :443
http_listen :80
links_socket /run/links.sock
provision_token tok
web_root /srv/web
chroot /var/evrd
user _evrd
public_url https://bnc.example.org///
irc_host irc.example.org
password_login off
session_ttl 2h
cookie_secure 0
oidc_issuer https://id.example/oidc
oidc_client_id cid
oidc_client_secret csec
oidc_label Example
oidc_auto_create no
oidc_link_existing yes
oidc_admin_role evrd-admin
node_id n1
internal_listen :8099
internal_url http://n1:8099/
internal_irc_listen :6699
internal_irc_addr n1:6699
cluster_secret cs
cluster_lease 30s
connect_rate 2.5
bindhost 192.0.2.1 192.0.2.2
bindhost host.example
settings_write_conf yes
secret_key k1
secret_key_file /etc/key
secret_key_old o1 o2
upstream_bind 192.0.2.5 2001:db8::1
upstream_prefix6 2001:db8:1::/48
otel_endpoint http://vm:8428/otlp
otel_interval 1m
metrics_url http://vm:8428/
max_networks 4
max_clients 5
backlog_lines 600
`)
	c, err := LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	check := func(name string, got, want any) {
		t.Helper()
		if got != want {
			t.Errorf("%s = %v, want %v", name, got, want)
		}
	}
	check("listen", strings.Join(c.Listen, ","), "ircs://:6697,wss://:8097")
	check("db", c.DB, "postgres://evrd@db/evrd")
	check("history", c.History, "scylla://a,b/evrd")
	check("api", c.API, ":9000")
	check("tls_cert", c.TLSCert, "/etc/x.crt")
	check("tls_key", c.TLSKey, "/etc/x.key")
	check("tls", c.TLS.Source, "acme")
	check("acme_email", c.TLS.ACMEEmail, "ops@example.org")
	check("acme_hosts", strings.Join(c.TLS.ACMEHosts, " "), "a.example.org b.example.org c.example.org")
	check("acme_ca", c.TLS.ACMECA, "https://ca.example/dir")
	check("https_listen", c.HTTPSListen, ":443")
	check("http_listen", c.HTTPListen, ":80")
	check("links_socket", c.LinksSocket, "/run/links.sock")
	check("provision_token", c.ProvisionToken, "tok")
	check("web_root", c.WebRoot, "/srv/web")
	check("chroot", c.Chroot, "/var/evrd")
	check("user", c.User, "_evrd")
	check("public_url", c.PublicURL, "https://bnc.example.org")
	check("irc_host", c.IRCHost, "irc.example.org")
	check("password_login", c.Auth.PasswordLogin, false)
	check("session_ttl", c.Auth.SessionTTL, 2*time.Hour)
	check("cookie_secure", c.Auth.CookieSecure, false)
	check("oidc_issuer", c.Auth.OIDCIssuer, "https://id.example/oidc")
	check("oidc_client_id", c.Auth.OIDCClientID, "cid")
	check("oidc_client_secret", c.Auth.OIDCClientSecret, "csec")
	check("oidc_label", c.Auth.OIDCLabel, "Example")
	check("oidc_auto_create", c.Auth.OIDCAutoCreate, false)
	check("oidc_link_existing", c.Auth.OIDCLinkExisting, true)
	check("oidc_admin_role", c.Auth.OIDCAdminRole, "evrd-admin")
	check("node_id", c.Cluster.NodeID, "n1")
	check("internal_listen", c.Cluster.InternalListen, ":8099")
	check("internal_url", c.Cluster.InternalURL, "http://n1:8099")
	check("internal_irc_listen", c.Cluster.InternalIRCListen, ":6699")
	check("internal_irc_addr", c.Cluster.InternalIRCAddr, "n1:6699")
	check("cluster_secret", c.Cluster.Secret, "cs")
	check("cluster_lease", c.Cluster.LeaseTTL, 30*time.Second)
	check("connect_rate", c.Outbound.ConnectRate, 2.5)
	check("bindhost", strings.Join(c.Outbound.BindHosts, " "), "192.0.2.1 192.0.2.2 host.example")
	check("settings_write_conf", c.WriteConf, true)
	check("secret_key", c.Secrets.Key, "k1")
	check("secret_key_file", c.Secrets.KeyFile, "/etc/key")
	check("secret_key_old", strings.Join(c.Secrets.Old, " "), "o1 o2")
	check("upstream_bind", len(c.Outbound.Bind), 2)
	check("upstream_prefix6", c.Outbound.Prefix6, netip.MustParsePrefix("2001:db8:1::/48"))
	check("otel_endpoint", c.Metrics.OTLPEndpoint, "http://vm:8428/otlp")
	check("otel_interval", c.Metrics.Interval, time.Minute)
	check("metrics_url", c.Metrics.QueryURL, "http://vm:8428")
	check("max_networks", c.Limits.MaxNetworks, 4)
	check("max_clients", c.Limits.MaxClients, 5)
	check("backlog_lines", c.Limits.BacklogLines, 600)
	check("path", c.Path, path)
	check("cluster enabled", c.Cluster.Enabled(), true)
	check("oidc enabled", c.Auth.OIDCEnabled(), true)
	if !c.InFile("connect_rate") || c.InFile("history_dir") {
		t.Error("InFile doesn't track the file's directives")
	}
}

func TestLoadFileDefaultsAndOldNames(t *testing.T) {
	c, err := LoadFile(writeConf(t, "\n# nothing but\nhistory_dir /data/h\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(c.Listen, Default().Listen) {
		t.Errorf("no listen directive: %v, want the default", c.Listen)
	}
	if c.History != "pebble:/data/h" {
		t.Errorf("history_dir: %q", c.History)
	}
	if c.DB != "sqlite:evrd.db" || c.Limits.BacklogLines != 2000 || c.Outbound.ConnectRate != 5 {
		t.Errorf("defaults not kept: %q %d %v", c.DB, c.Limits.BacklogLines, c.Outbound.ConnectRate)
	}
	if c.Cluster.Enabled() || c.Auth.OIDCEnabled() {
		t.Error("cluster or SSO on by default")
	}
}

func TestLoadFileErrors(t *testing.T) {
	if _, err := LoadFile(filepath.Join(t.TempDir(), "missing.conf")); err == nil {
		t.Error("a missing file loaded")
	}
	if _, err := Load(filepath.Join(t.TempDir(), "missing.conf")); err == nil {
		t.Error("Load: a missing file loaded")
	}
	for _, line := range []string{
		"nonsense 1", "tls magic", "password_login maybe", "session_ttl soon", "cookie_secure 2",
		"oidc_auto_create x", "oidc_link_existing x", "cluster_lease x", "connect_rate fast",
		"settings_write_conf x", "upstream_bind not-an-ip", "upstream_prefix6 192.0.2.1",
		"otel_interval x", "max_networks x", "max_clients x", "backlog_lines x",
	} {
		_, err := LoadFile(writeConf(t, "irc_host ok\n"+line+"\n"))
		if err == nil {
			t.Errorf("%q accepted", line)
			continue
		}
		key, _, _ := strings.Cut(line, " ")
		if !strings.Contains(err.Error(), ":2: "+key+":") {
			t.Errorf("%q: error %q doesn't name the line and directive", line, err)
		}
	}
}

func TestLoadAppliesEnv(t *testing.T) {
	path := writeConf(t, "db sqlite:file.db\nupstream_bind 192.0.2.1\n")
	env := map[string]string{
		"EVRD_DB":                             "postgres://env/evrd",
		"EVRD_HISTORY":                        "scylla://env/evrd",
		"EVRD_OIDC_CLIENT_SECRET":             "oidc",
		"EVRD_CLUSTER_SECRET":                 "cluster",
		"EVRD_SECRET_KEY":                     "key",
		"EVRD_SECRET_KEY_FILE":                "/key",
		"EVRD_PROVISION_TOKEN":                "prov",
		"EVRD_LINKS_SOCKET":                   "/links",
		"EVRD_NODE_ID":                        "node",
		"EVRD_INTERNAL_URL":                   "http://node",
		"EVRD_INTERNAL_IRC_ADDR":              "node:1",
		"OTEL_EXPORTER_OTLP_METRICS_ENDPOINT": "http://otel",
		"EVRD_METRICS_URL":                    "http://vm",
		"EVRD_UPSTREAM_BIND":                  "198.51.100.1 198.51.100.2",
		"EVRD_UPSTREAM_PREFIX6":               "2001:db8::/64",
	}
	for k, v := range env {
		t.Setenv(k, v)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	got := []string{c.DB, c.History, c.Auth.OIDCClientSecret, c.Cluster.Secret, c.Secrets.Key, c.Secrets.KeyFile,
		c.ProvisionToken, c.LinksSocket, c.Cluster.NodeID, c.Cluster.InternalURL, c.Cluster.InternalIRCAddr,
		c.Metrics.OTLPEndpoint, c.Metrics.QueryURL}
	want := []string{"postgres://env/evrd", "scylla://env/evrd", "oidc", "cluster", "key", "/key", "prov", "/links",
		"node", "http://node", "node:1", "http://otel", "http://vm"}
	if !slices.Equal(got, want) {
		t.Errorf("env overrides:\n got %q\nwant %q", got, want)
	}
	if len(c.Outbound.Bind) != 2 || c.Outbound.Bind[0].String() != "198.51.100.1" {
		t.Errorf("EVRD_UPSTREAM_BIND replaces the file's list: %v", c.Outbound.Bind)
	}
	if c.Outbound.Prefix6.String() != "2001:db8::/64" {
		t.Errorf("prefix6: %v", c.Outbound.Prefix6)
	}
}

func TestApplyEnvErrors(t *testing.T) {
	t.Setenv("EVRD_UPSTREAM_BIND", "nope")
	if err := Default().ApplyEnv(); err == nil || !strings.Contains(err.Error(), "EVRD_UPSTREAM_BIND") {
		t.Errorf("bad bind: %v", err)
	}
	if _, err := Load(writeConf(t, "irc_host x\n")); err == nil {
		t.Error("Load ignores a bad environment")
	}
	if _, err := Default().WithSettings(nil); err == nil {
		t.Error("WithSettings ignores a bad environment")
	}
	t.Setenv("EVRD_UPSTREAM_BIND", "")
	t.Setenv("EVRD_UPSTREAM_PREFIX6", "nope")
	if err := Default().ApplyEnv(); err == nil || !strings.Contains(err.Error(), "EVRD_UPSTREAM_PREFIX6") {
		t.Errorf("bad prefix: %v", err)
	}
}

func TestTLSSource(t *testing.T) {
	c := Default()
	if c.TLSSource() != "selfsigned" {
		t.Errorf("default: %s", c.TLSSource())
	}
	c.TLSCert = "a"
	if c.TLSSource() != "selfsigned" {
		t.Error("a cert without a key counts as files")
	}
	c.TLSKey = "b"
	if c.TLSSource() != "files" {
		t.Errorf("cert and key: %s", c.TLSSource())
	}
	c.TLS.Source = "acme"
	if c.TLSSource() != "acme" {
		t.Errorf("explicit: %s", c.TLSSource())
	}
}

func TestCertAndACMEHosts(t *testing.T) {
	c := Default()
	c.PublicURL = "https://BNC.example.org:8443/app"
	c.IRCHost = " bnc.example.org "
	if got := c.CertHosts(); !slices.Equal(got, []string{"bnc.example.org"}) {
		t.Errorf("public_url and irc_host deduplicated: %v", got)
	}
	c.IRCHost = "irc.example.org"
	if got := c.CertHosts(); !slices.Equal(got, []string{"bnc.example.org", "irc.example.org"}) {
		t.Errorf("CertHosts: %v", got)
	}
	c.TLS.ACMEHosts = []string{"a.example.org", "A.example.org", "localhost", "127.0.0.1", "box", "x.localhost", "printer.local", ""}
	if got := c.CertHosts(); len(got) != 6 {
		t.Errorf("acme_hosts win and are deduplicated: %v", got)
	}
	if got := c.ACMEHosts(); !slices.Equal(got, []string{"a.example.org"}) {
		t.Errorf("ACMEHosts leaves out what a public CA won't sign: %v", got)
	}
}

func TestCheckSignInAndTLS(t *testing.T) {
	c := Default()
	if err := c.CheckSignIn(); err != nil {
		t.Errorf("password sign-in: %v", err)
	}
	c.Auth.PasswordLogin = false
	if err := c.CheckSignIn(); err == nil {
		t.Error("no way to sign in accepted")
	}
	c.Auth.OIDCIssuer = "https://id"
	if err := c.CheckSignIn(); err != nil {
		t.Errorf("SSO only: %v", err)
	}

	c = Default()
	if err := c.CheckTLS(); err != nil {
		t.Errorf("selfsigned: %v", err)
	}
	c.TLS.Source = "acme"
	c.IRCHost = "localhost"
	if err := c.CheckTLS(); err == nil {
		t.Error("acme with no public host accepted")
	}
	c.IRCHost = "irc.example.org"
	if err := c.CheckTLS(); err != nil {
		t.Errorf("acme: %v", err)
	}
	c.TLS.Source = "files"
	c.TLSCert = "/x.crt"
	if err := c.CheckTLS(); err == nil {
		t.Error("files without a key accepted")
	}
	c.TLSKey = "/x.key"
	if err := c.CheckTLS(); err != nil {
		t.Errorf("files: %v", err)
	}
}

func TestSecretKeys(t *testing.T) {
	raw := []byte("0123456789abcdef0123456789abcdef")
	c := Default()
	if cur, old, err := c.SecretKeys(); cur != nil || old != nil || err != nil {
		t.Errorf("no key: %v %v %v", cur, old, err)
	}
	for _, enc := range []string{
		hex.EncodeToString(raw), base64.StdEncoding.EncodeToString(raw), base64.RawStdEncoding.EncodeToString(raw),
		base64.URLEncoding.EncodeToString(raw), base64.RawURLEncoding.EncodeToString(raw), " " + hex.EncodeToString(raw) + "\n",
	} {
		c.Secrets.Key = enc
		cur, _, err := c.SecretKeys()
		if err != nil || string(cur) != string(raw) {
			t.Errorf("%q: %q %v", enc, cur, err)
		}
	}

	c = Default()
	file := filepath.Join(t.TempDir(), "key")
	os.WriteFile(file, []byte(hex.EncodeToString(raw)+"\n"), 0o600)
	c.Secrets.KeyFile = file
	c.Secrets.Old = []string{base64.StdEncoding.EncodeToString(raw), hex.EncodeToString(raw)}
	cur, old, err := c.SecretKeys()
	if err != nil || string(cur) != string(raw) || len(old) != 2 {
		t.Errorf("key file and old keys: %q %d %v", cur, len(old), err)
	}

	c.Secrets.Key = "short"
	if _, _, err := c.SecretKeys(); err == nil || !strings.HasPrefix(err.Error(), "secret_key:") {
		t.Errorf("a short key: %v", err)
	}
	c.Secrets.Key = ""
	c.Secrets.KeyFile = filepath.Join(t.TempDir(), "missing")
	if _, _, err := c.SecretKeys(); err == nil || !strings.HasPrefix(err.Error(), "secret_key_file:") {
		t.Errorf("a missing key file: %v", err)
	}
	c.Secrets.KeyFile = ""
	c.Secrets.Old = []string{"zz"}
	if _, _, err := c.SecretKeys(); err == nil || !strings.HasPrefix(err.Error(), "secret_key_old:") {
		t.Errorf("a bad old key: %v", err)
	}
}

func TestWithSettingsClearsEveryStringSetting(t *testing.T) {
	base := Default()
	values := map[string]string{}
	for _, s := range Specs {
		if s.Kind == "string" || s.Kind == "secret" {
			if err := base.set(s.Key, "x-"+s.Key); err != nil {
				t.Fatal(err)
			}
			values[s.Key] = " "
		}
	}
	eff, err := base.WithSettings(values)
	if err != nil {
		t.Fatal(err)
	}
	for k := range values {
		if v := eff.Value(k); v != "" {
			t.Errorf("%s not cleared: %q", k, v)
		}
		if base.Value(k) != "x-"+k {
			t.Errorf("%s: the base changed", k)
		}
	}
	if eff.Value("not_a_setting") != "" {
		t.Error("an unknown key has a value")
	}
	if _, err := base.WithSettings(map[string]string{"max_clients": "lots"}); err == nil || !strings.HasPrefix(err.Error(), "max_clients:") {
		t.Errorf("a bad value: %v", err)
	}
}

func TestEverySpecRoundTrips(t *testing.T) {
	// Each setting's displayed value is valid and loads back to itself.
	c := Default()
	c.TLS.ACMEHosts = []string{"a.example.org", "b.example.org"}
	c.Outbound.BindHosts = []string{"192.0.2.1"}
	for _, s := range Specs {
		v := s.get(c)
		if strings.TrimSpace(v) == "" {
			continue
		}
		if err := Check(s.Key, v); err != nil {
			t.Errorf("%s: its own value %q fails Check: %v", s.Key, v, err)
		}
		eff, err := c.WithSettings(map[string]string{s.Key: v})
		if err != nil || eff.Value(s.Key) != v {
			t.Errorf("%s: %q round-trips to %q (%v)", s.Key, v, eff.Value(s.Key), err)
		}
	}
}

func TestRewriteFileErrors(t *testing.T) {
	v := "1"
	if err := RewriteFile(filepath.Join(t.TempDir(), "missing"), map[string]*string{"max_clients": &v}); err == nil {
		t.Error("a missing file was rewritten")
	}
	// An empty file gets the managed block without a blank line first.
	path := writeConf(t, "")
	if err := RewriteFile(path, map[string]*string{"max_clients": &v}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if string(b) != managedHeader+"\nmax_clients 1\n" {
		t.Errorf("empty file: %q", b)
	}
}

func TestInChrootEdges(t *testing.T) {
	c := Default()
	if p, ok := c.InChroot("/var/evrd/x"); ok || p != "/var/evrd/x" {
		t.Error("no chroot: paths aren't mapped")
	}
	c.Chroot = "/var/evrd"
	if p, ok := c.InChroot("rel/x"); ok || p != "rel/x" {
		t.Error("relative paths aren't mapped")
	}
	if p, ok := c.InChroot("/var/evrd"); !ok || p != "/" {
		t.Errorf("the chroot itself: %q %v", p, ok)
	}
	if err := Default().Jail(); err != nil {
		t.Errorf("Jail without a chroot: %v", err)
	}
	c.DB = ""
	c.History = "pebble:/var/evrd/h"
	c.WebRoot = "web"
	if err := c.Jail(); err == nil || !strings.Contains(err.Error(), "web_root") {
		t.Errorf("a relative web_root: %v", err)
	}
}

func TestNetworkDirectory(t *testing.T) {
	t.Setenv("EVRD_NETWORK_DIRECTORY", "")
	if Default().NetworkDirectory != DefaultNetworkDirectory {
		t.Fatal("not on by default")
	}
	for conf, want := range map[string]string{
		"network_directory https://example.org/n.json\n": "https://example.org/n.json",
		"network_directory /etc/evrd/networks.json\n":    "/etc/evrd/networks.json",
		"network_directory off\n":                        "",
		"network_directory\n":                            "",
		"irc_host x\n":                                   DefaultNetworkDirectory,
	} {
		c, err := LoadFile(writeConf(t, conf))
		if err != nil || c.NetworkDirectory != want {
			t.Errorf("%q: %v, want %q", conf, err, want)
		}
	}
	t.Setenv("EVRD_NETWORK_DIRECTORY", "None")
	c, err := Load(writeConf(t, "network_directory https://example.org/n.json\n"))
	if err != nil || c.NetworkDirectory != "" {
		t.Errorf("env off: %v", err)
	}
	eff, err := Default().WithSettings(map[string]string{"network_directory": ""})
	if err != nil || eff.NetworkDirectory != "" || eff.Value("network_directory") != "" {
		t.Errorf("cleared in the admin UI: %v", err)
	}
}
