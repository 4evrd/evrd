package links

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sys/unix"

	"github.com/4evrd/evrd/internal/irctls"
)

// Holder keeps links (see the package comment).
type Holder struct {
	log *slog.Logger
	// Hash is the holder's binary (SelfHash, read as it started), told to
	// evrd so it can tell whether it's the evrd-links shipped with it.
	Hash string
	// MaxBuffer is the most bytes kept per link while detached; older lines
	// are dropped (and counted) past it.
	MaxBuffer int
	// KeepAlive is how often a detached link pings the server; ReadTimeout
	// drops a link the server has gone quiet on.
	KeepAlive   time.Duration
	ReadTimeout time.Duration

	// HoldFor is how long a client waits for evrd before it's told to
	// reconnect; MaxClientBuffer is the most it may send meanwhile.
	HoldFor         time.Duration
	MaxClientBuffer int

	mu    sync.Mutex
	links map[string]*link

	clients   map[string]*heldClient
	listeners map[string]*heldListener
	watcher   net.Conn   // evrd's clients stream, told of clients waiting
	wmu       sync.Mutex // one write to the watcher at a time
	cert      atomic.Pointer[tls.Certificate]
	tlsOnce   sync.Once
	tlsConf   *tls.Config // clientTLS
}

// NewHolder makes a holder with default limits.
func NewHolder(log *slog.Logger) *Holder {
	return &Holder{log: log, MaxBuffer: 4 << 20, KeepAlive: 90 * time.Second, ReadTimeout: 5 * time.Minute,
		HoldFor: 2 * time.Minute, MaxClientBuffer: 64 << 10,
		links: map[string]*link{}, clients: map[string]*heldClient{}, listeners: map[string]*heldListener{}}
}

type link struct {
	id   string
	conn net.Conn // to the IRC server

	mu          sync.Mutex
	nick        string
	isupport    []string
	registered  bool
	connectedAt time.Time
	buf         [][]byte
	bufBytes    int
	dropped     int
	att         net.Conn // the attached evrd stream; nil while detached
	quitSent    bool
	closed      bool
}

// Serve accepts requests on ln until ctx ends, then closes every link.
func (h *Holder) Serve(ctx context.Context, ln *net.UnixListener) error {
	go func() {
		<-ctx.Done()
		ln.Close()
	}()
	go h.expireClients(ctx)
	for {
		c, err := ln.AcceptUnix()
		if err != nil {
			if ctx.Err() != nil {
				h.closeClients()
				h.closeAll()
				return nil
			}
			return err
		}
		go h.handle(c)
	}
}

func (h *Holder) closeAll() {
	h.mu.Lock()
	ls := make([]*link, 0, len(h.links))
	for _, l := range h.links {
		ls = append(ls, l)
	}
	h.mu.Unlock()
	for _, l := range ls {
		l.mu.Lock()
		if l.registered {
			l.conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
			io.WriteString(l.conn, "QUIT :Bouncer restarting\r\n")
		}
		l.mu.Unlock()
		h.drop(l, "holder stopping")
	}
}

// handle reads one request (and, for open, the socket passed with it).
func (h *Holder) handle(c *net.UnixConn) {
	line, fd, err := readRequest(c)
	if err != nil {
		c.Close()
		return
	}
	var req request
	w := bufio.NewWriter(c)
	if err := json.Unmarshal(line, &req); err != nil || req.Version < 1 || req.Version > Version {
		if fd >= 0 {
			unix.Close(fd)
		}
		writeJSON(w, response{Error: "bad request or protocol version"})
		c.Close()
		return
	}
	switch req.Op {
	case "open":
		h.open(c, w, req, fd)
	case "attach":
		h.attach(c, w, req.Link)
	case "list":
		writeJSON(w, response{OK: true, Links: h.list()})
		c.Close()
	case "listen":
		h.listen(c, w, req, fd)
	case "hello":
		writeJSON(w, response{OK: true, Protocol: Version, Hash: h.Hash})
		c.Close()
	case "listeners":
		writeJSON(w, response{OK: true, Listeners: h.listenerList()})
		c.Close()
	case "cert":
		h.setCert(c, w, req)
	case "clients":
		h.watchClients(c, w)
	case "attach-client":
		h.attachClient(c, w, req.Client)
	case "close":
		h.mu.Lock()
		l := h.links[req.Link]
		h.mu.Unlock()
		if l != nil {
			if req.Quit != "" {
				l.conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
				io.WriteString(l.conn, "QUIT :"+req.Quit+"\r\n")
			}
			h.drop(l, "closed by evrd")
		}
		writeJSON(w, response{OK: true})
		c.Close()
	default:
		writeJSON(w, response{Error: "unknown op"})
		c.Close()
	}
}

