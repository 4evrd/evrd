package client

// Telling whether an IRC client is still there (issue #92): a phone that
// loses signal doesn't close its connection, it just goes quiet, so evrd
// asks — and a client that stops taking what evrd sends is let go rather
// than holding up everything else.

import (
	"errors"
	"net"
	"time"

	"github.com/4evrd/evrd/internal/bouncer"
)

// pingToken is what evrd's PING to a client says; the client's PONG with it
// is evrd's, not the network's.
const pingToken = "evrd"

const (
	// pingQuiet is how long a client may say nothing before evrd pings it.
	pingQuiet = 90 * time.Second
	// writeStall is how long a client may take to accept a line.
	writeStall = 30 * time.Second
	// defaultPingTimeout is client_ping_timeout's default.
	defaultPingTimeout = 60 * time.Second
)

// SetClientPingTimeout sets how long evrd waits for a quiet client's answer
// to its PING before dropping it (client_ping_timeout; 0: never pings).
func (s *Server) SetClientPingTimeout(d time.Duration) { s.pingTimeout.Store(int64(d) + 1) }

// clientPingTimeout is client_ping_timeout.
func (s *Server) clientPingTimeout() time.Duration {
	if v := s.pingTimeout.Load(); v != 0 {
		return time.Duration(v - 1)
	}
	return defaultPingTimeout
}

// checkAlive pings a client that has been quiet a while, and drops it when
// it doesn't answer in time (anything it sends counts as an answer).
func (se *session) checkAlive() {
	wait := se.srv.clientPingTimeout()
	if wait <= 0 || se.web != nil {
		return
	}
	last := time.Unix(0, se.lastRead.Load())
	switch {
	case !se.pinged.IsZero() && last.After(se.pinged):
		se.pinged = time.Time{} // it said something since
	case !se.pinged.IsZero() && time.Since(se.pinged) >= wait:
		se.srv.log.Info("irc client stopped answering", "user", se.uid, "ip", se.clientIP(), "quiet", time.Since(last).Round(time.Second))
		se.kick(bouncer.DropPingTimeout)
	case se.pinged.IsZero() && time.Since(last) >= orDefault(se.srv.pingQuiet, pingQuiet):
		se.pinged = time.Now()
		se.send("PING :" + pingToken)
	}
}

// answers reports whether the client is still there: it said something
// just now, or answers a PING within wait. Asked when a connection with
// the same client password arrives (bouncer.AttachClient), to tell a second
// client from this one's ghost.
func (se *session) answers(wait time.Duration) bool {
	asked := time.Now()
	if time.Since(time.Unix(0, se.lastRead.Load())) < orDefault(se.srv.heardJustNow, heardJustNow) {
		return true
	}
	go se.send("PING :" + pingToken) // a ghost's write may stall
	for end := asked.Add(wait); time.Now().Before(end); time.Sleep(50 * time.Millisecond) {
		if time.Unix(0, se.lastRead.Load()).After(asked) {
			return true
		}
	}
	return false
}

// heardJustNow is how recently a client must have said something to count
// as there without asking.
const heardJustNow = 2 * time.Second

// kick ends the connection, noting why.
func (se *session) kick(reason string) {
	se.drop(reason)
	se.conn.Close()
}

// write sends s to the client, closing the connection as stalled when the
// client doesn't take it in time. Called with wmu held.
func (se *session) write(s string) {
	d, ok := se.conn.(interface{ SetWriteDeadline(time.Time) error })
	if ok {
		d.SetWriteDeadline(time.Now().Add(orDefault(se.srv.writeStall, writeStall)))
	}
	_, err := se.w.WriteString(s)
	if err == nil {
		err = se.w.Flush()
	}
	if ok {
		d.SetWriteDeadline(time.Time{})
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		se.drop(bouncer.DropWriteStalled)
		go se.conn.Close() // not under wmu: closing may wait on a writer
	}
}
