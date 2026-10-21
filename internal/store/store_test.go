package store

import (
	"path/filepath"
	"testing"
	"time"
)

func open(t *testing.T) Store {
	t.Helper()
	st, err := Open("sqlite:" + filepath.Join(t.TempDir(), "evrd.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestNetworkAuthAndItsColumns(t *testing.T) {
	st := open(t)
	if (Network{}).Auth() != AuthAuto || (Network{AuthMethod: AuthNickServ}).Auth() != AuthNickServ {
		t.Fatal("an empty method is auto")
	}
	n, err := st.CreateNetwork(Network{UserID: 1, Name: "dal", Nick: "a", Host: "h", AuthMethod: AuthNickServ,
		ServicesNick: "NickServ@services.dal.net", IdentifyArgs: IdentifyPassword, IdentifyOn: IdentifyOnPrompt,
		AuthCommand: "x", GhostCmd: "ghost", JoinAfterAuth: true, AuthTimeout: 9})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := st.NetworkByID(n.ID)
	if got.ServicesNick != "NickServ@services.dal.net" || got.IdentifyArgs != IdentifyPassword || got.IdentifyOn != IdentifyOnPrompt ||
		got.AuthCommand != "x" || got.GhostCmd != "ghost" || !got.JoinAfterAuth || got.AuthTimeout != 9 || got.Auth() != AuthNickServ {
		t.Fatalf("%+v", got)
	}
}

func TestDeleteUserRemovesEverythingTheyOwn(t *testing.T) {
	st := open(t)
	u, _ := st.CreateUser(User{Username: "alice"})
	other, _ := st.CreateUser(User{Username: "bob"})
	st.CreateNetwork(Network{UserID: u.ID, Name: "x"})
	st.CreateSession(Session{ID: "s", UserID: u.ID, ExpiresAt: time.Now().Add(time.Hour)})
	st.CreateClientPassword(ClientPassword{UserID: u.ID, Name: "c", Hash: "h"})
	st.CreateSession(Session{ID: "keep", UserID: other.ID, ExpiresAt: time.Now().Add(time.Hour)})

	if err := st.DeleteUser(u.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.UserByID(u.ID); err != ErrNotFound {
		t.Fatal("user still there")
	}
	if nets, _ := st.ListNetworks(u.ID); len(nets) != 0 {
		t.Fatal("network still there")
	}
	if _, err := st.SessionByID("s"); err != ErrNotFound {
		t.Fatal("session still there")
	}
	if cps, _ := st.ListClientPasswords(u.ID); len(cps) != 0 {
		t.Fatal("client password still there")
	}
	if _, err := st.SessionByID("keep"); err != nil {
		t.Fatal("someone else's session was deleted")
	}
}

func TestUpdateUserClearsFields(t *testing.T) {
	st := open(t)
	five := 5
	u, _ := st.CreateUser(User{Username: "alice", MaxNetworks: &five, Suspended: true, SuspendedReason: "x"})
	u.MaxNetworks, u.Suspended, u.SuspendedReason = nil, false, ""
	if err := st.UpdateUser(u); err != nil {
		t.Fatal(err)
	}
	got, _ := st.UserByID(u.ID)
	if got.MaxNetworks != nil || got.Suspended || got.SuspendedReason != "" {
		t.Fatalf("zero values didn't stick: %+v", got)
	}
}

func TestSetNetworkChannels(t *testing.T) {
	st := open(t)
	n, _ := st.CreateNetwork(Network{UserID: 1, Name: "x", Channels: []string{"#a"}, Enabled: true})
	if err := st.SetNetworkChannels(n.ID, []string{"#a", "#b"}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetNetworkChannels(n.ID, nil); err != nil {
		t.Fatal(err)
	}
	got, _ := st.NetworkByID(n.ID)
	if len(got.Channels) != 0 || !got.Enabled || got.Name != "x" {
		t.Fatalf("got %+v", got)
	}
}

func TestMigrateLegacyMessages(t *testing.T) {
	st := open(t)
	db := st.(*gormStore).db
	if err := db.Exec("CREATE TABLE messages (id integer primary key, network_id integer, target text, sender text, kind text, text text, time datetime)").Error; err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 2500; i++ {
		db.Exec("INSERT INTO messages (id, network_id, target, sender, kind, text, time) VALUES (?, 1, '#a', 'bob', 'message', ?, ?)", i, "x", time.Now())
	}
	var got []LegacyMessage
	n, err := st.MigrateLegacyMessages(func(m LegacyMessage) error { got = append(got, m); return nil })
	if err != nil || n != 2500 || len(got) != 2500 || got[2499].ID != 2500 || got[0].Target != "#a" {
		t.Fatalf("n=%d err=%v got=%d", n, err, len(got))
	}
	if db.Migrator().HasTable("messages") {
		t.Fatal("table not dropped")
	}
	if n, err := st.MigrateLegacyMessages(func(LegacyMessage) error { return nil }); n != 0 || err != nil {
		t.Fatalf("second run: %d %v", n, err)
	}
}

func TestLeases(t *testing.T) {
	st := open(t)
	now := time.Now()
	ttl := 20 * time.Second
	st.UpsertNode(Node{ID: "a", HTTPURL: "http://a", HeartbeatAt: now})
	st.UpsertNode(Node{ID: "b", HTTPURL: "http://b", HeartbeatAt: now})

	if ok, err := st.AcquireLease(1, "a", now.Add(ttl), now); !ok || err != nil {
		t.Fatalf("a acquires a free lease: %v %v", ok, err)
	}
	if ok, _ := st.AcquireLease(1, "b", now.Add(ttl), now); ok {
		t.Fatal("b took a live lease")
	}
	if ok, _ := st.AcquireLease(1, "a", now.Add(ttl), now); !ok {
		t.Fatal("a can't re-acquire its own lease")
	}
	n, l, ok, _ := st.LeaseOwner(1, now)
	if !ok || n.ID != "a" || n.HTTPURL != "http://a" || l.Epoch != 1 {
		t.Fatalf("owner: %+v %+v %v", n, l, ok)
	}

	// Expired: b takes over and the epoch moves on.
	later := now.Add(ttl + time.Second)
	if _, _, ok, _ := st.LeaseOwner(1, later); ok {
		t.Fatal("expired lease still has an owner")
	}
	if ok, _ := st.AcquireLease(1, "b", later.Add(ttl), later); !ok {
		t.Fatal("b can't take an expired lease")
	}
	if _, l, _, _ := st.LeaseOwner(1, later); l.NodeID != "b" || l.Epoch != 2 {
		t.Fatalf("after takeover: %+v", l)
	}
	if ids, _ := st.HeldLeases("a", later); len(ids) != 0 {
		t.Fatalf("a still holds %v", ids)
	}

	// Renew extends only live leases; release frees one at once.
	st.RenewLeases("b", later.Add(time.Hour), later)
	if ids, _ := st.HeldLeases("b", later.Add(30*time.Minute)); len(ids) != 1 {
		t.Fatal("renew didn't extend")
	}
	st.ReleaseLease(1, "b", later)
	if ok, _ := st.AcquireLease(1, "a", later.Add(ttl), later.Add(time.Millisecond)); !ok {
		t.Fatal("released lease couldn't be taken")
	}

	if nodes, _ := st.LiveNodes(now.Add(-time.Second)); len(nodes) != 2 {
		t.Fatalf("live nodes: %v", nodes)
	}
	if nodes, _ := st.LiveNodes(now.Add(time.Second)); len(nodes) != 0 {
		t.Fatalf("stale nodes counted live: %v", nodes)
	}

	u, _ := st.CreateUser(User{Username: "alice"})
	st.AcquireLease(u.ID, "a", later.Add(ttl), later)
	st.AcquireLease(999, "a", later.Add(ttl), later)
	st.DeleteOrphanLeases()
	if _, _, ok, _ := st.LeaseOwner(999, later.Add(time.Millisecond)); ok {
		t.Fatal("lease of a missing user survived")
	}
	if _, _, ok, _ := st.LeaseOwner(u.ID, later.Add(time.Millisecond)); !ok {
		t.Fatal("lease of a real user was deleted")
	}
}

func TestBufferPlayback(t *testing.T) {
	st := open(t)
	n, _ := st.CreateNetwork(Network{UserID: 1, Name: "x"})
	two, now := 2, time.Now().UTC().Truncate(time.Second)
	if err := st.UpdateBufferPlayback(n.ID, []string{"#A", "bob"}, func(bp *BufferPlayback) { bp.Lines, bp.ClearedAt = &two, &now }); err != nil {
		t.Fatal(err)
	}
	on := true
	st.UpdateBufferPlayback(n.ID, []string{"#a"}, func(bp *BufferPlayback) { bp.AutoClear = &on })
	bps, _ := st.BufferPlaybacks(n.ID)
	a := bps["#a"]
	if len(bps) != 2 || *a.Lines != 2 || !*a.AutoClear || !a.ClearedAt.Equal(now) {
		t.Fatalf("%+v", bps)
	}
	// back to all defaults: gone
	st.UpdateBufferPlayback(n.ID, []string{"bob"}, func(bp *BufferPlayback) { *bp = BufferPlayback{} })
	if bps, _ := st.BufferPlaybacks(n.ID); len(bps) != 1 {
		t.Fatalf("%+v", bps)
	}
	st.DeleteNetwork(1, n.ID)
	if bps, _ := st.BufferPlaybacks(n.ID); len(bps) != 0 {
		t.Fatalf("left after the network went: %+v", bps)
	}
}

func TestBufferPlaybackStartsFromTheLastVisit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "evrd.db")
	st, err := Open("sqlite:" + path)
	if err != nil {
		t.Fatal(err)
	}
	seen := time.Now().UTC().Truncate(time.Second)
	u, _ := st.CreateUser(User{Username: "alice"})
	st.TouchUserSeen(u.ID, seen)
	n, _ := st.CreateNetwork(Network{UserID: u.ID, Name: "x"})
	// as before buffer playback existed
	st.(*gormStore).db.Exec("DROP TABLE buffer_playbacks")
	st.Close()

	st, err = Open("sqlite:" + path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	bps, _ := st.BufferPlaybacks(n.ID)
	if at := bps[""].ClearedAt; at == nil || !at.Equal(seen) {
		t.Fatalf("%+v", bps)
	}
}
