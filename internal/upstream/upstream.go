// Package upstream maintains a live connection to one IRC network on behalf of
// a user: it dials, registers, stays connected (reconnecting with backoff),
// parses incoming messages, persists them, and fans them out to subscribers
// (the web console). It also sends messages on the user's behalf, and tracks
// its own nick and channels — joins and parts are reported so the network's
// saved channel list follows what the user actually does.
package upstream

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"code.ircforever.org/ircforever/evrd/internal/history"
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
	persist func(history.Message)
	// onChannels is called with the new saved channel list after the user
	// joins or parts a channel.
	onChannels func([]string)
	dial       func(ctx context.Context, host string, port int) (net.Conn, error)

	mu       sync.Mutex
	w        io.Writer
	status   Status
	nick     string
	saved    []string        // channels to join on (re)connect, in order
	channels map[string]bool // channels currently joined
	// members is who's in each joined channel (lowercased channel -> lowercased
	// nick), so a QUIT or NICK — which name no channel — is filed under
	// every channel the user shared with that nick.
	members map[string]map[string]bool
	subs    map[chan history.Message]struct{}
	rawSubs map[chan string]struct{}
}

// Options are a connection's hooks.
type Options struct {
	// Persist is called for every message and event (incoming or sent).
	Persist func(history.Message)
	// OnChannels is called with the new saved channel list after the user
	// joins or parts a channel. May be nil.
	OnChannels func([]string)
	// Dial opens the TCP connection (TLS is added on top). The bouncer uses
	// it to pace connects and pick the source address; nil dials directly.
	Dial func(ctx context.Context, host string, port int) (net.Conn, error)
}

