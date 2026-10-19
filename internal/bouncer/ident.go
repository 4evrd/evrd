package bouncer

import (
	"net/netip"

	"github.com/4evrd/evrd/internal/upstream"
)

// Ident answers an ident query (internal/identd) from this node's
// connections: from is the server asking, lport our port as it sees it,
// fport its own. A server asks while we register, from the address we
// dialed; NAT may change our port and Docker the address it asks from, so
// those narrow the choice but needn't match. Only one connection left
// answers — never a guess between users.
func (b *Bouncer) Ident(from netip.Addr, lport, fport int) (string, bool) {
	type cand struct {
		conn          *upstream.Conn
		local, remote netip.AddrPort
	}
	b.mu.Lock()
	conns := make([]*upstream.Conn, 0, len(b.conns))
	for _, e := range b.conns {
		conns = append(conns, e.conn)
	}
	b.mu.Unlock()

	var all []cand
	for _, c := range conns {
		if l, r, ok := c.Addrs(); ok && int(r.Port()) == fport {
			all = append(all, cand{c, l, r})
		}
	}
	keep := func(cs []cand, f func(cand) bool) []cand {
		var out []cand
		for _, c := range cs {
			if f(c) {
				out = append(out, c)
			}
		}
		return out
	}
	from = from.Unmap()
	cs := keep(all, func(c cand) bool { return c.remote.Addr() == from })
	if len(cs) == 0 {
		cs = all // the query came through a port mapping
	}
	if m := keep(cs, func(c cand) bool { return int(c.local.Port()) == lport }); len(m) > 0 {
		cs = m
	} else {
		cs = keep(cs, func(c cand) bool { return c.conn.Status() != upstream.StatusConnected })
	}
	if len(cs) != 1 {
		return "", false
	}
	return cs[0].conn.Ident(), true
}
