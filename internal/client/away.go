package client

// awaymessage: the account's own auto-away message (ZNC simple_away's
// reason), from *status.

import (
	"context"
	"strings"

	"github.com/4evrd/evrd/internal/accounts"
	"github.com/4evrd/evrd/internal/bouncer"
)

// cmdAwayMessage shows the auto-away message, sets it, or (-) goes back to
// the server's. It's used the next time the account goes away: with a
// client attached to say it, the account isn't away now.
func (se *session) cmdAwayMessage(args []string) {
	u, err := se.srv.acc.UserByID(context.Background(), se.uid)
	if err != nil {
		se.say("accountFailedPlain")
		return
	}
	if len(args) == 0 {
		if u.AwayMessage == "" {
			se.say("away.default", "message", bouncer.AwayMessage)
		} else {
			se.say("away.current", "message", u.AwayMessage)
		}
		if !u.AutoAway {
			se.say("away.autoAwayOff")
		}
		return
	}
	msg := strings.Join(args, " ")
	if msg == "-" {
		msg = ""
	}
	msg, ok := accounts.CleanAwayMessage(msg)
	if !ok {
		se.say("away.tooLong", "max", accounts.MaxAwayMessage)
		return
	}
	u.AwayMessage = msg
	if err := se.srv.acc.UpdateUser(context.Background(), u); err != nil {
		se.say("saveFailed", "error", err)
		return
	}
	se.acct.AwayMessage = msg
	if msg == "" {
		se.say("away.cleared", "message", bouncer.AwayMessage)
	} else {
		se.say("away.set", "message", msg)
	}
	if !u.AutoAway {
		se.say("away.autoAwayOff")
	}
}
