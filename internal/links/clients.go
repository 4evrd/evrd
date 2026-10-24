package links

// The holder's side of IRC clients (protocol 2): evrd hands over its IRC
// listeners, the holder accepts clients on them (doing their TLS), and
// passes each client's lines to evrd over a stream. While evrd is away the
// holder keeps the clients it was serving: it answers their PINGs, keeps
// what they send (within MaxClientBuffer) and gives them to the next evrd
// with the state the last one saved. A client evrd isn't back for within
// HoldFor is told to reconnect.

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// reconnectLine is what a client the holder gives up on is told.
const reconnectLine = "ERROR :Closing link: evrd is restarting, reconnect\r\n"

// heldListener is an IRC listener evrd handed over.
type heldListener struct {
	Listener
	ln net.Listener
}

// heldClient is an IRC client the holder accepted.
type heldClient struct {
	id, addr, local string
	tls             bool
	conn            net.Conn

	mu       sync.Mutex
	att      net.Conn        // evrd's stream; nil while waiting for evrd
	state    json.RawMessage // what evrd saved; nil: not held over a restart
	buf      [][]byte        // lines from the client while waiting
	bufBytes int
	since    time.Time // waiting for evrd since
	closing  bool      // evrd closed it
	closed   bool
}

var clientSeq atomic.Int64

func (h *Holder) listen(c *net.UnixConn, w *bufio.Writer, req request, fd int) {
	defer c.Close()
	if fd < 0 {
		writeJSON(w, response{Error: "listen needs a socket"})
		return
	}
	f := os.NewFile(uintptr(fd), "listener")
	ln, err := net.FileListener(f)
	f.Close()
	if err != nil {
		writeJSON(w, response{Error: "socket: " + err.Error()})
		return
	}
	hl := &heldListener{Listener: Listener{Addr: req.Addr, TLS: req.TLS}, ln: ln}
	h.mu.Lock()
	old := h.listeners[req.Addr]
	h.listeners[req.Addr] = hl
	h.mu.Unlock()
	if old != nil {
		old.ln.Close()
	}
	writeJSON(w, response{OK: true})
	h.log.Info("irc listener", "addr", req.Addr, "tls", req.TLS)
	go h.accept(hl)
}

func (h *Holder) listenerList() []Listener {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := []Listener{}
	for _, l := range h.listeners {
		out = append(out, l.Listener)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Addr < out[j].Addr })
	return out
}

func (h *Holder) setCert(c *net.UnixConn, w *bufio.Writer, req request) {
	defer c.Close()
	cert, err := tls.X509KeyPair([]byte(req.Cert), []byte(req.Key))
	if err != nil {
		writeJSON(w, response{Error: "cert: " + err.Error()})
		return
	}
	h.cert.Store(&cert)
	writeJSON(w, response{OK: true})
}

func (h *Holder) getCert(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	if c := h.cert.Load(); c != nil {
		return c, nil
	}
	return nil, errors.New("no certificate yet")
}

func (h *Holder) accept(hl *heldListener) {
	for {
		conn, err := hl.ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			time.Sleep(100 * time.Millisecond) // out of descriptors, say
			continue
		}
		go h.newClient(hl, conn)
	}
}

func (h *Holder) newClient(hl *heldListener, conn net.Conn) {
	if hl.TLS {
		tc := tls.Server(conn, h.clientTLS())
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		err := tc.HandshakeContext(ctx)
		cancel()
		if err != nil {
			conn.Close()
			return
		}
		conn = tc
	}
	cl := &heldClient{
		id: "c" + strconv.FormatInt(clientSeq.Add(1), 10), addr: conn.RemoteAddr().String(),
		local: hl.Addr, tls: hl.TLS, conn: conn, since: time.Now(),
	}
	h.mu.Lock()
	h.clients[cl.id] = cl
	h.mu.Unlock()
	go h.fromClient(cl)
	h.announce(cl)
}

// watchClients keeps c as evrd's clients stream: every client waiting for
// evrd now, then an empty line ({"client":""}), then each one as it comes.
func (h *Holder) watchClients(c *net.UnixConn, w *bufio.Writer) {
	h.wmu.Lock()
	if err := writeJSON(w, response{OK: true}); err != nil {
		h.wmu.Unlock()
		c.Close()
		return
	}
	h.mu.Lock()
	old := h.watcher
	h.watcher = c
	var waiting []*heldClient
	for _, cl := range h.clients {
		waiting = append(waiting, cl)
	}
	h.mu.Unlock()
	if old != nil {
		old.Close()
	}
	sort.Slice(waiting, func(i, j int) bool { return clientNum(waiting[i].id) < clientNum(waiting[j].id) })
	for _, cl := range waiting {
		if hc, ok := cl.waiting(); ok {
			if json.NewEncoder(c).Encode(hc) != nil {
				break
			}
		}
	}
	io.WriteString(c, "{\"client\":\"\"}\n")
	h.wmu.Unlock()
	io.Copy(io.Discard, c) // until evrd goes
	h.mu.Lock()
	if h.watcher == c {
		h.watcher = nil
	}
	h.mu.Unlock()
	c.Close()
}

