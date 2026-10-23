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
	n, err := st.CreateNetwork(t.Context(), Network{UserID: 1, Name: "dal", Nick: "a", Host: "h", AuthMethod: AuthNickServ,
		ServicesNick: "NickServ@services.dal.net", IdentifyArgs: IdentifyPassword, IdentifyOn: IdentifyOnPrompt,
		AuthCommand: "x", GhostCmd: "ghost", JoinAfterAuth: true, AuthTimeout: 9})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := st.NetworkByID(t.Context(), n.ID)
	if got.ServicesNick != "NickServ@services.dal.net" || got.IdentifyArgs != IdentifyPassword || got.IdentifyOn != IdentifyOnPrompt ||
		got.AuthCommand != "x" || got.GhostCmd != "ghost" || !got.JoinAfterAuth || got.AuthTimeout != 9 || got.Auth() != AuthNickServ {
		t.Fatalf("%+v", got)
	}
}

func TestDeleteUserRemovesEverythingTheyOwn(t *testing.T) {
	st := open(t)
	u, _ := st.CreateUser(t.Context(), User{Username: "alice"})
	other, _ := st.CreateUser(t.Context(), User{Username: "bob"})
	st.CreateNetwork(t.Context(), Network{UserID: u.ID, Name: "x"})
	st.CreateSession(t.Context(), Session{ID: "s", UserID: u.ID, ExpiresAt: time.Now().Add(time.Hour)})
	st.CreateClientPassword(t.Context(), ClientPassword{UserID: u.ID, Name: "c", Hash: "h"})
	st.CreateSession(t.Context(), Session{ID: "keep", UserID: other.ID, ExpiresAt: time.Now().Add(time.Hour)})

	if err := st.DeleteUser(t.Context(), u.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.UserByID(t.Context(), u.ID); err != ErrNotFound {
		t.Fatal("user still there")
	}
	if nets, _ := st.ListNetworks(t.Context(), u.ID); len(nets) != 0 {
		t.Fatal("network still there")
	}
	if _, err := st.SessionByID(t.Context(), "s"); err != ErrNotFound {
		t.Fatal("session still there")
	}
	if cps, _ := st.ListClientPasswords(t.Context(), u.ID); len(cps) != 0 {
		t.Fatal("client password still there")
	}
	if _, err := st.SessionByID(t.Context(), "keep"); err != nil {
		t.Fatal("someone else's session was deleted")
	}
}

func TestUpdateUserClearsFields(t *testing.T) {
	st := open(t)
	five := 5
	u, _ := st.CreateUser(t.Context(), User{Username: "alice", MaxNetworks: &five, Suspended: true, SuspendedReason: "x"})
	u.MaxNetworks, u.Suspended, u.SuspendedReason = nil, false, ""
	if err := st.UpdateUser(t.Context(), u); err != nil {
		t.Fatal(err)
	}
	got, _ := st.UserByID(t.Context(), u.ID)
	if got.MaxNetworks != nil || got.Suspended || got.SuspendedReason != "" {
		t.Fatalf("zero values didn't stick: %+v", got)
	}
}

func TestSetNetworkChannels(t *testing.T) {
	st := open(t)
	n, _ := st.CreateNetwork(t.Context(), Network{UserID: 1, Name: "x", Channels: []string{"#a"}, Enabled: true})
	if err := st.SetNetworkChannels(t.Context(), n.ID, []string{"#a", "#b"}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetNetworkChannels(t.Context(), n.ID, nil); err != nil {
		t.Fatal(err)
	}
	got, _ := st.NetworkByID(t.Context(), n.ID)
	if len(got.Channels) != 0 || !got.Enabled || got.Name != "x" {
		t.Fatalf("got %+v", got)
	}
}

func TestMigrateLegacyMessages(t *testing.T) {
	st := open(t)
	db := st.(*gormStore).db.DB
	if err := db.Exec("CREATE TABLE messages (id integer primary key, network_id integer, target text, sender text, kind text, text text, time datetime)").Error; err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 2500; i++ {
		db.Exec("INSERT INTO messages (id, network_id, target, sender, kind, text, time) VALUES (?, 1, '#a', 'bob', 'message', ?, ?)", i, "x", time.Now())
	}
	var got []LegacyMessage
	n, err := st.MigrateLegacyMessages(t.Context(), func(m LegacyMessage) error { got = append(got, m); return nil })
	if err != nil || n != 2500 || len(got) != 2500 || got[2499].ID != 2500 || got[0].Target != "#a" {
		t.Fatalf("n=%d err=%v got=%d", n, err, len(got))
	}
	if db.Migrator().HasTable("messages") {
		t.Fatal("table not dropped")
	}
	if n, err := st.MigrateLegacyMessages(t.Context(), func(LegacyMessage) error { return nil }); n != 0 || err != nil {
		t.Fatalf("second run: %d %v", n, err)
	}
}

func TestLeases(t *testing.T) {
	st := open(t)
	now := time.Now()
	ttl := 20 * time.Second
	st.UpsertNode(t.Context(), Node{ID: "a", HTTPURL: "http://a", HeartbeatAt: now})
	st.UpsertNode(t.Context(), Node{ID: "b", HTTPURL: "http://b", HeartbeatAt: now})

	if ok, err := st.AcquireLease(t.Context(), 1, "a", now.Add(ttl), now); !ok || err != nil {
		t.Fatalf("a acquires a free lease: %v %v", ok, err)
	}
	if ok, _ := st.AcquireLease(t.Context(), 1, "b", now.Add(ttl), now); ok {
		t.Fatal("b took a live lease")
	}
	if ok, _ := st.AcquireLease(t.Context(), 1, "a", now.Add(ttl), now); !ok {
		t.Fatal("a can't re-acquire its own lease")
	}
	n, l, ok, _ := st.LeaseOwner(t.Context(), 1, now)
	if !ok || n.ID != "a" || n.HTTPURL != "http://a" || l.Epoch != 1 {
		t.Fatalf("owner: %+v %+v %v", n, l, ok)
	}

	// Expired: b takes over and the epoch moves on.
	later := now.Add(ttl + time.Second)
	if _, _, ok, _ := st.LeaseOwner(t.Context(), 1, later); ok {
		t.Fatal("expired lease still has an owner")
	}
	if ok, _ := st.AcquireLease(t.Context(), 1, "b", later.Add(ttl), later); !ok {
		t.Fatal("b can't take an expired lease")
	}
	if _, l, _, _ := st.LeaseOwner(t.Context(), 1, later); l.NodeID != "b" || l.Epoch != 2 {
		t.Fatalf("after takeover: %+v", l)
	}
	if ids, _ := st.HeldLeases(t.Context(), "a", later); len(ids) != 0 {
		t.Fatalf("a still holds %v", ids)
	}

	// Renew extends only live leases; release frees one at once.
	st.RenewLeases(t.Context(), "b", later.Add(time.Hour), later)
	if ids, _ := st.HeldLeases(t.Context(), "b", later.Add(30*time.Minute)); len(ids) != 1 {
		t.Fatal("renew didn't extend")
	}
	st.ReleaseLease(t.Context(), 1, "b", later)
	if ok, _ := st.AcquireLease(t.Context(), 1, "a", later.Add(ttl), later.Add(time.Millisecond)); !ok {
		t.Fatal("released lease couldn't be taken")
	}

	if nodes, _ := st.LiveNodes(t.Context(), now.Add(-time.Second)); len(nodes) != 2 {
		t.Fatalf("live nodes: %v", nodes)
	}
	if nodes, _ := st.LiveNodes(t.Context(), now.Add(time.Second)); len(nodes) != 0 {
		t.Fatalf("stale nodes counted live: %v", nodes)
	}

	u, _ := st.CreateUser(t.Context(), User{Username: "alice"})
	st.AcquireLease(t.Context(), u.ID, "a", later.Add(ttl), later)
	st.AcquireLease(t.Context(), 999, "a", later.Add(ttl), later)
	st.DeleteOrphanLeases(t.Context())
	if _, _, ok, _ := st.LeaseOwner(t.Context(), 999, later.Add(time.Millisecond)); ok {
		t.Fatal("lease of a missing user survived")
	}
	if _, _, ok, _ := st.LeaseOwner(t.Context(), u.ID, later.Add(time.Millisecond)); !ok {
		t.Fatal("lease of a real user was deleted")
	}
}

func TestBufferPlayback(t *testing.T) {
	st := open(t)
	n, _ := st.CreateNetwork(t.Context(), Network{UserID: 1, Name: "x"})
	two, now := 2, time.Now().UTC().Truncate(time.Second)
	if err := st.UpdateBufferPlayback(t.Context(), n.ID, []string{"#A", "bob"}, func(bp *BufferPlayback) { bp.Lines, bp.ClearedAt = &two, &now }); err != nil {
		t.Fatal(err)
	}
	on := true
	st.UpdateBufferPlayback(t.Context(), n.ID, []string{"#a"}, func(bp *BufferPlayback) { bp.AutoClear = &on })
	bps, _ := st.BufferPlaybacks(t.Context(), n.ID)
	a := bps["#a"]
	if len(bps) != 2 || *a.Lines != 2 || !*a.AutoClear || !a.ClearedAt.Equal(now) {
		t.Fatalf("%+v", bps)
	}
	// back to all defaults: gone
	st.UpdateBufferPlayback(t.Context(), n.ID, []string{"bob"}, func(bp *BufferPlayback) { *bp = BufferPlayback{} })
	if bps, _ := st.BufferPlaybacks(t.Context(), n.ID); len(bps) != 1 {
		t.Fatalf("%+v", bps)
	}
	st.DeleteNetwork(t.Context(), 1, n.ID)
	if bps, _ := st.BufferPlaybacks(t.Context(), n.ID); len(bps) != 0 {
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
	u, _ := st.CreateUser(t.Context(), User{Username: "alice"})
	st.TouchUserSeen(t.Context(), u.ID, seen)
	n, _ := st.CreateNetwork(t.Context(), Network{UserID: u.ID, Name: "x"})
	// as before buffer playback existed
	st.(*gormStore).db.DB.Exec("DROP TABLE buffer_playbacks")
	st.Close()

	st, err = Open("sqlite:" + path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	bps, _ := st.BufferPlaybacks(t.Context(), n.ID)
	if at := bps[""].ClearedAt; at == nil || !at.Equal(seen) {
		t.Fatalf("%+v", bps)
	}
}
