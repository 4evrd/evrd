// Package upstream maintains a live connection to one IRC network on behalf of
// a user: it dials, registers, stays connected (reconnecting with backoff),
// parses incoming messages, persists them, and fans them out to subscribers
// (the web console). It also sends messages on the user's behalf.
package upstream

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strings"
	"sync"
	"time"

	"code.ircforever.org/ircforever/evrd/internal/irc"
	"code.ircforever.org/ircforever/evrd/internal/store"
)

// Status is the lifecycle state of an upstream connection.
type Status string

const (
	StatusDisconnected Status = "disconnected"
	StatusConnecting   Status = "connecting"
	StatusConnected    Status = "connected"
)

// Conn is a single upstream IRC connection.
type Conn struct {
	net     store.Network
	log     *slog.Logger
	persist func(store.Message)

	mu       sync.Mutex
	w        io.Writer
	status   Status
	nick     string
	channels map[string]bool
	subs     map[chan store.Message]struct{}
	rawSubs  map[chan string]struct{}
}

// New creates a connection for a network. persist is called for every message
// (incoming or sent) so it can be buffered to the store.
func New(n store.Network, log *slog.Logger, persist func(store.Message)) *Conn {
	return &Conn{
		net:      n,
		log:      log,
		persist:  persist,
		status:   StatusDisconnected,
		nick:     n.Nick,
		channels: map[string]bool{},
		subs:     map[chan store.Message]struct{}{},
		rawSubs:  map[chan string]struct{}{},
	}
}