// readRequest reads the first line, and a socket if one came with it.
func readRequest(c *net.UnixConn) ([]byte, int, error) {
	c.SetReadDeadline(time.Now().Add(10 * time.Second))
	defer c.SetReadDeadline(time.Time{})
	buf := make([]byte, 64<<10)
	oob := make([]byte, unix.CmsgSpace(4))
	fd := -1
	var line []byte
	for {
		n, oobn, _, _, err := c.ReadMsgUnix(buf, oob)
		if oobn > 0 && fd < 0 {
			if msgs, err := unix.ParseSocketControlMessage(oob[:oobn]); err == nil {
				for _, m := range msgs {
					if fds, err := unix.ParseUnixRights(&m); err == nil && len(fds) > 0 {
						fd = fds[0]
						for _, extra := range fds[1:] {
							unix.Close(extra)
						}
					}
				}
			}
		}
		line = append(line, buf[:n]...)
		if i := bytes.IndexByte(line, '\n'); i >= 0 {
			// evrd sends nothing more until it has the answer
			return line[:i], fd, nil
		}
		if err != nil || n == 0 || len(line) > 64<<10 {
			if fd >= 0 {
				unix.Close(fd)
			}
			if err == nil {
				err = io.ErrUnexpectedEOF
			}
			return nil, -1, err
		}
	}
}

func (h *Holder) open(c *net.UnixConn, w *bufio.Writer, req request, fd int) {
	fail := func(resp response) {
		writeJSON(w, resp)
		c.Close()
	}
	if fd < 0 {
		fail(response{Error: "open needs a socket"})
		return
	}
	f := os.NewFile(uintptr(fd), "irc")
	conn, err := net.FileConn(f)
	f.Close()
	if err != nil {
		fail(response{Error: "socket: " + err.Error()})
		return
	}
	if req.TLS {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		tc, err := irctls.Handshake(ctx, conn, irctls.Config(req.Host, req.Trust, nil))
		cancel()
		if err != nil {
			conn.Close()
			resp := response{Error: err.Error()}
			var ue *irctls.UntrustedCertError
			if errors.As(err, &ue) {
				resp.Error, resp.Fingerprint = ue.Reason, ue.Fingerprint
			}
			fail(resp)
			return
		}
		conn = tc
	}
	l := &link{id: req.Link, conn: conn}
	h.mu.Lock()
	old := h.links[req.Link]
	h.links[req.Link] = l
	h.mu.Unlock()
	if old != nil {
		h.drop(old, "replaced by a new connection")
	}
	if err := writeJSON(w, response{OK: true}); err != nil {
		h.drop(l, "evrd went away")
		c.Close()
		return
	}
	h.log.Info("link opened", "link", l.id, "tls", req.TLS, "host", req.Host)
	l.mu.Lock()
	l.att = c
	l.mu.Unlock()
	go h.fromServer(l)
	go h.fromEvrd(l, c, bufio.NewReader(c))
	go h.keepAlive(l)
}

