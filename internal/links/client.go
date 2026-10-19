package links

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	"github.com/4evrd/evrd/internal/irctls"
)

// Client is evrd's side: it talks to the holder's socket.
type Client struct {
	path string
}

// NewClient talks to the holder listening at path.
func NewClient(path string) *Client { return &Client{path: path} }

// Path is the holder's socket.
func (c *Client) Path() string { return c.path }

func (c *Client) dial(ctx context.Context) (*net.UnixConn, error) {
	d := net.Dialer{Timeout: 5 * time.Second}
	conn, err := d.DialContext(ctx, "unix", c.path)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	return conn.(*net.UnixConn), nil
}

// roundTrip sends req (with fd, if >= 0) and reads the answer line. The
// returned reader holds anything that came after it (a link's stream).
func (c *Client) roundTrip(ctx context.Context, req request, fd int) (*net.UnixConn, *bufio.Reader, response, error) {
	conn, err := c.dial(ctx)
	if err != nil {
		return nil, nil, response{}, err
	}
	return roundTripOn(ctx, conn, req, fd)
}

func roundTripOn(ctx context.Context, conn *net.UnixConn, req request, fd int) (*net.UnixConn, *bufio.Reader, response, error) {
	req.Version = Version
	b, _ := json.Marshal(req)
	b = append(b, '\n')
	var oob []byte
	if fd >= 0 {
		oob = unix.UnixRights(fd)
	}
	if dl, ok := ctx.Deadline(); ok {
		conn.SetDeadline(dl)
	} else {
		conn.SetDeadline(time.Now().Add(45 * time.Second))
	}
	if _, _, err := conn.WriteMsgUnix(b, oob, nil); err != nil {
		conn.Close()
		return nil, nil, response{}, fmt.Errorf("links: %w", err)
	}
	r := bufio.NewReaderSize(conn, 16<<10)
	line, err := r.ReadBytes('\n')
	conn.SetDeadline(time.Time{})
	if err != nil {
		conn.Close()
		return nil, nil, response{}, fmt.Errorf("links: %w", err)
	}
	var resp response
	if err := json.Unmarshal(line, &resp); err != nil {
		conn.Close()
		return nil, nil, response{}, fmt.Errorf("links: bad answer: %w", err)
	}
	return conn, r, resp, nil
}

// Open hands tcp — a connected socket to an IRC server — to the holder,
// which does the TLS handshake (if tlsOn) and keeps it. It returns the
// stream to talk to the server through. tcp is closed either way; if the
// holder can't be reached, it's returned unused with ErrUnavailable.
func (c *Client) Open(ctx context.Context, link string, tcp net.Conn, tlsOn bool, host string, trust irctls.Trust) (net.Conn, error) {
	sc, ok := tcp.(syscall.Conn)
	if !ok {
		return nil, errors.New("links: not a socket")
	}
	raw, err := sc.SyscallConn()
	if err != nil {
		return nil, err
	}
	hc, err := c.dial(ctx) // before giving the socket away
	if err != nil {
		return nil, err
	}
	var conn *net.UnixConn
	var r *bufio.Reader
	var resp response
	var rtErr error
	cerr := raw.Control(func(fd uintptr) {
		conn, r, resp, rtErr = roundTripOn(ctx, hc, request{Op: "open", Link: link, TLS: tlsOn, Host: host, Trust: trust}, int(fd))
	})
	tcp.Close() // the holder has its own copy now
	if cerr != nil {
		hc.Close()
		return nil, cerr
	}
	if rtErr != nil {
		return nil, rtErr
	}
	if !resp.OK {
		conn.Close()
		if resp.Fingerprint != "" {
			return nil, &irctls.UntrustedCertError{Fingerprint: resp.Fingerprint, Reason: resp.Error}
		}
		// The server's failure, which the holder only passes on: no "links:"
		// prefix. Holders before ErrHungUp say just "EOF".
		if resp.Error == "EOF" || resp.Error == "unexpected EOF" || resp.Error == irctls.ErrHungUp.Error() || strings.Contains(resp.Error, "connection reset") {
			return nil, irctls.ErrHungUp
		}
		return nil, errors.New(resp.Error)
	}
	return &stream{UnixConn: conn, r: r}, nil
}

// Resume attaches to a link the holder kept: the stream starts with the
// lines that arrived while detached. ErrNoLink if there isn't one.
func (c *Client) Resume(ctx context.Context, link string) (net.Conn, *Resumed, error) {
	conn, r, resp, err := c.roundTrip(ctx, request{Op: "attach", Link: link}, -1)
	if err != nil {
		return nil, nil, err
	}
	if !resp.OK {
		conn.Close()
		if resp.Error == ErrNoLink.Error() {
			return nil, nil, ErrNoLink
		}
		return nil, nil, fmt.Errorf("links: %s", resp.Error)
	}
	return &stream{UnixConn: conn, r: r}, &Resumed{
		Nick: resp.Nick, ISupport: resp.ISupport,
		ConnectedAt: time.Unix(resp.ConnectedAt, 0), Dropped: resp.Dropped,
	}, nil
}

// List is the links the holder keeps.
func (c *Client) List(ctx context.Context) ([]Info, error) {
	conn, _, resp, err := c.roundTrip(ctx, request{Op: "list"}, -1)
	if err != nil {
		return nil, err
	}
	conn.Close()
	if !resp.OK {
		return nil, fmt.Errorf("links: %s", resp.Error)
	}
	return resp.Links, nil
}

// Close ends a link (sending QUIT with quit, if not empty).
func (c *Client) Close(ctx context.Context, link, quit string) error {
	conn, _, resp, err := c.roundTrip(ctx, request{Op: "close", Link: link, Quit: quit}, -1)
	if err != nil {
		return err
	}
	conn.Close()
	if !resp.OK {
		return fmt.Errorf("links: %s", resp.Error)
	}
	return nil
}

// stream is a link's connection, reading first what the answer's reader
// already buffered.
type stream struct {
	*net.UnixConn
	r *bufio.Reader
}

func (s *stream) Read(p []byte) (int, error) { return s.r.Read(p) }
