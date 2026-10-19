package client

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/4evrd/evrd/internal/accounts"
	"github.com/4evrd/evrd/internal/bouncer"
	"github.com/4evrd/evrd/internal/history"
	"github.com/4evrd/evrd/internal/irc"
	"github.com/4evrd/evrd/internal/store"
	"github.com/4evrd/evrd/internal/telemetry"
	"github.com/4evrd/evrd/internal/upstream"
)

var sessionSeq atomic.Int64

type session struct {
	id   string
	srv  *Server
	conn net.Conn
	r    *bufio.Reader

	wmu sync.Mutex
	w   *bufio.Writer

	// registration state
	pass           string
	nick           string
	user           string
	capNegotiating bool
	caps           map[string]bool
	regLines       []string // PASS/NICK/USER as sent, replayed when forwarding
	internal       bool     // forwarded here by another node (already vetted)

	// Attached state. After registration, only the bridge goroutine touches
	// these, so jumping networks needs no locking.
	uid      int64
	acct     store.User // as loaded at attach (LastSeenAt is the previous visit)
	net      store.Network
	up       *upstream.Conn
	unick    string
	raw      <-chan string
	msgs     <-chan history.Message
	unsubRaw func()
	unsubMsg func()
	release  func() // frees this client's slot in the user's client limit
	att      *bouncer.Attachment
	fromIP   string // the client's address when another node forwarded it

	// web is set for a *status command from the web chat: replies go to it
	// instead of an IRC connection, and IRC-only commands say so.
	web func(string)
}

// supportedCaps are the IRCv3 capabilities a client may request.
var supportedCaps = map[string]bool{"echo-message": true, "server-time": true}

func newSession(srv *Server, conn net.Conn) *session {
	return &session{
		id:   strconv.FormatInt(sessionSeq.Add(1), 10),
		srv:  srv,
		conn: conn,
		r:    bufio.NewReaderSize(conn, 4096),
		w:    bufio.NewWriter(conn),
		caps: map[string]bool{},
	}
}

// maxLine bounds one IRC line (512 bytes plus up to 8 KiB of tags, with room).
const maxLine = 16 << 10

// readLine reads one line without its line ending.
func (se *session) readLine() (string, error) {
	var line []byte
	for {
		chunk, err := se.r.ReadSlice('\n')
		line = append(line, chunk...)
		if len(line) > maxLine {
			return "", errors.New("line too long")
		}
		if err == bufio.ErrBufferFull {
			continue
		}
		if err != nil {
			return "", err
		}
		return strings.TrimRight(string(line), "\r\n"), nil
	}
}

func (se *session) serve(ctx context.Context) {
	defer se.conn.Close()
	if se.internal && !se.acceptForwarded() {
		return
	}

	// Registration: gather CAP / PASS / NICK / USER.
	for {
		line, err := se.readLine()
		if err != nil {
			return
		}
		m := irc.Parse(line)
		switch m.Command {
		case "CAP":
			se.handleCap(m)
		case "PASS":
			if len(m.Params) > 0 {
				se.pass = m.Params[0]
				se.regLines = append(se.regLines, line)
			}
		case "NICK":
			if len(m.Params) > 0 {
				se.nick = m.Params[0]
				se.regLines = append(se.regLines, line)
			}
		case "USER":
			if len(m.Params) > 0 {
				se.user = m.Params[0]
				se.regLines = append(se.regLines, line)
			}
		case "QUIT":
			return
		}
		if se.nick != "" && se.user != "" && !se.capNegotiating {
			break
		}
	}
	if se.forwardIfElsewhere(ctx) {
		return
	}
	if !se.attach() {
		return
	}
	defer se.release()
	defer se.unsubscribe()
	se.bridge(ctx)
}

