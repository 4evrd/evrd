package client

import (
	"strings"
	"testing"
)

// A PART from one client leaves the channel for every client on that
// network, and evrd forgets it, so the next attach doesn't bring it back.
func TestPartReachesEveryClientAndIsForgotten(t *testing.T) {
	r := newRig(t)
	a := r.attached()
	b := r.attached()
	a.send("PART #one :bye")
	a.expect("PART #one")
	b.expect("PART #one")
	eventually(t, "#one forgotten", func() bool { return len(r.network("one").Channels) == 0 })

	c := r.attach("alice/one:alice-password")
	if seen := c.until(" 376 "); anyHas(seen, "JOIN #one") {
		t.Fatalf("rejoined a parted channel: %q", seen)
	}
}

// The same with the capabilities a modern client (Boson) takes.
func TestPartReachesEveryClientWithCaps(t *testing.T) {
	r := newRigCaps(t, everyLinkCap)
	caps := []string{"message-tags", "echo-message", "server-time", "batch", "draft/chathistory", "draft/read-marker", "standard-replies", "account-tag", "extended-join", "away-notify"}
	a := r.attached(caps...)
	b := r.attached(caps...)
	a.send("PART #one :bye")
	a.expect("PART #one")
	b.expect("PART #one")
	eventually(t, "#one forgotten", func() bool { return len(r.network("one").Channels) == 0 })
}

// Taking a channel out in the web app's network settings parts it for the
// clients, and a later JOIN doesn't save it back.
func TestEditedChannelListApplies(t *testing.T) {
	r := newRig(t)
	a := r.attached()
	n := r.network("one")
	n.Channels = []string{"#two"}
	if err := r.st.UpdateNetwork(r.ctx, n); err != nil {
		t.Fatal(err)
	}
	r.b.EditChannels(n.ID, []string{"#one"}, n.Channels)
	// #one may still be joining when the edit lands: it is parted then, so
	// the PART can come before or after the JOIN of #two.
	a.expectAll("PART #one", "JOIN #two")
	a.send("JOIN #three")
	a.expect("JOIN #three")
	eventually(t, "saved list", func() bool { return strings.Join(r.network("one").Channels, " ") == "#two #three" })
}
