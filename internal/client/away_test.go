package client

import (
	"context"
	"strings"
	"testing"

	"github.com/4evrd/evrd/internal/bouncer"
)

func TestAwayMessageCommand(t *testing.T) {
	r := newRig(t)
	ic := r.attached()
	ic.status("awaymessage", "Your auto-away message is the server's: "+bouncer.AwayMessage)
	ic.expect("Auto-away is off")
	ic.status("awaymessage Gone fishing since %awaytime%", "Auto-away message set: Gone fishing since %awaytime%.")
	ic.status("awayreason", "Your auto-away message: Gone fishing since %awaytime%")
	if u, _ := r.st.UserByID(context.Background(), r.user.ID); u.AwayMessage != "Gone fishing since %awaytime%" {
		t.Fatalf("saved %q", u.AwayMessage)
	}
	ic.status("awaymessage "+strings.Repeat("x", 301), "An away message is one line of up to 300 characters.")
	ic.status("awaymessage -", "Auto-away message cleared: the server's is used")
	if u, _ := r.st.UserByID(context.Background(), r.user.ID); u.AwayMessage != "" {
		t.Fatalf("not cleared: %q", u.AwayMessage)
	}
}