func (h *Holder) attach(c *net.UnixConn, w *bufio.Writer, id string) {
	h.mu.Lock()
	l := h.links[id]
	h.mu.Unlock()
	if l == nil {
		writeJSON(w, response{Error: ErrNoLink.Error()})
		c.Close()
		return
	}
	l.mu.Lock()
	if !l.registered || l.closed {
		l.mu.Unlock()
		writeJSON(w, response{Error: ErrNoLink.Error()})
		c.Close()
		return
	}
	if l.att != nil {
		l.att.Close() // a newer evrd takes over
	}
	resp := response{OK: true, Nick: l.nick, ISupport: append([]string(nil), l.isupport...), ConnectedAt: l.connectedAt.Unix(), Dropped: l.dropped}
	// Under the lock, so no line from the server slips between the buffer
	// and the live stream.
	err := writeJSON(w, resp)
	for _, b := range l.buf {
		if err != nil {
			break
		}
		_, err = w.Write(b)
	}
	if err == nil {
		err = w.Flush()
	}
	if err != nil {
		l.mu.Unlock()
		c.Close()
		return
	}
	l.buf, l.bufBytes, l.dropped = nil, 0, 0
	l.att = c
	l.mu.Unlock()
	h.log.Info("link attached", "link", id, "nick", resp.Nick)
	go h.fromEvrd(l, c, bufio.NewReader(c))
}

func (h *Holder) list() []Info {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := []Info{}
	for _, l := range h.links {
		l.mu.Lock()
		out = append(out, Info{Link: l.id, Nick: l.nick, Registered: l.registered, Attached: l.att != nil, Buffered: len(l.buf)})
		l.mu.Unlock()
	}
	return out
}

// fromServer reads the IRC server: tracks what a resume needs, answers
// PINGs while detached, and forwards or buffers every line.
func (h *Holder) fromServer(l *link) {
	r := bufio.NewReaderSize(l.conn, 16<<10)
	for {
		l.conn.SetReadDeadline(time.Now().Add(h.ReadTimeout))
		line, err := r.ReadBytes('\n')
		if len(line) > 0 {
			h.fromServerLine(l, line)
		}
		if err != nil {
			h.drop(l, "server: "+describe(err))
			return
		}
	}
}

func (h *Holder) fromServerLine(l *link, raw []byte) {
	if raw[len(raw)-1] != '\n' {
		raw = append(raw, '\r', '\n')
	}
	prefix, cmd, params := parse(strings.TrimRight(string(raw), "\r\n"))
	l.mu.Lock()
	defer l.mu.Unlock()
	switch cmd {
	case "001":
		l.registered, l.connectedAt, l.isupport = true, time.Now(), nil
		if len(params) > 0 {
			l.nick = params[0]
		}
	case "005":
		l.isupport = append(l.isupport, strings.TrimRight(string(raw), "\r\n"))
	case "NICK":
		if len(params) > 0 && strings.EqualFold(nickOf(prefix), l.nick) {
			l.nick = params[0]
		}
	case "PING":
		if l.att == nil {
			// evrd is away: keep the link alive ourselves
			l.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			arg := ""
			if len(params) > 0 {
				arg = params[len(params)-1]
			}
			io.WriteString(l.conn, "PONG :"+arg+"\r\n")
			return
		}
	case "PONG":
		if l.att == nil && len(params) > 0 && params[len(params)-1] == keepAliveToken {
			return // our own keepalive's answer
		}
	}
	if l.att != nil {
		l.att.SetWriteDeadline(time.Now().Add(30 * time.Second))
		if _, err := l.att.Write(raw); err == nil {
			return
		}
		// evrd stopped reading: hold the line for when it's back
		l.att.Close()
		l.att = nil
	}
	if !l.registered {
		return // nothing to resume into
	}
	cp := append([]byte(nil), raw...)
	l.buf = append(l.buf, cp)
	l.bufBytes += len(cp)
	for l.bufBytes > h.MaxBuffer && len(l.buf) > 0 {
		l.bufBytes -= len(l.buf[0])
		l.buf = l.buf[1:]
		l.dropped++
	}
}