// handleCap answers CAP during and after registration. A REQ is all or
// nothing: ACK only when every capability in it is supported.
func (se *session) handleCap(m irc.Message) {
	if len(m.Params) == 0 {
		return
	}
	nick := orElse(se.nick, "*")
	switch strings.ToUpper(m.Params[0]) {
	case "LS":
		if se.up == nil {
			se.capNegotiating = true
		}
		se.send(fmt.Sprintf(":%s CAP %s LS :echo-message server-time", serverName, nick))
	case "LIST":
		var on []string
		for c := range se.caps {
			on = append(on, c)
		}
		sort.Strings(on)
		se.send(fmt.Sprintf(":%s CAP %s LIST :%s", serverName, nick, strings.Join(on, " ")))
	case "REQ":
		if len(m.Params) < 2 {
			return
		}
		req := strings.Fields(m.Params[1])
		for _, c := range req {
			if !supportedCaps[strings.TrimPrefix(c, "-")] {
				se.send(fmt.Sprintf(":%s CAP %s NAK :%s", serverName, nick, m.Params[1]))
				return
			}
		}
		for _, c := range req {
			if name, off := strings.CutPrefix(c, "-"); off {
				delete(se.caps, name)
			} else {
				se.caps[c] = true
			}
		}
		se.send(fmt.Sprintf(":%s CAP %s ACK :%s", serverName, nick, m.Params[1]))
	case "END":
		se.capNegotiating = false
	}
}

// attach authenticates and binds the session to a network, then sends the
// welcome burst, channel state, and backlog.
func (se *session) attach() bool {
	login, password, ok := strings.Cut(se.pass, ":")
	if !ok || password == "" {
		// Never log PASS itself: without a colon it may be just the password.
		se.srv.log.Info("irc sign-in without a password", "ip", se.clientIP(), "pass_sent", se.pass != "")
		se.numeric("464", ":Password required — use PASS <user>[/<network>]:<password>")
		return false
	}
	username, network, _ := strings.Cut(login, "/")

	// The secret is the account password or one of the user's IRC client
	// passwords (the only option for accounts that sign in through SSO).
	u, device, err := se.srv.acc.IRCLogin(username, password)
	switch {
	case errors.Is(err, accounts.ErrSuspended):
		telemetry.IRCLogins.Add(context.Background(), 1, telemetry.Result("suspended"))
		se.loginFailed(username, network, "suspended")
		se.numeric("465", ":This account is suspended")
		return false
	case errors.Is(err, accounts.ErrNeedsClientPassword):
		telemetry.IRCLogins.Add(context.Background(), 1, telemetry.Result("invalid"))
		se.loginFailed(username, network, "sso_needs_client_password")
		se.numeric("464", ":Invalid credentials — this account signs in with SSO, so use an IRC client password from "+se.srv.clientsPage())
		return false
	case err != nil:
		telemetry.IRCLogins.Add(context.Background(), 1, telemetry.Result("invalid"))
		se.loginFailed(username, network, "invalid")
		se.numeric("464", ":Invalid credentials")
		return false
	}
	se.uid, se.acct = u.ID, u
	if ip := se.clientIP(); ip != "" && !accounts.HostAllowed(u, ip) {
		telemetry.IRCLogins.Add(context.Background(), 1, telemetry.Result("host_denied"))
		se.loginFailed(username, network, "host_denied")
		se.numeric("464", ":Signing in from "+ip+" isn't allowed for this account")
		return false
	}
	if !se.srv.b.Owns(u.ID) {
		se.numeric("464", ":"+movingMsg)
		return false
	}

	nets, _ := se.srv.b.Store().ListNetworks(u.ID)
	if len(nets) == 0 {
		se.numeric("464", ":No networks configured — add one in the web app first")
		return false
	}
	chosen := nets[0]
	if network != "" {
		n, ok := findNetwork(nets, network)
		if !ok {
			se.numeric("464", ":Unknown network "+network)
			return false
		}
		chosen = n
	}

	att, err := se.srv.b.AttachClient(u.ID, se.srv.acc.LimitsFor(u).MaxClients,
		bouncer.ClientInfo{Device: device, Networks: []string{chosen.Name}, IP: se.clientIP()},
		func() { se.conn.Close() })
	if errors.Is(err, bouncer.ErrTooManyClients) {
		telemetry.IRCLogins.Add(context.Background(), 1, telemetry.Result("too_many_clients"))
		se.numeric("464", fmt.Sprintf(":Too many clients attached (limit %d) — detach one first", se.srv.acc.LimitsFor(u).MaxClients))
		return false
	}
	se.att = att
	se.release = att.Release

	if err := se.bind(chosen); err != nil {
		se.release()
		se.numeric("464", ":Network unavailable: "+err.Error())
		return false
	}
	se.unick = orElse(se.up.Nick(), chosen.Nick)
	telemetry.IRCLogins.Add(context.Background(), 1, telemetry.Result("ok"))
	se.welcome()
	se.joinChannels()
	se.replayBacklog()
	// Ask the client what it is (shown on the clients page and in the
	// activity feed); the reply arrives as a NOTICE to *status.
	se.send(fmt.Sprintf(":%s!status@%s PRIVMSG %s :\x01VERSION\x01", statusNick, serverName, se.unick))
	uid, ip, netName := u.ID, se.clientIP(), chosen.Name
	go func() {
		time.Sleep(3 * time.Second)
		sw := "IRC client"
		for _, c := range se.srv.b.ClientsOf(uid) {
			if c.ID == att.ID() && c.Software != "" {
				sw = c.Software
			}
		}
		se.srv.b.AddEvent(uid, "attached", sw+" attached", strings.TrimSpace(username+"/"+netName+" · "+ip))
	}()
	return true
}

