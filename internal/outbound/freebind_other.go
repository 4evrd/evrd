//go:build !linux

package outbound

import (
	"errors"
	"syscall"
)

// canFreebind: without IPV6_FREEBIND, a bind host must be on an interface.
const canFreebind = false

// freebindControl: binding addresses from a prefix needs Linux.
func freebindControl(_, _ string, _ syscall.RawConn) error {
	return errors.New("outbound: an IPv6 prefix needs Linux (IPV6_FREEBIND)")
}
