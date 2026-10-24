package client

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"github.com/4evrd/evrd/internal/auth"
	"github.com/4evrd/evrd/internal/mfa"
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
	"github.com/4evrd/evrd/internal/links"
	"github.com/4evrd/evrd/internal/messages"
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
	capVersion     int               // the client's CAP LS version (302…)
	caps           map[string]bool   // what it asked for
	advertised     map[string]string // what it was last told it may ask for
	registered     bool
	ubatch         map[string]bool // the network's open batches: passed to this client?
	regLines       []string        // PASS/NICK/USER as sent, replayed when forwarding
	internal       bool            // forwarded here by another node (already vetted)
	saslMech       string          // the SASL mechanism under way ("" none)
	saslBuf        string          // its payload so far
	saslUser       store.User      // who SASL signed in
	saslDevice     string
	saslOTP        string // a second-factor code after SASL's password
	refusal        string // why the last sign-in was refused (signIn)
	partial        []byte // a line read so far
	lsWaiting      bool   // a CAP LS waits for the network to be known
	lsSince        time.Time
	lsLine         string         // that CAP LS, passed on if the client is forwarded
	regKey         string         // what regUp was worked out from
	regUp          *upstream.Conn // the network a registering client is headed for
	bindID         string         // BOUNCER BIND's network

	// Attached state. After registration, only the bridge goroutine touches
	// these, so jumping networks needs no locking.
	uid      int64
	acct     store.User        // as loaded at attach (LastSeenAt is the previous visit)
	pr       *messages.Printer // *status's language, for the command running (printer)
	lang     string            // the web chat's language ("": the account's)
	net      store.Network
	up       *upstream.Conn
	unick    string
	raw      <-chan upstream.Line
	msgs     <-chan history.Message
	unsubRaw func()
	unsubMsg func()
	release  func() // frees this client's slot in the user's client limit
	att      *bouncer.Attachment
	// dropped is why the connection ended (bouncer.Drop*), the first reason
	// to be noted winning (drops.go).
	dropMu  sync.Mutex
	dropped string
	// droppedAt is when the reason was noted: when the client left, which
	// may be a little before evrd is done with the session.
	droppedAt time.Time
	// lastRead is when the client last sent a line (unix nanoseconds), and
	// pinged when evrd asked it whether it's still there (liveness.go).
	lastRead atomic.Int64
	pinged   time.Time
	fromIP   string // the client's address when another node forwarded it
	// locked is set while the client waits for its second factor (mfa.go):
	// signed in, on no network, only *status answering.
	locked *lockedSignIn

	// web is set for a *status command from the web chat: replies go to it
	// instead of an IRC connection, and IRC-only commands say so.
	web func(string)

	batches  int                          // BATCH references handed out
	netsSent map[string]map[string]string // networks as last told (bouncer-networks-notify)
	marks    map[string]time.Time         // read markers as last told (read-marker)

	// labeled-response: while a labeled command runs, what evrd answers is
	// captured to label it; a command passed on carries the label upstream.
	capture   *[]string
	label     string
	labelSent bool

	mlines map[string]*multiline // the client's open draft/multiline batches

	namesAsked map[string]int // NAMES the client asked for, by channel (no-implicit-names)

	// held is the client's connection when the holder has it (hold.go);
	// restored, that it was picked up from the last evrd's state.
	held       *links.ClientConn
	restored   bool
	restoredAt time.Time
	savedState []byte
	preAway    *string // AWAY sent while registering (pre-away)
}

func newSession(srv *Server, conn net.Conn) *session {
	return &session{
		id:     strconv.FormatInt(sessionSeq.Add(1), 10),
		srv:    srv,
		conn:   conn,
		r:      bufio.NewReaderSize(conn, 4096),
		w:      bufio.NewWriter(conn),
		caps:   map[string]bool{},
		ubatch: map[string]bool{},
	}
}

// maxLine bounds one IRC line (512 bytes plus up to 8 KiB of tags, with room).
const maxLine = 16 << 10

// readLine reads one line without its line ending.
// A line cut short by a read deadline is kept for the next call.
func (se *session) readLine() (string, error) {
	for {
		chunk, err := se.r.ReadSlice('\n')
		se.partial = append(se.partial, chunk...)
		if len(se.partial) > maxLine {
			return "", errors.New("line too long")
		}
		if err == bufio.ErrBufferFull {
			continue
		}
		if err != nil {
			return "", err
		}
		line := strings.TrimRight(string(se.partial), "\r\n")
		se.partial = nil
		return line, nil
	}
}

