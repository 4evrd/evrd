// Package outbound opens evrd's connections to IRC networks: it paces them,
// and picks the source address each user connects from.
//
// Pacing. IRC networks throttle bursts of connections from one address. When
// a node takes over another's users, or a network comes back after a split,
// every affected user would otherwise reconnect at once; the pacer spaces
// connects to each server host to at most Rate per second, queueing the rest.
//
// Source addresses. Every user on a node shares the node's address unless
// told otherwise, and large networks cap connections per address. A node can
// be given a list of addresses to spread users over (each user always gets
// the same one, so the network sees a stable address per user), and/or an
// IPv6 prefix from which each user gets an address of their own. Using a
// prefix needs the host to accept traffic for the whole prefix, e.g. on Linux:
//
//	ip -6 route add local 2001:db8:42::/64 dev lo
//
// evrd sets IPV6_FREEBIND on those sockets so it can bind any address in it.
package outbound

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// Config is a node's outbound policy.
type Config struct {
	// Rate is the most connects per second to one server host; 0 means
	// unlimited.
	Rate float64
	// Bind lists source addresses (IPv4 and/or IPv6) to spread users over.
	Bind []netip.Addr
	// Prefix6 gives each user their own IPv6 source address from it. It wins
	// over Bind for IPv6 destinations.
	Prefix6 netip.Prefix
	// IPv4First tries a server's IPv4 addresses before its IPv6 ones
	// (prefer_ipv6 no); IPv6 goes first otherwise.
	IPv4First bool
}

// Dialer opens upstream connections according to a Config.
type Dialer struct {
	cfg       Config
	v4        []netip.Addr
	v6        []netip.Addr
	pace      pacer
	ipv4First atomic.Bool // cfg.IPv4First, changed live (SetIPv4First)
	// resolve and dial are replaceable for tests.
	resolve func(ctx context.Context, host string) ([]netip.Addr, error)
	dial    func(ctx context.Context, local, remote netip.AddrPort, freebind bool) (net.Conn, error)
}

// New returns a Dialer for cfg.
func New(cfg Config) (*Dialer, error) {
	d := &Dialer{cfg: cfg, pace: pacer{next: map[string]time.Time{}}, resolve: resolve, dial: dialFrom}
	d.ipv4First.Store(cfg.IPv4First)
	if cfg.Rate > 0 {
		d.pace.interval = time.Duration(float64(time.Second) / cfg.Rate)
	}
	for _, a := range cfg.Bind {
		a = a.Unmap()
		if a.Is4() {
			d.v4 = append(d.v4, a)
		} else {
			d.v6 = append(d.v6, a)
		}
	}
	if cfg.Prefix6.IsValid() {
		if !cfg.Prefix6.Addr().Is6() || cfg.Prefix6.Bits() > 120 {
			return nil, fmt.Errorf("outbound: %s isn't an IPv6 prefix with room for users (want /120 or shorter)", cfg.Prefix6)
		}
	}
	return d, nil
}

