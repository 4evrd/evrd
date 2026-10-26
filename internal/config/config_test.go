package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestExportIntervalDefaultsTo15s(t *testing.T) {
	c := Default()
	if c.Metrics.Interval != 15*time.Second {
		t.Fatalf("default otel_interval %v", c.Metrics.Interval)
	}
	s, _ := SpecFor("otel_interval")
	if got := s.get(c); got != "15s" {
		t.Fatalf("settings page shows %q", got)
	}
}

func TestIRCSTS(t *testing.T) {
	if Default().IRCSTS != DefaultIRCSTS {
		t.Fatal("default")
	}
	for val, want := range map[string]time.Duration{"off": 0, "no": 0, "on": DefaultIRCSTS, "168h": 168 * time.Hour} {
		c := Default()
		if err := c.set("irc_sts", val); err != nil || c.IRCSTS != want {
			t.Errorf("%s: %v %v", val, c.IRCSTS, err)
		}
	}
	for _, bad := range []string{"soon", "-1h"} {
		if err := Default().set("irc_sts", bad); err == nil {
			t.Errorf("%s accepted", bad)
		}
	}
}

func TestModulesOff(t *testing.T) {
	path := filepath.Join(t.TempDir(), "evrd.conf")
	os.WriteFile(path, []byte("modules_off autoop autovoice\nmodules_off q\n"), 0o600)
	c, err := LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(c.ModulesOff, " ") != "autoop autovoice q" {
		t.Fatalf("modules_off: %q", c.ModulesOff)
	}
	s, ok := SpecFor("modules_off")
	if !ok || s.get(c) != "autoop autovoice q" {
		t.Fatalf("setting: %v %q", ok, s.get(c))
	}
	s.reset(c)
	if len(c.ModulesOff) != 0 {
		t.Fatal("reset kept them")
	}
}

func TestWebChatOrigins(t *testing.T) {
	c := Default()
	if len(c.Auth.WebChatOrigins) != 0 || c.Auth.WebChatDays != 30 || c.Auth.WebChatAllowed("https://chat.example") {
		t.Fatalf("defaults: %+v", c.Auth)
	}
	if err := c.set("web_chat_origins", "HTTPS://Chat.Example/ http://localhost:5180"); err != nil {
		t.Fatal(err)
	}
	if err := c.set("web_chat_origins", "https://chat.example"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(c.Auth.WebChatOrigins, " "); got != "https://chat.example http://localhost:5180" {
		t.Fatalf("origins %q", got)
	}
	for o, ok := range map[string]bool{
		"https://chat.example": true, "https://CHAT.example": true, "http://localhost:5180": true,
		"http://chat.example": false, "https://chat.example:443": false, "http://localhost:5181": false, "null": false, "": false,
	} {
		if c.Auth.WebChatAllowed(o) != ok {
			t.Errorf("allowed %q: %v", o, !ok)
		}
	}
	for _, bad := range []string{"chat.example", "https://chat.example/chat", "https://chat.example?x", "https://u@chat.example", "ftp://chat.example", "https://", "https://chat.example#x"} {
		if err := c.set("web_chat_origins", bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	for v, ok := range map[string]bool{"1": true, "365": true, "0": false, "366": false, "x": false} {
		if err := c.set("web_chat_days", v); (err == nil) != ok {
			t.Errorf("web_chat_days %s: %v", v, err)
		}
	}
	// From the admin UI: a list that replaces the file's, cleared by empty.
	c2, err := c.WithSettings(map[string]string{"web_chat_origins": "https://other.example", "web_chat_network": "IRCForever"})
	if err != nil || strings.Join(c2.Auth.WebChatOrigins, " ") != "https://other.example" || c2.Auth.WebChatNetwork != "IRCForever" || c2.Value("web_chat_network") != "IRCForever" {
		t.Fatalf("settings: %+v %v", c2.Auth, err)
	}
	c3, err := c2.WithSettings(map[string]string{"web_chat_origins": "", "web_chat_network": ""})
	if err != nil || len(c3.Auth.WebChatOrigins) != 0 || c3.Auth.WebChatNetwork != "" {
		t.Fatalf("cleared: %+v %v", c3.Auth, err)
	}
	if len(c.Auth.WebChatOrigins) != 2 {
		t.Fatal("WithSettings changed the base config")
	}
}
