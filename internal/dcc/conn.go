package dcc

// The sockets: one-shot listeners, and copying between the two sides.

import (
	"context"
	"errors"
	"io"
	"math/rand/v2"
	"net"
	"net/netip"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/4evrd/evrd/internal/config"
)

// ErrTooBig is a file transfer going past the size limit.
var ErrTooBig = errors.New("the file is larger than this server relays")

// errNoPort is every port in the range being taken.
var errNoPort = errors.New("no free DCC port")

// listen opens a listener on the configured address, on a free port of
// the configured ranges (any port without one).
func listen(cfg config.DCC) (*net.TCPListener, error) {
	try := func(port int) (*net.TCPListener, error) {
		l, err := net.Listen("tcp", net.JoinHostPort(cfg.Listen, strconv.Itoa(port)))
		if err != nil {
			return nil, err
		}
		return l.(*net.TCPListener), nil
	}
	if len(cfg.Ports) == 0 {
		return try(0)
	}
	var ports []int
	for _, r := range cfg.Ports {
		for p := r.Lo; p <= r.Hi; p++ {
			ports = append(ports, p)
		}
	}
	// from a random place, so two DCCs at once don't race for one port
	start := rand.IntN(len(ports))
	for i := range ports {
		if l, err := try(ports[(start+i)%len(ports)]); err == nil {
			return l, nil
		}
	}
	return nil, errNoPort
}

func portOf(l *net.TCPListener) int { return l.Addr().(*net.TCPAddr).Port }

// acceptOne waits for the one connection a listener is for, until the
// deadline, then closes the listener. Connections from addresses allow
// refuses are closed and the wait goes on (nil allow takes anyone).
func acceptOne(l *net.TCPListener, deadline time.Time, allow func(netip.Addr) bool) (net.Conn, error) {
	defer l.Close()
	l.SetDeadline(deadline)
	for {
		c, err := l.Accept()
		if err != nil {
			return nil, err
		}
		from := c.RemoteAddr().(*net.TCPAddr).AddrPort().Addr().Unmap()
		if allow == nil || allow(from) {
			return c, nil
		}
		c.Close()
	}
}

// dial connects to the other side.
func dial(to netip.AddrPort, timeout time.Duration) (net.Conn, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	var d net.Dialer
	return d.DialContext(ctx, "tcp", to.String())
}

// pipe copies between a and b until both sides are done (each closing
// its end passes the close on; with chat, the first to end ends both),
// then closes them. idle (0: never) closes both when nothing moves for
// that long; max (0: no limit) is the most bytes taken from a, which sends
// the file. It returns the bytes a sent.
func pipe(a, b net.Conn, chat bool, idle time.Duration, max int64) (int64, error) {
	var (
		last  atomic.Int64 // when bytes last moved
		sent  atomic.Int64
		once  sync.Once
		first error
		wg    sync.WaitGroup
	)
	last.Store(time.Now().UnixNano())
	stop := func(err error) {
		once.Do(func() { first = err })
		a.Close()
		b.Close()
	}
	copyOne := func(dst, src net.Conn, limit int64, n *atomic.Int64) {
		defer wg.Done()
		buf := make([]byte, 32<<10)
		for {
			k, err := src.Read(buf)
			if k > 0 {
				last.Store(time.Now().UnixNano())
				if limit > 0 && n.Load()+int64(k) > limit {
					stop(ErrTooBig)
					return
				}
				if _, werr := dst.Write(buf[:k]); werr != nil {
					stop(werr)
					return
				}
				n.Add(int64(k))
			}
			if errors.Is(err, io.EOF) {
				if tc, ok := dst.(*net.TCPConn); ok && !chat {
					tc.CloseWrite() // the other side sees it end, and ends too
					return
				}
				stop(nil)
				return
			}
			if err != nil {
				stop(err)
				return
			}
		}
	}
	var back atomic.Int64
	wg.Add(2)
	go copyOne(b, a, max, &sent)
	go copyOne(a, b, 0, &back)
	done := make(chan struct{})
	if idle > 0 {
		go func() {
			t := time.NewTicker(min(idle/4, time.Second))
			defer t.Stop()
			for {
				select {
				case <-done:
					return
				case <-t.C:
					if time.Since(time.Unix(0, last.Load())) > idle {
						stop(errIdle)
						return
					}
				}
			}
		}()
	}
	wg.Wait()
	close(done)
	stop(nil)
	if errors.Is(first, net.ErrClosed) {
		first = nil // closed by the other copy, after an error it kept
	}
	return sent.Load(), first
}

// errIdle is a transfer that stalled.
var errIdle = errors.New("nothing moved for too long")
