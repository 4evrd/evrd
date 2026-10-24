package client

// Why an IRC client's connection ended (issue #64), for the IRC clients page,
// the admin overview and metrics.

import (
	"errors"
	"io"
	"net"
	"time"

	"github.com/4evrd/evrd/internal/bouncer"
)

// drop notes why the connection is ending, unless a reason is noted
// already: the first one is the cause, what follows is its effect (closing
// the socket makes the read fail too).
func (se *session) drop(reason string) {
	se.dropMu.Lock()
	defer se.dropMu.Unlock()
	if se.dropped == "" {
		se.dropped, se.droppedAt = reason, time.Now()
	}
}

// readDrop is why reading from the client failed ("" when evrd closed the
// connection itself: whatever closed it said why).
func readDrop(err error) string {
	var ne net.Error
	switch {
	case errors.Is(err, net.ErrClosed):
		return ""
	case errors.Is(err, io.EOF):
		return bouncer.DropClosed
	case errors.As(err, &ne) && ne.Timeout():
		return bouncer.DropPingTimeout
	default: // reset, broken pipe, a TLS failure…
		return bouncer.DropReset
	}
}

// recordDrop records an attached client's going with the bouncer.
func (se *session) recordDrop() {
	if se.att == nil || se.locked != nil {
		return // never attached, or never past its second factor
	}
	se.dropMu.Lock()
	reason := se.dropped
	se.dropMu.Unlock()
	if reason == "" {
		reason = bouncer.DropClosed
	}
	se.srv.b.RecordDisconnect(se.uid, se.att.Info(), se.net.Name, reason, time.Now())
}
