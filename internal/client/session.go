package client

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"golang.org/x/crypto/bcrypt"

	"code.ircforever.org/ircforever/evrd/internal/irc"
	"code.ircforever.org/ircforever/evrd/internal/store"
	"code.ircforever.org/ircforever/evrd/internal/upstream"
)

var sessionSeq atomic.Int64

type session struct {
	id   string
	srv  *Server
	conn net.Conn
	r    *bufio.Scanner

	wmu sync.Mutex
	w   *bufio.Writer

	// registration state
	pass           string
	nick           string
	user           string
	capNegotiating bool
	caps           map[string]bool

	// attached state
	net  store.Network
	up   *upstream.Conn
	unick string
}

func newSession(srv *Server, conn net.Conn) *session {
	return &session{
		id:   strconv.FormatInt(sessionSeq.Add(1), 10),
		srv:  srv,
		conn: conn,
		r:    bufio.NewScanner(conn),
		w:    bufio.NewWriter(conn),
		caps: map[string]bool{},
	}
}

func (se *session) serve(ctx context.Context) {
	defer se.conn.Close()
	se.r.Buffer(make([]byte, 0, 4096), 1<<20)

	// Registration: gather CAP / PASS / NICK / USER.
	for se.r.Scan() {
		m := irc.Parse(se.r.Text())
		switch m.Command {
		case "CAP":
			se.handleCap(m)
		case "PASS":
			if len(m.Params) > 0 {
				se.pass = m.Params[0]
			}
		case "NICK":
			if len(m.Params) > 0 {
				se.nick = m.Params[0]
			}
		case "USER":
			if len(m.Params) > 0 {
				se.user = m.Params[0]
			}
		case "QUIT":
			return
		}
		if se.nick != "" && se.user != "" && !se.capNegotiating {
			break
		}
	}
	if se.r.Err() != nil || se.nick == "" {
		return
	}
	if !se.attach() {
		return
	}
	se.bridge(ctx)
}

func (se *session) handleCap(m irc.Message) {
	if len(m.Params) == 0 {
		return
	}
	switch strings.ToUpper(m.Params[0]) {
	case "LS":
		se.capNegotiating = true
		se.send("CAP * LS :echo-message server-time")
	case "REQ":
		if len(m.Params) > 1 {
			for _, c := range strings.Fields(m.Params[1]) {
				if c == "echo-message" || c == "server-time" {
					se.caps[c] = true
				}
			}
			se.send("CAP * ACK :" + m.Params[1])
		}
	case "END":
		se.capNegotiating = false
	}
}

// attach authenticates and binds the session to a network, then sends the
// welcome burst, channel state, and backlog.
func (se *session) attach() bool {
	login, password, ok := strings.Cut(se.pass, ":")
	if !ok || password == "" {
		se.numeric("464", ":Password required — use PASS <user>[/<network>]:<password>")
		return false
	}
	username, network, _ := strings.Cut(login, "/")

	u, err := se.srv.b.Store().UserByUsername(username)
	if err != nil || bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)) != nil {
		se.numeric("464", ":Invalid credentials")
		return false
	}

	nets, _ := se.srv.b.Store().ListNetworks(u.ID)
	if len(nets) == 0 {
		se.numeric("464", ":No networks configured — add one in the web UI first")
		return false
	}
	chosen := nets[0]
	if network != "" {
		match := false
		for _, n := range nets {
			if strings.EqualFold(n.Name, network) {
				chosen, match = n, true
				break
			}
		}
		if !match {
			se.numeric("464", ":Unknown network "+network)
			return false
		}
	}
	se.net = chosen

	// Ensure the upstream is connected, then grab it.
	se.srv.b.Connect(chosen.ID)
	conn, ok := se.srv.b.ConnFor(chosen.ID)
	if !ok {
		se.numeric("464", ":Network unavailable")
		return false
	}
	se.up = conn
	se.unick = orElse(conn.Nick(), chosen.Nick)

	se.welcome()
	se.joinChannels()
	se.replayBacklog()
	return true
}

func (se *session) welcome() {
	n := se.unick
	se.send(fmt.Sprintf(":%s 001 %s :Welcome to evrd — attached to %s", serverName, n, se.net.Name))
	se.send(fmt.Sprintf(":%s 002 %s :Your host is %s", serverName, n, serverName))
	se.send(fmt.Sprintf(":%s 003 %s :evrd bouncer", serverName, n))
	se.send(fmt.Sprintf(":%s 004 %s %s evrd o o", serverName, n, serverName))
	se.send(fmt.Sprintf(":%s 375 %s :- %s message of the day -", serverName, n, serverName))
	se.send(fmt.Sprintf(":%s 372 %s :- network %s (%s:%d)", serverName, n, se.net.Name, se.net.Host, se.net.Port))
	se.send(fmt.Sprintf(":%s 376 %s :End of /MOTD command.", serverName, n))

	// If the client registered under a different nick than the upstream's, tell
	// it explicitly — otherwise it won't recognize the synthesized self-JOIN and
	// won't open the channel.
	if se.nick != "" && se.nick != n {
		se.send(fmt.Sprintf(":%s!%s@%s NICK %s", se.nick, se.nick, serverName, n))
	}
}

