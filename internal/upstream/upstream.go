// Package upstream maintains a live connection to one IRC network on behalf of
// a user: it dials, registers, stays connected (reconnecting with backoff),
// parses incoming messages, persists them, and fans them out to subscribers
// (the web console). It also sends messages on the user's behalf, and tracks
// its own nick and channels — joins and parts are reported so the network's
// saved channel list follows what the user actually does.
//
// It keeps the live state the web app shows: each channel's topic and members
// (with their op/voice prefixes), the link's health (lag from our own PINGs,
// the last error, reconnect attempts, uptime), and reports notable events
// (connected, link lost, netsplit) to the bouncer's per-user event feed.
package upstream

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/4evrd/evrd/internal/history"
	"github.com/4evrd/evrd/internal/irc"
	"github.com/4evrd/evrd/internal/store"
	"github.com/4evrd/evrd/internal/telemetry"
)

// Status is the lifecycle state of an upstream connection.
type Status string

const (
	StatusDisconnected Status = "disconnected" // switched off
	StatusConnecting   Status = "connecting"   // first attempt
	StatusConnected    Status = "connected"
	StatusReconnecting Status = "reconnecting" // lost, retrying
)

// Defaults for link supervision.
const (
	DefaultPingInterval = 30 * time.Second // how often we measure lag
	DefaultReadTimeout  = 4 * time.Minute  // silence this long is a ping timeout
	maxBackoff          = 60 * time.Second
)

// ErrPingTimeout is the error when the server goes silent.
var ErrPingTimeout = errors.New("ping timeout")

// Member is one nick in a channel, with its highest status prefix ("@", "+"…).
type Member struct {
	Nick   string `json:"nick"`
	Prefix string `json:"prefix"`
}

// Health is a connection's link state, for the web app and metrics.
type Health struct {
	Status      Status        `json:"status"`
	LastError   string        `json:"lastError,omitempty"`
	LastErrorAt time.Time     `json:"lastErrorAt"`
	Attempts    int           `json:"attempts"` // failed attempts since last connected
	RetryIn     time.Duration `json:"retryIn"`  // wait before the next attempt
	ConnectedAt time.Time     `json:"connectedAt"`
	Lag         time.Duration `json:"lag"` // -1 while unknown
	// Server is the host:port of the server in use (or being tried).
	Server string `json:"server"`
	// UntrustedFingerprint is the SHA-256 of the last certificate refused,
	// so the user can choose to trust it.
	UntrustedFingerprint string `json:"untrustedFingerprint,omitempty"`
}

// Event is something worth telling the user about.
type Event struct {
	Kind   string // connected | disconnected | netsplit
	Title  string
	Detail string
	At     time.Time
}

// Conn is a single upstream IRC connection.
type Conn struct {
	net     store.Network
	log     *slog.Logger
	persist func(history.Message)
	// onChannels is called with the new saved channel list after the user
	// joins or parts a channel.
	onChannels func([]string)
	onEvent    func(Event)
	dial       func(ctx context.Context, host string, port int) (net.Conn, error)
	altNick    string
	pingEvery  time.Duration
	readLimit  time.Duration
	userAttr   string // user id, for metric labels
	netAttr    string // network id, for metric labels

	mu       sync.Mutex
	w        io.Writer
	nick     string
	triedAlt bool
	saved    []string        // channels to join on (re)connect, in order
	channels map[string]bool // channels currently joined
	// members is who's in each joined channel (lowercased channel ->
	// lowercased nick), so a QUIT or NICK — which name no channel — is filed
	// under every channel the user shared with that nick.
	members map[string]map[string]*Member
	topics  map[string]string // lowercased channel -> topic
	// prefixes maps channel modes to their nick prefix, highest first, from
	// the server's ISUPPORT PREFIX (default (ov)@+).
	prefixModes string
	prefixChars string
	away        string // the away message to keep set; "" = here
	health      Health
	lagToken    string
	lagSent     time.Time
	splits      splitTracker
	subs        map[chan history.Message]struct{}
	rawSubs     map[chan string]struct{}
	// pending holds lines users send before registration completes (a client
	// attaching while the upstream is still connecting); 001 flushes them.
	pending []string

	// Settings that can change while connected (Reconfigure).
	quitMsg      string
	joinDelay    time.Duration
	chanSettings map[string]store.ChannelSettings
	ctcpReplies  map[string]string
	flood        flood
	codec        codec
	serverIdx    int
	serverPass   string // the current server's PASS (then no SASL-fallback PASS)
	idle         func() bool
	version      string
}

