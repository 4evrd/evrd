package client

import (
	"net"
	"time"
)

// How quickly the kernel gives up on an IRC client that vanished without
// closing its connection (a phone that lost signal): keepalive probes after
// clientKeepIdle of silence, every clientKeepInterval, clientKeepCount of
// them; and on Linux, data the client hasn't acknowledged after
// clientUserTimeout. evrd's own PING (liveness.go) covers the rest, and
// connections the holder accepts keep its defaults.
const (
	clientKeepIdle     = 30 * time.Second
	clientKeepInterval = 10 * time.Second
	clientKeepCount    = 3
	clientUserTimeout  = 60 * time.Second
)

// tuneClientConn sets c's keepalive and, where the system has it, its user
// timeout. Anything but a TCP connection (or TLS on one) is left alone.
func tuneClientConn(c net.Conn) {
	if tc, ok := c.(interface{ NetConn() net.Conn }); ok {
		c = tc.NetConn()
	}
	tcp, ok := c.(*net.TCPConn)
	if !ok {
		return
	}
	tcp.SetKeepAliveConfig(net.KeepAliveConfig{Enable: true, Idle: clientKeepIdle, Interval: clientKeepInterval, Count: clientKeepCount})
	setUserTimeout(tcp, clientUserTimeout)
}
