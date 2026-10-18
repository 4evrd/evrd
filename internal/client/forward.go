package client

// In a cluster a user's IRC clients are served by the node that owns the user
// (internal/cluster). A client may connect to any node: once it has
// registered (CAP, PASS, NICK, USER), a node that isn't the owner opens a
// connection to the owner's internal IRC listener, introduces it with
//
//	EVRD-PROXY <cluster secret> <client address> <negotiated caps | ->
//
// replays PASS/NICK/USER, and then splices the two sockets. CAP was already
// answered here, so it isn't replayed; the owner just takes the caps as
// negotiated. The owner authenticates the client as usual.

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"io"
	"net"
	"sort"
	"strings"
	"sync"
	"time"

	"code.ircforever.org/ircforever/evrd/internal/cluster"
	"code.ircforever.org/ircforever/evrd/internal/irc"
	"code.ircforever.org/ircforever/evrd/internal/store"
)

const movingMsg = "Your bouncer is moving to another server — reconnect in a moment"

// Cluster is what the IRC listener needs from internal/cluster.
type Cluster interface {
	Self() store.Node
	Owns(userID int64) bool
	WaitOwner(ctx context.Context, userID int64, max time.Duration) (store.Node, error)
}

// SetCluster makes the listener forward clients to the node owning their user.
func (s *Server) SetCluster(cl Cluster, secret string) { s.cl, s.secret = cl, secret }

// ListenInternal accepts clients forwarded by other nodes. It must only be
// reachable between nodes.
func (s *Server) ListenInternal(ctx context.Context, addr string) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		s.log.Error("internal irc listen", "addr", addr, "err", err)
		return
	}
	s.log.Info("internal irc listener", "addr", addr)
	go func() {
		<-ctx.Done()
		ln.Close()
	}()
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		se := newSession(s, conn)
		se.internal = true
		go se.serve(ctx)
	}
}

// acceptForwarded checks a forwarded connection's introduction line.
func (se *session) acceptForwarded() bool {
	se.conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	line, err := se.readLine()
	se.conn.SetReadDeadline(time.Time{})
	if err != nil {
		return false
	}
	m := irc.Parse(line)
	if m.Command != "EVRD-PROXY" || len(m.Params) < 3 || se.srv.secret == "" ||
		subtle.ConstantTimeCompare([]byte(m.Params[0]), []byte(se.srv.secret)) != 1 {
		se.srv.log.Warn("refused an internal irc connection", "from", se.conn.RemoteAddr())
		return false
	}
	if m.Params[2] != "-" {
		for _, c := range strings.Split(m.Params[2], ",") {
			if supportedCaps[c] {
				se.caps[c] = true
			}
		}
	}
	return true
}

// forwardIfElsewhere hands a registered client to the node that owns its
// user. It reports whether it did (and the session is over here).
func (se *session) forwardIfElsewhere(ctx context.Context) bool {
	if se.srv.cl == nil || se.internal {
		return false
	}
	login, _, _ := strings.Cut(se.pass, ":")
	username, _, _ := strings.Cut(login, "/")
	u, err := se.srv.b.Store().UserByUsername(strings.ToLower(username))
	if err != nil || se.srv.cl.Owns(u.ID) {
		return false // unknown user (attach refuses it) or ours
	}
	node, err := se.srv.cl.WaitOwner(ctx, u.ID, 10*time.Second)
	if errors.Is(err, cluster.ErrNoOwner) || (err == nil && node.ID == se.srv.cl.Self().ID) {
		return false // nobody runs them (suspended?): attach says why
	}
	if err != nil || node.IRCAddr == "" {
		se.numeric("464", ":"+movingMsg)
		return true
	}
	if err := se.splice(node); err != nil {
		se.srv.log.Warn("forward irc client", "node", node.ID, "err", err)
		se.numeric("464", ":"+movingMsg)
	}
	return true
}

// splice connects the client to the owner and copies bytes both ways until
// either side closes.
func (se *session) splice(node store.Node) error {
	owner, err := net.DialTimeout("tcp", node.IRCAddr, 5*time.Second)
	if err != nil {
		return err
	}
	defer owner.Close()

	var caps []string
	for c := range se.caps {
		caps = append(caps, c)
	}
	sort.Strings(caps)
	capList := strings.Join(caps, ",")
	if capList == "" {
		capList = "-"
	}
	intro := fmt.Sprintf("EVRD-PROXY %s %s %s\r\n", se.srv.secret, remoteHost(se.conn), capList)
	for _, l := range se.regLines {
		intro += l + "\r\n"
	}
	// Whatever the client already sent past registration is in our buffer.
	pending, _ := se.r.Peek(se.r.Buffered())
	if _, err := io.WriteString(owner, intro); err != nil {
		return err
	}
	if _, err := owner.Write(pending); err != nil {
		return err
	}

	var once sync.Once
	stop := func() { once.Do(func() { owner.Close(); se.conn.Close() }) }
	go func() { io.Copy(owner, se.conn); stop() }()
	io.Copy(se.conn, owner)
	stop()
	return nil
}

func remoteHost(c net.Conn) string {
	host, _, err := net.SplitHostPort(c.RemoteAddr().String())
	if err != nil || host == "" {
		return "-"
	}
	return host
}
