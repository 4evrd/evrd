package realip

import (
	"net/http/httptest"
	"net/netip"
	"testing"
)

func TestClientIP(t *testing.T) {
	docker := []netip.Prefix{netip.MustParsePrefix("172.18.0.0/16")}
	for _, c := range []struct {
		name, remote string
		xff          []string
		trusted      []netip.Prefix
		want         string
	}{
		{"direct, no header", "203.0.113.5:4000", nil, nil, "203.0.113.5"},
		// the bug: anyone could claim an allowed address
		{"direct, forged header", "203.0.113.5:4000", []string{"198.51.100.1"}, nil, "203.0.113.5"},
		{"local proxy", "127.0.0.1:5000", []string{"198.51.100.1"}, nil, "198.51.100.1"},
		{"local proxy, IPv6", "[::1]:5000", []string{"2001:db8::7"}, nil, "2001:db8::7"},
		// a client behind the proxy adds its own entry first: not believed
		{"local proxy, client-forged entry", "127.0.0.1:5000", []string{"10.9.9.9, 198.51.100.1"}, nil, "198.51.100.1"},
		{"local proxy, no header", "127.0.0.1:5000", nil, nil, "127.0.0.1"},
		{"proxy chain", "172.18.0.4:80", []string{"198.51.100.1, 172.18.0.9"}, docker, "198.51.100.1"},
		{"several headers", "172.18.0.4:80", []string{"10.0.0.1", "198.51.100.1"}, docker, "198.51.100.1"},
		{"all trusted", "172.18.0.4:80", []string{"172.18.0.2"}, docker, "172.18.0.2"},
		{"garbage from a trusted proxy", "127.0.0.1:5000", []string{"not-an-ip"}, nil, "127.0.0.1"},
		{"untrusted with a custom list", "127.0.0.1:5000", []string{"198.51.100.1"}, docker, "127.0.0.1"},
		{"no port", "203.0.113.5", nil, nil, "203.0.113.5"},
	} {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = c.remote
		for _, h := range c.xff {
			r.Header.Add("X-Forwarded-For", h)
		}
		if got := ClientIP(r, c.trusted); got != c.want {
			t.Errorf("%s: got %s, want %s", c.name, got, c.want)
		}
	}
}

func TestParsePrefixes(t *testing.T) {
	got, err := ParsePrefixes([]string{"10.0.0.0/8", "192.0.2.7", "fd00::/8", "::ffff:192.0.2.9"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"10.0.0.0/8", "192.0.2.7/32", "fd00::/8", "192.0.2.9/32"}
	for i, p := range got {
		if p.String() != want[i] {
			t.Fatalf("got %v", got)
		}
	}
	for _, bad := range []string{"10.0.0.0/33", "example.org", ""} {
		if _, err := ParsePrefixes([]string{bad}); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}
