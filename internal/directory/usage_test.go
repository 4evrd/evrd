package directory

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestUsageHeader(t *testing.T) {
	u := Usage{Users: 12, Active: 9, Networks: 30, Connected: 28, Clients: 5, Nodes: 1, Uptime: 26 * time.Hour,
		DB: "postgres", History: "Pebble!", Holder: true, OnDirectory: map[string]int{"oftc": 2, "libera": 5, "BAD ID": 9, "dalnet": 2}}
	want := "users=12; active=9; networks=30; connected=28; clients=5; nodes=1; uptime=93600; db=postgres; history=pebble; holder=on; sso=off; nets=libera:5,dalnet:2,oftc:2"
	if got := u.Header(); got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
	if got := (Usage{SSO: true}).Header(); !strings.Contains(got, "db=unknown") || !strings.Contains(got, "sso=on") || strings.Contains(got, "nets=") {
		t.Fatal(got)
	}
	many := Usage{OnDirectory: map[string]int{}}
	for i := range 30 {
		many.OnDirectory[string(rune('a'+i%26))+strings.Repeat("x", i/26)] = i + 1
	}
	if n := strings.Count(many.Header(), ":"); n != maxListed {
		t.Fatalf("listed %d networks", n)
	}
}

func TestUsageSharing(t *testing.T) {
	var mu sync.Mutex
	var hdr http.Header
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hdr = r.Header.Clone()
		mu.Unlock()
		io.WriteString(w, good)
	}))
	t.Cleanup(s.Close)
	old := statsHost
	statsHost = "127.0.0.1"
	t.Cleanup(func() { statsHost = old })
	ctx := context.Background()

	d := New(s.URL, &memCache{}, quiet)
	if err := d.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if hdr.Get("X-Evrd-Install") == "" || hdr.Get("X-Evrd-Usage") != "" {
		t.Fatalf("no usage set: %v", hdr)
	}
	d.SetUsage(func(context.Context) Usage { return Usage{Users: 3} })
	d.Refresh(ctx)
	if !strings.HasPrefix(hdr.Get("X-Evrd-Usage"), "users=3; ") || !strings.HasPrefix(hdr.Get("User-Agent"), "evrd/") {
		t.Fatalf("shared: %v", hdr)
	}

	// usage_stats off: nothing about the install
	d.SetSharing(false)
	d.Refresh(ctx)
	if hdr.Get("X-Evrd-Install") != "" || hdr.Get("X-Evrd-Usage") != "" || hdr.Get("User-Agent") != "evrd (+https://evrd.sh)" {
		t.Fatalf("opted out: %v", hdr)
	}
	d.SetSharing(true)
	d.Refresh(ctx)
	if hdr.Get("X-Evrd-Install") == "" {
		t.Fatal("back on")
	}

	// the directory's hosts name the public networks
	ids := d.HostIDs()
	if len(ids) == 0 {
		t.Fatal("no hosts")
	}
	for h, id := range ids {
		if h != strings.ToLower(h) || id == "" {
			t.Fatalf("%q → %q", h, id)
		}
	}
}