// Dial connects userID to host:port, after waiting its turn for host.
func (d *Dialer) Dial(ctx context.Context, userID int64, host string, port int) (net.Conn, error) {
	if err := d.pace.wait(ctx, host); err != nil {
		return nil, err
	}
	addrs, err := d.resolve(ctx, host)
	if err != nil {
		return nil, err
	}
	var lastErr error
	for _, remote := range d.order(addrs) {
		local, freebind := d.Source(userID, remote)
		conn, err := d.dial(ctx, netip.AddrPortFrom(local, 0), netip.AddrPortFrom(remote, uint16(port)), freebind)
		if err == nil {
			return conn, nil
		}
		lastErr = err
		if ctx.Err() != nil {
			break
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("outbound: no addresses for %s", host)
	}
	return nil, lastErr
}

// DialFrom is Dial from a chosen local address: bind is an IP or a host name
// for one (a "bind host"). Destinations of the address family bind has are
// used; an empty bind is plain Dial.
func (d *Dialer) DialFrom(ctx context.Context, userID int64, bind, host string, port int) (net.Conn, error) {
	if bind == "" {
		return d.Dial(ctx, userID, host, port)
	}
	locals, err := d.resolve(ctx, bind)
	if err != nil {
		return nil, fmt.Errorf("bind host %s: %w", bind, err)
	}
	if err := d.pace.wait(ctx, host); err != nil {
		return nil, err
	}
	addrs, err := d.resolve(ctx, host)
	if err != nil {
		return nil, err
	}
	var lastErr error
	for _, remote := range d.order(addrs) {
		var local netip.Addr
		for _, l := range locals {
			if l = l.Unmap(); l.Is4() == remote.Is4() {
				local = l
				break
			}
		}
		if !local.IsValid() {
			continue
		}
		// An IPv6 bind host may come from a routed prefix with only a local
		// route for it (one address per user, nothing on the interface), as
		// upstream_prefix6's addresses do: bind it the same way.
		freebind := canFreebind && local.Is6()
		conn, err := d.dial(ctx, netip.AddrPortFrom(local, 0), netip.AddrPortFrom(remote, uint16(port)), freebind)
		if err == nil {
			return conn, nil
		}
		lastErr = err
		if ctx.Err() != nil {
			break
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("bind host %s has no address of the same family as %s", bind, host)
	}
	return nil, lastErr
}

// SetRate changes the most connects per second to one host (0: unlimited).
func (d *Dialer) SetRate(rate float64) {
	d.pace.mu.Lock()
	d.pace.interval = 0
	if rate > 0 {
		d.pace.interval = time.Duration(float64(time.Second) / rate)
	}
	d.pace.mu.Unlock()
}

// SetIPv4First changes whether IPv4 addresses are tried before IPv6 ones.
func (d *Dialer) SetIPv4First(on bool) { d.ipv4First.Store(on) }

// order puts a server's IPv6 addresses first, or its IPv4 ones when
// IPv4First is set or the only source addresses of our own are IPv4 binds
// (so users keep them), keeping the resolver's order within each family.
func (d *Dialer) order(addrs []netip.Addr) []netip.Addr {
	only4 := len(d.v4) > 0 && len(d.v6) == 0 && !d.cfg.Prefix6.IsValid()
	prefer4 := d.ipv4First.Load() || only4
	prefer6 := !prefer4
	var first, rest []netip.Addr
	for _, a := range addrs {
		a = a.Unmap()
		if (prefer6 && a.Is6()) || (prefer4 && a.Is4()) {
			first = append(first, a)
		} else {
			rest = append(rest, a)
		}
	}
	return append(first, rest...)
}

// Source is the address a user connects to remote from (the zero Addr: let
// the system choose), and whether it comes from the prefix (needs freebind).
func (d *Dialer) Source(userID int64, remote netip.Addr) (netip.Addr, bool) {
	h := userHash(userID)
	if remote.Is6() {
		if d.cfg.Prefix6.IsValid() {
			return prefixAddr(d.cfg.Prefix6, h), true
		}
		if len(d.v6) > 0 {
			return d.v6[h%uint64(len(d.v6))], false
		}
		return netip.Addr{}, false
	}
	if len(d.v4) > 0 {
		return d.v4[h%uint64(len(d.v4))], false
	}
	return netip.Addr{}, false
}

func userHash(userID int64) uint64 {
	sum := sha256.Sum256([]byte("evrd-user:" + strconv.FormatInt(userID, 10)))
	return binary.BigEndian.Uint64(sum[:8])
}

// prefixAddr fills a prefix's host bits from h. The all-zero host part is
// avoided (it's the subnet-router anycast address).
func prefixAddr(p netip.Prefix, h uint64) netip.Addr {
	net16 := p.Masked().Addr().As16()
	hi, lo := binary.BigEndian.Uint64(net16[:8]), binary.BigEndian.Uint64(net16[8:])
	hostBits := 128 - p.Bits()
	low := func(n int) uint64 { // a mask of the n low bits
		if n >= 64 {
			return ^uint64(0)
		}
		return 1<<n - 1
	}
	if hostBits <= 64 {
		lo |= h & low(hostBits)
	} else {
		lo = h
		hi |= (h * 0x9e3779b97f4a7c15) & low(hostBits-64)
	}
	if lo == binary.BigEndian.Uint64(net16[8:]) && hostBits <= 64 {
		lo |= 1
	}
	var out [16]byte
	binary.BigEndian.PutUint64(out[:8], hi)
	binary.BigEndian.PutUint64(out[8:], lo)
	return netip.AddrFrom16(out)
}

func resolve(ctx context.Context, host string) ([]netip.Addr, error) {
	if a, err := netip.ParseAddr(host); err == nil {
		return []netip.Addr{a}, nil
	}
	ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil, err
	}
	return ips, nil
}

func dialFrom(ctx context.Context, local, remote netip.AddrPort, freebind bool) (net.Conn, error) {
	d := net.Dialer{}
	if local.Addr().IsValid() {
		d.LocalAddr = &net.TCPAddr{IP: local.Addr().AsSlice()}
	}
	if freebind {
		d.Control = freebindControl
	}
	return d.DialContext(ctx, "tcp", remote.String())
}

// pacer spaces connects to each host by interval.
type pacer struct {
	interval time.Duration
	mu       sync.Mutex
	next     map[string]time.Time // host -> earliest next connect
}

func (p *pacer) wait(ctx context.Context, host string) error {
	p.mu.Lock()
	if p.interval <= 0 {
		p.mu.Unlock()
		return nil
	}
	now := time.Now()
	at := p.next[host]
	if at.Before(now) {
		at = now
	}
	p.next[host] = at.Add(p.interval)
	// Forget hosts that have been quiet a while.
	if len(p.next) > 1024 {
		for h, t := range p.next {
			if t.Before(now) {
				delete(p.next, h)
			}
		}
	}
	p.mu.Unlock()
	if d := time.Until(at); d > 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(d):
		}
	}
	return nil
}