// bind connects n if needed and subscribes this session to its feeds.
func (se *session) bind(n store.Network) error {
	if err := se.srv.b.Connect(n.ID); err != nil {
		return err
	}
	conn, ok := se.srv.b.ConnFor(n.ID)
	if !ok {
		return errors.New("not connected")
	}
	se.unsubscribe()
	se.net, se.up = n, conn
	se.raw, se.unsubRaw = conn.SubscribeRaw()
	se.msgs, se.unsubMsg = conn.Subscribe()
	return nil
}

func (se *session) unsubscribe() {
	if se.unsubRaw != nil {
		se.unsubRaw()
		se.unsubMsg()
		se.unsubRaw, se.unsubMsg = nil, nil
	}
}

func (se *session) welcome() {
	n := se.unick
	se.send(fmt.Sprintf(":%s 001 %s :Welcome to evrd — attached to %s", serverName, n, se.net.Name))
	se.send(fmt.Sprintf(":%s 002 %s :Your host is %s", serverName, n, serverName))
	se.send(fmt.Sprintf(":%s 003 %s :evrd bouncer", serverName, n))
	se.send(fmt.Sprintf(":%s 004 %s %s evrd o o", serverName, n, serverName))
	se.send(fmt.Sprintf(":%s 375 %s :- %s message of the day -", serverName, n, serverName))
	se.send(fmt.Sprintf(":%s 372 %s :- network %s (%s:%d)", serverName, n, se.net.Name, se.net.Host, se.net.Port))
	se.send(fmt.Sprintf(":%s 372 %s :- /msg *status help for bouncer commands", serverName, n))
	se.send(fmt.Sprintf(":%s 376 %s :End of /MOTD command.", serverName, n))

	// If the client registered under a different nick than the upstream's, tell
	// it explicitly — otherwise it won't recognize the synthesized self-JOIN and
	// won't open the channel.
	if se.nick != "" && se.nick != n {
		se.send(fmt.Sprintf(":%s!%s@%s NICK %s", se.nick, se.nick, serverName, n))
	}
}

func (se *session) joinChannels() {
	for _, ch := range se.up.Channels() {
		if se.up.ChannelSettings(ch).Detached {
			continue
		}
		// Make the client open the buffer, then ask the upstream for the real
		// names/topic — the replies stream back over the raw feed.
		se.send(fmt.Sprintf(":%s!%s@%s JOIN %s", se.unick, se.unick, serverName, ch))
		se.up.SendRaw("NAMES " + ch)
		se.up.SendRaw("TOPIC " + ch)
	}
}

// replayBacklog plays back each attached channel and each private buffer:
// the last PlaybackLines lines, or — when the user auto-clears that kind of
// buffer — only what arrived since they were last here, ZNC-style.
func (se *session) replayBacklog() {
	n := se.acct.PlaybackLines
	if n <= 0 {
		return
	}
	var since time.Time
	if se.acct.LastSeenAt != nil {
		since = *se.acct.LastSeenAt
	}
	cutoff := func(auto bool) time.Time {
		if auto {
			return since
		}
		return time.Time{}
	}
	for _, ch := range se.up.Channels() {
		if se.up.ChannelSettings(ch).Detached {
			continue
		}
		se.playbackSince(ch, n, cutoff(se.acct.AutoClearChanBuffer))
	}
	bufs, _ := se.srv.b.History().Buffers(se.net.ID)
	for _, b := range bufs {
		if isChannelName(b) {
			continue
		}
		se.playbackSince(b, n, cutoff(se.acct.AutoClearQueryBuffer))
	}
}

// playback sends the last n lines of a buffer.
func (se *session) playback(target string, n int) int {
	return se.playbackSince(target, n, time.Time{})
}

