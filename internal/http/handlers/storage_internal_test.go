package handlers

import (
	"strings"
	"testing"
	"time"
)

func TestStorageDescriptions(t *testing.T) {
	for dsn, want := range map[string][2]string{
		"sqlite:/var/lib/evrd/evrd.db":              {"SQLite", "/var/lib/evrd/evrd.db"},
		"postgres://evrd:secret@db.internal:5432/x": {"PostgreSQL", "db.internal:5432"},
		"scylla://user:pw@scylla-1,scylla-2/evrd":   {"ScyllaDB", "scylla-1,scylla-2"},
		"cassandra://c1/ks":                         {"ScyllaDB", "c1"},
		"pebble:/var/lib/evrd/history":              {"Pebble", "/var/lib/evrd/history"},
		"/var/lib/evrd/history":                     {"Pebble", "/var/lib/evrd/history"},
	} {
		if k, a := dbKind(dsn), dbAddr(dsn); k != want[0] || a != want[1] {
			t.Errorf("%s: %s %s, want %s %s", dsn, k, a, want[0], want[1])
		}
		if strings.Contains(dbAddr(dsn), "secret") {
			t.Errorf("%s: the address shows the password", dsn)
		}
	}
	for d, want := range map[time.Duration]string{
		5 * time.Minute:      "5 minutes",
		3 * time.Hour:        "3 hours",
		50 * time.Hour:       "2 days",
		15 * 24 * time.Hour:  "2 weeks",
		100 * 24 * time.Hour: "14 weeks",
	} {
		if got := ago(time.Now().Add(-d)).English(); got != want {
			t.Errorf("ago(%v) = %q, want %q", d, got, want)
		}
	}
	if orDash("") != "—" || orDash("x") != "x" || pct(1, 0) != 0 || pct(1, 3) != 33 {
		t.Error("helpers")
	}
}