// maxPending caps the lines held for an upstream that isn't registered yet.
const maxPending = 100

// Options are a connection's hooks and settings.
type Options struct {
	// Persist is called for every message and event (incoming or sent).
	Persist func(history.Message)
	// OnChannels is called with the new saved channel list after the user
	// joins or parts a channel. May be nil.
	OnChannels func([]string)
	// OnEvent is called with link events (connected, lost, netsplit). May be nil.
	OnEvent func(Event)
	// Dial opens the TCP connection (TLS is added on top). The bouncer uses
	// it to pace connects and pick the source address; nil dials directly.
	Dial func(ctx context.Context, host string, port int) (net.Conn, error)
	// AltNick is tried when the nick is taken while registering (the
	// network's own alternate nick wins).
	AltNick string
	// QuitMsg is sent when the network is switched off (the network's own
	// quit message wins); empty sends a plain QUIT.
	QuitMsg string
	// CTCPReplies answers CTCP requests with fixed text; "" blocks one.
	CTCPReplies map[string]string
	// Idle reports whether no client is attached, in which case evrd itself
	// answers CTCP VERSION, PING and TIME. Nil: never.
	Idle func() bool
	// Version is evrd's version, for CTCP VERSION.
	Version string
	// PingInterval and ReadTimeout override the link supervision defaults.
	PingInterval, ReadTimeout time.Duration
}

// New creates a connection for a network.
func New(n store.Network, log *slog.Logger, opts Options) *Conn {
	var saved []string
	for _, ch := range n.Channels {
		if ch = strings.TrimSpace(ch); ch != "" {
			saved = append(saved, ch)
		}
	}
	c := &Conn{
		net:          n,
		log:          log,
		persist:      opts.Persist,
		onChannels:   opts.OnChannels,
		onEvent:      opts.OnEvent,
		dial:         opts.Dial,
		altNick:      orElse(strings.TrimSpace(n.AltNick), strings.TrimSpace(opts.AltNick)),
		quitMsg:      orElse(n.QuitMsg, opts.QuitMsg),
		joinDelay:    time.Duration(n.JoinDelay) * time.Second,
		chanSettings: n.ChannelSettings,
		ctcpReplies:  upperKeys(opts.CTCPReplies),
		codec:        newCodec(n.Encoding),
		idle:         opts.Idle,
		version:      opts.Version,
		pingEvery:    opts.PingInterval,
		readLimit:    opts.ReadTimeout,
		userAttr:     strconv.FormatInt(n.UserID, 10),
		netAttr:      strconv.FormatInt(n.ID, 10),
		nick:         n.Nick,
		saved:        saved,
		channels:     map[string]bool{},
		members:      map[string]map[string]*Member{},
		topics:       map[string]string{},
		prefixModes:  "ov",
		prefixChars:  "@+",
		health:       Health{Status: StatusDisconnected, Lag: -1},
		subs:         map[chan history.Message]struct{}{},
		rawSubs:      map[chan string]struct{}{},
	}
	if c.pingEvery <= 0 {
		c.pingEvery = DefaultPingInterval
	}
	if c.readLimit <= 0 {
		c.readLimit = DefaultReadTimeout
	}
	c.flood.set(n.FloodRate, n.FloodBurst)
	return c
}

// Reconfigure applies settings that don't need a new connection: quit
// message, join delay, channel options, flood protection and CTCP replies.
func (c *Conn) Reconfigure(n store.Network, defaultQuit string, ctcp map[string]string) {
	c.mu.Lock()
	c.quitMsg = orElse(n.QuitMsg, defaultQuit)
	c.joinDelay = time.Duration(n.JoinDelay) * time.Second
	c.chanSettings = n.ChannelSettings
	c.ctcpReplies = upperKeys(ctcp)
	c.mu.Unlock()
	c.flood.set(n.FloodRate, n.FloodBurst)
}

