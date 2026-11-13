package evrd

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/4evrd/evrd/internal/bouncer"
	"github.com/4evrd/evrd/internal/store"
)

func TestUsageReport(t *testing.T) {
	src := usageSource{
		users: func(context.Context) ([]store.User, error) { return make([]store.User, 3), nil },
		networks: func(context.Context) ([]store.Network, error) {
			return []store.Network{
				{UserID: 1, Enabled: true, Host: "irc.libera.chat"},
				{UserID: 1, Enabled: true, Host: "private.example", Servers: []store.Server{{Host: "private.example"}, {Host: "IRC.OFTC.NET"}}},
				{UserID: 2, Enabled: false, Host: "irc.libera.chat"},
				{UserID: 3, Enabled: false, Host: "my.own.server"},
			}, nil
		},
		live:  func() bouncer.Usage { return bouncer.Usage{ConnectedNetworks: 2, AttachedClients: 4} },
		nodes: func() int { return 2 },
		hosts: func() map[string]string {
			return map[string]string{"irc.libera.chat": "libera", "irc.oftc.net": "oftc"}
		},
		started: time.Now().Add(-90 * time.Minute),
		db:      "postgres://evrd@db/evrd", hist: "pebble:/var/lib/evrd/history",
		holder: true, sso: func() bool { return true },
	}
	r := src.report(context.Background())
	if r.Users != 3 || r.Active != 1 || r.Networks != 4 || r.Connected != 2 || r.Clients != 4 || r.Nodes != 2 ||
		r.Uptime != 90*time.Minute || r.DB != "postgres" || r.History != "pebble" || !r.Holder || !r.SSO {
		t.Fatalf("%+v", r)
	}
	if len(r.OnDirectory) != 2 || r.OnDirectory["libera"] != 2 || r.OnDirectory["oftc"] != 1 {
		t.Fatalf("on the directory: %v", r.OnDirectory)
	}

	// what can't be read stays 0
	src.users = func(context.Context) ([]store.User, error) { return nil, errors.New("down") }
	src.networks = func(context.Context) ([]store.Network, error) { return nil, errors.New("down") }
	r = src.report(context.Background())
	if r.Users != 0 || r.Networks != 0 || r.Connected != 2 {
		t.Fatalf("%+v", r)
	}
	for dsn, want := range map[string]string{
		"sqlite:evrd.db": "sqlite", "evrd.db": "sqlite", "postgresql://x/y": "postgres", "postgres://x": "postgres",
		"scylla://a,b/evrd": "scylla", "cassandra://a/evrd": "scylla", "pebble:history": "pebble",
	} {
		local := "sqlite"
		if want == "pebble" {
			local = "pebble"
		}
		if got := dsnKind(dsn, local); got != want {
			t.Errorf("%s: %s", dsn, got)
		}
	}
}