func clientNum(id string) int64 {
	n, _ := strconv.ParseInt(strings.TrimPrefix(id, "c"), 10, 64)
	return n
}

// waiting is the client as evrd is told of it, if it's waiting for evrd.
func (cl *heldClient) waiting() (HeldClient, bool) {
	cl.mu.Lock()
	defer cl.mu.Unlock()
	if cl.att != nil || cl.closed {
		return HeldClient{}, false
	}
	return HeldClient{Client: cl.id, Addr: cl.addr, Local: cl.local, TLS: cl.tls, State: cl.state}, true
}

// announce tells evrd's clients stream, if there is one, that cl waits.
func (h *Holder) announce(cl *heldClient) {
	hc, ok := cl.waiting()
	if !ok {
		return
	}
	h.wmu.Lock()
	defer h.wmu.Unlock()
	h.mu.Lock()
	wc := h.watcher
	h.mu.Unlock()
	if wc == nil {
		return
	}
	wc.SetWriteDeadline(time.Now().Add(5 * time.Second))
	if json.NewEncoder(wc).Encode(hc) != nil {
		wc.Close()
	}
	wc.SetWriteDeadline(time.Time{})
}

func (h *Holder) attachClient(c *net.UnixConn, w *bufio.Writer, id string) {
	h.mu.Lock()
	cl := h.clients[id]
	h.mu.Unlock()
	if cl == nil {
		writeJSON(w, response{Error: ErrNoClient.Error()})
		c.Close()
		return
	}
	cl.mu.Lock()
	if cl.closed {
		cl.mu.Unlock()
		writeJSON(w, response{Error: ErrNoClient.Error()})
		c.Close()
		return
	}
	if cl.att != nil {
		cl.att.Close() // a newer evrd takes over
	}
	// Under the lock, so no line from the client slips between the buffer
	// and the live stream.
	err := writeJSON(w, response{OK: true, State: cl.state})
	for _, b := range cl.buf {
		if err != nil {
			break
		}
		_, err = w.Write(b)
	}
	if err == nil {
		err = w.Flush()
	}
	if err != nil {
		cl.mu.Unlock()
		c.Close()
		return
	}
	cl.buf, cl.bufBytes = nil, 0
	cl.att = c
	cl.mu.Unlock()
	go h.toClient(cl, c, bufio.NewReader(c))
}

// maxClientLine bounds a line from a client (evrd allows 16 KiB).
const maxClientLine = 32 << 10

// fromClient reads the client: to evrd while attached, else answering its
// PINGs and keeping the rest for evrd.
func (h *Holder) fromClient(cl *heldClient) {
	r := bufio.NewReaderSize(cl.conn, 4096)
	var line []byte
	for {
		chunk, err := r.ReadSlice('\n')
		line = append(line, chunk...)
		if err == bufio.ErrBufferFull {
			if len(line) > maxClientLine {
				h.dropClient(cl, "line too long")
				return
			}
			continue
		}
		if len(line) > 0 && err == nil {
			if !h.fromClientLine(cl, line) {
				return
			}
			line = nil
		}
		if err != nil {
			h.dropClient(cl, "client: "+describe(err))
			return
		}
	}
}

// fromClientLine handles one line from the client; false once it's closed.
func (h *Holder) fromClientLine(cl *heldClient, raw []byte) bool {
	cl.mu.Lock()
	if cl.att != nil {
		cl.att.SetWriteDeadline(time.Now().Add(30 * time.Second))
		_, err := cl.att.Write(raw)
		if err == nil {
			cl.mu.Unlock()
			return true
		}
		// evrd stopped reading: keep the line for when it's back
		cl.att.Close()
		cl.att, cl.since = nil, time.Now()
	}
	_, cmd, params := parse(strings.TrimRight(string(raw), "\r\n"))
	switch {
	case cmd == "PING" && cl.state != nil:
		arg := ""
		if len(params) > 0 {
			arg = params[len(params)-1]
		}
		cl.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
		io.WriteString(cl.conn, ":evrd PONG evrd :"+arg+"\r\n")
		cl.mu.Unlock()
		return true
	case cmd == "QUIT":
		cl.mu.Unlock()
		h.dropClient(cl, "client quit")
		return false
	}
	cl.buf = append(cl.buf, append([]byte(nil), raw...))
	cl.bufBytes += len(raw)
	over := cl.bufBytes > h.MaxClientBuffer
	if over {
		cl.conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
		io.WriteString(cl.conn, reconnectLine)
	}
	cl.mu.Unlock()
	if over {
		h.dropClient(cl, "sent too much while evrd was away")
		return false
	}
	return true
}