// ChannelSettings returns a channel's options.
func (c *Conn) ChannelSettings(ch string) store.ChannelSettings {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.chanSettings[strings.ToLower(ch)]
}

// SetChannelSettings changes a channel's options (detach / attach) and
// returns the new map for saving.
func (c *Conn) SetChannelSettings(ch string, f func(*store.ChannelSettings)) map[string]store.ChannelSettings {
	c.mu.Lock()
	defer c.mu.Unlock()
	m := make(map[string]store.ChannelSettings, len(c.chanSettings)+1)
	for k, v := range c.chanSettings {
		m[k] = v
	}
	cs := m[strings.ToLower(ch)]
	f(&cs)
	if cs == (store.ChannelSettings{}) {
		delete(m, strings.ToLower(ch))
	} else {
		m[strings.ToLower(ch)] = cs
	}
	c.chanSettings = m
	return m
}

func upperKeys(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[strings.ToUpper(strings.TrimSpace(k))] = v
	}
	return out
}

// Run keeps the connection alive until ctx is cancelled, reconnecting with
// jittered exponential backoff (up to a minute).
func (c *Conn) Run(ctx context.Context) {
	backoff := time.Second
	defer func() {
		c.mu.Lock()
		c.health.Status, c.health.Lag, c.health.RetryIn = StatusDisconnected, -1, 0
		c.pending = nil // switched off: nothing will carry them
		c.mu.Unlock()
	}()
	for ctx.Err() == nil {
		c.update(func(h *Health) {
			if h.Attempts == 0 {
				h.Status = StatusConnecting
			} else {
				h.Status = StatusReconnecting
			}
			h.RetryIn = 0
		})
		wasConnected, err := c.dialAndServe(ctx)
		if ctx.Err() != nil {
			return
		}
		if wasConnected {
			backoff = time.Second // a working link that dropped: retry quickly
		}
		// Jitter, so connections dropped together don't all retry together.
		wait := backoff/2 + time.Duration(rand.Int64N(int64(backoff)))
		msg := describeErr(err)
		c.update(func(h *Health) {
			h.Status, h.Lag = StatusReconnecting, -1
			h.LastError, h.LastErrorAt = msg, time.Now()
			h.Attempts++
			h.RetryIn = wait
		})
		c.log.Warn("upstream disconnected", "network", c.net.Name, "err", err)
		telemetry.UpstreamDisconnects.Add(ctx, 1, telemetry.Attrs("user", c.userAttr, "network", c.netAttr))
		if wasConnected {
			c.notify(Event{Kind: "disconnected", Title: c.net.Name + " " + msg, Detail: fmt.Sprintf("Reconnecting in %s", roundDur(wait))})
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		if backoff < maxBackoff {
			backoff = min(backoff*2, maxBackoff)
		}
	}
}

// dialAndServe runs one connection. It reports whether registration
// completed (the link was up) and why it ended.
func (c *Conn) dialAndServe(ctx context.Context) (bool, error) {
	dctxDial, cancelDial := context.WithTimeout(ctx, 30*time.Second)
	defer cancelDial()
	// Each attempt moves on to the next server (failover, ZNC-style).
	servers := c.net.ServerList()
	if len(servers) == 0 {
		return false, errors.New("no servers configured")
	}
	c.mu.Lock()
	sv := servers[c.serverIdx%len(servers)]
	c.serverIdx++
	c.serverPass = sv.Password
	c.health.Server = net.JoinHostPort(sv.Host, strconv.Itoa(sv.Port))
	c.mu.Unlock()
	var conn net.Conn
	var err error
	if c.dial != nil {
		conn, err = c.dial(dctxDial, sv.Host, sv.Port)
	} else {
		d := &net.Dialer{}
		conn, err = d.DialContext(dctxDial, "tcp", net.JoinHostPort(sv.Host, strconv.Itoa(sv.Port)))
	}
	if err != nil {
		telemetry.UpstreamConnects.Add(ctx, 1, telemetry.Result("error"))
		return false, err
	}
	if sv.TLS {
		tc := tls.Client(conn, c.tlsConfig(sv.Host))
		if err := tc.HandshakeContext(dctxDial); err != nil {
			conn.Close()
			telemetry.UpstreamConnects.Add(ctx, 1, telemetry.Result("error"))
			return false, err
		}
		conn = tc
		c.mu.Lock()
		c.health.UntrustedFingerprint = ""
		c.mu.Unlock()
	}
	telemetry.UpstreamConnects.Add(ctx, 1, telemetry.Result("ok"))
	defer conn.Close()

	// Close the socket when this dial's context ends (ctx cancel or return).
	dctx, dcancel := context.WithCancel(ctx)
	defer dcancel()
	go func() {
		<-dctx.Done()
		// Switched off (not just this link ending): say goodbye first.
		if ctx.Err() != nil && c.Status() == StatusConnected {
			c.mu.Lock()
			quit := c.quitMsg
			c.mu.Unlock()
			conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
			if quit != "" {
				io.WriteString(conn, c.codec.encode("QUIT :"+quit)+"\r\n")
			} else {
				io.WriteString(conn, "QUIT\r\n")
			}
		}
		conn.Close()
	}()

	c.mu.Lock()
	c.w = conn
	c.nick = c.net.Nick
	c.triedAlt = false
	c.channels = map[string]bool{}
	c.members = map[string]map[string]*Member{}
	c.topics = map[string]string{}
	c.mu.Unlock()

	// Measure lag every pingEvery; the read deadline below turns silence into
	// a ping timeout.
	go c.pingLoop(dctx)

	// Registration. CAP LS holds registration open so we can negotiate SASL;
	// CAP END (sent once auth resolves, see handleCap) completes it.
	if sv.Password != "" {
		c.send("PASS " + sv.Password)
	}
	c.send("CAP LS 302")
	c.send("NICK " + c.net.Nick)
	user := orElse(c.net.Username, c.net.Nick)
	real := orElse(c.net.Realname, c.net.Nick)
	c.send(fmt.Sprintf("USER %s 0 * :%s", user, real))

	sc := bufio.NewScanner(conn)
	sc.Buffer(make([]byte, 0, 4096), 1<<20)
	conn.SetReadDeadline(time.Now().Add(c.readLimit))
	for sc.Scan() {
		conn.SetReadDeadline(time.Now().Add(c.readLimit))
		line := c.codec.decode(sc.Bytes())
		m := irc.Parse(line)
		if m.Command == "PRIVMSG" && len(m.Params) == 2 && c.ctcp(m.Prefix, m.Params[0], m.Params[1]) {
			continue // answered (or blocked) here
		}
		c.handle(m)
		c.broadcastRaw(line) // full-fidelity feed for attached IRC clients
	}

	c.mu.Lock()
	c.w = nil
	c.mu.Unlock()
	h := c.Health()
	err = sc.Err()
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		err = ErrPingTimeout
	}
	if err == nil {
		err = io.EOF
	}
	return !h.ConnectedAt.IsZero() && h.Status == StatusConnected, err
}