// Run keeps the connection alive until ctx is cancelled, reconnecting with
// exponential backoff.
func (c *Conn) Run(ctx context.Context) {
	backoff := time.Second
	for ctx.Err() == nil {
		c.setStatus(StatusConnecting)
		err := c.dialAndServe(ctx)
		c.setStatus(StatusDisconnected)
		if ctx.Err() != nil {
			return
		}
		c.log.Warn("upstream disconnected", "network", c.net.Name, "err", err)
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

func (c *Conn) dialAndServe(ctx context.Context) error {
	addr := fmt.Sprintf("%s:%d", c.net.Host, c.net.Port)
	d := &net.Dialer{Timeout: 15 * time.Second}

	var conn net.Conn
	var err error
	if c.net.TLS {
		conn, err = tls.DialWithDialer(d, "tcp", addr, &tls.Config{ServerName: c.net.Host})
	} else {
		conn, err = d.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return err
	}
	defer conn.Close()

	// Close the socket when this dial's context ends (ctx cancel or return).
	dctx, dcancel := context.WithCancel(ctx)
	defer dcancel()
	go func() {
		<-dctx.Done()
		conn.Close()
	}()

	c.mu.Lock()
	c.w = conn
	c.nick = c.net.Nick
	c.mu.Unlock()

	// Registration. CAP LS holds registration open so we can negotiate SASL;
	// CAP END (sent once auth resolves, see handleCap) completes it.
	c.send("CAP LS 302")
	c.send("NICK " + c.net.Nick)
	user := orElse(c.net.Username, c.net.Nick)
	real := orElse(c.net.Realname, c.net.Nick)
	c.send(fmt.Sprintf("USER %s 0 * :%s", user, real))

	sc := bufio.NewScanner(conn)
	sc.Buffer(make([]byte, 0, 4096), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		c.handle(irc.Parse(line))
		c.broadcastRaw(line) // full-fidelity feed for attached IRC clients
	}

	c.mu.Lock()
	c.w = nil
	c.mu.Unlock()
	return sc.Err()
}

func (c *Conn) handle(m irc.Message) {
	switch m.Command {
	case "PING":
		c.send("PONG :" + lastParam(m))
	case "CAP":
		c.handleCap(m)
	case "AUTHENTICATE":
		if len(m.Params) > 0 && m.Params[0] == "+" {
			c.sendSASL()
		}
	case "903": // RPL_SASLSUCCESS
		c.send("CAP END")
	case "902", "904", "905", "906", "908": // SASL failures
		c.log.Warn("SASL failed, continuing unauthenticated", "network", c.net.Name)
		c.send("CAP END")
	case "001": // RPL_WELCOME — registration complete
		c.setStatus(StatusConnected)
		for _, ch := range c.net.Channels {
			if ch = strings.TrimSpace(ch); ch != "" {
				c.send("JOIN " + ch)
				c.mu.Lock()
				c.channels[ch] = true
				c.mu.Unlock()
			}
		}
	case "433": // ERR_NICKNAMEINUSE — try a variant
		c.mu.Lock()
		c.nick += "_"
		nick := c.nick
		c.mu.Unlock()
		c.send("NICK " + nick)
	case "PRIVMSG", "NOTICE":
		c.handleMessage(m)
	}
}

// handleCap drives SASL negotiation. With a password and server SASL support we
// authenticate via SASL PLAIN; otherwise we fall back to a server PASS. Either
// way we finish with CAP END to complete registration.
func (c *Conn) handleCap(m irc.Message) {
	if len(m.Params) < 2 {
		return
	}
	sub := strings.ToUpper(m.Params[1])
	caps := ""
	if len(m.Params) > 2 {
		caps = m.Params[2]
	}
	switch sub {
	case "LS":
		if c.net.Password != "" && strings.Contains(caps, "sasl") {
			c.send("CAP REQ :sasl")
		} else {
			if c.net.Password != "" {
				c.send("PASS " + c.net.Password)
			}
			c.send("CAP END")
		}
	case "ACK":
		if strings.Contains(caps, "sasl") {
			c.send("AUTHENTICATE PLAIN")
		} else {
			c.send("CAP END")
		}
	case "NAK":
		if c.net.Password != "" {
			c.send("PASS " + c.net.Password)
		}
		c.send("CAP END")
	}
}

// sendSASL responds to an AUTHENTICATE + challenge with a SASL PLAIN payload:
// base64("\0" + authcid + "\0" + password).
func (c *Conn) sendSASL() {
	authcid := orElse(c.net.Username, c.net.Nick)
	payload := "\x00" + authcid + "\x00" + c.net.Password
	c.send("AUTHENTICATE " + base64.StdEncoding.EncodeToString([]byte(payload)))
}

func (c *Conn) handleMessage(m irc.Message) {
	if len(m.Params) < 2 {
		return
	}
	targetField := m.Params[0]
	text := m.Params[len(m.Params)-1]
	sender := nickOf(m.Prefix)

	kind := "message"
	if m.Command == "NOTICE" {
		kind = "notice"
	}
	if strings.HasPrefix(text, "\x01ACTION ") && strings.HasSuffix(text, "\x01") {
		text = strings.TrimSuffix(strings.TrimPrefix(text, "\x01ACTION "), "\x01")
		kind = "action"
	}

	// Channel messages bucket under the channel; PMs bucket under the sender.
	target := targetField
	if !isChannel(targetField) {
		target = sender
	}

	c.emit(store.Message{
		NetworkID: c.net.ID,
		Target:    target,
		Sender:    sender,
		Kind:      kind,
		Text:      text,
		Time:      time.Now(),
	})
}

// SendMessage sends a PRIVMSG to target and echoes it locally so the sender
// sees their own line.
func (c *Conn) SendMessage(target, text string) { c.SendMessageFrom("", target, text) }

// SendMessageFrom is SendMessage, tagging the echo with the originating session
// id so other sessions can show it live while the originator avoids a double.
func (c *Conn) SendMessageFrom(origin, target, text string) {
	c.send("PRIVMSG " + target + " :" + text)
	c.mu.Lock()
	nick := c.nick
	c.mu.Unlock()
	c.emit(store.Message{
		NetworkID: c.net.ID,
		Target:    target,
		Sender:    nick,
		Kind:      "message",
		Text:      text,
		Time:      time.Now(),
		Origin:    origin,
	})
}

func (c *Conn) emit(m store.Message) {
	if c.persist != nil {
		c.persist(m)
	}
	c.mu.Lock()
	for ch := range c.subs {
		select {
		case ch <- m:
		default: // drop for slow subscribers
		}
	}
	c.mu.Unlock()
}

// Subscribe returns a channel of live messages and an unsubscribe function.
func (c *Conn) Subscribe() (<-chan store.Message, func()) {
	ch := make(chan store.Message, 64)
	c.mu.Lock()
	c.subs[ch] = struct{}{}
	c.mu.Unlock()
	return ch, func() {
		c.mu.Lock()
		if _, ok := c.subs[ch]; ok {
			delete(c.subs, ch)
			close(ch)
		}
		c.mu.Unlock()
	}
}

// SubscribeRaw returns a channel of raw server lines (verbatim) and an
// unsubscribe function. Used by attached IRC clients for a full-fidelity feed.
func (c *Conn) SubscribeRaw() (<-chan string, func()) {
	ch := make(chan string, 256)
	c.mu.Lock()
	c.rawSubs[ch] = struct{}{}
	c.mu.Unlock()
	return ch, func() {
		c.mu.Lock()
		if _, ok := c.rawSubs[ch]; ok {
			delete(c.rawSubs, ch)
			close(ch)
		}
		c.mu.Unlock()
	}
}

func (c *Conn) broadcastRaw(line string) {
	c.mu.Lock()
	for ch := range c.rawSubs {
		select {
		case ch <- line:
		default:
		}
	}
	c.mu.Unlock()
}

// SendRaw writes a raw line to the upstream server (used to pass through client
// commands like JOIN/NAMES/WHO/TOPIC).
func (c *Conn) SendRaw(line string) { c.send(line) }

func (c *Conn) send(line string) {
	c.mu.Lock()
	w := c.w
	c.mu.Unlock()
	if w == nil {
		return
	}
	io.WriteString(w, line+"\r\n")
}

func (c *Conn) setStatus(s Status) {
	c.mu.Lock()
	c.status = s
	c.mu.Unlock()
}

// Status returns the current connection state.
func (c *Conn) Status() Status {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.status
}

// Nick returns the connection's current nick.
func (c *Conn) Nick() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.nick
}

// Channels returns the channels this connection has joined.
func (c *Conn) Channels() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, 0, len(c.channels))
	for ch := range c.channels {
		out = append(out, ch)
	}
	return out
}

// --- helpers -------------------------------------------------------------

func nickOf(prefix string) string {
	if i := strings.IndexByte(prefix, '!'); i >= 0 {
		return prefix[:i]
	}
	return prefix
}

func isChannel(s string) bool {
	return s != "" && strings.ContainsRune("#&+!", rune(s[0]))
}

func lastParam(m irc.Message) string {
	if len(m.Params) == 0 {
		return ""
	}
	return m.Params[len(m.Params)-1]
}

func orElse(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
