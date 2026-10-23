package outbound

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"testing"
	"time"
)

// fakeDialer records what a Dialer dials, failing for the listed remotes.
type fakeDialer struct {
	dialed []string // "local -> remote"
	fail   map[string]bool
}

func (f *fakeDialer) dial(_ context.Context, l, r netip.AddrPort, freebind bool) (net.Conn, error) {
	f.dialed = append(f.dialed, fmt.Sprintf("%s -> %s %v", l.Addr(), r, freebind))
	if f.fail[r.Addr().String()] {
		return nil, fmt.Errorf("refused by %s", r)
	}
	a, b := net.Pipe()
	b.Close()
	return a, nil
}

func resolveTo(addrs map[string][]string) func(context.Context, string) ([]netip.Addr, error) {
	return func(_ context.Context, host string) ([]netip.Addr, error) {
		list, ok := addrs[host]
		if !ok {
			return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
		}
		var out []netip.Addr
		for _, a := range list {
			out = append(out, netip.MustParseAddr(a))
		}
		return out, nil
	}
}

func TestDialTriesEachAddressInOrder(t *testing.T) {
	d, err := New(Config{Prefix6: netip.MustParsePrefix("2001:db8:42::/64")})
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeDialer{fail: map[string]bool{"2001:db8::1": true}}
	d.dial = f.dial
	d.resolve = resolveTo(map[string][]string{"irc.example.org": {"::ffff:198.51.100.1", "2001:db8::1"}})
	conn, err := d.Dial(context.Background(), 7, "irc.example.org", 6697)
	if err != nil {
		t.Fatal(err)
	}
	conn.Close()
	src, _ := d.Source(7, netip.MustParseAddr("2001:db8::1"))
	want := []string{
		src.String() + " -> [2001:db8::1]:6697 true", // IPv6 first, from the user's prefix address
		"invalid IP -> 198.51.100.1:6697 false",      // then IPv4 (unmapped), from wherever
	}
	if strings.Join(f.dialed, "\n") != strings.Join(want, "\n") {
		t.Fatalf("dialed\n%s\nwant\n%s", strings.Join(f.dialed, "\n"), strings.Join(want, "\n"))
	}
}

func TestDialErrors(t *testing.T) {
	ctx := context.Background()
	d, _ := New(Config{})
	f := &fakeDialer{fail: map[string]bool{"192.0.2.1": true, "192.0.2.2": true}}
	d.dial = f.dial
	d.resolve = resolveTo(map[string][]string{"down.example.org": {"192.0.2.1", "192.0.2.2"}, "empty.example.org": {}})

	if _, err := d.Dial(ctx, 1, "nxdomain.example.org", 6667); err == nil || !strings.Contains(err.Error(), "no such host") {
		t.Fatalf("unresolvable: %v", err)
	}
	if _, err := d.Dial(ctx, 1, "empty.example.org", 6667); err == nil || err.Error() != "outbound: no addresses for empty.example.org" {
		t.Fatalf("no addresses: %v", err)
	}
	f.dialed = nil
	if _, err := d.Dial(ctx, 1, "down.example.org", 6667); err == nil || err.Error() != "refused by 192.0.2.2:6667" {
		t.Fatalf("all refused: %v (the last error)", err)
	}
	if len(f.dialed) != 2 {
		t.Fatalf("dialed %v", f.dialed)
	}

	// A cancelled context stops after the first failure.
	cctx, cancel := context.WithCancel(ctx)
	d.dial = func(c context.Context, l, r netip.AddrPort, fb bool) (net.Conn, error) {
		cancel()
		return f.dial(c, l, r, fb)
	}
	f.dialed = nil
	if _, err := d.Dial(cctx, 1, "down.example.org", 6667); err == nil || len(f.dialed) != 1 {
		t.Fatalf("cancelled: %v, dialed %v", err, f.dialed)
	}
}