func (c *Conn) pingLoop(ctx context.Context) {
	t := time.NewTicker(c.pingEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if c.Status() != StatusConnected {
				continue
			}
			tok := "evrd-lag-" + strconv.FormatInt(time.Now().UnixNano(), 36)
			c.mu.Lock()
			c.lagToken, c.lagSent = tok, time.Now()
			c.mu.Unlock()
			c.send("PING :" + tok)
		}
	}
}

func (c *Conn) handle(m irc.Message) {
	switch m.Command {
	case "PING":
		c.send("PONG :" + lastParam(m))
	case "PONG":
		c.mu.Lock()
		if c.lagToken != "" && lastParam(m) == c.lagToken {
			c.health.Lag = time.Since(c.lagSent)
			c.lagToken = ""
		}
		c.mu.Unlock()
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
		settings, delay0 := c.chanSettings, c.joinDelay
		away := c.away
		c.health.Status, c.health.ConnectedAt = StatusConnected, time.Now()
		c.health.Attempts, c.health.RetryIn, c.health.Lag = 0, 0, -1
		c.mu.Unlock()
		joins := joinLines(saved, settings)
		if delay0 > 0 {
			go func() {
				time.Sleep(delay0)
				for _, l := range joins {
					c.send(l)
				}
			}()
		} else {
			for _, l := range joins {
				c.send(l)
			}
		}
		if away != "" {
			c.send("AWAY :" + away)
		}
		c.mu.Lock()
		pending := c.pending
		c.pending = nil
		c.mu.Unlock()
		for _, l := range pending {
			c.send(l)
		}
		c.notify(Event{Kind: "connected", Title: "Connected to " + c.net.Name, Detail: fmt.Sprintf("%s:%d as %s", c.net.Host, c.net.Port, c.Nick())})
	case "005": // RPL_ISUPPORT
		for _, tok := range m.Params[1:] {
			if v, ok := strings.CutPrefix(tok, "PREFIX=("); ok {
				if modes, chars, ok := strings.Cut(v, ")"); ok && len(modes) == len(chars) {
					c.mu.Lock()
					c.prefixModes, c.prefixChars = modes, chars
					c.mu.Unlock()
				}
			}
		}
	case "433": // ERR_NICKNAMEINUSE
		// While registering, try the alternate nick, then variants. Once
		// connected, it's the answer to a client's NICK and goes back to that
		// client as-is.
		if c.Status() == StatusConnected {
			return
		}
		c.mu.Lock()
		if !c.triedAlt && c.altNick != "" && !strings.EqualFold(c.altNick, c.nick) {
			c.nick, c.triedAlt = c.altNick, true
		} else {
			c.nick += "_"
		}
		nick := c.nick
		c.mu.Unlock()
		c.send("NICK " + nick)
	case "332": // RPL_TOPIC: me #chan :topic
		if len(m.Params) >= 3 {
			c.mu.Lock()
			c.topics[strings.ToLower(m.Params[1])] = m.Params[2]
			c.mu.Unlock()
		}
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
			set := c.members[strings.ToLower(ch)]
			prefix := ""
			if mem := set[strings.ToLower(old)]; mem != nil {
				prefix = mem.Prefix
			}
			delete(set, strings.ToLower(old))
			if set != nil {
				set[strings.ToLower(nu)] = &Member{Nick: nu, Prefix: prefix}
			}
		}
		c.mu.Unlock()
		for _, ch := range chans {
			c.event("nick", ch, old, "", nu)
		}
	case "QUIT":
		who := nickOf(m.Prefix)
		reason := lastParamIf(m, 1)
		c.mu.Lock()
		chans := c.channelsOf(who, false)
		for _, ch := range chans {
			delete(c.members[strings.ToLower(ch)], strings.ToLower(who))
		}
		split := len(chans) > 0 && isSplitReason(reason) && c.splits.quit(time.Now())
		c.mu.Unlock()
		for _, ch := range chans {
			c.event("quit", ch, who, reason, "")
		}
		if split {
			c.notify(Event{Kind: "netsplit", Title: "Netsplit on " + c.net.Name, Detail: reason})
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
			c.mu.Lock()
			c.topics[strings.ToLower(m.Params[0])] = m.Params[1]
			c.mu.Unlock()
			c.event("topic", m.Params[0], nickOf(m.Prefix), m.Params[1], "")
		}
	case "MODE":
		if len(m.Params) >= 2 && isChannel(m.Params[0]) {
			c.mu.Lock()
			c.applyModes(m.Params[0], m.Params[1], m.Params[2:])
			c.mu.Unlock()
			c.event("mode", m.Params[0], nickOf(m.Prefix), strings.Join(m.Params[1:], " "), "")
		}
	case "PRIVMSG", "NOTICE":
		c.handleMessage(m)
	}
}

