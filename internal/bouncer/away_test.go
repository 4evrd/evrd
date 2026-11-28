package bouncer

import (
	"strings"
	"testing"
	"time"

	"github.com/4evrd/evrd/internal/store"
)

func TestAwayText(t *testing.T) {
	at := time.Date(2026, 11, 27, 9, 5, 7, 0, time.UTC)
	u := store.User{AwayMessage: "Gone since %awaytime%", Timezone: "Europe/Berlin"}
	if got := awayText(u, at); got != "Gone since Fri Nov 27 10:05:07 2026" {
		t.Errorf("in the user's zone: %q", got)
	}
	if got := awayText(store.User{AwayMessage: "plain"}, at); got != "plain" {
		t.Errorf("no placeholder: %q", got)
	}

	r := newLiveRig(t)
	if got := r.b.awayMessage(store.User{}, at); got != AwayMessage {
		t.Errorf("no message of the user's: %q", got)
	}
	if got := r.b.awayMessage(store.User{AwayMessage: "brb"}, at); got != "brb" {
		t.Errorf("the user's: %q", got)
	}
	// managed mode's wins: the managing service decides
	r.b.SetManaged(Managed{AwayMessage: "Away for now"})
	if got := r.b.awayMessage(store.User{AwayMessage: "brb"}, at); got != "Away for now" {
		t.Errorf("managed: %q", got)
	}
}

func TestOwnAwayMessage(t *testing.T) {
	r := newLiveRig(t)
	alice := r.user("alice", func(u *store.User) {
		u.AutoAway, u.AutoAwayDelay, u.AwayMessage = true, new(0), "Out at %awaytime%"
	})
	n := r.network(alice, "net", true)
	r.b.Start(r.ctx)
	r.connected(n.ID)
	var first string
	eventually(t, "away with the user's message", func() bool {
		for _, l := range r.irc.Lines() {
			if strings.HasPrefix(l, "AWAY :Out at ") {
				first = l
				return true
			}
		}
		return false
	})

	// changed while away: sent at once, with the same time
	u, _ := r.st.UserByID(t.Context(), alice.ID)
	u.AwayMessage = "Still out (since %awaytime%)"
	r.st.UpdateUser(t.Context(), u)
	r.b.RefreshAway(alice.ID)
	since := strings.TrimPrefix(first, "AWAY :Out at ")
	eventually(t, "the new message", func() bool { return r.irc.Has("AWAY :Still out (since " + since + ")") })

	// cleared: the server's
	u.AwayMessage = ""
	r.st.UpdateUser(t.Context(), u)
	r.b.RefreshAway(alice.ID)
	eventually(t, "the default", func() bool { return r.irc.Has("AWAY :" + AwayMessage) })
}
