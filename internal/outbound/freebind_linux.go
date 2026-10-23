package outbound

import "syscall"

// canFreebind: IPv6 bind hosts are bound with IPV6_FREEBIND.
const canFreebind = true

// ipv6Freebind is IPV6_FREEBIND (linux/in6.h); the syscall package lacks it.
const ipv6Freebind = 78

// freebindControl lets a socket bind an address the host accepts traffic for
// (a local route for the whole prefix) without it being configured on an
// interface.
func freebindControl(_, _ string, c syscall.RawConn) error {
	var serr error
	err := c.Control(func(fd uintptr) {
		serr = syscall.SetsockoptInt(int(fd), syscall.IPPROTO_IPV6, ipv6Freebind, 1)
	})
	if err != nil {
		return err
	}
	return serr
}
