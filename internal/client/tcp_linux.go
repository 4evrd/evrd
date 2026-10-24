package client

import (
	"net"
	"time"

	"golang.org/x/sys/unix"
)

// setUserTimeout sets TCP_USER_TIMEOUT: unacknowledged data fails the
// connection after d instead of the kernel's ~15 minutes of retries.
func setUserTimeout(c *net.TCPConn, d time.Duration) {
	rc, err := c.SyscallConn()
	if err != nil {
		return
	}
	rc.Control(func(fd uintptr) {
		unix.SetsockoptInt(int(fd), unix.IPPROTO_TCP, unix.TCP_USER_TIMEOUT, int(d/time.Millisecond))
	})
}