func (se *session) serve(ctx context.Context) {
	defer se.closeConn(ctx)
	if se.internal && !se.acceptForwarded() {
		return
	}

	if !se.restored {
		if !se.register(ctx) {
			return
		}
		se.registered = true
		if se.forwardIfElsewhere(ctx) {
			return
		}
		if !se.attach() {
			return
		}
		se.saveState()
	}
	defer func() {
		se.unsubscribe()
		se.dropLink() // a sign-in link dies with its connection
		if se.heldOver(ctx) {
			return // still attached, as far as the next evrd is concerned
		}
		se.recordDrop()
		se.savePosition()
		se.markOnNetwork() // until now
		if se.release != nil {
			se.release()
		}
	}()
	se.bridge(ctx)
}

// register reads the client's registration: CAP, PASS, NICK, USER (and
// SASL). False if the client went, or went to another node.
func (se *session) register(ctx context.Context) bool {
	for {
		if se.lsWaiting {
			se.conn.SetReadDeadline(se.lsSince.Add(orDefault(se.srv.lsWait, lsWait)))
		}
		line, err := se.readLine()
		if se.lsWaiting {
			se.conn.SetReadDeadline(time.Time{})
		}
		var ne net.Error
		if errors.As(err, &ne) && ne.Timeout() && se.lsWaiting {
			se.answerLS() // the client waits for it
			continue
		}
		if err != nil {
			return false
		}
		m := irc.Parse(line)
		if se.lsWaiting && (m.Command == "CAP" || m.Command == "AUTHENTICATE") {
			se.answerLS()
		}
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
		case "AUTHENTICATE":
			se.authenticate(m)
		case "AWAY": // draft/pre-away: kept for once attached
			if se.caps["draft/pre-away"] {
				away := lastParam(m)
				se.preAway = &away
			}
		case "BOUNCER":
			// BIND picks the network; it goes first when forwarded, so the
			// owner has it before NICK and USER end registration
			if len(m.Params) > 1 && strings.EqualFold(m.Params[0], "BIND") {
				se.bindID = m.Params[1]
				se.regLines = append([]string{line}, se.regLines...)
			} else {
				se.bouncerFail("ACCOUNT_REQUIRED", strings.Join(append(m.Params, "*")[:1], ""), "Sign in first")
			}
		case "QUIT":
			return false
		}
		if se.lsWaiting && se.nick != "" && se.user != "" {
			// another node's user: that node answers, knowing the network
			if se.pass != "" && se.forwardIfElsewhere(ctx) {
				return false
			}
			se.answerLS()
		}
		if se.nick != "" && se.user != "" && !se.capNegotiating {
			return true
		}
	}
}

// signIn checks a user's secret: one of their IRC client passwords, the
// account password, or (sso_irc_password) their SSO password, checked
// with the identity provider. Failures count toward the same lockout as
// the web sign-in. A refusal is logged and comes back as the numeric to
// answer PASS with and why. A password ending in ":<code>" (an
// authenticator app's 6 digits, or a recovery code) that isn't right as a
// whole is tried without it, the code coming back as otp for the second
// factor.
func (se *session) signIn(username, network, password string) (u store.User, device, otp, code, why string) {
	key := strings.ToLower(strings.TrimSpace(username))
	var fails *auth.FailureLimiter
	if se.srv.signIns != nil {
		fails = se.srv.signIns.Failures()
	}
	if fails != nil && fails.Blocked(key) {
		telemetry.IRCLogins.Add(context.Background(), 1, telemetry.Result("locked"))
		se.loginFailed(username, network, "locked")
		return store.User{}, "", "", "464", "Too many failed sign-ins — try again in a few minutes"
	}
	u, device, err := se.checkSecret(username, password)
	if base, c, ok := cutCode(password); ok && errors.Is(err, accounts.ErrInvalidCredentials) {
		if u2, d2, err2 := se.checkSecret(username, base); err2 == nil || !errors.Is(err2, accounts.ErrInvalidCredentials) {
			u, device, otp, err = u2, d2, c, err2
		}
	}
	result, reason := "invalid", "invalid"
	switch {
	case err == nil:
		if fails != nil {
			fails.Reset(key)
		}
		return u, device, otp, "", ""
	case errors.Is(err, accounts.ErrSuspended), errors.Is(err, auth.ErrSSOSuspended):
		result, reason, code, why = "suspended", "suspended", "465", "This account is suspended"
	case errors.Is(err, auth.ErrSSOUnavailable):
		// not the user's fault: no lockout, and they can still use a client password
		result, reason, code, why = "sso_unavailable", "sso_unavailable", "464", "Can't check your SSO password right now — use an IRC client password from "+se.srv.clientsPage()
		se.srv.log.Warn("sso password check", "user", username, "err", err)
		telemetry.IRCLogins.Add(context.Background(), 1, telemetry.Result(result))
		se.loginFailed(username, network, reason)
		return store.User{}, "", "", code, why
	case errors.Is(err, auth.ErrSSONoPassword):
		reason, code, why = "sso_no_password", "464", "Your SSO account has no password — set one with your identity provider, or use an IRC client password from "+se.srv.clientsPage()
	case errors.Is(err, accounts.ErrNeedsClientPassword):
		reason, code, why = "sso_needs_client_password", "464", "Invalid credentials — this account signs in with SSO, so use an IRC client password from "+se.srv.clientsPage()
	default:
		code, why = "464", "Invalid credentials"
	}
	if fails != nil {
		fails.Add(key)
	}
	telemetry.IRCLogins.Add(context.Background(), 1, telemetry.Result(result))
	se.loginFailed(username, network, reason)
	se.refusal = reason
	return store.User{}, "", "", code, why
}

