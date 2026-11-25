package bouncer

import "testing"

// An edit of a network's channel list in the web app reaches its running
// link: the removed channel is parted, the added one joined.
func TestEditChannelsReachesTheLink(t *testing.T) {
	r := newLiveRig(t)
	alice := r.user("alice", nil)
	n := r.network(alice, "net", true)
	if err := r.b.Start(r.ctx); err != nil {
		t.Fatal(err)
	}
	r.connected(n.ID)
	// in it as far as the link knows (the server's JOIN handled), not just asked
	eventually(t, "in #net", func() bool { c, ok := r.b.ConnFor(n.ID); return ok && c.Joined("#net") })
	r.b.EditChannels(n.ID, []string{"#net"}, []string{"#net"}) // no change: nothing sent
	r.b.EditChannels(n.ID, []string{"#net", "#Kept"}, []string{"#kept", "#new"})
	eventually(t, "parted and joined", func() bool { return r.irc.Has("PART #net") && r.irc.Has("JOIN #new") })
	if r.irc.Has("JOIN #kept") || r.irc.Has("PART #Kept") {
		t.Fatalf("case-only change acted on: %q", r.irc.Lines())
	}
	r.b.EditChannels(999999, nil, []string{"#x"}) // no such network: ignored
}
