package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFmtDur(t *testing.T) {
	for d, want := range map[time.Duration]string{720 * time.Hour: "720h", 90 * time.Minute: "1h30m", 10 * time.Second: "10s", 15 * time.Second: "15s", 0: "0s"} {
		if got := fmtDur(d); got != want {
			t.Errorf("%v: %q, want %q", d, got, want)
		}
	}
}

func TestWithSettingsLayers(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "evrd.conf")
	os.WriteFile(path, []byte("max_networks 3\nbindhost 192.0.2.1\nirc_host file.example\n"), 0o600)
	base, err := LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !base.InFile("max_networks") || base.InFile("max_clients") {
		t.Fatal("file directives aren't tracked")
	}
	eff, err := base.WithSettings(map[string]string{"max_networks": "7", "bindhost": "192.0.2.2 192.0.2.3", "irc_host": ""})
	if err != nil {
		t.Fatal(err)
	}
	if eff.Limits.MaxNetworks != 7 || strings.Join(eff.Outbound.BindHosts, " ") != "192.0.2.2 192.0.2.3" || eff.IRCHost != "" {
		t.Fatalf("database values win over the file: %+v %v %q", eff.Limits, eff.Outbound.BindHosts, eff.IRCHost)
	}
	if base.Limits.MaxNetworks != 3 || len(base.Outbound.BindHosts) != 1 {
		t.Fatal("the base config was modified")
	}
	t.Setenv("EVRD_METRICS_URL", "http://env:8428")
	eff, _ = base.WithSettings(map[string]string{"metrics_url": "http://db:8428"})
	if eff.Metrics.QueryURL != "http://env:8428" {
		t.Fatal("the environment wins over the database")
	}
	if eff.Value("session_ttl") != "720h" || eff.Value("password_login") != "yes" {
		t.Fatalf("values: %q %q", eff.Value("session_ttl"), eff.Value("password_login"))
	}
}

func TestCheck(t *testing.T) {
	for k, v := range map[string]string{"max_networks": "-1", "session_ttl": "soon", "password_login": "maybe", "listen": ":1", "irc_host": "a\nb"} {
		if Check(k, v) == nil {
			t.Errorf("%s %q accepted", k, v)
		}
	}
	for k, v := range map[string]string{"max_networks": "0", "session_ttl": "12h", "bindhost": "", "oidc_issuer": ""} {
		if err := Check(k, v); err != nil {
			t.Errorf("%s %q: %v", k, v, err)
		}
	}
}

func TestRewriteFileKeepsTheRest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "evrd.conf")
	orig := "# my bouncer\nlisten ircs://:6697\nmax_networks 3   \n\n# hosts\nbindhost 192.0.2.1\nbindhost 192.0.2.2\nirc_host old.example\n"
	os.WriteFile(path, []byte(orig), 0o640)
	v := func(s string) *string { return &s }
	err := RewriteFile(path, map[string]*string{
		"max_networks": v("5"), "bindhost": v("192.0.2.9"), "irc_host": nil, "session_ttl": v("12h"),
	})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	want := "# my bouncer\nlisten ircs://:6697\nmax_networks 5\n\n# hosts\nbindhost 192.0.2.9\n\n" + managedHeader + "\nsession_ttl 12h\n"
	if string(b) != want {
		t.Fatalf("got:\n%s\nwant:\n%s", b, want)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o640 {
		t.Fatalf("mode changed: %v", info.Mode())
	}
	// Again: the managed block isn't duplicated.
	RewriteFile(path, map[string]*string{"session_ttl": v("24h"), "cookie_secure": v("no")})
	b, _ = os.ReadFile(path)
	if strings.Count(string(b), managedHeader) != 1 || !strings.Contains(string(b), "session_ttl 24h\ncookie_secure no") {
		t.Fatalf("second rewrite:\n%s", b)
	}
	if _, err := LoadFile(path); err != nil {
		t.Fatalf("the rewritten file doesn't load: %v", err)
	}
}

func TestInlineComments(t *testing.T) {
	path := filepath.Join(t.TempDir(), "evrd.conf")
	os.WriteFile(path, []byte("cookie_secure no   # testing only\nirc_host #notacomment\nmax_clients 3\t# tab\n"), 0o600)
	c, err := LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.Auth.CookieSecure || c.IRCHost != "#notacomment" || c.Limits.MaxClients != 3 {
		t.Fatalf("parsed: %v %q %d", c.Auth.CookieSecure, c.IRCHost, c.Limits.MaxClients)
	}
	v := "yes"
	RewriteFile(path, map[string]*string{"cookie_secure": &v})
	b, _ := os.ReadFile(path)
	if !strings.HasPrefix(string(b), "cookie_secure yes   # testing only\n") {
		t.Fatalf("comment not kept:\n%s", b)
	}
}