// toClient passes evrd's lines to the client until the stream ends, taking
// the control lines (state, close) for itself. A client with saved state is
// then held for the next evrd.
func (h *Holder) toClient(cl *heldClient, c net.Conn, r *bufio.Reader) {
	for {
		line, err := r.ReadBytes('\n')
		if len(line) > 0 && line[0] == 0 {
			ctl := bytes.TrimRight(line[1:], "\r\n")
			switch {
			case bytes.HasPrefix(ctl, []byte("state ")):
				cl.mu.Lock()
				cl.state = append(json.RawMessage(nil), ctl[len("state "):]...)
				cl.mu.Unlock()
			case bytes.Equal(ctl, []byte("close")):
				cl.mu.Lock()
				cl.closing = true
				cl.mu.Unlock()
			}
		} else if len(line) > 0 {
			cl.mu.Lock()
			cl.conn.SetWriteDeadline(time.Now().Add(30 * time.Second))
			_, werr := cl.conn.Write(line)
			cl.mu.Unlock()
			if werr != nil {
				h.dropClient(cl, "client write: "+describe(werr))
				c.Close()
				return
			}
		}
		if err != nil {
			break
		}
	}
	cl.mu.Lock()
	mine := cl.att == c
	if mine {
		cl.att, cl.since = nil, time.Now()
	}
	closing, held := cl.closing, cl.state != nil
	cl.mu.Unlock()
	c.Close()
	switch {
	case !mine:
		// a newer evrd attached meanwhile
	case closing:
		h.dropClient(cl, "closed by evrd")
	case !held:
		h.dropClient(cl, "evrd went away before it was signed in")
	default:
		h.log.Info("client detached — holding it", "client", cl.id, "addr", cl.addr)
		h.announce(cl)
	}
}

func (h *Holder) dropClient(cl *heldClient, why string) {
	cl.mu.Lock()
	if cl.closed {
		cl.mu.Unlock()
		return
	}
	cl.closed = true
	att := cl.att
	cl.att = nil
	cl.mu.Unlock()
	hangUp(cl.conn)
	if att != nil {
		att.Close() // evrd sees the client go
	}
	h.mu.Lock()
	if h.clients[cl.id] == cl {
		delete(h.clients, cl.id)
	}
	h.mu.Unlock()
	h.log.Debug("client closed", "client", cl.id, "why", why)
}

// expireClients tells clients evrd isn't back for in time to reconnect.
func (h *Holder) expireClients(ctx context.Context) {
	every := h.HoldFor / 10
	if every > time.Second {
		every = time.Second
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		h.mu.Lock()
		var cls []*heldClient
		for _, cl := range h.clients {
			cls = append(cls, cl)
		}
		h.mu.Unlock()
		for _, cl := range cls {
			cl.mu.Lock()
			late := cl.att == nil && !cl.closed && time.Since(cl.since) > h.HoldFor
			if late {
				cl.conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
				io.WriteString(cl.conn, reconnectLine)
			}
			cl.mu.Unlock()
			if late {
				h.dropClient(cl, "evrd didn't come back in time")
			}
		}
	}
}

// closeClients ends every client and listener: the holder is stopping.
func (h *Holder) closeClients() {
	h.mu.Lock()
	var cls []*heldClient
	for _, cl := range h.clients {
		cls = append(cls, cl)
	}
	for _, l := range h.listeners {
		l.ln.Close()
	}
	h.listeners = map[string]*heldListener{}
	h.mu.Unlock()
	for _, cl := range cls {
		cl.mu.Lock()
		cl.conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
		io.WriteString(cl.conn, reconnectLine)
		cl.mu.Unlock()
		h.dropClient(cl, "holder stopping")
	}
}

// hangUp closes a client's connection gently: what was written to it (an
// ERROR) is sent before the connection goes, rather than reset because the
// client had sent more.
func hangUp(conn net.Conn) {
	cw, ok := conn.(interface{ CloseWrite() error })
	if !ok || cw.CloseWrite() != nil {
		conn.Close()
		return
	}
	go func() {
		conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		io.Copy(io.Discard, conn)
		conn.Close()
	}()
}

// clientTLS is the TLS config the holder's IRC clients connect with: one for
// all of them, so a client's session ticket from its last connection resumes
// (Go keeps ticket keys per config).
func (h *Holder) clientTLS() *tls.Config {
	h.tlsOnce.Do(func() { h.tlsConf = &tls.Config{GetCertificate: h.getCert} })
	return h.tlsConf
}