// checkSecret is username's account if secret is one of its passwords: a
// client password, the account password, or the SSO password.
func (se *session) checkSecret(username, secret string) (store.User, string, error) {
	u, device, err := se.srv.acc.IRCLogin(username, secret)
	if errors.Is(err, accounts.ErrInvalidCredentials) && se.srv.signIns != nil && se.srv.signIns.SSOPasswordsOn() {
		return se.ssoSignIn(username, secret, err)
	}
	return u, device, err
}

// cutCode splits a second-factor code off the end of a password
// ("password:123456").
func cutCode(password string) (base, code string, ok bool) {
	i := strings.LastIndexByte(password, ':')
	if i <= 0 || !mfa.LooksLikeCode(password[i+1:]) {
		return "", "", false
	}
	return password[:i], password[i+1:], true
}

// ssoDevice is how a sign-in with the SSO password shows among the user's
// clients.
const ssoDevice = "SSO password"

// ssoSignIn tries password as the SSO password of username's account, after
// it wasn't a client password or the account password (failed: why). An
// account not linked to the provider keeps failed.
func (se *session) ssoSignIn(username, password string, failed error) (store.User, string, error) {
	u, err := se.srv.acc.UserByUsername(context.Background(), strings.ToLower(username))
	if err != nil || u.OIDCSubject == nil {
		return store.User{}, "", failed
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := se.srv.signIns.CheckSSOPassword(ctx, u, password); err != nil {
		if errors.Is(err, auth.ErrSSOWrongPassword) || errors.Is(err, auth.ErrSSONotLinked) {
			return store.User{}, "", failed
		}
		return store.User{}, "", err
	}
	if u.Suspended {
		return store.User{}, "", accounts.ErrSuspended
	}
	se.srv.log.Info("irc sign-in with the SSO password", "user", u.Username, "method", se.srv.signIns.SSOPasswordMethod(), "ip", se.clientIP())
	return u, ssoDevice, nil
}

// attach authenticates and binds the session to a network, then sends the
// welcome burst, channel state, and backlog. A sign-in that needs a second
// factor first waits for it on no network (mfa.go).
func (se *session) attach() bool {
	login, password, ok := strings.Cut(se.pass, ":")
	if (!ok || password == "") && se.srv.linkSignInOn() && !strings.ContainsAny(login, " ") {
		// no password: sign in on the website instead (user, user/network
		// or nothing at all)
		return se.waitForLink(login, "")
	}
	if !ok || password == "" {
		// Never log PASS itself: without a colon it may be just the password.
		se.srv.log.Info("irc sign-in without a password", "ip", se.clientIP(), "pass_sent", se.pass != "")
		se.numeric("464", ":Password required — use PASS <user>[/<network>]:<password>")
		return false
	}
	username, network, _ := strings.Cut(login, "/")
	u, device, otp := se.saslUser, se.saslDevice, se.saslOTP // SASL checked it already
	if se.saslUser.ID == 0 {
		var code, why string
		if u, device, otp, code, why = se.signIn(username, network, password); code != "" {
			if se.linkAfterRefusal(password) {
				// a long secret evrd doesn't know: allowed on the website, it
				// can be remembered as this device's password
				return se.waitForLink(login, password)
			}
			se.numeric(code, ":"+why)
			return false
		}
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
	locked, totp, code, why := se.secondFactor(u, username, network, device, otp)
	if code != "" {
		se.numeric(code, ":"+why)
		return false
	}

	chosen, unbound, code, why := se.chooseNetwork(u, network)
	if code != "" {
		se.numeric(code, ":"+why)
		return false
	}
	var bound []string
	if !unbound && !locked {
		bound = []string{chosen.Name}
	}

	att, err := se.srv.b.AttachClient(u.ID, se.srv.acc.LimitsFor(u).MaxClients,
		bouncer.ClientInfo{Device: device, Networks: bound, IP: se.clientIP()},
		se.kick)
	if errors.Is(err, bouncer.ErrTooManyClients) {
		telemetry.IRCLogins.Add(context.Background(), 1, telemetry.Result("too_many_clients"))
		now := time.Now()
		se.srv.b.RecordDisconnect(u.ID, bouncer.ClientInfo{Device: device, IP: se.clientIP(), Since: now}, chosen.Name, bouncer.DropClientLimit, now)
		se.numeric("464", fmt.Sprintf(":Too many clients attached (limit %d) — detach one first", se.srv.acc.LimitsFor(u).MaxClients))
		return false
	}
	se.att = att
	se.release = att.Release

	se.unick = orElse(se.nick, u.Username)
	if locked {
		// signed in, but on no network until the second factor
		telemetry.IRCLogins.Add(context.Background(), 1, telemetry.Result("second_factor"))
		se.welcome()
		se.syncCaps()
		se.lock(lockedSignIn{net: chosen, unbound: unbound, username: username, device: device, totp: totp})
		return true
	}
	if !unbound {
		if err := se.bind(chosen); err != nil {
			se.release()
			se.att = nil
			se.numeric("464", ":Network unavailable: "+err.Error())
			return false
		}
		se.unick = orElse(se.up.Nick(), chosen.Nick)
	}
	telemetry.IRCLogins.Add(context.Background(), 1, telemetry.Result("ok"))
	se.welcome()
	se.syncCaps()
	se.signedIn(!unbound, username, chosen.Name, device)
	return true
}

// chooseNetwork is the network u's client attaches to: the one it named
// (or BOUNCER BIND's), else the first; none (unbound) for a
// bouncer-networks client that names none. A refusal is the numeric and
// why.
func (se *session) chooseNetwork(u store.User, network string) (chosen store.Network, unbound bool, code, why string) {
	nets, _ := se.srv.acc.ListNetworks(context.Background(), u.ID)
	unbound = network == "" && se.bindID == "" && se.caps["soju.im/bouncer-networks"]
	switch {
	case unbound:
	case se.bindID != "":
		n, ok := findNetworkID(nets, se.bindID)
		if !ok {
			se.bouncerFail("INVALID_NETID", "BIND", se.bindID, "Unknown network ID")
			return chosen, false, "464", "Unknown network " + se.bindID
		}
		chosen = n
	case len(nets) == 0:
		return chosen, false, "464", "No networks configured — add one in the web app first"
	case network != "":
		n, ok := findNetwork(nets, network)
		if !ok {
			return chosen, false, "464", "Unknown network " + network
		}
		chosen = n
	default:
		chosen = nets[0]
	}
	return chosen, unbound, "", ""
}

// signedIn finishes attaching a client that's signed in: the network's
// channels and backlog (onNetwork), the bouncer networks it asked for, and
// its activity entry.
func (se *session) signedIn(onNetwork bool, username, netName, device string) {
	if onNetwork {
		se.startMarks()
		se.joinChannels()
		se.replayBacklog()
		se.syncMarks()
		// pre-away: "*" means present but not reading, which auto-away
		// already sees to
		if se.preAway != nil && *se.preAway != "*" {
			se.up.SetAway(*se.preAway)
		}
	}
	if se.caps["soju.im/bouncer-networks-notify"] {
		se.listNetworks()
	}
	// Ask the client what it is (shown on the clients page and in the
	// activity feed); the reply arrives as a NOTICE to *status.
	se.send(fmt.Sprintf(":%s!status@%s PRIVMSG %s :\x01VERSION\x01", statusNick, serverName, se.unick))
	uid, ip, att := se.uid, se.clientIP(), se.att
	go func() {
		time.Sleep(3 * time.Second)
		sw := "IRC client"
		for _, c := range se.srv.b.ClientsOf(uid) {
			if c.ID == att.ID() && c.Software != "" {
				sw = c.Software
			}
		}
		method := "password_grant"
		if se.srv.signIns != nil {
			method = orElse(se.srv.signIns.SSOPasswordMethod(), method)
		}
		se.srv.b.AddEvent(uid, "attached", eventTitle(device, sw, method), messages.Raw(strings.TrimSpace(username+"/"+netName+" · "+ip)))
	}()
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
	if se.up == nil {
		se.send(fmt.Sprintf(":%s 001 %s :%s", serverName, n, ircText(se.tr("welcome.bouncer"))))
	} else {
		se.send(fmt.Sprintf(":%s 001 %s :%s", serverName, n, ircText(se.tr("welcome.network", "network", se.net.Name))))
	}
	se.send(fmt.Sprintf(":%s 002 %s :Your host is %s", serverName, n, serverName))
	se.send(fmt.Sprintf(":%s 003 %s :evrd bouncer", serverName, n))
	se.send(fmt.Sprintf(":%s 004 %s %s evrd o o", serverName, n, serverName))
	se.sendISupport()
	se.send(fmt.Sprintf(":%s 375 %s :- %s message of the day -", serverName, n, serverName))
	if se.up != nil {
		se.send(fmt.Sprintf(":%s 372 %s :- %s", serverName, n, ircText(se.tr("welcome.motdNetwork", "network", se.net.Name, "host", se.net.Host, "port", se.net.Port))))
	}
	se.send(fmt.Sprintf(":%s 372 %s :- %s", serverName, n, ircText(se.tr("welcome.motdHelp", "status", statusNick))))
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
		if cs := se.up.ChannelSettings(ch); cs.Detached || cs.Disabled && !se.up.Joined(ch) {
			continue
		}
		// Make the client open the buffer, then give it the topic and names
		// a join brings, from evrd's state (or the network's, while evrd
		// doesn't know them whole).
		se.send(se.selfJoin(ch))
		se.joinedMark(ch)
		se.channelBurst(ch)
	}
}

// selfJoin is the JOIN that opens a channel on the client: in the
// extended-join form (account, realname) for a client that asked for it.
func (se *session) selfJoin(ch string) string {
	line := fmt.Sprintf(":%s!%s@%s JOIN %s", se.unick, se.unick, serverName, ch)
	if se.caps["extended-join"] {
		line += " " + orElse(se.up.Account(), "*") + " :" + orElse(se.net.Realname, se.unick)
	}
	return line
}

// replayBacklog plays back each attached channel and each private buffer:
// its last lines (its own count, the network's or the account's), after its
// clear mark — and when it auto-clears, ZNC-style, only what arrived since an
// IRC client was last on this network. A client with draft/chathistory gets
// none: it asks for what it wants.
func (se *session) replayBacklog() {
	if se.caps["draft/chathistory"] {
		return
	}
	p := se.playRules()
	play := func(name string) {
		if n := p.Lines(name); n > 0 {
			se.playbackSince(name, n, p.From(name))
		}
	}
	for _, ch := range se.up.Channels() {
		if cs := se.up.ChannelSettings(ch); cs.Detached || cs.Disabled && !se.up.Joined(ch) {
			continue
		}
		play(ch)
	}
	bufs, _ := se.srv.b.History().Buffers(se.net.ID)
	for _, b := range bufs {
		if b == "" || isChannelName(b) {
			continue
		}
		play(b)
	}
	se.markOnNetwork()
}

// playback sends the last n lines of a buffer.
func (se *session) playback(target string, n int) int {
	return se.playbackSince(target, n, time.Time{})
}

// playbackSince sends the last n lines of a buffer newer than since. Only
// lines the client gets count: channel events for a client that takes them
// (draft/event-playback, in a batch), else just the chat.
func (se *session) playbackSince(target string, n int, since time.Time) int {
	q := history.Query{Limit: n, Latest: true}
	if !since.IsZero() {
		q.After = history.ID{Nanos: since.UnixNano(), Seq: history.MaxSeq}
	}
	msgs, err := se.readLines(target, q, se.caps["batch"] && se.caps["draft/event-playback"])
	if err != nil {
		se.srv.log.Warn("playback", "network", se.net.ID, "target", target, "err", err)
	}
	if len(msgs) > 0 {
		se.sendHistory(target, msgs)
	}
	return len(msgs)
}

// sendHistory sends a buffer's lines as playback: in a chathistory batch for
// a client that takes batches, else one by one.
func (se *session) sendHistory(target string, msgs []history.Message) {
	batch := se.openBatch("chathistory", target)
	for _, m := range msgs {
		if line := se.formatIn(m, true, batch); line != "" {
			se.send(line)
		}
	}
	se.closeBatch(batch)
}

// openBatch starts a batch and returns its reference, or "" for a client
// without batches.
func (se *session) openBatch(kind string, params ...string) string {
	if !se.caps["batch"] {
		return ""
	}
	se.batches++
	ref := strconv.Itoa(se.batches)
	se.send(strings.Join(append([]string{":" + serverName, "BATCH", "+" + ref, kind}, params...), " "))
	return ref
}

func (se *session) closeBatch(ref string) {
	if ref != "" {
		se.send(fmt.Sprintf(":%s BATCH -%s", serverName, ref))
	}
}

func isChannelName(s string) bool { return s != "" && strings.ContainsRune("#&+!", rune(s[0])) }

// jump moves the session to another of the user's networks: the client
// leaves the old network's channels and gets the new one's, with backlog.
func (se *session) jump(n store.Network) error {
	old := se.up.Channels()
	se.markOnNetwork() // leaving it
	oldName := se.net.Name
	if err := se.bind(n); err != nil {
		return err
	}
	for _, ch := range old {
		se.send(fmt.Sprintf(":%s!%s@%s PART %s :%s", se.unick, se.unick, serverName, ch, ircText(se.tr("part.jumped", "network", n.Name))))
	}
	if nick := orElse(se.up.Nick(), n.Nick); nick != se.unick {
		se.send(fmt.Sprintf(":%s!%s@%s NICK %s", se.unick, se.unick, serverName, nick))
		se.unick = nick
	}
	if se.att != nil {
		se.att.Update(func(ci *bouncer.ClientInfo) { ci.Networks = []string{n.Name} })
	}
	se.say("jumped", "from", oldName, "to", n.Name)
	se.syncCaps()
	se.startMarks()
	se.joinChannels()
	se.replayBacklog()
	se.syncMarks()
	return nil
}

// bridge relays the upstream's raw feed to the client and the client's commands
// back to the upstream, until either side closes. Everything runs on this
// goroutine; the reader only parses lines and hands them over.
func (se *session) bridge(ctx context.Context) {
	se.lastRead.Store(time.Now().UnixNano())
	in := make(chan irc.Message, 16)
	done := make(chan struct{})
	defer close(done)
	go func() {
		defer close(in)
		for {
			line, err := se.readLine()
			if err != nil {
				se.drop(readDrop(err))
				return
			}
			se.lastRead.Store(time.Now().UnixNano())
			select {
			case in <- irc.Parse(line):
			case <-done:
				return
			}
		}
	}()

	// The network's connection is replaced when it's disconnected and
	// connected again (from *status or the web app); follow it.
	follow := time.NewTicker(orDefault(se.srv.followEvery, 3*time.Second))
	defer follow.Stop()

	for {
		select {
		case m, ok := <-in:
			if !ok {
				return
			}
			se.handleClient(m)
			se.saveState()
		case line, ok := <-se.raw:
			if !ok {
				se.raw = nil
				continue
			}
			se.relay(line)
			if se.held != nil && (strings.Contains(line.Text, " NICK ") || strings.Contains(line.Text, "BATCH ")) {
				se.saveState()
			}
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
			se.checkAlive()
			if se.locked != nil {
				se.checkLink() // allowed on the website yet?
				continue
			}
			if conn, ok := se.srv.b.ConnFor(se.net.ID); ok && conn != se.up {
				se.unsubscribe()
				se.up = conn
				se.raw, se.unsubRaw = conn.SubscribeRaw()
				se.msgs, se.unsubMsg = conn.Subscribe()
			}
			if se.restored && se.up != nil && se.up.Status() != upstream.StatusConnected && time.Since(se.restoredAt) < 30*time.Second {
				continue // picked up from the last evrd: the link isn't back yet
			}
			se.restored = false
			se.syncCaps()
			se.notifyNetworks()
			se.syncMarks()
			se.saveState()
		case <-ctx.Done():
			se.drop(bouncer.DropEvrdStopping)
			return
		}
	}
}

// clientTags are the client-only tags (+…) among tags, as "k=v;…" in a fixed
// order. Anything else a client sends (msgid, time, account…) is dropped:
// clients don't get to set server tags.
func clientTags(tags map[string]string) string {
	var out []string
	for k, v := range tags {
		if !strings.HasPrefix(k, "+") || len(k) < 2 {
			continue
		}
		if v != "" {
			k += "=" + v
		}
		out = append(out, k)
	}
	sort.Strings(out)
	s := strings.Join(out, ";")
	if len(s) > maxClientTags {
		return "" // over the spec's limit: drop them rather than cut one
	}
	return s
}

// clientTags are the client-only tags on a line this client sent, if it
// negotiated message-tags.
func (se *session) clientTags(m irc.Message) string {
	if !se.caps["message-tags"] {
		return ""
	}
	return clientTags(m.Tags)
}

// maxClientTags is the most bytes of client-only tags a line may carry.
const maxClientTags = 4094

// tags are the message tags a line about id gets: its time and msgid for a
// client that takes them, its batch, and client-only tags (for a client
// with message-tags). "" or "@…;… ".
func (se *session) tags(id history.ID, batch, client string) string {
	var t []string
	if se.caps["server-time"] && !id.IsZero() {
		t = append(t, "time="+id.Time().UTC().Format(timeFormat))
	}
	if batch != "" {
		t = append(t, "batch="+batch)
	}
	if se.caps["message-tags"] && id.Seq != 0 {
		t = append(t, "msgid="+id.String())
	}
	if client != "" {
		t = append(t, client)
	}
	if len(t) == 0 {
		return ""
	}
	return "@" + strings.Join(t, ";") + " "
}

// timeFormat is server-time's.
const timeFormat = "2006-01-02T15:04:05.000Z"

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
			se.say("notOn", "channel", ch)
			continue
		}
		se.srv.b.SetChannelSettings(se.net.ID, ch, func(cs *store.ChannelSettings) { cs.Detached = true })
		se.send(fmt.Sprintf(":%s!%s@%s PART %s :%s", se.unick, se.unick, serverName, ch, ircText(se.tr("part.detached", "channel", ch))))
	}
}

