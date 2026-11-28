package bouncer

// Managed mode (internal/managed): what the connections say about
// themselves, and WEBIRC with the address of the user's latest client.

import (
	"context"
	"time"

	"github.com/4evrd/evrd/internal/store"
)

// Managed is how connections present themselves in managed mode.
type Managed struct {
	// AwayMessage replaces AwayMessage; VersionReply answers CTCP VERSION
	// while no client is attached; QuitMessage is the quit message for
	// accounts without their own; ReplyFrom is who modules' replies come
	// from.
	AwayMessage, VersionReply, QuitMessage, ReplyFrom string
	// NickGuard keeps connections that don't log in to services off
	// nicks registered to someone else (upstream.Options.NickGuard).
	NickGuard bool
	// NickGuardGrace is how long a guarded connection may stay on a
	// registered nick for the user to identify (asked at each connect:
	// the managed API can change it); GuardWarning the notice clients get
	// then (upstream.Options).
	NickGuardGrace func() time.Duration
	GuardWarning   string
	// WebIRC is the WEBIRC line for a user at ip with ident ("" none).
	WebIRC func(ctx context.Context, ip, ident string) string
}

// SetManaged turns managed mode's presentation on. Call it before Start.
func (b *Bouncer) SetManaged(m Managed) {
	b.mu.Lock()
	b.managed = &m
	b.mu.Unlock()
}

// awayMessage is u's auto-away message, u having gone away at since.
func (b *Bouncer) awayMessage(u store.User, since time.Time) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.awayMessageLocked(u, since)
}

// awayMessageLocked is awayMessage with b.mu held: managed mode's when it
// has one (the managing service decides how its accounts look), else the
// user's own (away.go), else AwayMessage.
func (b *Bouncer) awayMessageLocked(u store.User, since time.Time) string {
	if b.managed != nil && b.managed.AwayMessage != "" {
		return b.managed.AwayMessage
	}
	if u.AwayMessage != "" {
		return awayText(u, since)
	}
	return AwayMessage
}

// noteIP remembers the address a user's client attached from, for WEBIRC.
func (b *Bouncer) noteIP(userID int64, ip string) {
	if ip == "" {
		return
	}
	b.mu.Lock()
	if b.lastIP == nil {
		b.lastIP = map[int64]string{}
	}
	b.lastIP[userID] = ip
	b.mu.Unlock()
}

// userIP is the address the user's latest client came from: the one that
// attached last here, else the last IRC sign-in on record ("" none).
func (b *Bouncer) userIP(userID int64) string {
	b.mu.Lock()
	ip := b.lastIP[userID]
	b.mu.Unlock()
	if ip != "" {
		return ip
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ins, err := b.store.IRCSignIns(ctx, userID, time.Time{}, 20)
	if err != nil {
		return ""
	}
	for _, s := range ins { // newest first
		if s.Result == "ok" && s.IP != "" {
			return s.IP
		}
	}
	return ""
}

// webIRC is the WEBIRC opener for userID's connections with ident (nil:
// not managed).
func (b *Bouncer) webIRC(userID int64, ident string) func(ctx context.Context) string {
	if b.managed == nil || b.managed.WebIRC == nil {
		return nil
	}
	f := b.managed.WebIRC
	return func(ctx context.Context) string { return f(ctx, b.userIP(userID), ident) }
}