// New creates a connection for a network.
func New(n store.Network, log *slog.Logger, opts Options) *Conn {
	persist, onChannels := opts.Persist, opts.OnChannels
	var saved []string
	for _, ch := range n.Channels {
		if ch = strings.TrimSpace(ch); ch != "" {
			saved = append(saved, ch)
		}
	}
	return &Conn{
		net:        n,
		log:        log,
		persist:    persist,
		onChannels: onChannels,
		dial:       opts.Dial,
		status:     StatusDisconnected,
		nick:       n.Nick,
		saved:      saved,
		channels:   map[string]bool{},
		members:    map[string]map[string]bool{},
		subs:       map[chan history.Message]struct{}{},
		rawSubs:    map[chan string]struct{}{},
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
		// Jitter, so connections dropped together don't all retry together.
		wait := backoff/2 + time.Duration(rand.Int64N(int64(backoff)))
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

func (c *Conn) dialAndServe(ctx context.Context) error {
	dctxDial, cancelDial := context.WithTimeout(ctx, 30*time.Second)
	defer cancelDial()
	var conn net.Conn
	var err error
	if c.dial != nil {
		conn, err = c.dial(dctxDial, c.net.Host, c.net.Port)
	} else {
		d := &net.Dialer{}
		conn, err = d.DialContext(dctxDial, "tcp", net.JoinHostPort(c.net.Host, strconv.Itoa(c.net.Port)))
	}
	if err != nil {
		return err
	}
	if c.net.TLS {
		tc := tls.Client(conn, &tls.Config{ServerName: c.net.Host})
		if err := tc.HandshakeContext(dctxDial); err != nil {
			conn.Close()
			return err
		}
		conn = tc
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
	c.channels = map[string]bool{}
	c.members = map[string]map[string]bool{}
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
		c.mu.Lock()
		if len(m.Params) > 0 {
			c.nick = m.Params[0] // the server's word on our nick
		}
		saved := append([]string(nil), c.saved...)
		c.mu.Unlock()
		c.setStatus(StatusConnected)
		for _, ch := range saved {
			c.send("JOIN " + ch)
		}
	case "433": // ERR_NICKNAMEINUSE
		// While registering, try a variant. Once connected, it's the answer
		// to a client's NICK and goes back to that client as-is.
		if c.Status() == StatusConnected {
			return
		}
		c.mu.Lock()
		c.nick += "_"
		nick := c.nick
		c.mu.Unlock()
		c.send("NICK " + nick)
	case "353": // RPL_NAMREPLY: me symbol #chan :names
		if len(m.Params) >= 4 {
			c.mu.Lock()
			for _, n := range strings.Fields(m.Params[3]) {
				c.addMember(m.Params[2], n)
			}
			c.mu.Unlock()
		}
	case "NICK":
		if len(m.Params) == 0 {
			return
		}
		old, nu := nickOf(m.Prefix), m.Params[0]
		c.mu.Lock()
		me := strings.EqualFold(old, c.nick)
		if me {
			c.nick = nu
		}
		chans := c.channelsOf(old, me)
		for _, ch := range chans {
			delete(c.members[strings.ToLower(ch)], strings.ToLower(old))
			c.addMember(ch, nu)
		}
		c.mu.Unlock()
		for _, ch := range chans {
			c.event("nick", ch, old, "", nu)
		}
	case "QUIT":
		who := nickOf(m.Prefix)
		c.mu.Lock()
		chans := c.channelsOf(who, false)
		for _, ch := range chans {
			delete(c.members[strings.ToLower(ch)], strings.ToLower(who))
		}
		c.mu.Unlock()
		for _, ch := range chans {
			c.event("quit", ch, who, lastParamIf(m, 1), "")
		}
	case "JOIN":
		if len(m.Params) == 0 {
			return
		}
		ch, who := m.Params[0], nickOf(m.Prefix)
		if c.isMe(m.Prefix) {
			c.joined(ch)
		}
		c.mu.Lock()
		c.addMember(ch, who)
		c.mu.Unlock()
		c.event("join", ch, who, "", "")
	case "PART":
		if len(m.Params) == 0 {
			return
		}
		ch, who := m.Params[0], nickOf(m.Prefix)
		c.event("part", ch, who, lastParamIf(m, 2), "")
		if c.isMe(m.Prefix) {
			c.parted(ch, true)
		} else {
			c.mu.Lock()
			delete(c.members[strings.ToLower(ch)], strings.ToLower(who))
			c.mu.Unlock()
		}
	case "KICK":
		if len(m.Params) < 2 {
			return
		}
		ch, victim := m.Params[0], m.Params[1]
		c.event("kick", ch, nickOf(m.Prefix), lastParamIf(m, 3), victim)
		if strings.EqualFold(victim, c.Nick()) {
			// Kicked: no longer in it, but keep it saved so a reconnect rejoins.
			c.parted(ch, false)
		} else {
			c.mu.Lock()
			delete(c.members[strings.ToLower(ch)], strings.ToLower(victim))
			c.mu.Unlock()
		}
	case "TOPIC":
		if len(m.Params) >= 2 {
			c.event("topic", m.Params[0], nickOf(m.Prefix), m.Params[1], "")
		}
	case "MODE":
		if len(m.Params) >= 2 && isChannel(m.Params[0]) {
			c.event("mode", m.Params[0], nickOf(m.Prefix), strings.Join(m.Params[1:], " "), "")
		}
	case "PRIVMSG", "NOTICE":
		c.handleMessage(m)
	}
}

// event records a channel event (join, part, quit, …) like a message.
func (c *Conn) event(kind, channel, who, text, extra string) {
	c.emit(history.Message{
		NetworkID: c.net.ID, Target: channel, Sender: who,
		Kind: kind, Text: text, Extra: extra, Time: time.Now(),
	})
}

// addMember adds a NAMES entry ("@nick", "nick!user@host", …). Holds c.mu.
func (c *Conn) addMember(channel, entry string) {
	nick := strings.TrimLeft(entry, "~&@%+")
	nick = nickOf(nick)
	if nick == "" {
		return
	}
	key := strings.ToLower(channel)
	if c.members[key] == nil {
		c.members[key] = map[string]bool{}
	}
	c.members[key][strings.ToLower(nick)] = true
}

// channelsOf lists the joined channels a nick is in (all of them for our own
// nick). Holds c.mu.
func (c *Conn) channelsOf(nick string, me bool) []string {
	var out []string
	for ch := range c.channels {
		if me || c.members[strings.ToLower(ch)][strings.ToLower(nick)] {
			out = append(out, ch)
		}
	}
	sort.Strings(out)
	return out
}

func (c *Conn) isMe(prefix string) bool { return strings.EqualFold(nickOf(prefix), c.Nick()) }

// joined records a channel we joined, saving it if it's new.
func (c *Conn) joined(ch string) {
	c.mu.Lock()
	c.channels[ch] = true
	changed := !containsFold(c.saved, ch)
	if changed {
		c.saved = append(c.saved, ch)
	}
	saved := append([]string(nil), c.saved...)
	c.mu.Unlock()
	if changed && c.onChannels != nil {
		c.onChannels(saved)
	}
}

// parted records a channel we left; forget drops it from the saved list too.
func (c *Conn) parted(ch string, forget bool) {
	c.mu.Lock()
	delete(c.members, strings.ToLower(ch))
	for k := range c.channels {
		if strings.EqualFold(k, ch) {
			delete(c.channels, k)
		}
	}
	changed := false
	if forget {
		kept := c.saved[:0]
		for _, s := range c.saved {
			if strings.EqualFold(s, ch) {
				changed = true
				continue
			}
			kept = append(kept, s)
		}
		c.saved = kept
	}
	saved := append([]string(nil), c.saved...)
	c.mu.Unlock()
	if changed && c.onChannels != nil {
		c.onChannels(saved)
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

	c.emit(history.Message{
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
func (c *Conn) SendMessage(target, text string) { c.SendFrom("", "PRIVMSG", target, text) }

// SendFrom sends a PRIVMSG or NOTICE and records it like an incoming message,
// tagged with the originating session id so other sessions can show it live
// while the originator avoids a double. A CTCP ACTION is recorded as an
// action; other CTCPs (VERSION, PING, …) are sent but not recorded.
func (c *Conn) SendFrom(origin, command, target, text string) {
	c.send(command + " " + target + " :" + text)
	kind := "message"
	if command == "NOTICE" {
		kind = "notice"
	}
	if strings.HasPrefix(text, "\x01") {
		if !strings.HasPrefix(text, "\x01ACTION ") {
			return
		}
		text = strings.TrimSuffix(strings.TrimPrefix(text, "\x01ACTION "), "\x01")
		kind = "action"
	}
	c.emit(history.Message{
		NetworkID: c.net.ID,
		Target:    target,
		Sender:    c.Nick(),
		Kind:      kind,
		Text:      text,
		Time:      time.Now(),
		Origin:    origin,
	})
}

func (c *Conn) emit(m history.Message) {
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
func (c *Conn) Subscribe() (<-chan history.Message, func()) {
	ch := make(chan history.Message, 64)
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

// Channels returns the channels to show an attaching client: the saved list
// (in order) plus any joined but not saved.
func (c *Conn) Channels() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := append([]string(nil), c.saved...)
	for ch := range c.channels {
		if !containsFold(out, ch) {
			out = append(out, ch)
		}
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

func containsFold(list []string, s string) bool {
	for _, v := range list {
		if strings.EqualFold(v, s) {
			return true
		}
	}
	return false
}

func isChannel(s string) bool {
	return s != "" && strings.ContainsRune("#&+!", rune(s[0]))
}

// lastParamIf is the last parameter when the message has at least n of them
// (an optional trailing reason), else "".
func lastParamIf(m irc.Message, n int) string {
	if len(m.Params) < n {
		return ""
	}
	return m.Params[len(m.Params)-1]
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