func (se *session) attachChannel(ch string) {
	se.srv.b.SetChannelSettings(se.net.ID, ch, func(cs *store.ChannelSettings) { cs.Detached = false })
	se.send(se.selfJoin(ch))
	se.joinedMark(ch)
	se.channelBurst(ch)
	if !se.caps["draft/chathistory"] {
		// what it missed while detached: after its clear mark
		p := se.playRules()
		se.playbackSince(ch, max(p.Lines(ch), 1), p.ClearedAt(ch))
	}
}

func (se *session) handleClient(m irc.Message) {
	if label := m.Tags["label"]; label != "" && se.caps["labeled-response"] {
		se.labeled(label, m)
		return
	}
	se.dispatch(m)
}

// dispatch carries out a client's command.
func (se *session) dispatch(m irc.Message) {
	if se.locked != nil && !se.lockedCommand(m) {
		return
	}
	if se.up == nil && !unboundOK[m.Command] {
		se.send(fmt.Sprintf(":%s NOTICE %s :%s", serverName, se.unick, ircText(se.tr("bouncerConnection"))))
		return
	}
	if se.ownNickLine(m) {
		return
	}
	switch m.Command {
	case "BOUNCER":
		se.bouncerCommand(m.Params)
	case "MARKREAD":
		if !se.caps["draft/read-marker"] {
			se.passOn(m)
			return
		}
		se.markRead(m.Params)
	case "PING":
		se.send(fmt.Sprintf(":%s PONG %s :%s", serverName, serverName, lastParam(m)))
	case "PONG":
		if lastParam(m) != pingToken {
			se.passOn(m) // as before: the network's business
		}
	case "QUIT":
		se.drop(bouncer.DropQuit)
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
		if se.inMultiline(m) {
			return
		}
		if strings.EqualFold(target, statusNick) {
			if m.Command == "PRIVMSG" {
				se.statusCommand(text)
			} else if v, ok := strings.CutPrefix(text, "\x01VERSION "); ok && se.att != nil {
				se.att.Update(func(ci *bouncer.ClientInfo) { ci.Software = clientName(strings.TrimSuffix(v, "\x01")) })
			}
			return
		}
		tags := se.clientTags(m)
		id := se.up.SendTagged(se.id, tags, m.Command, target, text)
		// The server won't echo our own line back over the raw feed, so
		// honor echo-message ourselves; plain clients display it locally.
		if se.caps["echo-message"] {
			se.send(se.tags(id, "", tags) + fmt.Sprintf(":%s!%s@%s %s %s :%s", se.unick, se.unick, serverName, m.Command, target, text))
		}
	case "REDACT": // REDACT <target> <msgid> [reason]: the network's msgid
		if len(m.Params) >= 2 {
			if theirs, ok := se.up.NetworkMsgID(m.Params[1]); ok {
				m.Params[1] = theirs
			}
		}
		se.passOn(m)
	case "TOPIC", "MODE":
		if !se.answerLocally(m.Command, m.Params) {
			se.passOn(m)
		}
	case "WHO":
		if !se.answerWho(m.Params) {
			se.passOn(m)
		}
	case "NAMES":
		if se.answerLocally(m.Command, m.Params) {
			return
		}
		if se.caps["no-implicit-names"] && len(m.Params) > 0 {
			if se.namesAsked == nil {
				se.namesAsked = map[string]int{}
			}
			for _, ch := range strings.Split(m.Params[0], ",") {
				se.namesAsked[strings.ToLower(ch)]++
			}
		}
		se.passOn(m)
	case "BATCH":
		if !se.clientBatch(m) {
			se.passOn(m)
		}
	case "CHATHISTORY":
		if !se.caps["draft/chathistory"] {
			se.passOn(m) // as before: the server's business
			return
		}
		se.chathistory(m.Params)
	case "TAGMSG":
		// typing notifications and the like: on to the network if it takes
		// tags (else dropped), and to the user's other clients
		if len(m.Params) > 0 {
			se.up.SendTagMsg(se.id, se.clientTags(m), m.Params[0])
		}
	case "NICK":
		// The server's NICK reply comes back over the raw feed and updates
		// both the upstream's and this session's nick.
		if len(m.Params) > 0 {
			se.toNetwork("NICK " + m.Params[0])
		}
	case "JOIN":
		if len(m.Params) == 0 {
			se.toNetwork(m.String())
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
			se.toNetwork(line)
		}
	case "DETACH": // ZNC's client command: /detach #chan[,#chan]
		if len(m.Params) > 0 {
			se.detach(strings.Split(m.Params[0], ","))
		}
	case "CAP":
		se.handleCap(m)
	case "USER", "PASS":
		se.numeric("462", ":You're already registered")
	case "AUTHENTICATE":
		se.numeric("907", ":You have already authenticated")
	default:
		se.passOn(m)
	}
}

