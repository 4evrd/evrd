package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWebPushDirectives(t *testing.T) {
	d := Default().WebPush
	if !d.Enabled || d.Delay != 20*time.Second || d.MaxSubscriptions != 8 || d.Expiration != 30*24*time.Hour || d.KeyFile != DefaultVAPIDKeyFile {
		t.Fatalf("defaults %+v", d)
	}
	path := filepath.Join(t.TempDir(), "evrd.conf")
	os.WriteFile(path, []byte("public_url https://bnc.example.org/\nwebpush no\nwebpush_delay 5s\nwebpush_max_subscriptions 3\nwebpush_expiration 14d\nwebpush_vapid_key /data/vapid.pem\n"), 0o600)
	c, err := LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	w := c.WebPush
	if w.Enabled || w.Delay != 5*time.Second || w.MaxSubscriptions != 3 || w.Expiration != 14*24*time.Hour || w.KeyFile != "/data/vapid.pem" {
		t.Fatalf("loaded %+v", w)
	}
	if c.PushSubscriber() != "https://bnc.example.org" {
		t.Fatalf("subscriber %q", c.PushSubscriber())
	}
	if err := c.set("webpush_subscriber", "mailto:admin@example.org"); err != nil || c.PushSubscriber() != "mailto:admin@example.org" {
		t.Fatal(err)
	}
	for _, ok := range [][2]string{{"webpush_delay", "0"}, {"webpush_expiration", "720h"}, {"webpush", "yes"}} {
		if err := c.set(ok[0], ok[1]); err != nil {
			t.Errorf("%s %s: %v", ok[0], ok[1], err)
		}
	}
	for _, bad := range [][2]string{
		{"webpush", "maybe"}, {"webpush_delay", "-1s"}, {"webpush_delay", "soon"},
		{"webpush_max_subscriptions", "0"}, {"webpush_max_subscriptions", "x"},
		{"webpush_expiration", "1d"}, {"webpush_expiration", "xd"},
	} {
		if err := c.set(bad[0], bad[1]); err == nil {
			t.Errorf("%s %s accepted", bad[0], bad[1])
		}
	}
	t.Setenv("EVRD_WEBPUSH", "off")
	t.Setenv("EVRD_WEBPUSH_VAPID_KEY", "/run/vapid.pem")
	c = Default()
	if err := c.ApplyEnv(); err != nil || c.WebPush.Enabled || c.WebPush.KeyFile != "/run/vapid.pem" {
		t.Fatalf("env: %v %+v", err, c.WebPush)
	}
	t.Setenv("EVRD_WEBPUSH", "perhaps")
	if err := Default().ApplyEnv(); err == nil {
		t.Fatal("EVRD_WEBPUSH perhaps")
	}
}
