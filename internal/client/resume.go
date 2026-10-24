package client

// A device back soon after its connection dropped (a phone between cell
// towers) gets a slimmer welcome: the message of the day and *status's
// VERSION question it had a minute ago are skipped, as is a new activity
// entry. Its channels' JOIN, names and topics still come — a client starts
// a new connection afresh — but from evrd's own state, without asking the
// network, and its playback is just what it missed (its device position).

import (
	"context"
	"time"

	"github.com/4evrd/evrd/internal/bouncer"
	"github.com/4evrd/evrd/internal/history"
)

// resumeWithin is how soon after dropping a device counts as resuming.
const resumeWithin = 2 * time.Minute

// moreCap is the most lines sayMore counts.
const moreCap = 5000

// resuming reports whether this client's device dropped from network
// within resumeWithin; if so its software carries over from then.
func (se *session) resuming(userID int64, network string) bool {
	if se.att == nil {
		return false
	}
	info := se.att.Info()
	if info.Device == "" {
		return false // can't tell it's the same device
	}
	if se.att.Replaced() {
		return true // its previous connection was still attached
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	d, ok := se.srv.b.LastDisconnect(ctx, userID, bouncer.DeviceKey(info, network), time.Now().Add(-resumeWithin))
	if !ok || d.Reason == bouncer.DropQuit {
		return false // a client that quit and came back is starting over
	}
	if d.Software != "" {
		se.att.Update(func(ci *bouncer.ClientInfo) { ci.Software = d.Software })
	}
	return true
}

// sayMore tells the client when a buffer missed more lines since from than
// the n it played back, and how to get them.
func (se *session) sayMore(buffer string, n int, from time.Time) {
	q := history.Query{After: history.ID{Nanos: from.UnixNano(), Seq: history.MaxSeq}, Limit: moreCap + n}
	msgs, err := se.readLines(buffer, q, false)
	if err != nil || len(msgs) <= n {
		return
	}
	more := len(msgs) - n
	se.say("playback.more", "count", more, "buffer", buffer, "lines", min(more+n, maxPlayback))
}