func (se *session) joinChannels() {
	for _, ch := range se.net.Channels {
		ch = strings.TrimSpace(ch)
		if ch == "" {
			continue
		}
		// Make the client open the buffer, then ask the upstream for the real
		// names/topic — the replies stream back over the raw feed.
		se.send(fmt.Sprintf(":%s!%s@%s JOIN %s", se.unick, se.unick, serverName, ch))
		se.up.SendRaw("NAMES " + ch)
		se.up.SendRaw("TOPIC " + ch)
	}
}

func (se *session) replayBacklog() {
	for _, ch := range se.net.Channels {
		ch = strings.TrimSpace(ch)
		if ch == "" {
			continue
		}
		msgs, _ := se.srv.b.Store().RecentMessages(se.net.ID, ch, 50)
		for _, m := range msgs {
			se.send(se.format(m))
		}
	}
}

// bridge relays the upstream's raw feed to the client and the client's commands
// back to the upstream, until either side closes.
func (se *session) bridge(ctx context.Context) {
	raw, unsubRaw := se.up.SubscribeRaw()
	defer unsubRaw()
	msgs, unsubMsg := se.up.Subscribe()
	defer unsubMsg()

	done := make(chan struct{})
	go func() {
		defer close(done)
		for se.r.Scan() {
			se.handleClient(irc.Parse(se.r.Text()))
		}
	}()

	for {
		select {
		case line, ok := <-raw:
			if !ok {
				return
			}
			// Incoming traffic + channel state come verbatim from the server.
			if cmd := command(line); cmd == "PING" || cmd == "PONG" {
				continue
			}
			se.send(line)
		case m, ok := <-msgs:
			if !ok {
				return
			}
			// Our own outgoing only — incoming arrives via the raw feed. Skip
			// what this session sent (already echoed/displayed); forward sends
			// from other sessions (web, another client) so we stay in sync.
			if m.Sender == se.unick && m.Origin != se.id {
				se.send(se.format(m))
			}
		case <-done:
			return
		case <-ctx.Done():
			return
		}
	}
}

func (se *session) handleClient(m irc.Message) {
	switch m.Command {
	case "PING":
		se.send("PONG :" + lastParam(m))
	case "QUIT":
		se.conn.Close()
	case "PRIVMSG":
		if len(m.Params) >= 2 {
			target, text := m.Params[0], m.Params[len(m.Params)-1]
			se.up.SendMessageFrom(se.id, target, text)
			// The server won't echo our own line back over the raw feed, so
			// honor echo-message ourselves; plain clients display it locally.
			if se.caps["echo-message"] {
				se.send(fmt.Sprintf(":%s!%s@%s PRIVMSG %s :%s", se.unick, se.unick, serverName, target, text))
			}
		}
	case "CAP", "NICK", "USER", "PASS":
		// registration leftovers — ignore post-attach
	default:
		// Everything else (JOIN, PART, MODE, WHO, NAMES, TOPIC, LIST, …) goes
		// straight to the upstream; replies come back over the raw feed.
		se.up.SendRaw(m.String())
	}
}

// format renders a stored message as an IRC line for replay, tagged with
// server-time when the client negotiated it.
func (se *session) format(m store.Message) string {
	var b strings.Builder
	if se.caps["server-time"] {
		b.WriteString("@time=" + m.Time.UTC().Format("2006-01-02T15:04:05.000Z") + " ")
	}
	sender := m.Sender
	if sender == "" {
		sender = "*"
	}
	text := m.Text
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

func (se *session) send(line string) {
	se.wmu.Lock()
	defer se.wmu.Unlock()
	se.w.WriteString(line + "\r\n")
	se.w.Flush()
}

func (se *session) numeric(code, trailing string) {
	n := se.nick
	if n == "" {
		n = "*"
	}
	se.send(fmt.Sprintf(":%s %s %s %s", serverName, code, n, trailing))
}

func command(line string) string {
	if strings.HasPrefix(line, "@") {
		if i := strings.IndexByte(line, ' '); i >= 0 {
			line = line[i+1:]
		}
	}
	if strings.HasPrefix(line, ":") {
		if i := strings.IndexByte(line, ' '); i >= 0 {
			line = line[i+1:]
		}
	}
	if i := strings.IndexByte(line, ' '); i >= 0 {
		return strings.ToUpper(line[:i])
	}
	return strings.ToUpper(line)
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