// unboundOK are the commands a bouncer connection (on no network) takes.
var unboundOK = map[string]bool{"PING": true, "QUIT": true, "CAP": true, "BOUNCER": true, "USER": true, "PASS": true, "AUTHENTICATE": true}

// passOn sends a client's line to the upstream. Everything handleClient
// doesn't take (JOIN, PART, MODE, WHO, NAMES, TOPIC, LIST, …) goes straight
// there; replies come back over the raw feed. The upstream saves joins and
// parts to the network's channel list. A module (alias) may take the line
// instead.
func (se *session) passOn(m irc.Message) {
	if se.up.ClientLine(m.String()) {
		return
	}
	se.toNetwork(m.String())
}

// format renders a stored message as an IRC line for replay, tagged with
// server-time when the client negotiated it. A channel event (join, part, …)
// is "" outside a draft/event-playback batch: replayed as a real line it
// would change the client's idea of who is in the channel.
func (se *session) format(m history.Message) string { return se.formatAs(m, true) }

// formatAs renders m; playback lines (not live ones) carry the user's
// timestamp for clients without server-time.
func (se *session) formatAs(m history.Message, playback bool) string {
	return se.formatIn(m, playback, "")
}

// formatIn is formatAs inside a batch ("" for none). There, a client with
// draft/event-playback gets channel events as the lines they were (JOIN,
// KICK, …): the batch tells it they're history.
func (se *session) formatIn(m history.Message, playback bool, batch string) string {
	var b strings.Builder
	client := ""
	if !playback && se.caps["message-tags"] {
		client = m.Tags // sent live with the line
	}
	b.WriteString(se.tags(m.ID(), batch, client))
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
	sender := m.Sender
	if sender == "" {
		sender = "*"
	}
	prefix := sender + "!" + sender + "@" + se.net.Name
	if !isChat(m.Kind) {
		ev, ok := eventLine(m, prefix)
		if !ok || batch == "" || !se.caps["draft/event-playback"] {
			return ""
		}
		b.WriteString(ev)
		return b.String()
	}
	text := stamp(m.Text)
	cmd := "PRIVMSG"
	switch m.Kind {
	case "notice":
		cmd = "NOTICE"
	case "action":
		text = "\x01ACTION " + text + "\x01"
	}
	b.WriteString(fmt.Sprintf(":%s %s %s :%s", prefix, cmd, m.Target, text))
	return b.String()
}

