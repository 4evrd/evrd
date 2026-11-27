package bouncer

import (
	"context"
	"testing"

	"github.com/4evrd/evrd/internal/store"
)

// A client's /nick is saved on the network and the next connection
// registers with it, as a bouncer keeps the nick across reconnects.
func TestLastNickOutlivesAReconnect(t *testing.T) {
	r := newLiveRig(t)
	alice := r.user("alice", nil)
	n := r.network(alice, "net", false)
	r.b.Start(r.ctx)
	if err := r.b.Connect(n.ID); err != nil {
		t.Fatal(err)
	}
	r.connected(n.ID)
	c, _ := r.b.ConnFor(n.ID)
	c.SendRaw("NICK TheLion")
	eventually(t, "the last nick saved", func() bool {
		got, _ := r.st.NetworkByID(context.Background(), n.ID)
		return got.LastNick == "TheLion"
	})
	r.b.Disconnect(n.ID)
	if err := r.b.Connect(n.ID); err != nil {
		t.Fatal(err)
	}
	r.connected(n.ID)
	c, _ = r.b.ConnFor(n.ID)
	eventually(t, "back on the last nick", func() bool { return c.Nick() == "TheLion" })
	if r.b.CurrentNick(n.ID) != "TheLion" {
		t.Fatalf("current nick %q", r.b.CurrentNick(n.ID))
	}

	// the network's nick changed in its settings: that wins
	got, _ := r.st.NetworkByID(context.Background(), n.ID)
	got.Nick = "alice2"
	if err := r.st.UpdateNetwork(context.Background(), got); err != nil {
		t.Fatal(err)
	}
	if got, _ = r.st.NetworkByID(context.Background(), n.ID); got.LastNick != "" {
		t.Fatalf("a new nick kept the last one: %q", got.LastNick)
	}
}

// A network that logs in to services keeps to its own nick (the
// account's): the last nick isn't used there.
func TestLastNickNotForANetworkThatLogsIn(t *testing.T) {
	r := newLiveRig(t)
	alice := r.user("alice", nil)
	n, err := r.st.CreateNetwork(context.Background(), store.Network{UserID: alice.ID, Name: "net", Host: r.host, Port: r.port,
		Nick: "alice", AuthMethod: store.AuthNickServ, Password: "pw"})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.st.SetNetworkLastNick(context.Background(), n.ID, "other"); err != nil {
		t.Fatal(err)
	}
	r.b.Start(r.ctx)
	if err := r.b.Connect(n.ID); err != nil {
		t.Fatal(err)
	}
	r.connected(n.ID)
	if got := r.b.CurrentNick(n.ID); got != "alice" {
		t.Fatalf("on %q", got)
	}
}