// event records a channel event (join, part, quit, …) like a message.
func (c *Conn) event(kind, channel, who, text, extra string) {
	telemetry.IRCMessages.Add(context.Background(), 1, telemetry.Attrs("direction", "in", "kind", "event"))
	c.emit(history.Message{
		NetworkID: c.net.ID, Target: channel, Sender: who,
		Kind: kind, Text: text, Extra: extra, Time: time.Now(),
	})
}

func (c *Conn) notify(e Event) {
	if c.onEvent != nil {
		if e.At.IsZero() {
			e.At = time.Now()
		}
		c.onEvent(e)
	}
}

// addMember adds a NAMES entry ("@nick", "nick!user@host", …). Holds c.mu.
func (c *Conn) addMember(channel, entry string) {
	prefix := ""
	for entry != "" && strings.ContainsRune(c.prefixChars+"~&@%+", rune(entry[0])) {
		if prefix == "" {
			prefix = entry[:1] // NAMES lists the highest first
		}
		entry = entry[1:]
	}
	nick := nickOf(entry)
	if nick == "" {
		return
	}
	key := strings.ToLower(channel)
	if c.members[key] == nil {
		c.members[key] = map[string]*Member{}
	}
	c.members[key][strings.ToLower(nick)] = &Member{Nick: nick, Prefix: prefix}
}