// eventLine is a stored channel event as the line the server sent. Only the
// nick was kept, so the prefix is made up like a replayed message's.
func eventLine(m history.Message, prefix string) (string, bool) {
	l := irc.Message{Prefix: prefix, Command: strings.ToUpper(m.Kind)}
	switch m.Kind {
	case "join":
		l.Params = []string{m.Target}
	case "part":
		l.Params = []string{m.Target}
		if m.Text != "" {
			l.Params = append(l.Params, m.Text)
		}
	case "quit":
		l.Params = []string{m.Text}
	case "kick":
		l.Params = []string{m.Target, m.Extra, m.Text}
	case "nick":
		l.Params = []string{m.Extra}
	case "topic":
		l.Params = []string{m.Target, m.Text}
	case "mode":
		l.Params = append([]string{m.Target}, strings.Fields(m.Text)...)
	default:
		return "", false
	}
	return l.String(), true
}

// isChat reports whether a stored kind is something someone said.
func isChat(kind string) bool {
	return kind == "message" || kind == "notice" || kind == "action" || kind == ""
}

func (se *session) send(line string) {
	se.wmu.Lock()
	defer se.wmu.Unlock()
	if se.capture != nil {
		*se.capture = append(*se.capture, line)
		return
	}
	se.write(line + "\r\n")
}

func (se *session) numeric(code, trailing string) {
	n := orElse(se.unick, orElse(se.nick, "*"))
	se.send(fmt.Sprintf(":%s %s %s %s", serverName, code, n, ircText(trailing)))
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

func findNetworkID(nets []store.Network, id string) (store.Network, bool) {
	for _, n := range nets {
		if strconv.FormatInt(n.ID, 10) == id {
			return n, true
		}
	}
	return store.Network{}, false
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
