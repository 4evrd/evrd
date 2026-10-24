//go:build !linux

package client

import (
	"net"
	"time"
)

// setUserTimeout is Linux's TCP_USER_TIMEOUT; elsewhere keepalive and
// evrd's PING do.
func setUserTimeout(*net.TCPConn, time.Duration) {}