func TestDialFromErrors(t *testing.T) {
	ctx := context.Background()
	d, _ := New(Config{})
	f := &fakeDialer{fail: map[string]bool{"198.51.100.1": true, "198.51.100.2": true}}
	d.dial = f.dial
	d.resolve = resolveTo(map[string][]string{
		"vhost":   {"192.0.2.5", "2001:db8::5"},
		"irc":     {"198.51.100.1", "198.51.100.2"},
		"irc6":    {"2001:db8::99"},
		"ircboth": {"2001:db8::99", "198.51.100.3"},
	})

	if _, err := d.DialFrom(ctx, 1, "nope", "irc", 6667); err == nil || !strings.HasPrefix(err.Error(), "bind host nope: ") {
		t.Fatalf("bad bind host: %v", err)
	}
	if _, err := d.DialFrom(ctx, 1, "vhost", "nope", 6667); err == nil || !strings.Contains(err.Error(), "no such host") {
		t.Fatalf("bad server: %v", err)
	}
	if _, err := d.DialFrom(ctx, 1, "vhost", "irc", 6667); err == nil || err.Error() != "refused by 198.51.100.2:6667" {
		t.Fatalf("refused: %v", err)
	}
	// an IPv6 server from the bind host's IPv6 address (freebind on Linux)
	f.dialed = nil
	if _, err := d.DialFrom(ctx, 1, "vhost", "irc6", 6667); err != nil || f.dialed[0] != fmt.Sprintf("2001:db8::5 -> [2001:db8::99]:6667 %v", canFreebind) {
		t.Fatalf("v6: %v %v", err, f.dialed)
	}
	// an IP literal bind (resolved as itself by the real resolver)
	d.resolve = resolve
	d2, _ := New(Config{})
	d2.dial = f.dial
	f.dialed = nil
	if _, err := d2.DialFrom(ctx, 1, "192.0.2.77", "198.51.100.9", 6667); err != nil || f.dialed[0] != "192.0.2.77 -> 198.51.100.9:6667 false" {
		t.Fatalf("literal: %v %v", err, f.dialed)
	}

	// An empty bind host is a plain Dial.
	f.dialed = nil
	if _, err := d2.DialFrom(ctx, 1, "", "198.51.100.9", 6667); err != nil || f.dialed[0] != "invalid IP -> 198.51.100.9:6667 false" {
		t.Fatalf("empty bind: %v %v", err, f.dialed)
	}

	// A cancelled context: no wait in the queue, no dial.
	d3, _ := New(Config{Rate: 0.001})
	d3.dial, d3.resolve = f.dial, resolveTo(map[string][]string{"vhost": {"192.0.2.5"}, "irc": {"198.51.100.1"}})
	f.dialed = nil
	d3.DialFrom(ctx, 1, "vhost", "irc", 6667) // takes the only slot for a long while
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := d3.DialFrom(cctx, 1, "vhost", "irc", 6667); !errors.Is(err, context.Canceled) || len(f.dialed) != 1 {
		t.Fatalf("cancelled: %v, dialed %v", err, f.dialed)
	}

	// Cancelled after a failed attempt: the rest aren't tried.
	d4, _ := New(Config{})
	d4.resolve = resolveTo(map[string][]string{"vhost": {"192.0.2.5"}, "irc": {"198.51.100.1", "198.51.100.2"}})
	cctx, cancel = context.WithCancel(ctx)
	f.dialed = nil
	d4.dial = func(c context.Context, l, r netip.AddrPort, fb bool) (net.Conn, error) {
		cancel()
		return f.dial(c, l, r, fb)
	}
	if _, err := d4.DialFrom(cctx, 1, "vhost", "irc", 6667); err == nil || len(f.dialed) != 1 {
		t.Fatalf("cancelled mid-way: %v, dialed %v", err, f.dialed)
	}
}

func TestSetRate(t *testing.T) {
	d, _ := New(Config{})
	if d.pace.interval != 0 {
		t.Fatal("unlimited by default")
	}
	d.SetRate(4)
	if d.pace.interval != 250*time.Millisecond {
		t.Fatalf("interval %v", d.pace.interval)
	}
	// queued now: the second connect to a host waits ~250ms
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	d.pace.wait(ctx, "h")
	if err := d.pace.wait(ctx, "h"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("second connect: %v", err)
	}
	d.SetRate(0)
	if err := d.pace.wait(ctx, "h"); err != nil {
		t.Fatalf("unlimited again: %v", err)
	}
	d.SetRate(-1)
	if d.pace.interval != 0 {
		t.Fatal("a negative rate isn't unlimited")
	}
}

