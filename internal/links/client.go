package links

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strconv"
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
	if req.Version == 0 {
		req.Version = linkVersion
	}
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

// errOldHolder is how a protocol 1 holder refuses a version 2 request.
const errOldHolder = "bad request or protocol version"

// clientOp makes a protocol 2 request that's answered and done.
func (c *Client) clientOp(ctx context.Context, req request, fd int) (response, error) {
	req.Version = Version
	var conn *net.UnixConn
	var resp response
	var err error
	if fd >= 0 {
		var hc *net.UnixConn
		if hc, err = c.dial(ctx); err != nil {
			return response{}, err
		}
		conn, _, resp, err = roundTripOn(ctx, hc, req, fd)
	} else {
		conn, _, resp, err = c.roundTrip(ctx, req, -1)
	}
	if err != nil {
		return response{}, err
	}
	conn.Close()
	switch {
	case resp.OK:
		return resp, nil
	case resp.Error == errOldHolder:
		return resp, ErrNoClients
	}
	return resp, fmt.Errorf("links: %s", resp.Error)
}

// Hello asks the holder which protocol it speaks and the hash of its
// binary. A protocol 1 holder doesn't know the request, and nor did protocol
// 2 ones before hello: their hash is unknown. ErrUnavailable if it can't
// be reached.
func (c *Client) Hello(ctx context.Context) (HolderInfo, error) {
	resp, err := c.clientOp(ctx, request{Op: "hello"}, -1)
	switch {
	case errors.Is(err, ErrNoClients):
		return HolderInfo{Protocol: 1}, nil
	case err != nil && resp.Error == "unknown op":
		return HolderInfo{Protocol: 2}, nil
	case err != nil:
		return HolderInfo{}, err
	}
	return HolderInfo{Protocol: resp.Protocol, Hash: resp.Hash}, nil
}

// Listeners is the IRC listeners the holder has. ErrNoClients if it's too
// old to hold clients.
func (c *Client) Listeners(ctx context.Context) ([]Listener, error) {
	resp, err := c.clientOp(ctx, request{Op: "listeners"}, -1)
	return resp.Listeners, err
}

// Listen hands ln, a listening TCP socket, to the holder, which accepts
// IRC clients on it from now on (doing TLS if tlsOn). ln is closed either
// way.
func (c *Client) Listen(ctx context.Context, ln net.Listener, addr string, tlsOn bool) error {
	defer ln.Close()
	sc, ok := ln.(syscall.Conn)
	if !ok {
		return errors.New("links: not a socket")
	}
	raw, err := sc.SyscallConn()
	if err != nil {
		return err
	}
	var opErr error
	if cerr := raw.Control(func(fd uintptr) {
		_, opErr = c.clientOp(ctx, request{Op: "listen", Addr: addr, TLS: tlsOn}, int(fd))
	}); cerr != nil {
		return cerr
	}
	return opErr
}

// SetCert gives the holder the certificate (and key, PEM) for its TLS
// listeners.
func (c *Client) SetCert(ctx context.Context, cert, key []byte) error {
	_, err := c.clientOp(ctx, request{Op: "cert", Cert: string(cert), Key: string(key)}, -1)
	return err
}

// Clients streams the clients waiting for evrd: those waiting now, then a
// HeldClient with an empty Client, then each new one. The channel closes
// when the holder goes or ctx ends.
func (c *Client) Clients(ctx context.Context) (<-chan HeldClient, error) {
	conn, r, resp, err := c.roundTrip(ctx, request{Version: Version, Op: "clients"}, -1)
	if err != nil {
		return nil, err
	}
	if !resp.OK {
		conn.Close()
		if resp.Error == errOldHolder {
			return nil, ErrNoClients
		}
		return nil, fmt.Errorf("links: %s", resp.Error)
	}
	ch := make(chan HeldClient)
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	go func() {
		defer close(ch)
		defer stop()
		defer conn.Close()
		for {
			line, err := r.ReadBytes('\n')
			if err != nil {
				return
			}
			var hc HeldClient
			if json.Unmarshal(line, &hc) != nil {
				continue
			}
			select {
			case ch <- hc:
			case <-ctx.Done():
				return
			}
		}
	}()
	return ch, nil
}

// AttachClient takes a client the holder has: its stream (starting with
// what it sent while waiting) and the state evrd last saved for it (nil
// for a new client). ErrNoClient if it's gone.
func (c *Client) AttachClient(ctx context.Context, hc HeldClient) (*ClientConn, error) {
	conn, r, resp, err := c.roundTrip(ctx, request{Version: Version, Op: "attach-client", Client: hc.Client}, -1)
	if err != nil {
		return nil, err
	}
	if !resp.OK {
		conn.Close()
		if resp.Error == ErrNoClient.Error() {
			return nil, ErrNoClient
		}
		return nil, fmt.Errorf("links: %s", resp.Error)
	}
	hc.State = resp.State
	return &ClientConn{stream: &stream{UnixConn: conn, r: r}, Info: hc}, nil
}

// ClientConn is an IRC client through the holder. It's a net.Conn to the
// client, with the client's and the listener's addresses.
type ClientConn struct {
	*stream
	Info HeldClient
}

func (cc *ClientConn) RemoteAddr() net.Addr { return tcpAddr(cc.Info.Addr) }
func (cc *ClientConn) LocalAddr() net.Addr  { return tcpAddr(cc.Info.Local) }

// Secure reports whether the client came over TLS.
func (cc *ClientConn) Secure() bool { return cc.Info.TLS }

// SaveState is the control line that saves state for the next evrd; it
// goes on the stream in order with the client's lines.
func SaveState(state []byte) string { return "\x00state " + string(state) }

// Close closes the client: the holder writes what came before and hangs up.
func (cc *ClientConn) Close() error {
	cc.UnixConn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	cc.UnixConn.Write([]byte("\x00close\n"))
	return cc.UnixConn.Close()
}

// Detach leaves the client with the holder, for the next evrd (if evrd
// saved its state).
func (cc *ClientConn) Detach() error { return cc.UnixConn.Close() }

func tcpAddr(s string) net.Addr {
	if ap, err := netip.ParseAddrPort(s); err == nil {
		return net.TCPAddrFromAddrPort(ap)
	}
	host, port, _ := net.SplitHostPort(s)
	p, _ := strconv.Atoi(port)
	return &net.TCPAddr{IP: net.ParseIP(host), Port: p}
}
