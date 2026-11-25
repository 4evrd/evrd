package upstream

import (
	"testing"

	"github.com/4evrd/evrd/internal/store"
)

func TestEditChannels(t *testing.T) {
	// not connected: the list changes, nothing is sent (it's joined on connect)
	off := New(store.Network{ID: 41, Nick: "dora", Host: "127.0.0.1", Port: 1, Channels: []string{"#a", "#b"}}, quiet, Options{})
	off.EditChannels([]string{"#B"}, []string{"#c"})
	if got := off.Channels(); len(got) != 2 || got[0] != "#a" || got[1] != "#c" {
		t.Fatalf("saved %v", got)
	}

	// connected: parted and joined on the network
	s, c := connected(t, store.Network{ID: 42, Nick: "dora", Channels: []string{"#a"}}, Options{})
	s.send(":dora!u@h JOIN #a")
	settle(t, s)
	c.EditChannels([]string{"#a"}, []string{"#z", "#a2"})
	eventually(t, "parted and joined", func() bool { return s.has("PART #a") && s.has("JOIN #z") && s.has("JOIN #a2") })
}
