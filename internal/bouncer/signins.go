package bouncer

import (
	"context"
	"time"

	"github.com/4evrd/evrd/internal/store"
)

// IRC sign-ins (store.IRCSignIn): the shape of what each client sent, for
// Admin → IRC sign-ins. Kept as long as client disconnects.

// RecordSignIn keeps s, returning its id (0 when it couldn't be kept).
func (b *Bouncer) RecordSignIn(s store.IRCSignIn) int64 {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	id, err := b.store.AddIRCSignIn(ctx, s)
	if err != nil {
		b.log.Warn("record irc sign-in", "err", err)
		return 0
	}
	return id
}

// SettleSignIn sets sign-in id's result, giving it to userID if it had
// nobody (a link allowed on the website).
func (b *Bouncer) SettleSignIn(id, userID int64, result string) {
	if id == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := b.store.SettleIRCSignIn(ctx, id, userID, result); err != nil {
		b.log.Warn("record irc sign-in", "err", err)
	}
}

// SignIns lists sign-ins of the last dropsKept, newest first: a user's,
// everyone's (0) or unknown ones' (-1).
func (b *Bouncer) SignIns(ctx context.Context, userID int64, limit int) ([]store.IRCSignIn, error) {
	return b.store.IRCSignIns(ctx, userID, time.Now().Add(-dropsKept), limit)
}
