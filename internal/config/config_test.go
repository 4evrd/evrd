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