// playbackSince sends the last n lines of a buffer newer than since.
func (se *session) playbackSince(target string, n int, since time.Time) int {
	msgs, _ := se.srv.b.History().Recent(se.net.ID, target, n)
	sent := 0
	for _, m := range msgs {
		if !m.Time.After(since) {
			continue
		}
		se.send(se.format(m))
		sent++
	}
	return sent
}

func isChannelName(s string) bool { return s != "" && strings.ContainsRune("#&+!", rune(s[0])) }

// jump moves the session to another of the user's networks: the client
// leaves the old network's channels and gets the new one's, with backlog.
func (se *session) jump(n store.Network) error {
	old := se.up.Channels()
	oldName := se.net.Name
	if err := se.bind(n); err != nil {
		return err
	}
	for _, ch := range old {
		se.send(fmt.Sprintf(":%s!%s@%s PART %s :jumped to %s", se.unick, se.unick, serverName, ch, n.Name))
	}
	if nick := orElse(se.up.Nick(), n.Nick); nick != se.unick {
		se.send(fmt.Sprintf(":%s!%s@%s NICK %s", se.unick, se.unick, serverName, nick))
		se.unick = nick
	}
	if se.att != nil {
		se.att.Update(func(ci *bouncer.ClientInfo) { ci.Networks = []string{n.Name} })
	}
	se.statusf("Jumped from %s to %s.", oldName, n.Name)
	se.joinChannels()
	se.replayBacklog()
	return nil
}

// bridge relays the upstream's raw feed to the client and the client's commands
// back to the upstream, until either side closes. Everything runs on this
// goroutine; the reader only parses lines and hands them over.
func (se *session) bridge(ctx context.Context) {
	in := make(chan irc.Message, 16)
	done := make(chan struct{})
	defer close(done)
	go func() {
		defer close(in)
		for {
			line, err := se.readLine()
			if err != nil {
				return
			}
			select {
			case in <- irc.Parse(line):
			case <-done:
				return
			}
		}
	}()

	// The network's connection is replaced when it's disconnected and
	// connected again (from *status or the web app); follow it.
	follow := time.NewTicker(3 * time.Second)
	defer follow.Stop()

	for {
		select {
		case m, ok := <-in:
			if !ok {
				return
			}
			se.handleClient(m)
		case line, ok := <-se.raw:
			if !ok {
				se.raw = nil
				continue
			}
			se.relay(line)
		case m, ok := <-se.msgs:
			if !ok {
				se.msgs = nil
				continue
			}
			// Our own outgoing only — incoming arrives via the raw feed. Skip
			// what this session sent (already echoed/displayed); forward sends
			// from other sessions (web, another client) so we stay in sync.
			if isChat(m.Kind) && strings.EqualFold(m.Sender, se.unick) && m.Origin != se.id {
				se.send(se.formatAs(m, false))
			}
		case <-follow.C:
			if conn, ok := se.srv.b.ConnFor(se.net.ID); ok && conn != se.up {
				se.unsubscribe()
				se.up = conn
				se.raw, se.unsubRaw = conn.SubscribeRaw()
				se.msgs, se.unsubMsg = conn.Subscribe()
			}
		case <-ctx.Done():
			return
		}
	}
}

// relay forwards one raw upstream line, keeping the session's view of its
// nick in step and hiding what only makes sense to the bouncer.
func (se *session) relay(line string) {
	m := irc.Parse(line)
	switch m.Command {
	case "PING", "PONG", "CAP", "AUTHENTICATE":
		return
	case "001":
		// The upstream re-registered (it reconnected). Don't replay the
		// welcome; just bring the client's nick along if it changed.
		if len(m.Params) > 0 && m.Params[0] != se.unick {
			se.send(fmt.Sprintf(":%s!%s@%s NICK %s", se.unick, se.unick, serverName, m.Params[0]))
			se.unick = m.Params[0]
		}
		return
	case "002", "003", "004", "005":
		return
	case "NICK":
		if len(m.Params) > 0 && strings.EqualFold(nickFromPrefix(m.Prefix), se.unick) {
			se.unick = m.Params[0]
		}
	}
	if ch := lineChannel(m); ch != "" && se.up.ChannelSettings(ch).Detached {
		return // detached: kept and logged, not shown
	}
	se.send(line)
}