// applyModes updates member prefixes for a channel MODE change. Holds c.mu.
func (c *Conn) applyModes(channel, modes string, args []string) {
	set := c.members[strings.ToLower(channel)]
	adding := true
	for _, m := range modes {
		switch m {
		case '+':
			adding = true
			continue
		case '-':
			adding = false
			continue
		}
		i := strings.IndexRune(c.prefixModes, m)
		takesArg := i >= 0 || strings.ContainsRune("bekl", m) && (adding || m != 'l')
		if !takesArg || len(args) == 0 {
			continue
		}
		arg := args[0]
		args = args[1:]
		if i < 0 || set == nil {
			continue
		}
		mem := set[strings.ToLower(arg)]
		if mem == nil {
			continue
		}
		p := c.prefixChars[i : i+1]
		switch {
		case adding && (mem.Prefix == "" || strings.Index(c.prefixChars, p) < strings.Index(c.prefixChars, mem.Prefix)):
			mem.Prefix = p
		case !adding && mem.Prefix == p:
			mem.Prefix = "" // without multi-prefix we can't know a lower one
		}
	}
}

// channelsOf lists the joined channels a nick is in (all of them for our own
// nick). Holds c.mu.
func (c *Conn) channelsOf(nick string, me bool) []string {
	var out []string
	for ch := range c.channels {
		if me || c.members[strings.ToLower(ch)][strings.ToLower(nick)] != nil {
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
	delete(c.topics, strings.ToLower(ch))
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
			if c.net.Password != "" && c.serverPass == "" {
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
		if c.net.Password != "" && c.serverPass == "" {
			c.send("PASS " + c.net.Password)
		}
		c.send("CAP END")
	}
}

// sendSASL responds to an AUTHENTICATE + challenge with a SASL PLAIN payload:
// base64("\0" + authcid + "\0" + password).
func (c *Conn) sendSASL() {
	authcid := orElse(c.net.SASLUser, orElse(c.net.Username, c.net.Nick))
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

	c.count("in", kind)

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

// count records a relayed line, both server-wide and per user and network.
func (c *Conn) count(direction, kind string) {
	ctx := context.Background()
	telemetry.IRCMessages.Add(ctx, 1, telemetry.Attrs("direction", direction, "kind", kind))
	telemetry.Lines.Add(ctx, 1, telemetry.Attrs("user", c.userAttr, "network", c.netAttr, "direction", direction))
}

// SendMessage sends a PRIVMSG to target and echoes it locally so the sender
// sees their own line.
func (c *Conn) SendMessage(target, text string) { c.SendFrom("", "PRIVMSG", target, text) }

// SendFrom sends a PRIVMSG or NOTICE and records it like an incoming message,
// tagged with the originating session id so other sessions can show it live
// while the originator avoids a double. A CTCP ACTION is recorded as an
// action; other CTCPs (VERSION, PING, …) are sent but not recorded.
func (c *Conn) SendFrom(origin, command, target, text string) {
	c.sendUser(command + " " + target + " :" + text)
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
	c.count("out", kind)
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

// SetAway marks the user away with msg ("" comes back), now and after any
// reconnect.
func (c *Conn) SetAway(msg string) {
	c.mu.Lock()
	changed := c.away != msg
	c.away = msg
	c.mu.Unlock()
	if !changed || c.Status() != StatusConnected {
		return
	}
	if msg == "" {
		c.send("AWAY")
	} else {
		c.send("AWAY :" + msg)
	}
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
func (c *Conn) SendRaw(line string) { c.sendUser(line) }

// sendUser sends a line on a user's behalf. Before the server has welcomed us
// it's held (lines written then would be lost, or land in the middle of
// registration) and sent once registration completes.
func (c *Conn) sendUser(line string) {
	c.mu.Lock()
	if c.health.Status != StatusConnected {
		if len(c.pending) < maxPending {
			c.pending = append(c.pending, line)
		}
		c.mu.Unlock()
		return
	}
	c.mu.Unlock()
	c.send(line)
}

func (c *Conn) send(line string) {
	c.mu.Lock()
	w := c.w
	registered := c.health.Status == StatusConnected
	c.mu.Unlock()
	if w == nil {
		return
	}
	if registered && !exempt(line) {
		c.flood.wait()
	}
	io.WriteString(w, c.codec.encode(line)+"\r\n")
}

func (c *Conn) update(f func(*Health)) {
	c.mu.Lock()
	f(&c.health)
	c.mu.Unlock()
}

// Status returns the current connection state.
func (c *Conn) Status() Status {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.health.Status
}

// Health returns the link's state.
func (c *Conn) Health() Health {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.health
}

// NetworkID is the network this connection serves.
func (c *Conn) NetworkID() int64 { return c.net.ID }

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

// Joined reports whether we're in a channel right now.
func (c *Conn) Joined(channel string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for ch := range c.channels {
		if strings.EqualFold(ch, channel) {
			return true
		}
	}
	return false
}

// Topic is a joined channel's topic.
func (c *Conn) Topic(channel string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.topics[strings.ToLower(channel)]
}

// Members lists a joined channel's members: highest status first, then by nick.
func (c *Conn) Members(channel string) []Member {
	c.mu.Lock()
	defer c.mu.Unlock()
	set := c.members[strings.ToLower(channel)]
	out := make([]Member, 0, len(set))
	for _, m := range set {
		out = append(out, *m)
	}
	rank := func(p string) int {
		if p == "" {
			return len(c.prefixChars)
		}
		if i := strings.Index(c.prefixChars, p); i >= 0 {
			return i
		}
		return len(c.prefixChars)
	}
	sort.Slice(out, func(i, j int) bool {
		if ri, rj := rank(out[i].Prefix), rank(out[j].Prefix); ri != rj {
			return ri < rj
		}
		return strings.ToLower(out[i].Nick) < strings.ToLower(out[j].Nick)
	})
	return out
}

// MemberCount is the number of members in a joined channel.
func (c *Conn) MemberCount(channel string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.members[strings.ToLower(channel)])
}

// --- netsplits -----------------------------------------------------------

// A netsplit shows up as many QUITs whose reason is two server names
// ("hub.example.net leaf.example.net").
var splitRe = regexp.MustCompile(`^[\w.-]+\.[\w-]+ [\w.-]+\.[\w-]+$`)

func isSplitReason(r string) bool { return splitRe.MatchString(r) }

// splitTracker reports a split once: when enough split QUITs arrive close
// together, and not again until things have been quiet a while.
type splitTracker struct {
	recent   []time.Time
	reported time.Time
}

func (s *splitTracker) quit(at time.Time) bool {
	kept := s.recent[:0]
	for _, t := range s.recent {
		if at.Sub(t) < 10*time.Second {
			kept = append(kept, t)
		}
	}
	s.recent = append(kept, at)
	if len(s.recent) >= 5 && at.Sub(s.reported) > 5*time.Minute {
		s.reported = at
		return true
	}
	return false
}

// --- helpers -------------------------------------------------------------

func describeErr(err error) string {
	var dnsErr *net.DNSError
	var opErr *net.OpError
	switch {
	case errors.Is(err, ErrPingTimeout):
		return "ping timeout"
	case errors.Is(err, io.EOF):
		return "connection closed by server"
	case errors.As(err, &dnsErr):
		return "can't resolve " + dnsErr.Name
	case errors.Is(err, context.DeadlineExceeded):
		return "connection timed out"
	case errors.As(err, &opErr) && opErr.Op == "dial":
		if strings.Contains(err.Error(), "refused") {
			return "connection refused"
		}
		return "can't connect"
	case err == nil:
		return "disconnected"
	}
	var cert *tls.CertificateVerificationError
	if errors.As(err, &cert) {
		return "TLS certificate rejected"
	}
	return err.Error()
}

func roundDur(d time.Duration) string {
	if d < time.Second {
		return "a moment"
	}
	return d.Round(time.Second).String()
}

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