const keepAliveToken = "evrd-links"

// quitGrace is how long a link evrd quit stays open, so the server sees the
// QUIT before the connection closes.
var quitGrace = 3 * time.Second

// keepAlive pings the server while evrd is away, so a quiet network still
// shows the link is alive (and a dead one is noticed).
func (h *Holder) keepAlive(l *link) {
	t := time.NewTicker(h.KeepAlive)
	defer t.Stop()
	for range t.C {
		l.mu.Lock()
		closed, detached := l.closed, l.att == nil
		if detached && !closed {
			l.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			io.WriteString(l.conn, "PING :"+keepAliveToken+"\r\n")
		}
		l.mu.Unlock()
		if closed {
			return
		}
	}
}

// fromEvrd passes evrd's lines to the server until the stream ends; the
// link is then held — unless evrd quit it.
func (h *Holder) fromEvrd(l *link, c net.Conn, r *bufio.Reader) {
	for {
		line, err := r.ReadBytes('\n')
		if len(line) > 0 {
			if len(line) >= 4 && strings.EqualFold(string(line[:4]), "QUIT") {
				l.mu.Lock()
				l.quitSent = true
				l.mu.Unlock()
			}
			l.conn.SetWriteDeadline(time.Now().Add(30 * time.Second))
			if _, werr := l.conn.Write(line); werr != nil {
				h.drop(l, "server write: "+werr.Error())
				c.Close()
				return
			}
		}
		if err != nil {
			break
		}
	}
	l.mu.Lock()
	mine := l.att == c
	if mine {
		l.att = nil
	}
	quit, registered := l.quitSent, l.registered
	l.mu.Unlock()
	c.Close()
	if !mine {
		return // a newer evrd attached meanwhile
	}
	switch {
	case quit:
		// let the server see the QUIT through, then close
		time.AfterFunc(quitGrace, func() { h.drop(l, "evrd quit") })
	case !registered:
		h.drop(l, "evrd went away before registration finished")
	default:
		h.log.Info("link detached — holding it", "link", l.id)
	}
}

func (h *Holder) drop(l *link, why string) {
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return
	}
	l.closed = true
	att := l.att
	l.att = nil
	l.mu.Unlock()
	l.conn.Close()
	if att != nil {
		att.Close() // evrd sees the link end and reconnects
	}
	h.mu.Lock()
	if h.links[l.id] == l {
		delete(h.links, l.id)
	}
	h.mu.Unlock()
	h.log.Info("link closed", "link", l.id, "why", why)
}

func describe(err error) string {
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return "timed out"
	}
	if errors.Is(err, io.EOF) {
		return "closed"
	}
	return err.Error()
}

// parse splits an IRC line into prefix, command and parameters — just
// enough for PING, 001, 005 and NICK.
func parse(line string) (prefix, cmd string, params []string) {
	if strings.HasPrefix(line, "@") { // message tags
		if i := strings.IndexByte(line, ' '); i >= 0 {
			line = line[i+1:]
		}
	}
	if strings.HasPrefix(line, ":") {
		i := strings.IndexByte(line, ' ')
		if i < 0 {
			return line[1:], "", nil
		}
		prefix, line = line[1:i], line[i+1:]
	}
	for line != "" {
		if strings.HasPrefix(line, ":") {
			params = append(params, line[1:])
			break
		}
		i := strings.IndexByte(line, ' ')
		if i < 0 {
			params = append(params, line)
			break
		}
		if i > 0 {
			params = append(params, line[:i])
		}
		line = line[i+1:]
	}
	if len(params) > 0 {
		cmd, params = strings.ToUpper(params[0]), params[1:]
	}
	return prefix, cmd, params
}

func nickOf(prefix string) string {
	if i := strings.IndexByte(prefix, '!'); i >= 0 {
		return prefix[:i]
	}
	return prefix
}
