// Package identd answers RFC 1413 ident queries for evrd's IRC connections,
// so networks see each user's ident where a host identd can't: evrd in a
// container, or behind NAT. A server asks "<lport> , <fport>" (evrd's port
// and its own, as it sees them) and gets the ident of that connection.
package identd

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/4evrd/evrd/internal/identfile"
)

const (
	maxLine     = 100 // a query is two ports and a comma
	readTimeout = 10 * time.Second
	maxConns    = 64
)

// Lookup finds the ident of one of evrd's connections: from is who asks
// (the IRC server), lport evrd's port as that server sees it, fport the
// server's own port.
type Lookup func(from netip.Addr, lport, fport int) (string, bool)

// Server answers ident queries from a Lookup.
type Server struct {
	// Timeout is how long a client has to send its query.
	Timeout time.Duration
	// MaxConns caps the queries answered at once; more are closed.
	MaxConns int

	lookup Lookup
	log    *slog.Logger
}

// New returns a Server asking lookup.
func New(lookup Lookup, log *slog.Logger) *Server {
	return &Server{Timeout: readTimeout, MaxConns: maxConns, lookup: lookup, log: log}
}

// Serve answers on ln until ctx ends, one query per connection.
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	defer context.AfterFunc(ctx, func() { ln.Close() })()
	defer ln.Close()
	busy := make(chan struct{}, s.MaxConns)
	for {
		c, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		select {
		case busy <- struct{}{}:
			go func() {
				defer func() { <-busy }()
				s.serve(c)
			}()
		default:
			c.Close() // too many at once
		}
	}
}

func (s *Server) serve(c net.Conn) {
	defer c.Close()
	c.SetDeadline(time.Now().Add(s.Timeout))
	line, err := readLine(c)
	if err != nil {
		return
	}
	var from netip.Addr
	if ap, err := netip.ParseAddrPort(c.RemoteAddr().String()); err == nil {
		from = ap.Addr().Unmap()
	}
	reply := s.Answer(from, line)
	s.log.Debug("ident query", "from", from, "query", line, "reply", strings.TrimSpace(reply))
	io.WriteString(c, reply)
}

// readLine reads the query: up to maxLine bytes, ending in LF or CRLF (or
// the end of the stream).
func readLine(r io.Reader) (string, error) {
	line, err := bufio.NewReader(io.LimitReader(r, maxLine+1)).ReadString('\n')
	if len(line) > maxLine {
		return "", errors.New("query too long")
	}
	if err != nil && (err != io.EOF || line == "") {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

// Answer is the reply to one query line from the address from.
func (s *Server) Answer(from netip.Addr, line string) string {
	lport, fport, ok := parse(line)
	if !ok {
		return fmt.Sprintf("%d , %d : ERROR : INVALID-PORT\r\n", lport, fport)
	}
	if ident, ok := s.lookup(from, lport, fport); ok {
		if ident = identfile.Clean(ident); ident != "" {
			return fmt.Sprintf("%d , %d : USERID : UNIX : %s\r\n", lport, fport, ident)
		}
	}
	return fmt.Sprintf("%d , %d : ERROR : NO-USER\r\n", lport, fport)
}

// parse reads "<lport> , <fport>"; ok is false unless both are ports. The
// numbers are echoed back either way (0 where there's none).
func parse(line string) (lport, fport int, ok bool) {
	a, b, found := strings.Cut(line, ",")
	if !found {
		return 0, 0, false
	}
	lport, lerr := strconv.Atoi(strings.TrimSpace(a))
	fport, ferr := strconv.Atoi(strings.TrimSpace(b))
	if lerr != nil || ferr != nil {
		return 0, 0, false
	}
	if lport < 1 || lport > 65535 || fport < 1 || fport > 65535 {
		return lport, fport, false
	}
	return lport, fport, true
}
