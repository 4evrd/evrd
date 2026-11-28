package bouncer

// The user's own auto-away message (store.User.AwayMessage), like ZNC
// simple_away's reason.

import (
	"strings"
	"time"

	"github.com/4evrd/evrd/internal/store"
)

// awayTimeLayout is how %awaytime% reads: C's ctime, as ZNC writes it.
const awayTimeLayout = "Mon Jan _2 15:04:05 2006"

// awayText is u's away message with %awaytime% as since, in u's time zone.
func awayText(u store.User, since time.Time) string {
	if !strings.Contains(u.AwayMessage, "%awaytime%") {
		return u.AwayMessage
	}
	loc := time.Local
	if u.Timezone != "" {
		if l, err := time.LoadLocation(u.Timezone); err == nil {
			loc = l
		}
	}
	return strings.ReplaceAll(u.AwayMessage, "%awaytime%", since.In(loc).Format(awayTimeLayout))
}

// DefaultAwayMessage is the away message of a user who has none of their
// own.
func (b *Bouncer) DefaultAwayMessage() string { return b.awayMessage(store.User{}, time.Now()) }

// awaySinceLocked is when the user was marked away (now if they aren't yet).
func (b *Bouncer) awaySinceLocked(userID int64) time.Time {
	if t, ok := b.awayAt[userID]; ok {
		return t
	}
	return time.Now()
}
