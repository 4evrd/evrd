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
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"reflect"

	"github.com/4evrd/evrd/internal/history"
	"github.com/4evrd/evrd/internal/irc"
	"github.com/4evrd/evrd/internal/irctls"
	"github.com/4evrd/evrd/internal/links"
	"github.com/4evrd/evrd/internal/messages"
	"github.com/4evrd/evrd/internal/modules"
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
	maxBackoff          = 5 * time.Minute
	// A link up this long was working: when it drops, evrd reconnects
	// quickly. One cut off sooner (killed or banned right after
	// registering) keeps backing off, so it can't reconnect in a loop.
	stableAfter = 2 * time.Minute
	// The least evrd waits after the server said it reconnects too fast,
	// and after a ban (doubling while they go on, up to maxBanWait).
	throttleWait = 2 * time.Minute
	banWait      = 30 * time.Minute
	maxBanWait   = 4 * time.Hour
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
	Status    Status `json:"status"`
	LastError string `json:"lastError,omitempty"`
	// Hint says what LastError means and what to do about it.
	Hint string `json:"hint,omitempty"`
	// LastErrorText and HintText are them in words, to show in the
	// reader's language.
	LastErrorText messages.Text `json:"lastErrorText,omitzero"`
	HintText      messages.Text `json:"hintText,omitzero"`

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
	// Auth is how the services login went on this connection (AuthPending,
	// AuthIdentified…); "" when there was none.
	Auth string `json:"auth,omitempty"`
}

// Event is something worth telling the user about.
type Event struct {
	Kind   string // connected | disconnected | failed (couldn't connect) | netsplit
	Title  string // in English
	Detail string
	At     time.Time
	// TitleText and DetailText are Title and Detail in words, to show in
	// the reader's language.
	TitleText, DetailText messages.Text
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
	onLog      func(LogEntry)
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
	// caps: what the network offers (CAP LS may take several lines), how
	// many of evrd's requests are unanswered, and what it acknowledged
	capsOffered map[string]string // name -> value
	capsWaiting int
	capsListing bool     // a CAP LIST reply is coming in over several lines
	isupport    []string // the server's ISUPPORT tokens, in order
	account     string   // the services account we're logged in to ("" unknown)
	ids         msgids   // the network's msgids for recent lines, and ours
	caps        map[string]bool
	saved       []string        // channels to join on (re)connect, in order
	channels    map[string]bool // channels currently joined
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
	rawSubs     map[chan Line]struct{}
	// resync is what a resumed link asked the server about its channels
	// ("names #chan", "topic #chan"): the answers bring evrd up to date,
	// and aren't passed to clients, which never asked.
	resync map[string]bool
	// line is the ID of the server line being handled, shared by all it
	// records (a QUIT is filed under each channel); recorded says it
	// recorded something. Only the reading goroutine uses them.
	line     history.ID
	recorded bool
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
	rests        rests  // servers resting after refusing this address
	serverPass   string // the current server's PASS (then no SASL-fallback PASS)
	saslOK       bool   // SASL succeeded on this connection (then no NickServ IDENTIFY)
	auth         authState
	serverErr    string // the server's ERROR line on this connection: why it closed
	// the socket this attempt dialed, for ident queries (unset between
	// attempts and on a link resumed from the holder)
	local, remote netip.AddrPort

	// modules (see modules.go): their calls are serialized by modMu
	modMu       sync.Mutex
	mods        []modules.Module
	modCtx      context.Context
	modCfg      map[string]store.ModuleConfig
	modsOff     func() map[string]bool
	idle        func() bool
	version     string
	links       Holder
	linkID      string
	detaching   func() bool
	tryResume   bool // the first connection tries the holder's link first
	resumeAfter <-chan struct{}
}

// maxPending caps the lines held for an upstream that isn't registered yet.
const maxPending = 100

// Holder keeps connections across evrd restarts (*links.Client).
type Holder interface {
	Open(ctx context.Context, link string, tcp net.Conn, tlsOn bool, host string, trust irctls.Trust) (net.Conn, error)
	Resume(ctx context.Context, link string) (net.Conn, *links.Resumed, error)
}

