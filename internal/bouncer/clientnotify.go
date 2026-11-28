package bouncer

// clientnotify (ZNC's): a user's IRC clients hear from *status when another
// one attaches, or goes (store.User.ClientNotify*). The words are the
// client package's; the bouncer knows who's attached and which addresses
// it has seen.

import (
	"context"
	"time"

	"github.com/4evrd/evrd/internal/messages"
)

// keepClientIPs is how many addresses a user's clients attached from are
// remembered, for "new addresses only".
const keepClientIPs = 50

// Tell sets how evrd passes the client a notice. f may be called from any
// goroutine and must not block.
func (a *Attachment) Tell(f func(messages.Text)) {
	a.b.mu.Lock()
	a.c.tell = f
	a.b.mu.Unlock()
}

// TellOthers passes t to a user's attached IRC clients but except (a
// client's ID), reporting how many it reached.
func (b *Bouncer) TellOthers(userID int64, except string, t messages.Text) int {
	b.mu.Lock()
	var tell []func(messages.Text)
	for c := range b.clients[userID] {
		if c.info.ID != except && c.tell != nil {
			tell = append(tell, c.tell)
		}
	}
	b.mu.Unlock()
	for _, f := range tell {
		f(t)
	}
	return len(tell)
}

// SeeClientIP remembers that one of a user's clients attached from ip,
// reporting whether none had from there lately (it's new). An address
// that can't be remembered counts as new.
func (b *Bouncer) SeeClientIP(userID int64, ip string) bool {
	if ip == "" {
		return true
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	fresh, err := b.store.SeeClientIP(ctx, userID, ip, time.Now(), keepClientIPs)
	if err != nil {
		b.log.Warn("remember a client's address", "user", userID, "err", err)
		return true
	}
	return fresh
}