// lineChannel is the channel a server line is about, if any.
func lineChannel(m irc.Message) string {
	for i, p := range m.Params {
		if i > 2 {
			break
		}
		// numerics start with our nick; 353 has a "=" before the channel
		if isChannelName(p) && !strings.Contains(p, " ") {
			return p
		}
	}
	return ""
}

// detach hides channels from IRC clients (they stay joined and logged);
// attach brings them back.
func (se *session) detach(chans []string) {
	for _, ch := range chans {
		if !se.up.Joined(ch) {
			se.statusf("You're not on %s.", ch)
			continue
		}
		se.srv.b.SetChannelSettings(se.net.ID, ch, func(cs *store.ChannelSettings) { cs.Detached = true })
		se.send(fmt.Sprintf(":%s!%s@%s PART %s :Detached — /join %s to come back", se.unick, se.unick, serverName, ch, ch))
	}
}

func (se *session) attachChannel(ch string) {
	se.srv.b.SetChannelSettings(se.net.ID, ch, func(cs *store.ChannelSettings) { cs.Detached = false })
	se.send(fmt.Sprintf(":%s!%s@%s JOIN %s", se.unick, se.unick, serverName, ch))
	se.up.SendRaw("NAMES " + ch)
	se.up.SendRaw("TOPIC " + ch)
	se.playback(ch, max(se.acct.PlaybackLines, 1))
}

func (se *session) handleClient(m irc.Message) {
	switch m.Command {
	case "PING":
		se.send(fmt.Sprintf(":%s PONG %s :%s", serverName, serverName, lastParam(m)))
	case "QUIT":
		se.conn.Close()
	case "ZNC":
		// ZNC's raw command (/znc ListNetworks, /quote znc …): a *status
		// command, as ZNC takes it, so ZNC habits and scripts work here
		se.statusCommand(strings.Join(m.Params, " "))
	case "PRIVMSG", "NOTICE":
		if len(m.Params) < 2 {
			return
		}
		target, text := m.Params[0], m.Params[len(m.Params)-1]
		if strings.EqualFold(target, statusNick) {
			if m.Command == "PRIVMSG" {
				se.statusCommand(text)
			} else if v, ok := strings.CutPrefix(text, "\x01VERSION "); ok && se.att != nil {
				se.att.Update(func(ci *bouncer.ClientInfo) { ci.Software = clientName(strings.TrimSuffix(v, "\x01")) })
			}
			return
		}
		se.up.SendFrom(se.id, m.Command, target, text)
		// The server won't echo our own line back over the raw feed, so
		// honor echo-message ourselves; plain clients display it locally.
		if se.caps["echo-message"] {
			se.send(fmt.Sprintf(":%s!%s@%s %s %s :%s", se.unick, se.unick, serverName, m.Command, target, text))
		}
	case "NICK":
		// The server's NICK reply comes back over the raw feed and updates
		// both the upstream's and this session's nick.
		if len(m.Params) > 0 {
			se.up.SendRaw("NICK " + m.Params[0])
		}
	case "JOIN":
		if len(m.Params) == 0 {
			se.up.SendRaw(m.String())
			return
		}
		chans := strings.Split(m.Params[0], ",")
		var keys []string
		if len(m.Params) > 1 {
			keys = strings.Split(m.Params[1], ",")
		}
		var rest, restKeys []string
		for i, ch := range chans {
			if se.up.Joined(ch) && se.up.ChannelSettings(ch).Detached {
				se.attachChannel(ch)
				continue
			}
			rest = append(rest, ch)
			if i < len(keys) {
				restKeys = append(restKeys, keys[i])
			}
		}
		if len(rest) > 0 {
			line := "JOIN " + strings.Join(rest, ",")
			if len(restKeys) > 0 {
				line += " " + strings.Join(restKeys, ",")
			}
			se.up.SendRaw(line)
		}
	case "DETACH": // ZNC's client command: /detach #chan[,#chan]
		if len(m.Params) > 0 {
			se.detach(strings.Split(m.Params[0], ","))
		}
	case "CAP":
		se.handleCap(m)
	case "USER", "PASS":
		se.numeric("462", ":You're already registered")
	default:
		// Everything else (JOIN, PART, MODE, WHO, NAMES, TOPIC, LIST, …) goes
		// straight to the upstream; replies come back over the raw feed. The
		// upstream saves joins and parts to the network's channel list.
		// A module (alias) may take the line instead.
		if se.up.ClientLine(m.String()) {
			return
		}
		se.up.SendRaw(m.String())
	}
}

