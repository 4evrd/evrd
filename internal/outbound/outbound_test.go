package outbound

import (
	"context"
	"net"
	"net/netip"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestPrefixGivesEachUserAStableAddressInThePrefix(t *testing.T) {
	for _, pfx := range []string{"2001:db8:42::/64", "2001:db8:42:a000::/52", "2001:db8::/32", "2001:db8::ff00/120"} {
		p := netip.MustParsePrefix(pfx)
		d, err := New(Config{Prefix6: p})
		if err != nil {
			t.Fatal(err)
		}
		seen := map[netip.Addr]int64{}
		remote := netip.MustParseAddr("2001:db8:ffff::1")
		for uid := int64(1); uid <= 200; uid++ {
			a, freebind := d.Source(uid, remote)
			if !p.Contains(a) || !freebind {
				t.Fatalf("%s: user %d got %s (freebind %v)", pfx, uid, a, freebind)
			}
			if a == p.Masked().Addr() {
				t.Fatalf("%s: user %d got the subnet-router address", pfx, uid)
			}
			if again, _ := d.Source(uid, remote); again != a {
				t.Fatalf("%s: user %d's address changed", pfx, uid)
			}
			if other, dup := seen[a]; dup && p.Bits() <= 64 {
				t.Fatalf("%s: users %d and %d share %s", pfx, other, uid, a)
			}
			seen[a] = uid
		}
	}
	if _, err := New(Config{Prefix6: netip.MustParsePrefix("10.0.0.0/8")}); err == nil {
		t.Fatal("an IPv4 prefix was accepted")
	}
}

func TestBindListByFamily(t *testing.T) {
	d, _ := New(Config{Bind: []netip.Addr{
		netip.MustParseAddr("192.0.2.1"), netip.MustParseAddr("192.0.2.2"), netip.MustParseAddr("2001:db8::1"),
	}})
	v4, v6 := netip.MustParseAddr("198.51.100.7"), netip.MustParseAddr("2001:db8:ffff::1")
	used := map[netip.Addr]bool{}
	for uid := int64(1); uid <= 50; uid++ {
		a, _ := d.Source(uid, v4)
		if !a.Is4() {
			t.Fatalf("IPv4 destination from %s", a)
		}
		used[a] = true
		if a6, _ := d.Source(uid, v6); a6 != netip.MustParseAddr("2001:db8::1") {
			t.Fatalf("IPv6 destination from %s", a6)
		}
	}
	if len(used) != 2 {
		t.Fatalf("users weren't spread over both IPv4 addresses: %v", used)
	}
	none, _ := New(Config{})
	if a, _ := none.Source(1, v4); a.IsValid() {
		t.Fatal("no binds should let the system choose")
	}
}

func TestOrderPrefersFamiliesWeHaveSourcesFor(t *testing.T) {
	addrs := []netip.Addr{netip.MustParseAddr("192.0.2.9"), netip.MustParseAddr("2001:db8::9")}
	d6, _ := New(Config{Prefix6: netip.MustParsePrefix("2001:db8:42::/64")})
	if got := d6.order(addrs); !got[0].Is6() {
		t.Fatalf("with a v6 prefix: %v", got)
	}
	d4, _ := New(Config{Bind: []netip.Addr{netip.MustParseAddr("192.0.2.1")}})
	if got := d4.order([]netip.Addr{addrs[1], addrs[0]}); !got[0].Is4() {
		t.Fatalf("with only v4 binds: %v", got)
	}
}

func TestOrderIPv6FirstUnlessToldOtherwise(t *testing.T) {
	a4, b4 := netip.MustParseAddr("192.0.2.9"), netip.MustParseAddr("192.0.2.10")
	a6, b6 := netip.MustParseAddr("2001:db8::9"), netip.MustParseAddr("2001:db8::10")
	addrs := []netip.Addr{a4, a6, b4, b6}
	d, _ := New(Config{})
	if got := d.order(addrs); !slices.Equal(got, []netip.Addr{a6, b6, a4, b4}) {
		t.Fatalf("no sources of our own: %v", got)
	}
	d.SetIPv4First(true)
	if got := d.order(addrs); !slices.Equal(got, []netip.Addr{a4, b4, a6, b6}) {
		t.Fatalf("IPv4 first: %v", got)
	}
	p, _ := New(Config{Prefix6: netip.MustParsePrefix("2001:db8:42::/64"), IPv4First: true})
	if got := p.order(addrs); !got[0].Is4() {
		t.Fatalf("IPv4 first wins over a v6 prefix: %v", got)
	}
}

func TestPacingSpacesConnectsPerHost(t *testing.T) {
	d, _ := New(Config{Rate: 20}) // one connect per 50ms per host
	var mu sync.Mutex
	var times []time.Time
	d.resolve = func(context.Context, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("192.0.2.1")}, nil
	}
	d.dial = func(context.Context, netip.AddrPort, netip.AddrPort, bool) (net.Conn, error) {
		mu.Lock()
		times = append(times, time.Now())
		mu.Unlock()
		return nil, nil
	}
	start := time.Now()
	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); d.Dial(context.Background(), int64(i), "irc.example.org", 6697) }()
	}
	// A different host isn't held up by that queue.
	other := time.Now()
	d.Dial(context.Background(), 99, "irc.other.org", 6697)
	if time.Since(other) > 30*time.Millisecond {
		t.Fatal("another host waited in the first host's queue")
	}
	wg.Wait()
	if el := time.Since(start); el < 240*time.Millisecond {
		t.Fatalf("6 connects to one host took %v — not paced", el)
	}
	// Cancelling while queued gives up.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for i := 0; i < 3; i++ {
		d.Dial(context.Background(), 1, "irc.example.org", 6697)
	}
	if _, err := d.Dial(ctx, 1, "irc.example.org", 6697); err == nil {
		t.Fatal("a cancelled wait still dialled")
	}
}

