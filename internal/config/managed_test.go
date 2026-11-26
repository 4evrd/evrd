package config

import (
	"slices"
	"strings"
	"testing"
)

func TestManagedOffByDefault(t *testing.T) {
	c := Default()
	if c.Managed.On || c.CheckManaged() != nil {
		t.Fatalf("managed: %+v", c.Managed)
	}
	if !c.Managed.TLSVerify || c.Managed.ServerName != "irc" || c.Managed.WebIRCGateway != "gateway" || !c.Managed.WebIRCResolve || !c.Managed.NickGuard {
		t.Errorf("defaults %+v", c.Managed)
	}
}

func TestLoadManagedDirectives(t *testing.T) {
	path := writeConf(t, `managed yes
managed_network Example Net
managed_server irc.example.org +6697
managed_tls_verify no
managed_fingerprint AB:CD ef01
managed_server_password serverpw
managed_webirc_password webircpw
managed_webirc_gateway boson
managed_webirc_resolve no
managed_server_name irc.example.org
managed_token api-token
managed_token_issuer https://auth.example.org/
managed_token_audience boson
managed_token_jwks https://auth.example.org/.well-known/jwks.json
managed_motd Welcome!
managed_motd Be nice.
managed_text disconnected -
managed_text welcome Hi {nick}, this is {network}
managed_away gone
managed_version Boson
managed_quit bye
managed_nick_guard no
`)
	c, err := LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	m := c.Managed
	if !m.On || m.Network != "Example Net" || m.Host != "irc.example.org" || m.Port != 6697 || !m.TLS || m.TLSVerify ||
		!slices.Equal(m.Fingerprints, []string{"abcd", "ef01"}) || m.ServerPassword != "serverpw" ||
		m.WebIRCPassword != "webircpw" || m.WebIRCGateway != "boson" || m.WebIRCResolve ||
		m.ServerName != "irc.example.org" || m.Token != "api-token" || m.TokenIssuer != "https://auth.example.org" ||
		m.TokenAudience != "boson" || m.TokenJWKS == "" || !slices.Equal(m.MOTD, []string{"Welcome!", "Be nice."}) ||
		m.AwayMessage != "gone" || m.VersionReply != "Boson" || m.QuitMessage != "bye" || m.NickGuard {
		t.Errorf("loaded %+v", m)
	}
	if err := c.CheckManaged(); err != nil {
		t.Error(err)
	}
	if got := m.Text("disconnected", "al"); got != "" {
		t.Errorf("suppressed text: %q", got)
	}
	if got := m.Text("welcome", "al"); got != "Hi al, this is Example Net" {
		t.Error(got)
	}
	if got := m.Text("reconnected", "al"); got != "Reconnected to Example Net" {
		t.Error(got)
	}
	for name, text := range ManagedTexts {
		if strings.Contains(strings.ToLower(text), "evrd") {
			t.Errorf("text %s names evrd: %q", name, text)
		}
	}
}

func TestManagedServerAndTextErrors(t *testing.T) {
	for _, bad := range []string{"managed_server", "managed_server a b c", "managed_server a +x", "managed_server a 70000",
		"managed_text nosuch hi", "managed maybe", "managed_tls_verify maybe", "managed_webirc_resolve maybe"} {
		if _, err := LoadFile(writeConf(t, bad+"\n")); err == nil {
			t.Errorf("%q loaded", bad)
		}
	}
	for val, want := range map[string]struct {
		port int
		tls  bool
	}{"h": {6667, false}, "h 7000": {7000, false}, "h +": {6697, true}, "h +7001": {7001, true}} {
		c, err := LoadFile(writeConf(t, "managed_server "+val+"\n"))
		if err != nil || c.Managed.Host != "h" || c.Managed.Port != want.port || c.Managed.TLS != want.tls {
			t.Errorf("%q: %+v %v", val, c.Managed, err)
		}
	}
}

func TestCheckManaged(t *testing.T) {
	ok := func() *Config {
		c := Default()
		c.Managed.On, c.Managed.Network, c.Managed.Host, c.Managed.Port = true, "Net", "irc", 6667
		c.Managed.Token, c.Managed.TokenIssuer, c.Managed.TokenAudience = "t", "https://auth", "boson"
		return c
	}
	if err := ok().CheckManaged(); err != nil {
		t.Fatal(err)
	}
	for name, f := range map[string]func(c *Config){
		"network":  func(c *Config) { c.Managed.Network = "" },
		"server":   func(c *Config) { c.Managed.Host = "" },
		"token":    func(c *Config) { c.Managed.Token = "" },
		"issuer":   func(c *Config) { c.Managed.TokenIssuer = "" },
		"audience": func(c *Config) { c.Managed.TokenAudience = "" },
		"name":     func(c *Config) { c.Managed.ServerName = "a b" },
		"gateway":  func(c *Config) { c.Managed.WebIRCGateway = "a b" },
	} {
		c := ok()
		f(c)
		if err := c.CheckManaged(); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	// the issuer can be the web sign-in's
	c := ok()
	c.Managed.TokenIssuer, c.Auth.OIDCIssuer = "", "https://sso.example/"
	if err := c.CheckManaged(); err != nil || c.ManagedIssuer() != "https://sso.example" {
		t.Errorf("oidc_issuer: %q %v", c.ManagedIssuer(), err)
	}
}

func TestManagedSecretsFromEnv(t *testing.T) {
	t.Setenv("EVRD_MANAGED_TOKEN", "envtok")
	t.Setenv("EVRD_MANAGED_WEBIRC_PASSWORD", "envwebirc")
	t.Setenv("EVRD_MANAGED_SERVER_PASSWORD", "envpass")
	c := Default()
	if err := c.ApplyEnv(); err != nil {
		t.Fatal(err)
	}
	if c.Managed.Token != "envtok" || c.Managed.WebIRCPassword != "envwebirc" || c.Managed.ServerPassword != "envpass" {
		t.Errorf("%+v", c.Managed)
	}
}