func TestPacerForgetsQuietHosts(t *testing.T) {
	p := pacer{interval: time.Nanosecond, next: map[string]time.Time{}}
	for i := 0; i < 1100; i++ {
		if err := p.wait(context.Background(), fmt.Sprintf("host%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	if n := len(p.next); n > 1025 {
		t.Fatalf("%d hosts remembered", n)
	}
}

func TestNewSortsBindsByFamily(t *testing.T) {
	d, err := New(Config{Bind: []netip.Addr{
		netip.MustParseAddr("::ffff:192.0.2.1"), netip.MustParseAddr("2001:db8::1"), netip.MustParseAddr("192.0.2.2"),
	}})
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(d.v4) != "[192.0.2.1 192.0.2.2]" || fmt.Sprint(d.v6) != "[2001:db8::1]" {
		t.Fatalf("v4 %v v6 %v", d.v4, d.v6)
	}
	if _, err := New(Config{Prefix6: netip.MustParsePrefix("2001:db8::/121")}); err == nil {
		t.Fatal("a /121 leaves too little room and was accepted")
	}
	if _, err := New(Config{Prefix6: netip.MustParsePrefix("2001:db8::/120")}); err != nil {
		t.Fatalf("/120: %v", err)
	}
}

func TestSourceStablePerUserAcrossDialers(t *testing.T) {
	cfg := Config{
		Bind:    []netip.Addr{netip.MustParseAddr("192.0.2.1"), netip.MustParseAddr("192.0.2.2"), netip.MustParseAddr("192.0.2.3")},
		Prefix6: netip.MustParsePrefix("2001:db8:42::/64"),
	}
	a, _ := New(cfg)
	b, _ := New(cfg) // another node, or after a restart
	v4, v6 := netip.MustParseAddr("198.51.100.1"), netip.MustParseAddr("2001:db8::1")
	for uid := int64(-5); uid < 100; uid++ {
		a4, _ := a.Source(uid, v4)
		b4, _ := b.Source(uid, v4)
		a6, _ := a.Source(uid, v6)
		b6, _ := b.Source(uid, v6)
		if a4 != b4 || a6 != b6 {
			t.Fatalf("user %d: %s/%s vs %s/%s", uid, a4, a6, b4, b6)
		}
	}
	// the IPv4 choice doesn't depend on the destination
	x, _ := a.Source(42, v4)
	y, _ := a.Source(42, netip.MustParseAddr("203.0.113.9"))
	if x != y {
		t.Fatalf("%s vs %s", x, y)
	}
}

func TestPrefixAddrDerivation(t *testing.T) {
	h := uint64(0x0123456789abcdef)
	for _, c := range []struct{ prefix, want string }{
		// up to /64: the host bits are the hash's low bits
		{"2001:db8:42::/64", "2001:db8:42:0:123:4567:89ab:cdef"},
		{"2001:db8:42::/96", "2001:db8:42::89ab:cdef"},
		{"2001:db8:42::ff00/120", "2001:db8:42::ffef"},
		// set bits outside the prefix are masked off first
		{"2001:db8:42::1234/112", "2001:db8:42::cdef"},
	} {
		if got := prefixAddr(netip.MustParsePrefix(c.prefix), h); got.String() != c.want {
			t.Errorf("%s: %s, want %s", c.prefix, got, c.want)
		}
	}

	// Wider than /64: the low 64 bits are the hash, and the remaining host
	// bits of the upper half come from it too.
	p := netip.MustParsePrefix("2001:db8::/48")
	a := prefixAddr(p, h)
	b16 := a.As16()
	if !p.Contains(a) || binary.BigEndian.Uint64(b16[8:]) != h {
		t.Fatalf("/48: %s", a)
	}
	if binary.BigEndian.Uint64(b16[:8])&0xffff != (h*0x9e3779b97f4a7c15)&0xffff {
		t.Fatalf("/48 upper host bits: %s", a)
	}

	// A hash that would give the all-zero host part gets ::1 instead.
	for _, pfx := range []string{"2001:db8:42::/64", "2001:db8:42::ff00/120"} {
		p := netip.MustParsePrefix(pfx)
		got := prefixAddr(p, 0)
		if got == p.Masked().Addr() || !p.Contains(got) || got != p.Masked().Addr().Next() {
			t.Errorf("%s with a zero hash: %s", pfx, got)
		}
	}
	// h=0x100 under a /120 has zero host bits too.
	p = netip.MustParsePrefix("2001:db8:42::ff00/120")
	if got := prefixAddr(p, 0x100); got.String() != "2001:db8:42::ff01" {
		t.Errorf("/120, hash 0x100: %s", got)
	}
}

func TestResolve(t *testing.T) {
	got, err := resolve(context.Background(), "2001:db8::7")
	if err != nil || len(got) != 1 || got[0].String() != "2001:db8::7" {
		t.Fatalf("literal: %v %v", got, err)
	}
	got, err = resolve(context.Background(), "localhost")
	if err != nil || len(got) == 0 {
		t.Fatalf("localhost: %v %v", got, err)
	}
	for _, a := range got {
		if !a.Unmap().IsLoopback() {
			t.Fatalf("localhost resolved to %s", a)
		}
	}
}

func TestDialFromRealSocket(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		c, err := ln.Accept()
		if err == nil {
			c.Close()
		}
	}()
	remote := netip.MustParseAddrPort(ln.Addr().String())
	// no local address: the system picks one
	c, err := dialFrom(context.Background(), netip.AddrPort{}, remote, false)
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
	// nothing listening: refused
	ln.Close()
	if _, err := dialFrom(context.Background(), netip.AddrPort{}, remote, false); err == nil {
		t.Fatal("dialled a closed port")
	}
}