// format renders a stored message as an IRC line for replay, tagged with
// server-time when the client negotiated it. Channel events (joins, parts, …)
// come from *buffextras as plain text, the way ZNC's module replays them, so
// replay never touches the client's idea of who is in the channel.
func (se *session) format(m history.Message) string { return se.formatAs(m, true) }

// formatAs renders m; playback lines (not live ones) carry the user's
// timestamp for clients without server-time.
func (se *session) formatAs(m history.Message, playback bool) string {
	var b strings.Builder
	if se.caps["server-time"] {
		b.WriteString("@time=" + m.Time.UTC().Format("2006-01-02T15:04:05.000Z") + " ")
	}
	stamp := func(text string) string { return text }
	if playback && !se.caps["server-time"] && (se.acct.PrependTimestamp || se.acct.AppendTimestamp) {
		ts := se.timestamp(m.Time)
		stamp = func(text string) string {
			if se.acct.PrependTimestamp {
				text = ts + " " + text
			}
			if se.acct.AppendTimestamp {
				text = text + " " + ts
			}
			return text
		}
	}
	if !isChat(m.Kind) {
		b.WriteString(fmt.Sprintf(":*buffextras!buffextras@%s PRIVMSG %s :%s", serverName, m.Target, stamp(describe(m))))
		return b.String()
	}
	sender := m.Sender
	if sender == "" {
		sender = "*"
	}
	text := stamp(m.Text)
	cmd := "PRIVMSG"
	switch m.Kind {
	case "notice":
		cmd = "NOTICE"
	case "action":
		text = "\x01ACTION " + text + "\x01"
	}
	b.WriteString(fmt.Sprintf(":%s!%s@%s %s %s :%s", sender, sender, se.net.Name, cmd, m.Target, text))
	return b.String()
}

// isChat reports whether a stored kind is something someone said.
func isChat(kind string) bool {
	return kind == "message" || kind == "notice" || kind == "action" || kind == ""
}

// describe is a channel event as a sentence.
func describe(m history.Message) string {
	why := func(s string) string {
		if s == "" {
			return ""
		}
		return " (" + s + ")"
	}
	switch m.Kind {
	case "join":
		return m.Sender + " joined"
	case "part":
		return m.Sender + " left" + why(m.Text)
	case "quit":
		return m.Sender + " quit" + why(m.Text)
	case "kick":
		return m.Sender + " kicked " + m.Extra + why(m.Text)
	case "nick":
		return m.Sender + " is now known as " + m.Extra
	case "topic":
		return m.Sender + " changed the topic to: " + m.Text
	case "mode":
		return m.Sender + " set mode " + m.Text
	}
	return m.Sender + " " + m.Kind + " " + m.Text
}

func (se *session) send(line string) {
	se.wmu.Lock()
	defer se.wmu.Unlock()
	se.w.WriteString(line + "\r\n")
	se.w.Flush()
}

func (se *session) numeric(code, trailing string) {
	n := orElse(se.unick, orElse(se.nick, "*"))
	se.send(fmt.Sprintf(":%s %s %s %s", serverName, code, n, trailing))
}

// clientIP is the client's address (the original one, if forwarded).
func (se *session) clientIP() string {
	if se.fromIP != "" && se.fromIP != "-" {
		return se.fromIP
	}
	return remoteHost(se.conn)
}

// loginFailed logs a refused IRC sign-in, one line per attempt (never the
// password).
func (se *session) loginFailed(username, network, reason string) {
	se.srv.log.Warn("irc sign-in failed", "user", username, "network", network, "ip", se.clientIP(), "reason", reason)
}

// clientName boils a CTCP VERSION reply down to the client's name:
// "HexChat 2.16.1 [x86_64] / Linux" -> "HexChat", "irssi v1.4.5 - …" -> "irssi".
func clientName(version string) string {
	f := strings.Fields(version)
	if len(f) == 0 {
		return ""
	}
	name := strings.TrimRight(f[0], ":,")
	if len(name) > 32 {
		name = name[:32]
	}
	return name
}

func nickFromPrefix(prefix string) string {
	nick, _, _ := strings.Cut(prefix, "!")
	return nick
}

func findNetwork(nets []store.Network, name string) (store.Network, bool) {
	for _, n := range nets {
		if strings.EqualFold(n.Name, name) {
			return n, true
		}
	}
	return store.Network{}, false
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
