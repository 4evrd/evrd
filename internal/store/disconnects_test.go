package store

import (
	"testing"
	"time"
)

func TestClientDisconnects(t *testing.T) {
	st := open(t)
	ctx := t.Context()
	u, _ := st.CreateUser(ctx, User{Username: "alice"})
	other, _ := st.CreateUser(ctx, User{Username: "bob"})
	t0 := time.Now().Add(-time.Hour).Truncate(time.Second)
	add := func(user int64, key, reason string, on, off time.Duration) time.Duration {
		t.Helper()
		gap, err := st.AddClientDisconnect(ctx, ClientDisconnect{UserID: user, DeviceKey: key, Reason: reason,
			ConnectedAt: t0.Add(on), EndedAt: t0.Add(off)}, t0.Add(-24*time.Hour))
		if err != nil {
			t.Fatal(err)
		}
		return gap
	}
	if gap := add(u.ID, "phone", "ping_timeout", 0, 10*time.Minute); gap != 0 {
		t.Fatalf("the first one came back from nothing: %v", gap)
	}
	// the phone again 40s later: its last one gets a gap
	if gap := add(u.ID, "phone", "reset", 10*time.Minute+40*time.Second, 20*time.Minute); gap != 40*time.Second {
		t.Fatalf("gap: %v", gap)
	}
	// another device, and another user's phone, are their own
	if gap := add(u.ID, "laptop", "quit", 5*time.Minute, 30*time.Minute); gap != 0 {
		t.Fatalf("another device: %v", gap)
	}
	add(other.ID, "phone", "closed", 21*time.Minute, 22*time.Minute)

	ds, err := st.ClientDisconnects(ctx, u.ID, t0)
	if err != nil || len(ds) != 3 || ds[0].DeviceKey != "laptop" || ds[2].ReconnectedAt == nil || ds[1].ReconnectedAt != nil {
		t.Fatalf("alice's, newest first: %+v %v", ds, err)
	}
	if all, _ := st.ClientDisconnects(ctx, 0, t0); len(all) != 4 {
		t.Fatalf("everyone's: %d", len(all))
	}
	if recent, _ := st.ClientDisconnects(ctx, u.ID, t0.Add(25*time.Minute)); len(recent) != 1 {
		t.Fatalf("since: %d", len(recent))
	}

	if err := st.DeleteClientDisconnectsBefore(ctx, t0.Add(21*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if all, _ := st.ClientDisconnects(ctx, 0, t0); len(all) != 2 {
		t.Fatalf("after pruning: %d", len(all))
	}
	if err := st.DeleteUser(ctx, other.ID); err != nil {
		t.Fatal(err)
	}
	if all, _ := st.ClientDisconnects(ctx, 0, t0); len(all) != 1 || all[0].UserID != u.ID {
		t.Fatalf("a deleted user's go with them: %+v", all)
	}
}
