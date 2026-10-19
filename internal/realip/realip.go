// Package realip works out the address a web request really came from.
//
// X-Forwarded-For is only believed from a trusted proxy — by default only
// the local host (relayd, nginx or Traefik on the same machine). Anyone else
// could put any address in it, and evrd uses the address for each user's
// allowed hosts and to show where a session signed in from.
package realip

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// Loopback is the default: proxies on the same machine.
var Loopback = []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8"), netip.MustParsePrefix("::1/128")}

// ClientIP is r's client address. When the request comes from a trusted
// proxy, X-Forwarded-For is read from the right: each trusted proxy added
// the address it got the request from, so the first one that isn't a
// trusted proxy is the client (entries further left are whatever the client
// sent, and aren't believed). trusted nil means Loopback.
func ClientIP(r *http.Request, trusted []netip.Prefix) string {
	if trusted == nil {
		trusted = Loopback
	}
	remote := r.RemoteAddr
	if host, _, err := net.SplitHostPort(remote); err == nil {
		remote = host
	}
	if !isTrusted(remote, trusted) {
		return remote
	}
	var hops []string
	for _, h := range r.Header.Values("X-Forwarded-For") {
		for _, e := range strings.Split(h, ",") {
			if e = strings.TrimSpace(e); e != "" {
				hops = append(hops, e)
			}
		}
	}
	for i := len(hops) - 1; i >= 0; i-- {
		if !isTrusted(hops[i], trusted) {
			if _, err := netip.ParseAddr(hops[i]); err != nil {
				return remote // garbage: don't believe any of it
			}
			return hops[i]
		}
	}
	if len(hops) > 0 {
		return hops[0] // every hop a trusted proxy: the first one
	}
	return remote
}

func isTrusted(addr string, trusted []netip.Prefix) bool {
	a, err := netip.ParseAddr(addr)
	if err != nil {
		return false
	}
	a = a.Unmap()
	for _, p := range trusted {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// ParsePrefixes reads addresses and CIDR ranges ("10.0.0.0/8 192.0.2.7
// fd00::/8"), as trusted_proxies takes them.
func ParsePrefixes(fields []string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, f := range fields {
		if strings.Contains(f, "/") {
			p, err := netip.ParsePrefix(f)
			if err != nil {
				return nil, err
			}
			out = append(out, p.Masked())
			continue
		}
		a, err := netip.ParseAddr(f)
		if err != nil {
			return nil, err
		}
		out = append(out, netip.PrefixFrom(a.Unmap(), a.Unmap().BitLen()))
	}
	return out, nil
}
