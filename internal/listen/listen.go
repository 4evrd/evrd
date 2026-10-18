// Package listen opens evrd's TCP listeners, some of them ahead of time:
// evrd binds its ports while it's still root (ports below 1024 need that),
// then chroots and drops privileges, and its servers pick the sockets up
// here.
package listen

import (
	"fmt"
	"net"
	"strings"
	"sync"
)

var (
	mu  sync.Mutex
	pre = map[string]net.Listener{}
)

// Prebind binds addrs (host:port, or scheme://host:port) now for TCP to
// return later. Empty addresses are skipped.
func Prebind(addrs ...string) error {
	mu.Lock()
	defer mu.Unlock()
	for _, a := range addrs {
		a = Addr(a)
		if a == "" || pre[a] != nil {
			continue
		}
		ln, err := net.Listen("tcp", a)
		if err != nil {
			return fmt.Errorf("listen %s: %w", a, err)
		}
		pre[a] = ln
	}
	return nil
}

// TCP returns the listener Prebind bound for addr, or binds it now.
func TCP(addr string) (net.Listener, error) {
	addr = Addr(addr)
	mu.Lock()
	ln := pre[addr]
	delete(pre, addr)
	mu.Unlock()
	if ln != nil {
		return ln, nil
	}
	return net.Listen("tcp", addr)
}

// Addr strips a listener URL's scheme: "ircs://:6697" → ":6697".
func Addr(s string) string {
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	}
	return strings.TrimSuffix(s, "/")
}