func TestDialsFromTheChosenAddress(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	got := make(chan string, 1)
	go func() {
		c, err := ln.Accept()
		if err == nil {
			got <- c.RemoteAddr().(*net.TCPAddr).IP.String()
			c.Close()
		}
	}()
	d, _ := New(Config{Bind: []netip.Addr{netip.MustParseAddr("127.0.0.1")}})
	port := ln.Addr().(*net.TCPAddr).Port
	c, err := d.Dial(context.Background(), 1, "127.0.0.1", port)
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
	if ip := <-got; ip != "127.0.0.1" {
		t.Fatalf("connected from %s", ip)
	}
}

// TestPrefixOnLinux needs a host that routes a whole prefix locally, e.g.
//
//	ip -6 route add local 2001:db8:42::/64 dev lo
//	EVRD_TEST_PREFIX6=2001:db8:42::/64 go test ./internal/outbound
func TestPrefixOnLinux(t *testing.T) {
	pfx := os.Getenv("EVRD_TEST_PREFIX6")
	if pfx == "" {
		t.Skip("set EVRD_TEST_PREFIX6 to a locally routed prefix")
	}
	ln, err := net.Listen("tcp", "[::1]:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	got := make(chan string, 2)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			got <- c.RemoteAddr().(*net.TCPAddr).IP.String()
			c.Close()
		}
	}()
	p := netip.MustParsePrefix(pfx)
	d, err := New(Config{Prefix6: p})
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	for _, uid := range []int64{1, 2} {
		c, err := d.Dial(context.Background(), uid, "::1", port)
		if err != nil {
			t.Fatalf("user %d: %v", uid, err)
		}
		c.Close()
		from := netip.MustParseAddr(<-got)
		want, _ := d.Source(uid, netip.MustParseAddr("::1"))
		if from != want || !p.Contains(from) {
			t.Fatalf("user %d connected from %s, want %s", uid, from, want)
		}
		t.Logf("user %d connected from %s", uid, from)
	}
}

func TestDialFromBindHost(t *testing.T) {
	d, _ := New(Config{Bind: []netip.Addr{netip.MustParseAddr("192.0.2.200")}})
	d.resolve = func(_ context.Context, host string) ([]netip.Addr, error) {
		switch host {
		case "vhost.example.org":
			return []netip.Addr{netip.MustParseAddr("192.0.2.5")}, nil
		default:
			return []netip.Addr{netip.MustParseAddr("2001:db8::1"), netip.MustParseAddr("198.51.100.1")}, nil
		}
	}
	var local, remote netip.AddrPort
	d.dial = func(_ context.Context, l, r netip.AddrPort, _ bool) (net.Conn, error) {
		local, remote = l, r
		a, _ := net.Pipe()
		return a, nil
	}
	conn, err := d.DialFrom(context.Background(), 1, "vhost.example.org", "irc.example.org", 6697)
	if err != nil {
		t.Fatal(err)
	}
	conn.Close()
	// the bind host (not the node's bind list) and a destination of its family
	if local.Addr().String() != "192.0.2.5" || remote.String() != "198.51.100.1:6697" {
		t.Fatalf("dialed %s -> %s", local, remote)
	}

	d.resolve = func(_ context.Context, host string) ([]netip.Addr, error) {
		if host == "v6only" {
			return []netip.Addr{netip.MustParseAddr("2001:db8::5")}, nil
		}
		return []netip.Addr{netip.MustParseAddr("198.51.100.1")}, nil
	}
	if _, err := d.DialFrom(context.Background(), 1, "v6only", "irc.example.org", 6697); err == nil || !strings.Contains(err.Error(), "same family") {
		t.Fatalf("an IPv6 bind host can't reach an IPv4-only server: %v", err)
	}
}