// Options are a connection's hooks and settings.
type Options struct {
	// Persist is called for every message and event (incoming or sent).
	Persist func(history.Message)
	// OnChannels is called with the new saved channel list after the user
	// joins or parts a channel. May be nil.
	OnChannels func([]string)
	// OnEvent is called with link events (connected, lost, netsplit). May be nil.
	OnEvent func(Event)
	// OnLog receives the connection log: evrd's steps and the server's
	// lines that explain a connection (see LogEntry).
	OnLog func(LogEntry)
	// ModulesOff lists modules an admin switched off server-wide.
	ModulesOff func() map[string]bool
	// Dial opens the TCP connection (TLS is added on top). The bouncer uses
	// it to pace connects and pick the source address; nil dials directly.
	Dial func(ctx context.Context, host string, port int) (net.Conn, error)
	// Links, when set, keeps the connection in the connection holder
	// (internal/links), so it survives evrd restarting; LinkID names it.
	// The holder does the TLS handshake; Dial still opens the TCP socket.
	Links  Holder
	LinkID string
	// Detaching reports that evrd itself is stopping: a held connection is
	// left to the holder then, instead of quitting.
	Detaching func() bool
	// ResumeAfter, when set, holds back picking up the holder's link until
	// it's closed (or a while has passed): IRC clients the holder kept are
	// back first, so what the network said meanwhile reaches them live.
	ResumeAfter <-chan struct{}
	// JumpTo, when set, starts at that server (an index into the server
	// list) on a new link, not a held one: ZNC's Jump.
	JumpTo *int
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
		onLog:        opts.OnLog,
		modCfg:       n.Modules,
		modsOff:      opts.ModulesOff,
		dial:         opts.Dial,
		links:        opts.Links,
		linkID:       opts.LinkID,
		detaching:    opts.Detaching,
		resumeAfter:  opts.ResumeAfter,
		resync:       map[string]bool{},
		tryResume:    opts.Links != nil && opts.JumpTo == nil,
		altNick:      orElse(strings.TrimSpace(n.AltNick), strings.TrimSpace(opts.AltNick)),
		quitMsg:      orElse(n.QuitMsg, opts.QuitMsg),
		joinDelay:    time.Duration(n.JoinDelay) * time.Second,
		chanSettings: n.ChannelSettings,
		ctcpReplies:  upperKeys(opts.CTCPReplies),
		codec:        newCodec(n.Encoding),
		rests:        rests{},
		idle:         opts.Idle,
		version:      opts.Version,
		pingEvery:    opts.PingInterval,
		readLimit:    opts.ReadTimeout,
		userAttr:     strconv.FormatInt(n.UserID, 10),
		netAttr:      strconv.FormatInt(n.ID, 10),
		nick:         n.Nick,
		capsOffered:  map[string]string{},
		caps:         map[string]bool{},
		saved:        saved,
		channels:     map[string]bool{},
		members:      map[string]map[string]*Member{},
		topics:       map[string]string{},
		prefixModes:  "ov",
		prefixChars:  "@+",
		health:       Health{Status: StatusDisconnected, Lag: -1},
		subs:         map[chan history.Message]struct{}{},
		rawSubs:      map[chan Line]struct{}{},
	}
	if opts.JumpTo != nil {
		c.serverIdx = *opts.JumpTo
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
	modsChanged := !reflect.DeepEqual(c.modCfg, n.Modules)
	c.mu.Unlock()
	c.flood.set(n.FloodRate, n.FloodBurst)
	if modsChanged {
		c.setModules(n.Modules)
	}
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
// jittered exponential backoff (up to five minutes). A server that refuses
// or throttles this address rests (longer after each ban, see rest.go) while
// the network's other servers are tried; only when all of them rest does
// evrd wait, for the first to come back.
func (c *Conn) Run(ctx context.Context) {
	backoff := time.Second
	lastHint := "" // logged once per kind of failure, not every attempt
	lastFail := "" // a failed attempt is an event once per error, until connected
	servers := c.net.ServerList()
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
		started := time.Now()
		wasConnected, err := c.dialAndServe(ctx)
		if ctx.Err() != nil {
			return
		}
		reason := reasonOf(err)
		msg := reason.English()
		now := time.Now()
		c.mu.Lock()
		server := c.health.Server
		stable := wasConnected && now.Sub(started) >= stableAfter
		if wasConnected {
			lastFail = ""
			if stable && !bannedRE.MatchString(msg) {
				backoff = time.Second // a working link that dropped: retry quickly
			}
		}
		rest := c.rests.ended(server, msg, stable, now)
		wait := c.rests.retryWait(backoff, servers, now)
		_, allBanned, allRest := c.rests.allResting(servers, now)
		c.mu.Unlock()
		hintT := HintText(msg)
		hint := hintT.English()
		if wasConnected {
			c.noteT(true, messages.T("log.disconnected", "reason", reason))
		} else {
			c.noteT(true, messages.T("log.couldntConnect", "reason", reason))
		}
		if hint != "" && hint != lastHint {
			c.noteT(false, hintT)
		}
		lastHint = hint
		next := restNote(c.net.Name, server, msg, rest, wait, len(servers), allRest, allBanned)
		c.noteT(false, next)
		c.update(func(h *Health) {
			h.Status, h.Lag = StatusReconnecting, -1
			h.LastError, h.LastErrorAt, h.Hint = msg, time.Now(), hint
			h.LastErrorText, h.HintText = reason, hintT
			h.Attempts++
			h.RetryIn = wait
		})
		c.log.Warn("upstream disconnected", "network", c.net.Name, "err", err)
		kind := "failed"
		if wasConnected {
			kind = "dropped"
		}
		telemetry.UpstreamDisconnects.Add(ctx, 1, telemetry.Attrs("user", c.userAttr, "network", c.netAttr, "kind", kind))
		switch {
		case wasConnected:
			c.notify(Event{Kind: "disconnected", TitleText: messages.T("event.disconnected", "network", c.net.Name, "reason", reason),
				DetailText: messages.T("event.reconnectingIn", "wait", durText(wait))})
		case msg != lastFail:
			lastFail = msg
			detail := hintT
			if detail.IsZero() {
				detail = next
			}
			c.notify(Event{Kind: "failed", TitleText: messages.T("event.failed", "network", c.net.Name, "reason", reason), DetailText: detail})
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
	if c.links != nil && c.tryResume && c.resumeAfter != nil {
		// the held clients first (before the dial's own time runs)
		select {
		case <-c.resumeAfter:
		case <-time.After(20 * time.Second):
		case <-ctx.Done():
		}
	}
	dctxDial, cancelDial := context.WithTimeout(ctx, 30*time.Second)
	defer cancelDial()
	// Each attempt moves on to the next server (failover, ZNC-style).
	servers := c.net.ServerList()
	if len(servers) == 0 {
		return false, errors.New("no servers configured")
	}
	c.mu.Lock()
	// (skipping servers that rest after refusing this address, see rest.go)
	i := c.rests.pick(servers, c.serverIdx, time.Now())
	sv := servers[i]
	c.serverIdx = i + 1
	c.serverPass = sv.Password
	c.saslOK = false
	c.auth = authState{gen: c.auth.gen + 1}
	c.health.Auth = ""
	c.serverErr = ""
	c.health.Server = net.JoinHostPort(sv.Host, strconv.Itoa(sv.Port))
	c.mu.Unlock()
	var conn net.Conn
	var err error
	var resumed *links.Resumed
	held := false // the connection lives in the holder
	if c.links != nil && c.tryResume {
		// evrd restarted: carry on with the link the holder kept
		c.tryResume = false
		rc, res, rerr := c.links.Resume(dctxDial, c.linkID)
		switch {
		case rerr == nil:
			conn, resumed, held = rc, res, true
			c.noteT(false, messages.T("log.resumed", "nick", res.Nick))
		case !errors.Is(rerr, links.ErrNoLink):
			c.log.Warn("resume held link", "network", c.net.Name, "err", rerr)
		}
	}
	if conn == nil {
		c.noteT(false, messages.T(map[bool]string{true: "log.connectingTLS", false: "log.connecting"}[sv.TLS], "host", sv.Host, "port", sv.Port))
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
		if ra := conn.RemoteAddr(); ra != nil {
			c.noteT(false, messages.T("log.connected", "address", ra.String()))
		}
		// Kept for ident queries: once in the holder, conn is a unix stream.
		c.setAddrs(addrPort(conn.LocalAddr()), addrPort(conn.RemoteAddr()))
		defer c.setAddrs(netip.AddrPort{}, netip.AddrPort{})
		if c.links != nil {
			hc, herr := c.links.Open(dctxDial, c.linkID, conn, sv.TLS, sv.Host, c.trust())
			switch {
			case herr == nil:
				conn, held = hc, true
				if sv.TLS {
					c.noteT(false, messages.T("log.tlsDone"))
				}
			case errors.Is(herr, links.ErrUnavailable):
				// no holder: connect directly, as without one
				c.log.Warn("connection holder unreachable — connecting directly", "network", c.net.Name, "err", herr)
			default:
				var ue *UntrustedCertError
				if errors.As(herr, &ue) {
					c.mu.Lock()
					c.health.UntrustedFingerprint = ue.Fingerprint
					c.mu.Unlock()
				}
				telemetry.UpstreamConnects.Add(ctx, 1, telemetry.Result("error"))
				return false, herr
			}
		}
	}
	if held {
		c.mu.Lock()
		c.health.UntrustedFingerprint = ""
		c.mu.Unlock()
	} else if sv.TLS {
		tc, err := irctls.Handshake(dctxDial, conn, c.tlsConfig(sv.Host))
		if err != nil {
			conn.Close()
			telemetry.UpstreamConnects.Add(ctx, 1, telemetry.Result("error"))
			return false, err
		}
		conn = tc
		c.noteT(false, messages.T("log.tlsDone"))
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
		// Switched off (not just this link ending): say goodbye first —
		// unless evrd itself is stopping and the holder keeps the link.
		if held && c.detaching != nil && c.detaching() {
			conn.Close()
			return
		}
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
	if resumed != nil {
		c.nick = resumed.Nick
	}
	c.triedAlt = false
	c.capsOffered, c.caps, c.capsWaiting = map[string]string{}, map[string]bool{}, 0
	c.isupport, c.account = nil, ""
	c.channels = map[string]bool{}
	c.members = map[string]map[string]*Member{}
	c.topics = map[string]string{}
	c.mu.Unlock()

	// Measure lag every pingEvery; the read deadline below turns silence into
	// a ping timeout.
	go c.pingLoop(dctx)
	c.startModules(dctx)

	if resumed != nil {
		c.resume(resumed)
	} else {
		// Registration. CAP LS holds registration open so we can negotiate
		// SASL; CAP END (sent once auth resolves, see handleCap) completes it.
		if sv.Password != "" {
			c.send("PASS " + sv.Password)
		}
		c.send("CAP LS 302")
		c.send("NICK " + c.net.Nick)
		real := orElse(c.net.Realname, c.net.Nick)
		c.send(fmt.Sprintf("USER %s 0 * :%s", c.Ident(), real))
		c.noteT(false, messages.T("log.signingIn", "nick", c.net.Nick))
	}

	sc := bufio.NewScanner(conn)
	sc.Buffer(make([]byte, 0, 4096), 1<<20)
	conn.SetReadDeadline(time.Now().Add(c.readLimit))
	for sc.Scan() {
		conn.SetReadDeadline(time.Now().Add(c.readLimit))
		raw := sc.Bytes()
		if len(raw) > 1 && (raw[0] == 0x15 || raw[0] == 0x16) && raw[1] == 0x03 {
			// a TLS alert or handshake: this port wants TLS
			c.mu.Lock()
			c.serverErr = "the server speaks TLS on this port"
			c.mu.Unlock()
			break
		}
		line := c.codec.decode(raw)
		m := irc.Parse(line)
		if logged(c.Status() == StatusConnected, m.Command, m.Prefix, m.Params) {
			c.note("server", m.Command == "ERROR" || m.Command == "KILL" || (len(m.Command) == 3 && m.Command[0] >= '4' && m.Command[0] <= '9'), line)
		}
		if m.Command == "ERROR" {
			c.mu.Lock()
			c.serverErr = lastParam(m)
			c.mu.Unlock()
		}
		if m.Command == "PRIVMSG" && len(m.Params) == 2 && c.ctcp(m.Prefix, m.Params[0], m.Params[1]) {
			continue // answered (or blocked) here
		}
		c.line, c.recorded = history.ID{Nanos: time.Now().UnixNano(), Seq: history.NextSeq()}, false
		c.handle(m)
		c.modsLine(m)
		out := Line{Text: line}
		if c.recorded {
			out.ID = c.line
			if id := m.Tags["msgid"]; id != "" {
				c.mu.Lock()
				c.ids.add(id, c.line.String())
				c.mu.Unlock()
			}
		}
		c.line = history.ID{}
		if c.resyncReply(m) {
			continue
		}
		c.broadcastRaw(out) // full-fidelity feed for attached IRC clients
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
	c.mu.Lock()
	if c.serverErr != "" && (errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed)) {
		err = errors.New(c.serverErr) // the server said why
	}
	c.mu.Unlock()
	return !h.ConnectedAt.IsZero() && h.Status == StatusConnected, err
}

// resume picks up a link the holder kept while evrd restarted: already
// registered, so instead of registering it takes the nick and server
// options from before, and asks for the channels' members and topics
// (replies only — nobody sees a JOIN). Lines that arrived meanwhile follow
// on the stream and go through the normal path. The asking goes on in the
// background, at the network's flood rate, while the link is read: the
// holder drops an evrd that stops reading for 30s.
func (c *Conn) resume(r *links.Resumed) {
	c.mu.Lock()
	saved := append([]string(nil), c.saved...)
	away := c.away
	c.health.Status, c.health.ConnectedAt = StatusConnected, r.ConnectedAt
	c.health.Attempts, c.health.RetryIn, c.health.Lag = 0, 0, -1
	for _, ch := range saved {
		c.channels[ch] = true
	}
	pending := c.pending
	c.pending = nil
	c.resync = map[string]bool{}
	c.mu.Unlock()
	for _, l := range r.ISupport {
		c.handle(irc.Parse(c.codec.decode([]byte(l))))
	}
	c.mu.Lock()
	w := c.w
	c.mu.Unlock()
	go func() {
		// (not on a later connection, should this one end first)
		send := func(line string) {
			c.mu.Lock()
			same := c.w == w
			c.mu.Unlock()
			if same {
				c.send(line)
			}
		}
		// the capabilities this connection acknowledged before the restart
		send("CAP LIST")
		for _, ch := range saved {
			c.mu.Lock()
			c.resync["names "+strings.ToLower(ch)] = true
			c.resync["topic "+strings.ToLower(ch)] = true
			c.mu.Unlock()
			send("NAMES " + ch)
			send("TOPIC " + ch)
		}
		if away != "" {
			send("AWAY :" + away)
		}
		for _, l := range pending {
			send(l)
		}
	}()
	c.log.Info("resumed held link", "network", c.net.Name, "nick", r.Nick, "channels", len(saved), "dropped", r.Dropped)
	if r.Dropped > 0 {
		c.notify(Event{Kind: "netsplit", TitleText: messages.T("event.linesMissed", "network", c.net.Name),
			DetailText: messages.T("event.linesDropped", "count", r.Dropped)})
	}
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
		c.mu.Lock()
		c.saslOK = true
		c.health.Auth = AuthIdentified
		c.mu.Unlock()
		c.noteT(false, messages.T("log.saslDone", "account", orElse(c.net.SASLUser, orElse(c.net.Username, c.net.Nick))))
		c.send("CAP END")
	case "902", "904", "905", "906", "908": // SASL failures
		c.log.Warn("SASL failed, continuing unauthenticated", "network", c.net.Name)
		c.saslFailed(messages.Raw(lastParam(m)))
		c.send("CAP END")
	case "901": // RPL_LOGGEDOUT
		c.mu.Lock()
		c.account = ""
		c.mu.Unlock()
	case "900": // RPL_LOGGEDIN: after registration, the answer to IDENTIFY
		if len(m.Params) > 2 {
			c.mu.Lock()
			c.account = m.Params[2]
			c.mu.Unlock()
		}
		if c.Status() == StatusConnected {
			c.mu.Lock()
			gen := c.auth.gen
			c.mu.Unlock()
			c.authDone(gen, true, lastParam(m))
		}
	case "401", "440": // no such nick, services down
		c.servicesMissing(m)
	case "477": // ERR_NEEDREGGEDNICK: a channel for logged-in users only
		if h := HintText(lastParam(m)); !h.IsZero() {
			c.noteT(false, h)
		}
	case "001": // RPL_WELCOME — registration complete
		moduleLogin := c.modsLogIn()
		c.mu.Lock()
		if len(m.Params) > 0 {
			c.nick = m.Params[0] // the server's word on our nick
		}
		saved := append([]string(nil), c.saved...)
		settings := c.chanSettings
		away := c.away
		c.health.Status, c.health.ConnectedAt = StatusConnected, time.Now()
		c.health.Attempts, c.health.RetryIn, c.health.Lag = 0, 0, -1
		c.mu.Unlock()
		// Log in to services (unless SASL did) before joining: some
		// channels need you identified.
		c.login(moduleLogin, joinLines(saved, settings))
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
		c.notify(Event{Kind: "connected", TitleText: messages.T("event.connected", "network", c.net.Name),
			DetailText: messages.T("event.connectedAs", "host", c.net.Host, "port", c.net.Port, "nick", c.Nick())})
		c.noteT(false, messages.T("log.signedIn", "nick", c.Nick()))
		c.modsRegistered()
	case "005": // RPL_ISUPPORT
		if len(m.Params) > 2 {
			c.mu.Lock()
			c.isupport = mergeISupport(c.isupport, m.Params[1:len(m.Params)-1])
			c.mu.Unlock()
		}
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
		if me {
			c.nickChanged(nu)
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
		ghost := c.auth.ghosted && strings.EqualFold(who, c.net.Nick)
		gen := c.auth.gen
		c.mu.Unlock()
		if ghost {
			c.takeNick(gen) // the ghost is gone
		}
		for _, ch := range chans {
			c.event("quit", ch, who, reason, "")
		}
		if split {
			c.notify(Event{Kind: "netsplit", TitleText: messages.T("event.netsplit", "network", c.net.Name), DetailText: messages.Raw(reason)})
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
			// a disabled channel stays saved, to enable again
			c.parted(ch, !c.ChannelSettings(ch).Disabled)
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
	case "RENAME": // draft/channel-rename: RENAME old new [reason]
		if len(m.Params) >= 2 && c.Joined(m.Params[0]) {
			c.renamed(m.Params[0], m.Params[1])
		}
	case "PRIVMSG", "NOTICE":
		if c.fromServices(m.Prefix) {
			c.servicesSaid(m)
		}
		c.handleMessage(m)
	}
}

// event records a channel event (join, part, quit, …) like a message.
func (c *Conn) event(kind, channel, who, text, extra string) {
	telemetry.IRCMessages.Add(context.Background(), 1, telemetry.Attrs("direction", "in", "kind", "event"))
	c.emit(c.stamp(history.Message{
		NetworkID: c.net.ID, Target: channel, Sender: who,
		Kind: kind, Text: text, Extra: extra,
	}))
}

// stamp gives m the time and ID of the server line being handled, so
// clients can match the live line to its history.
func (c *Conn) stamp(m history.Message) history.Message {
	if c.line.IsZero() {
		m.Time, m.Seq = time.Now(), history.NextSeq()
		return m
	}
	m.Time, m.Seq = c.line.Time(), c.line.Seq
	c.recorded = true
	return m
}

// note adds to the connection log. The network's password never shows,
// even when a server or services repeat it.
func (c *Conn) note(from string, bad bool, text string) {
	c.logEntry(LogEntry{At: time.Now(), From: from, Text: text, Bad: bad})
}

// noteT is note for evrd's own words.
func (c *Conn) noteT(bad bool, t messages.Text) {
	c.logEntry(LogEntry{At: time.Now(), From: "evrd", Text: t.English(), Bad: bad, Msg: t})
}

func (c *Conn) logEntry(e LogEntry) {
	if p := c.net.Password; len(p) >= 4 {
		e.Text = strings.ReplaceAll(e.Text, p, "••••••")
		e.Msg = redact(e.Msg, p)
	}
	if c.onLog != nil {
		c.onLog(e)
	}
}

// redact is t with secret blanked out of its arguments.
func redact(t messages.Text, secret string) messages.Text {
	if t.IsZero() {
		return t
	}
	out := messages.Text{Key: t.Key}
	for k, v := range t.Args {
		if out.Args == nil {
			out.Args = map[string]string{}
		}
		out.Args[k] = strings.ReplaceAll(v, secret, "••••••")
	}
	for k, v := range t.Sub {
		if out.Sub == nil {
			out.Sub = map[string]messages.Text{}
		}
		out.Sub[k] = redact(v, secret)
	}
	return out
}

func (c *Conn) notify(e Event) {
	if c.onEvent != nil {
		if e.At.IsZero() {
			e.At = time.Now()
		}
		if e.Title == "" {
			e.Title = e.TitleText.English()
		}
		if e.Detail == "" {
			e.Detail = e.DetailText.English()
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

// handleCap negotiates capabilities: each one in wantCaps the network offers,
// and sasl when there's a password. Each is asked for on its own, so a
// refusal costs only that one. Once all are answered: SASL PLAIN if sasl was
// acknowledged, else a server PASS as the fallback; CAP END follows to
// complete registration. After registration, ACK, LIST (asked for when a
// held link is resumed), NEW and DEL keep the acknowledged set current.
func (c *Conn) handleCap(m irc.Message) {
	if len(m.Params) < 2 {
		return
	}
	sub := strings.ToUpper(m.Params[1])
	caps, more := "", false
	if len(m.Params) > 3 && m.Params[2] == "*" { // more lines follow
		caps, more = m.Params[3], true
	} else if len(m.Params) > 2 {
		caps = m.Params[2]
	}
	names := func() []string {
		var out []string
		for _, f := range strings.Fields(caps) {
			name, _, _ := strings.Cut(f, "=")
			out = append(out, name)
		}
		return out
	}
	registered := c.Status() == StatusConnected
	switch sub {
	case "LIST":
		c.mu.Lock()
		if !c.capsListing {
			c.caps, c.capsListing = map[string]bool{}, true
		}
		for _, n := range names() {
			c.caps[n] = true
		}
		c.capsListing = more
		c.mu.Unlock()
		return
	case "DEL":
		c.mu.Lock()
		for _, n := range names() {
			delete(c.caps, n)
			delete(c.capsOffered, n)
		}
		c.mu.Unlock()
		return
	case "NEW":
		c.mu.Lock()
		c.offer(caps)
		c.mu.Unlock()
		for _, n := range names() {
			if slices.Contains(wantCaps, n) && !c.HasCap(n) {
				c.send("CAP REQ :" + n)
			}
		}
		return
	case "ACK", "NAK":
		c.mu.Lock()
		if sub == "ACK" {
			for _, n := range names() {
				if name, off := strings.CutPrefix(n, "-"); off {
					delete(c.caps, name)
				} else if _, ok := c.capsOffered[n]; ok {
					c.caps[n] = true // only what was offered: some servers ACK oddly
				}
			}
		}
		waiting := c.capsWaiting > 0
		if waiting {
			c.capsWaiting--
		}
		done := waiting && c.capsWaiting == 0
		c.mu.Unlock()
		if registered || !done {
			return
		}
	}
	method := c.net.Auth()
	sasl := c.net.Password != "" && (method == store.AuthAuto || method == store.AuthSASL)
	// without SASL: say so, and (auto only) try the password as PASS
	noSASL := func(why messages.Text) {
		if sasl && method == store.AuthSASL {
			c.saslFailed(why)
		}
		if sasl && method == store.AuthAuto && c.serverPass == "" {
			c.send("PASS " + c.net.Password)
		}
		c.send("CAP END")
	}
	switch sub {
	case "LS":
		if registered {
			return
		}
		c.mu.Lock()
		c.offer(caps)
		var req []string
		if _, ok := c.capsOffered["sasl"]; ok && sasl {
			req = append(req, "sasl")
		}
		for _, n := range wantCaps {
			if _, ok := c.capsOffered[n]; ok {
				req = append(req, n)
			}
		}
		if !more {
			c.capsWaiting = len(req)
		}
		c.mu.Unlock()
		if more {
			return
		}
		if len(req) == 0 {
			noSASL(messages.T("reason.noSASL"))
			return
		}
		for _, n := range req {
			c.send("CAP REQ :" + n)
		}
	case "ACK", "NAK": // the last answer
		switch {
		case c.HasCap("sasl"):
			c.noteT(false, messages.T("log.saslLoggingIn", "account", orElse(c.net.SASLUser, orElse(c.net.Username, c.net.Nick))))
			c.send("AUTHENTICATE PLAIN")
		case sasl && c.offers("sasl"):
			noSASL(messages.T("reason.refusedSASL"))
		default:
			noSASL(messages.T("reason.noSASL"))
		}
	}
}

// offers reports whether the network offered a capability in CAP LS.
func (c *Conn) offers(name string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.capsOffered[name]
	return ok
}

// offer notes the capabilities (with their values) in a CAP LS or NEW.
// Holds c.mu.
func (c *Conn) offer(caps string) {
	for _, f := range strings.Fields(caps) {
		name, value, _ := strings.Cut(f, "=")
		c.capsOffered[name] = value
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
	if sender == "" {
		// A server notice without a prefix ("NOTICE * :*** Looking up your
		// hostname", before registration): file it under the server, with
		// the ones that name it.
		c.mu.Lock()
		host, _, _ := net.SplitHostPort(c.health.Server)
		c.mu.Unlock()
		sender = orElse(host, c.net.Host)
	}

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

	c.emit(c.stamp(history.Message{
		NetworkID: c.net.ID,
		Target:    target,
		Sender:    sender,
		Kind:      kind,
		Text:      text,
	}))
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
// action; other CTCPs (VERSION, PING, …) are sent but not recorded. It
// returns the recorded line's ID (zero when nothing was recorded).
func (c *Conn) SendFrom(origin, command, target, text string) history.ID {
	return c.SendTagged(origin, "", command, target, text)
}

// SendTagged is SendFrom with client-only tags ("+draft/reply=…;…"), sent
// along when the network takes message-tags and passed on live to the
// user's other clients.
func (c *Conn) SendTagged(origin, tags, command, target, text string) history.ID {
	c.sendUser(c.withTags(tags, command+" "+target+" :"+text))
	kind := "message"
	if command == "NOTICE" {
		kind = "notice"
	}
	if strings.HasPrefix(text, "\x01") {
		if !strings.HasPrefix(text, "\x01ACTION ") {
			return history.ID{}
		}
		text = strings.TrimSuffix(strings.TrimPrefix(text, "\x01ACTION "), "\x01")
		kind = "action"
	}
	c.count("out", kind)
	m := history.Message{
		NetworkID: c.net.ID,
		Target:    target,
		Sender:    c.Nick(),
		Kind:      kind,
		Text:      text,
		Time:      time.Now(),
		Seq:       history.NextSeq(),
		Origin:    origin,
		Tags:      tags,
	}
	c.emit(m)
	return m.ID()
}

// SendMultiline sends lines as one draft/multiline batch of PRIVMSGs or
// NOTICEs and records them, a message per line (a concat line joins the one
// before). It returns the recorded messages' IDs.
func (c *Conn) SendMultiline(origin, tags, command, target string, lines []string, concat []bool) []history.ID {
	ref := "ml" + strconv.FormatUint(uint64(history.NextSeq()), 36)
	c.sendUser(c.withTags(tags, "BATCH +"+ref+" draft/multiline "+target))
	var texts []string
	for i, l := range lines {
		tag := "batch=" + ref
		if concat[i] {
			tag += ";draft/multiline-concat"
		}
		c.sendUser("@" + tag + " " + command + " " + target + " :" + l)
		if concat[i] && len(texts) > 0 {
			texts[len(texts)-1] += l
		} else {
			texts = append(texts, l)
		}
	}
	c.sendUser("BATCH -" + ref)
	kind := "message"
	if command == "NOTICE" {
		kind = "notice"
	}
	var ids []history.ID
	for _, text := range texts {
		c.count("out", kind)
		m := history.Message{
			NetworkID: c.net.ID, Target: target, Sender: c.Nick(), Kind: kind, Text: text,
			Time: time.Now(), Seq: history.NextSeq(), Origin: origin, Tags: tags,
		}
		c.emit(m)
		ids = append(ids, m.ID())
	}
	return ids
}

// SendTagMsg sends a TAGMSG (typing, a reaction) with client-only tags when
// the network takes message-tags, and passes it on live to the user's other
// clients. It's never recorded: typing is gone in a moment.
func (c *Conn) SendTagMsg(origin, tags, target string) {
	if tags == "" || !c.HasCap("message-tags") {
		return
	}
	c.sendUser(c.withTags(tags, "TAGMSG "+target))
	c.broadcastRaw(Line{Text: "@" + tags + " :" + c.Nick() + " TAGMSG " + target, Origin: origin})
}

// withTags puts client-only tags on a line for a network that takes them.
func (c *Conn) withTags(tags, line string) string {
	if tags == "" || !c.HasCap("message-tags") {
		return line
	}
	return "@" + c.networkRefs(tags) + " " + line
}

// networkRefs turns the msgids replies and reactions name from evrd's, which
// clients know, into the network's.
func (c *Conn) networkRefs(tags string) string {
	kvs := strings.Split(tags, ";")
	for i, kv := range kvs {
		k, v, _ := strings.Cut(kv, "=")
		if k == "+draft/reply" || k == "+reply" {
			if theirs, ok := c.NetworkMsgID(v); ok {
				kvs[i] = k + "=" + theirs
			}
		}
	}
	return strings.Join(kvs, ";")
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

// Line is a raw server line, with the ID of what it recorded in history
// (zero when it recorded nothing). Origin is set on a line a session sent
// itself (a TAGMSG) and is shown to the others.
type Line struct {
	Text   string
	ID     history.ID
	Origin string
}

// resyncReply reports whether m answers resume's NAMES or TOPIC, noting
// the end of each answer.
func (c *Conn) resyncReply(m irc.Message) bool {
	p := m.Params
	key, end := "", false
	switch {
	case m.Command == "353" && len(p) >= 3: // me = #chan :names
		key = "names " + strings.ToLower(p[2])
	case m.Command == "366" && len(p) >= 2:
		key, end = "names "+strings.ToLower(p[1]), true
	case m.Command == "332" && len(p) >= 2:
		key = "topic " + strings.ToLower(p[1])
	case (m.Command == "333" || m.Command == "331") && len(p) >= 2:
		key, end = "topic "+strings.ToLower(p[1]), true
	default:
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.resync[key] {
		return false
	}
	if end {
		delete(c.resync, key)
	}
	return true
}

// SubscribeRaw returns a channel of raw server lines (verbatim) and an
// unsubscribe function. Used by attached IRC clients for a full-fidelity feed.
func (c *Conn) SubscribeRaw() (<-chan Line, func()) {
	ch := make(chan Line, 256)
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

func (c *Conn) broadcastRaw(line Line) {
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

// Ident is the ident this connection registers with (USER's user part).
func (c *Conn) Ident() string { return orElse(c.net.Username, c.net.Nick) }

// Addrs are the local and remote addresses of the socket the current
// attempt dialed; ok is false between attempts and on a resumed link.
func (c *Conn) Addrs() (local, remote netip.AddrPort, ok bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.local, c.remote, c.remote.IsValid()
}

func (c *Conn) setAddrs(local, remote netip.AddrPort) {
	c.mu.Lock()
	c.local, c.remote = local, remote
	c.mu.Unlock()
}

// addrPort is a socket address as an AddrPort, IPv4 unmapped (invalid if
// it isn't one).
func addrPort(a net.Addr) netip.AddrPort {
	var ap netip.AddrPort
	if t, ok := a.(*net.TCPAddr); ok {
		ap = t.AddrPort()
	} else if a != nil {
		ap, _ = netip.ParseAddrPort(a.String())
	}
	return netip.AddrPortFrom(ap.Addr().Unmap(), ap.Port())
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

func describeErr(err error) string { return reasonOf(err).English() }

// durText is roundDur in words ("a moment" in the reader's language).
func durText(d time.Duration) messages.Text {
	if d < time.Second {
		return messages.T("moment")
	}
	return messages.Raw(roundDur(d))
}

func roundDur(d time.Duration) string {
	switch {
	case d < time.Second:
		return "a moment"
	case d < 10*time.Minute:
		return d.Round(time.Second).String()
	}
	// a long rest: to the minute ("32m", "1h", "4h3m")
	s := strings.TrimSuffix(d.Round(time.Minute).String(), "0s")
	if h, ok := strings.CutSuffix(s, "h0m"); ok {
		return h + "h"
	}
	return s
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
