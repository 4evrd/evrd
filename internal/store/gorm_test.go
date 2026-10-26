package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"
)

func TestOpenErrors(t *testing.T) {
	// A directory isn't a database file.
	if _, err := Open("sqlite:" + t.TempDir()); err == nil {
		t.Fatal("opened a directory as a database")
	}
	// Neither is a file of junk: migrating it fails.
	junk := filepath.Join(t.TempDir(), "junk.db")
	if err := os.WriteFile(junk, []byte("this is not a database, just text that goes on long enough"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(junk); err == nil {
		t.Fatal("opened a file that isn't SQLite")
	}
}

func TestOpenBarePathAndReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "evrd.db")
	st, err := Open(path) // no sqlite: prefix
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Ping(t.Context()); err != nil {
		t.Fatal(err)
	}
	u, _ := st.CreateUser(t.Context(), User{Username: "alice"})
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	if err := st.Ping(t.Context()); err == nil {
		t.Fatal("ping after close succeeded")
	}
	st, err = Open("sqlite:" + path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if got, err := st.UserByID(t.Context(), u.ID); err != nil || got.Username != "alice" {
		t.Fatalf("after reopen: %+v %v", got, err)
	}
}

func TestUsers(t *testing.T) {
	st := open(t)
	sub := "oidc|42"
	bob, err := st.CreateUser(t.Context(), User{Username: "bob", Email: "b@example.org", OIDCSubject: &sub})
	if err != nil {
		t.Fatal(err)
	}
	// A new user gets the role and playback defaults: the columns' own.
	if bob.ID == 0 || bob.Role != RoleUser || bob.IsAdmin() || bob.PlaybackLines != 50 ||
		!bob.AutoClearQueryBuffer || !bob.PrependTimestamp || bob.TimestampFormat != "[%H:%M:%S]" {
		t.Fatalf("defaults: %+v", bob)
	}
	// Explicit settings are kept.
	alice, err := st.CreateUser(t.Context(), User{Username: "alice", Role: RoleAdmin, TimestampFormat: "%H:%M", PlaybackLines: 7,
		CTCPReplies: map[string]string{"VERSION": "evrd"}, AllowedHosts: []string{"10.*"}})
	if err != nil {
		t.Fatal(err)
	}
	if !alice.IsAdmin() || alice.TimestampFormat != "%H:%M" || alice.PlaybackLines != 7 {
		t.Fatalf("explicit settings: %+v", alice)
	}

	if _, err := st.CreateUser(t.Context(), User{Username: "bob"}); err == nil {
		t.Fatal("duplicate username accepted")
	}
	if _, err := st.CreateUser(t.Context(), User{Username: "carol", OIDCSubject: &sub}); err == nil {
		t.Fatal("duplicate OIDC subject accepted")
	}

	if got, err := st.UserByUsername(t.Context(), "bob"); err != nil || got.ID != bob.ID {
		t.Fatalf("by username: %+v %v", got, err)
	}
	if _, err := st.UserByUsername(t.Context(), "nobody"); err != ErrNotFound {
		t.Fatalf("missing username: %v", err)
	}
	if got, err := st.UserBySubject(t.Context(), sub); err != nil || got.ID != bob.ID {
		t.Fatalf("by subject: %+v %v", got, err)
	}
	if _, err := st.UserBySubject(t.Context(), "nope"); err != ErrNotFound {
		t.Fatalf("missing subject: %v", err)
	}
	got, err := st.UserByID(t.Context(), alice.ID)
	if err != nil || got.CTCPReplies["VERSION"] != "evrd" || len(got.AllowedHosts) != 1 || got.AllowedHosts[0] != "10.*" {
		t.Fatalf("JSON fields: %+v %v", got, err)
	}

	users, err := st.ListUsers(t.Context())
	if err != nil || len(users) != 2 || users[0].Username != "alice" || users[1].Username != "bob" {
		t.Fatalf("list (by username): %+v %v", users, err)
	}

	// Admins who aren't suspended.
	if n, err := st.CountAdmins(t.Context()); err != nil || n != 1 {
		t.Fatalf("admins: %d %v", n, err)
	}
	bob.Role = RoleAdmin
	st.UpdateUser(t.Context(), bob)
	if n, _ := st.CountAdmins(t.Context()); n != 2 {
		t.Fatalf("admins after promotion: %d", n)
	}
	alice.Suspended = true
	st.UpdateUser(t.Context(), alice)
	if n, _ := st.CountAdmins(t.Context()); n != 1 {
		t.Fatalf("a suspended admin counted: %d", n)
	}

	at := time.Now().Add(-time.Minute).Truncate(time.Second)
	if err := st.TouchUserSeen(t.Context(), bob.ID, at); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.UserByID(t.Context(), bob.ID); got.LastSeenAt == nil || !got.LastSeenAt.Equal(at) {
		t.Fatalf("last seen: %v", got.LastSeenAt)
	}
	// UpdateUser keeps the creation time.
	if got, _ := st.UserByID(t.Context(), bob.ID); !got.CreatedAt.Equal(bob.CreatedAt) {
		t.Fatalf("created_at changed: %v -> %v", bob.CreatedAt, got.CreatedAt)
	}
}

func TestDeleteUserRemovesReadMarkersAndEvents(t *testing.T) {
	st := open(t)
	u, _ := st.CreateUser(t.Context(), User{Username: "alice"})
	other, _ := st.CreateUser(t.Context(), User{Username: "bob"})
	n, _ := st.CreateNetwork(t.Context(), Network{UserID: u.ID, Name: "x"})
	on, _ := st.CreateNetwork(t.Context(), Network{UserID: other.ID, Name: "y"})
	now := time.Now()
	st.SetReadMarker(t.Context(), n.ID, "#a", now)
	st.SetReadMarker(t.Context(), on.ID, "#a", now)
	st.AddUserEvent(t.Context(), UserEvent{UserID: u.ID, At: now, Kind: "k"})
	st.AddUserEvent(t.Context(), UserEvent{UserID: other.ID, At: now, Kind: "k"})

	if err := st.DeleteUser(t.Context(), u.ID); err != nil {
		t.Fatal(err)
	}
	if m, _ := st.ReadMarkers(t.Context(), n.ID); len(m) != 0 {
		t.Fatalf("read markers survived: %v", m)
	}
	if ev, _ := st.UserEvents(t.Context(), u.ID, time.Time{}, 10); len(ev) != 0 {
		t.Fatalf("events survived: %v", ev)
	}
	if m, _ := st.ReadMarkers(t.Context(), on.ID); len(m) != 1 {
		t.Fatal("someone else's read marker was deleted")
	}
	if ev, _ := st.UserEvents(t.Context(), other.ID, time.Time{}, 10); len(ev) != 1 {
		t.Fatal("someone else's event was deleted")
	}
	// A user without networks deletes too.
	if err := st.DeleteUser(t.Context(), other.ID+100); err != nil {
		t.Fatalf("deleting a missing user: %v", err)
	}
}

func TestSessions(t *testing.T) {
	st := open(t)
	now := time.Now().Truncate(time.Second)
	for i, id := range []string{"s1", "s2", "s3"} {
		err := st.CreateSession(t.Context(), Session{ID: id, UserID: 1, Method: "password", IP: "127.0.0.1",
			LastSeenAt: now.Add(time.Duration(i) * time.Minute), ExpiresAt: now.Add(time.Hour)})
		if err != nil {
			t.Fatal(err)
		}
	}
	st.CreateSession(t.Context(), Session{ID: "old", UserID: 1, LastSeenAt: now, ExpiresAt: now.Add(-time.Minute)})
	st.CreateSession(t.Context(), Session{ID: "other", UserID: 2, LastSeenAt: now, ExpiresAt: now.Add(time.Hour)})
	if err := st.CreateSession(t.Context(), Session{ID: "s1", UserID: 1}); err == nil {
		t.Fatal("duplicate session ID accepted")
	}

	got, err := st.SessionByID(t.Context(), "s1")
	if err != nil || got.UserID != 1 || got.Method != "password" || got.IP != "127.0.0.1" {
		t.Fatalf("by id: %+v %v", got, err)
	}
	if _, err := st.SessionByID(t.Context(), "nope"); err != ErrNotFound {
		t.Fatalf("missing: %v", err)
	}

	// Touching s1 makes it the most recently seen.
	if err := st.TouchSession(t.Context(), "s1", now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	list, err := st.ListSessions(t.Context(), 1)
	if err != nil || ids(list) != "[s1 s3 s2 old]" {
		t.Fatalf("list (newest first): %v %v", ids(list), err)
	}
	// Renewing moves its end too, and keeps the label.
	st.CreateSession(t.Context(), Session{ID: "chat", UserID: 3, Method: "webchat", Label: "chat.example", LastSeenAt: now, ExpiresAt: now.Add(time.Hour)})
	if err := st.RenewSession(t.Context(), "chat", now.Add(time.Minute), now.Add(48*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.SessionByID(t.Context(), "chat"); !got.ExpiresAt.Equal(now.Add(48*time.Hour)) || !got.LastSeenAt.Equal(now.Add(time.Minute)) || got.Label != "chat.example" {
		t.Fatalf("renewed: %+v", got)
	}

	if err := st.DeleteExpiredSessions(t.Context(), now); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SessionByID(t.Context(), "old"); err != ErrNotFound {
		t.Fatal("expired session survived")
	}

	// Deleting someone else's session does nothing.
	st.DeleteSession(t.Context(), 2, "s2")
	if _, err := st.SessionByID(t.Context(), "s2"); err != nil {
		t.Fatal("deleted another user's session")
	}
	st.DeleteSession(t.Context(), 1, "s2")
	if _, err := st.SessionByID(t.Context(), "s2"); err != ErrNotFound {
		t.Fatal("session not deleted")
	}

	// Sign out everywhere but here.
	if err := st.DeleteUserSessions(t.Context(), 1, "s3"); err != nil {
		t.Fatal(err)
	}
	if list, _ := st.ListSessions(t.Context(), 1); ids(list) != "[s3]" {
		t.Fatalf("after sign-out elsewhere: %v", ids(list))
	}
	st.DeleteUserSessions(t.Context(), 1, "")
	if list, _ := st.ListSessions(t.Context(), 1); len(list) != 0 {
		t.Fatalf("after sign-out everywhere: %v", ids(list))
	}
	if _, err := st.SessionByID(t.Context(), "other"); err != nil {
		t.Fatal("another user was signed out")
	}
}

func ids(ss []Session) string {
	var out []string
	for _, s := range ss {
		out = append(out, s.ID)
	}
	return fmt.Sprint(out)
}

func TestClientPasswords(t *testing.T) {
	st := open(t)
	a, err := st.CreateClientPassword(t.Context(), ClientPassword{UserID: 1, Name: "laptop", Hash: "h1"})
	if err != nil || a.ID == 0 {
		t.Fatalf("create: %+v %v", a, err)
	}
	b, _ := st.CreateClientPassword(t.Context(), ClientPassword{UserID: 1, Name: "phone", Hash: "h2"})
	st.CreateClientPassword(t.Context(), ClientPassword{UserID: 2, Name: "other", Hash: "h3"})
	if _, err := st.CreateClientPassword(t.Context(), ClientPassword{ID: a.ID, UserID: 1, Name: "dup", Hash: "h"}); err == nil {
		t.Fatal("duplicate ID accepted")
	}

	at := time.Now().Truncate(time.Second)
	if err := st.TouchClientPassword(t.Context(), b.ID, at); err != nil {
		t.Fatal(err)
	}
	list, err := st.ListClientPasswords(t.Context(), 1)
	if err != nil || len(list) != 2 || list[0].Name != "laptop" || list[1].Name != "phone" {
		t.Fatalf("list: %+v %v", list, err)
	}
	if list[0].LastUsedAt != nil || list[1].LastUsedAt == nil || !list[1].LastUsedAt.Equal(at) {
		t.Fatalf("last used: %v %v", list[0].LastUsedAt, list[1].LastUsedAt)
	}

	// Only the owner can delete one; a missing one is ErrNotFound.
	if err := st.DeleteClientPassword(t.Context(), 2, a.ID); err != ErrNotFound {
		t.Fatalf("delete by another user: %v", err)
	}
	if err := st.DeleteClientPassword(t.Context(), 1, a.ID); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteClientPassword(t.Context(), 1, a.ID); err != ErrNotFound {
		t.Fatalf("delete twice: %v", err)
	}
	if list, _ := st.ListClientPasswords(t.Context(), 1); len(list) != 1 || list[0].ID != b.ID {
		t.Fatalf("after delete: %+v", list)
	}
}

func TestNetworks(t *testing.T) {
	st := open(t)
	// Zero values of columns with defaults are kept, not replaced by the default.
	a, err := st.CreateNetwork(t.Context(), Network{UserID: 1, Name: "a", Host: "irc.a", Port: 6697, TLS: true,
		TrustPKI: false, FloodRate: 0, FloodBurst: 0, Enabled: true,
		ChannelSettings: map[string]ChannelSettings{"#go": {Key: "k", Detached: true}}})
	if err != nil {
		t.Fatal(err)
	}
	got, err := st.NetworkByID(t.Context(), a.ID)
	if err != nil || got.TrustPKI || got.FloodRate != 0 || got.FloodBurst != 0 || !got.Enabled {
		t.Fatalf("zero values: %+v %v", got, err)
	}
	if c := got.Channel("#Go"); c.Key != "k" || !c.Detached {
		t.Fatalf("channel settings: %+v", c)
	}
	if c := got.Channel("#none"); c != (ChannelSettings{}) {
		t.Fatalf("missing channel: %+v", c)
	}
	b, _ := st.CreateNetwork(t.Context(), Network{UserID: 1, Name: "b", TrustPKI: true, FloodRate: 2, FloodBurst: 9})
	c, _ := st.CreateNetwork(t.Context(), Network{UserID: 2, Name: "c", Enabled: true})
	if _, err := st.CreateNetwork(t.Context(), Network{ID: a.ID, UserID: 1}); err == nil {
		t.Fatal("duplicate network ID accepted")
	}
	if _, err := st.NetworkByID(t.Context(), 9999); err != ErrNotFound {
		t.Fatalf("missing network: %v", err)
	}

	if list, _ := st.ListNetworks(t.Context(), 1); len(list) != 2 || list[0].ID != a.ID || list[1].ID != b.ID {
		t.Fatalf("list: %+v", list)
	}
	if n, err := st.CountNetworks(t.Context(), 1); err != nil || n != 2 {
		t.Fatalf("count: %d %v", n, err)
	}
	if n, _ := st.CountNetworks(t.Context(), 3); n != 0 {
		t.Fatalf("count for none: %d", n)
	}
	if counts, err := st.NetworkCounts(t.Context()); err != nil || len(counts) != 2 || counts[1] != 2 || counts[2] != 1 {
		t.Fatalf("counts: %v %v", counts, err)
	}
	if all, _ := st.AllNetworks(t.Context()); len(all) != 3 {
		t.Fatalf("all: %d", len(all))
	}
	if en, _ := st.AllEnabledNetworks(t.Context()); len(en) != 2 || en[0].ID != a.ID || en[1].ID != c.ID {
		t.Fatalf("enabled: %+v", en)
	}

	if err := st.SetNetworkEnabled(t.Context(), b.ID, true); err != nil {
		t.Fatal(err)
	}
	st.SetNetworkEnabled(t.Context(), a.ID, false)
	if en, _ := st.AllEnabledNetworks(t.Context()); len(en) != 2 || en[0].ID != b.ID {
		t.Fatalf("enabled after toggling: %+v", en)
	}

	// UpdateNetwork changes settings but not ownership or Enabled.
	upd := got
	upd.UserID, upd.Enabled, upd.Nick, upd.TrustPKI = 99, true, "newnick", true
	if err := st.UpdateNetwork(t.Context(), upd); err != nil {
		t.Fatal(err)
	}
	got, _ = st.NetworkByID(t.Context(), a.ID)
	if got.UserID != 1 || got.Enabled || got.Nick != "newnick" || !got.TrustPKI {
		t.Fatalf("after update: %+v", got)
	}

	if err := st.SetNetworkChannelSettings(t.Context(), a.ID, map[string]ChannelSettings{"#rust": {Disabled: true}}); err != nil {
		t.Fatal(err)
	}
	got, _ = st.NetworkByID(t.Context(), a.ID)
	if !got.Channel("#rust").Disabled || got.Channel("#go").Key != "" || got.Nick != "newnick" {
		t.Fatalf("channel settings replaced: %+v", got)
	}

	// Deleting needs the owner, and takes the read markers with it.
	st.SetReadMarker(t.Context(), a.ID, "#go", time.Now())
	if err := st.DeleteNetwork(t.Context(), 2, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.NetworkByID(t.Context(), a.ID); err != nil {
		t.Fatal("another user deleted the network")
	}
	if m, _ := st.ReadMarkers(t.Context(), a.ID); len(m) != 1 {
		t.Fatal("read markers deleted without the network")
	}
	if err := st.DeleteNetwork(t.Context(), 1, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.NetworkByID(t.Context(), a.ID); err != ErrNotFound {
		t.Fatal("network survived")
	}
	if m, _ := st.ReadMarkers(t.Context(), a.ID); len(m) != 0 {
		t.Fatalf("read markers survived: %v", m)
	}
}

func TestServerList(t *testing.T) {
	if l := (Network{}).ServerList(); l != nil {
		t.Fatalf("no servers: %v", l)
	}
	n := Network{Host: "irc.example", Port: 6697, TLS: true}
	if l := n.ServerList(); len(l) != 1 || l[0] != (Server{Host: "irc.example", Port: 6697, TLS: true}) {
		t.Fatalf("legacy host: %v", l)
	}
	n.Servers = []Server{{Host: "a", Port: 1}, {Host: "b", Port: 2}}
	if l := n.ServerList(); len(l) != 2 || l[0].Host != "a" {
		t.Fatalf("servers win: %v", l)
	}
}

func TestReadMarkers(t *testing.T) {
	st := open(t)
	t1 := time.Now().Add(-time.Hour).Truncate(time.Second)
	t2 := t1.Add(30 * time.Minute)
	if err := st.SetReadMarker(t.Context(), 1, "#Go", t1); err != nil {
		t.Fatal(err)
	}
	st.SetReadMarker(t.Context(), 1, "alice", t1)
	st.SetReadMarker(t.Context(), 2, "#go", t1)
	// Same buffer, any case: an upsert.
	if err := st.SetReadMarker(t.Context(), 1, "#GO", t2); err != nil {
		t.Fatal(err)
	}
	m, err := st.ReadMarkers(t.Context(), 1)
	if err != nil || len(m) != 2 || !m["#go"].Equal(t2) || !m["alice"].Equal(t1) {
		t.Fatalf("markers: %v %v", m, err)
	}
	if m, _ := st.ReadMarkers(t.Context(), 3); len(m) != 0 {
		t.Fatalf("no markers: %v", m)
	}
}

func TestUserEvents(t *testing.T) {
	st := open(t)
	base := time.Now().Add(-time.Hour).Truncate(time.Second)
	for i := 0; i < 5; i++ {
		if err := st.AddUserEvent(t.Context(), UserEvent{UserID: 1, At: base.Add(time.Duration(i) * time.Minute), Kind: "net", Title: fmt.Sprint(i)}); err != nil {
			t.Fatal(err)
		}
	}
	st.AddUserEvent(t.Context(), UserEvent{UserID: 2, At: base, Title: "other"})

	titles := func(ev []UserEvent) string {
		var out []string
		for _, e := range ev {
			out = append(out, e.Title)
		}
		return fmt.Sprint(out)
	}
	ev, err := st.UserEvents(t.Context(), 1, time.Time{}, 3)
	if err != nil || titles(ev) != "[4 3 2]" {
		t.Fatalf("newest first, limited: %v %v", titles(ev), err)
	}
	if ev, _ := st.UserEvents(t.Context(), 1, base.Add(3*time.Minute), 10); titles(ev) != "[4 3]" {
		t.Fatalf("since: %v", titles(ev))
	}
	if err := st.PruneUserEvents(t.Context(), base.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if ev, _ := st.UserEvents(t.Context(), 1, time.Time{}, 10); titles(ev) != "[4 3 2]" {
		t.Fatalf("after prune: %v", titles(ev))
	}
	if ev, _ := st.UserEvents(t.Context(), 2, time.Time{}, 10); len(ev) != 0 {
		t.Fatalf("prune is by time, for everyone: %v", titles(ev))
	}
}

func TestNodesAndLeaseListing(t *testing.T) {
	st := open(t)
	now := time.Now()
	st.UpsertNode(t.Context(), Node{ID: "a", HTTPURL: "http://a", HeartbeatAt: now.Add(-time.Hour)})
	st.UpsertNode(t.Context(), Node{ID: "b", HTTPURL: "http://b", HeartbeatAt: now})
	// Upserting again updates the node in place.
	if err := st.UpsertNode(t.Context(), Node{ID: "a", HTTPURL: "http://a2", IRCAddr: "a:6667", HeartbeatAt: now}); err != nil {
		t.Fatal(err)
	}
	nodes, _ := st.LiveNodes(t.Context(), now.Add(-time.Minute))
	if len(nodes) != 2 || nodes[0].HTTPURL != "http://a2" || nodes[0].IRCAddr != "a:6667" {
		t.Fatalf("after upsert: %+v", nodes)
	}

	st.UpsertNode(t.Context(), Node{ID: "c", HeartbeatAt: now.Add(-time.Hour)})
	if err := st.DeleteNodesBefore(t.Context(), now.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if nodes, _ := st.LiveNodes(t.Context(), time.Time{}); len(nodes) != 2 {
		t.Fatalf("stale node survived: %+v", nodes)
	}

	ttl := time.Minute
	st.AcquireLease(t.Context(), 1, "a", now.Add(ttl), now)
	st.AcquireLease(t.Context(), 2, "b", now.Add(ttl), now)
	st.AcquireLease(t.Context(), 3, "b", now.Add(time.Second), now)
	leases, err := st.Leases(t.Context(), now.Add(2*time.Second))
	if err != nil || len(leases) != 2 {
		t.Fatalf("leases: %+v %v", leases, err)
	}
	sort.Slice(leases, func(i, j int) bool { return leases[i].UserID < leases[j].UserID })
	if leases[0].UserID != 1 || leases[0].NodeID != "a" || leases[1].UserID != 2 {
		t.Fatalf("leases: %+v", leases)
	}

	// A lease held by a node that's gone has no owner to report.
	st.AcquireLease(t.Context(), 4, "ghost", now.Add(ttl), now)
	if _, _, ok, err := st.LeaseOwner(t.Context(), 4, now); ok || err != ErrNotFound {
		t.Fatalf("lease of a missing node: %v %v", ok, err)
	}
}

func TestSettingsAndNodeConfs(t *testing.T) {
	st := open(t)
	if err := st.SaveSettings(t.Context(), map[string]string{"a": "1", "b": "2", "c": "3"}, nil, "root"); err != nil {
		t.Fatal(err)
	}
	// Set and delete in one go.
	if err := st.SaveSettings(t.Context(), map[string]string{"a": "10"}, []string{"b", "missing"}, "admin"); err != nil {
		t.Fatal(err)
	}
	rows, err := st.Settings(t.Context())
	if err != nil || len(rows) != 2 || rows[0].Key != "a" || rows[0].Value != "10" || rows[0].UpdatedBy != "admin" ||
		rows[1].Key != "c" || rows[1].UpdatedBy != "root" {
		t.Fatalf("settings: %+v %v", rows, err)
	}
	if err := st.SaveSettings(t.Context(), nil, nil, "x"); err != nil {
		t.Fatal(err)
	}

	at := time.Now().Truncate(time.Second)
	if err := st.SetNodeConf(t.Context(), NodeConf{NodeID: "b", Path: "/etc/evrd.conf", WroteAt: at, Keys: []string{"x"}}); err != nil {
		t.Fatal(err)
	}
	st.SetNodeConf(t.Context(), NodeConf{NodeID: "a", Error: "read-only"})
	st.SetNodeConf(t.Context(), NodeConf{NodeID: "b", Path: "/etc/evrd.conf", WroteAt: at, Keys: []string{"x", "y"}}) // replaces
	confs, err := st.NodeConfs(t.Context())
	if err != nil || len(confs) != 2 || confs[0].NodeID != "a" || confs[0].Error != "read-only" ||
		fmt.Sprint(confs[1].Keys) != "[x y]" || !confs[1].WroteAt.Equal(at) {
		t.Fatalf("node confs: %+v %v", confs, err)
	}
}

func TestCertCache(t *testing.T) {
	st := open(t)
	if _, err := st.CertCache(t.Context(), "acme"); err != ErrNotFound {
		t.Fatalf("missing: %v", err)
	}
	if err := st.PutCertCache(t.Context(), "acme", "pem1"); err != nil {
		t.Fatal(err)
	}
	st.PutCertCache(t.Context(), "acme", "pem2") // replaces
	if d, err := st.CertCache(t.Context(), "acme"); err != nil || d != "pem2" {
		t.Fatalf("got %q %v", d, err)
	}
	if err := st.DeleteCertCache(t.Context(), "acme"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CertCache(t.Context(), "acme"); err != ErrNotFound {
		t.Fatalf("after delete: %v", err)
	}
	if err := st.DeleteCertCache(t.Context(), "never"); err != nil {
		t.Fatalf("deleting a missing key: %v", err)
	}
}

func TestCached(t *testing.T) {
	st := open(t)
	if _, err := st.Cached(t.Context(), "directory"); err != ErrNotFound {
		t.Fatalf("missing: %v", err)
	}
	if err := st.PutCached(t.Context(), "directory", "v1"); err != nil {
		t.Fatal(err)
	}
	st.PutCached(t.Context(), "directory", "v2") // replaces
	if d, err := st.Cached(t.Context(), "directory"); err != nil || d != "v2" {
		t.Fatalf("got %q %v", d, err)
	}
}

func TestMigrateLegacyMessagesStopsOnError(t *testing.T) {
	st := open(t)
	db := st.(*gormStore).db.DB
	db.Exec("CREATE TABLE messages (id integer primary key, network_id integer, target text, sender text, kind text, text text, extra text, time datetime)")
	for i := 1; i <= 3; i++ {
		db.Exec("INSERT INTO messages (id, network_id, target, text, time) VALUES (?, 1, '#a', ?, ?)", i, fmt.Sprint(i), time.Now())
	}
	boom := errors.New("boom")
	n, err := st.MigrateLegacyMessages(t.Context(), func(m LegacyMessage) error {
		if m.ID == 2 {
			return boom
		}
		return nil
	})
	if !errors.Is(err, boom) || n != 0 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if !db.Migrator().HasTable("messages") {
		t.Fatal("table dropped after a failed migration")
	}
	// Retried, it hands over everything.
	var got []string
	if n, err := st.MigrateLegacyMessages(t.Context(), func(m LegacyMessage) error { got = append(got, m.Text); return nil }); n != 3 || err != nil || fmt.Sprint(got) != "[1 2 3]" {
		t.Fatalf("retry: %d %v %v", n, err, got)
	}

	// A table that can't be read is an error, not an empty migration.
	if err := db.Exec("CREATE TABLE messages (junk text)").Error; err != nil {
		t.Fatal(err)
	}
	db.Exec("INSERT INTO messages (junk) VALUES ('x')")
	if _, err := st.MigrateLegacyMessages(t.Context(), func(LegacyMessage) error { return nil }); err == nil {
		t.Fatal("a malformed messages table migrated")
	}
}

// After Close every call fails with an error rather than panicking or
// pretending to succeed.
func TestClosedStoreReturnsErrors(t *testing.T) {
	st, err := Open("sqlite:" + filepath.Join(t.TempDir(), "evrd.db"))
	if err != nil {
		t.Fatal(err)
	}
	st.Close()
	now := time.Now()
	errs := map[string]error{}
	check := func(name string, err error) { errs[name] = err }

	_, err = st.CreateUser(t.Context(), User{Username: "x"})
	check("CreateUser", err)
	_, err = st.UserByID(t.Context(), 1)
	check("UserByID", err)
	_, err = st.ListUsers(t.Context())
	check("ListUsers", err)
	check("UpdateUser", st.UpdateUser(t.Context(), User{ID: 1}))
	check("DeleteUser", st.DeleteUser(t.Context(), 1))
	_, err = st.CountAdmins(t.Context())
	check("CountAdmins", err)
	check("CreateSession", st.CreateSession(t.Context(), Session{ID: "s"}))
	_, err = st.CreateClientPassword(t.Context(), ClientPassword{UserID: 1})
	check("CreateClientPassword", err)
	check("DeleteClientPassword", st.DeleteClientPassword(t.Context(), 1, 1))
	_, err = st.CreateNetwork(t.Context(), Network{UserID: 1})
	check("CreateNetwork", err)
	check("DeleteNetwork", st.DeleteNetwork(t.Context(), 1, 1))
	_, err = st.NetworkCounts(t.Context())
	check("NetworkCounts", err)
	_, err = st.ReadMarkers(t.Context(), 1)
	check("ReadMarkers", err)
	_, err = st.AcquireLease(t.Context(), 1, "a", now, now)
	check("AcquireLease", err)
	_, _, _, err = st.LeaseOwner(t.Context(), 1, now)
	check("LeaseOwner", err)
	check("SaveSettings", st.SaveSettings(t.Context(), map[string]string{"a": "b"}, nil, ""))
	check("SaveSettings (delete)", st.SaveSettings(t.Context(), nil, []string{"a"}, ""))
	_, err = st.CertCache(t.Context(), "k")
	check("CertCache", err)
	_, err = st.EncryptSecrets(t.Context())
	check("EncryptSecrets", err)

	for name, err := range errs {
		if err == nil || err == ErrNotFound {
			t.Errorf("%s on a closed store: %v", name, err)
		}
	}
}
