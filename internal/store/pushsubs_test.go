package store

import (
	"errors"
	"testing"
	"time"
)

func TestPushSubscriptions(t *testing.T) {
	st := open(t)
	ctx := t.Context()
	u, _ := st.CreateUser(ctx, User{Username: "alice"})
	n, _ := st.CreateNetwork(ctx, Network{UserID: u.ID, Name: "one", Nick: "a", Host: "h"})
	t0 := time.Now().Add(-time.Hour).Truncate(time.Millisecond)

	a, err := st.SavePushSubscription(ctx, PushSubscription{UserID: u.ID, NetworkID: n.ID, Endpoint: "https://push.example/a", P256DH: "k1", Auth: "a1", LastRefresh: t0, LastSuccess: t0}, 2)
	if err != nil || a.ID == 0 {
		t.Fatalf("save: %+v %v", a, err)
	}
	// the same endpoint again: an update, not another one
	t1 := t0.Add(time.Minute)
	b, err := st.SavePushSubscription(ctx, PushSubscription{UserID: u.ID, NetworkID: n.ID, Endpoint: "https://push.example/a", P256DH: "k2", Auth: "a2", LastRefresh: t1}, 1)
	if err != nil || b.ID != a.ID {
		t.Fatalf("update: %+v %v", b, err)
	}
	subs, _ := st.PushSubscriptions(ctx, u.ID)
	if len(subs) != 1 || subs[0].P256DH != "k2" || !subs[0].LastRefresh.Equal(t1) || !subs[0].LastSuccess.Equal(t0) {
		t.Fatalf("after the update: %+v", subs)
	}
	// the limit is per network
	if _, err := st.SavePushSubscription(ctx, PushSubscription{UserID: u.ID, NetworkID: n.ID, Endpoint: "https://push.example/b"}, 1); !errors.Is(err, ErrPushLimit) {
		t.Fatalf("over the limit: %v", err)
	}
	if _, err := st.SavePushSubscription(ctx, PushSubscription{UserID: u.ID, NetworkID: 0, Endpoint: "https://push.example/a"}, 1); err != nil {
		t.Fatalf("another network: %v", err)
	}
	if _, err := st.SavePushSubscription(ctx, PushSubscription{UserID: u.ID, NetworkID: n.ID, Endpoint: "https://push.example/b"}, 0); err != nil {
		t.Fatalf("no limit: %v", err)
	}
	all, _ := st.AllPushSubscriptions(ctx)
	if len(all) != 3 {
		t.Fatalf("all: %+v", all)
	}
	t2 := t1.Add(time.Minute)
	if err := st.TouchPushSubscription(ctx, a.ID, t2); err != nil {
		t.Fatal(err)
	}
	subs, _ = st.PushSubscriptions(ctx, u.ID)
	if !subs[0].LastSuccess.Equal(t2) {
		t.Fatalf("touched: %+v", subs[0])
	}
	if err := st.DeletePushEndpoint(ctx, u.ID, 0, "https://push.example/a"); err != nil {
		t.Fatal(err)
	}
	if err := st.DeletePushSubscription(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if subs, _ = st.PushSubscriptions(ctx, u.ID); len(subs) != 1 || subs[0].Endpoint != "https://push.example/b" {
		t.Fatalf("after deleting: %+v", subs)
	}
	// they go with their network, and with their user
	st.DeleteNetwork(ctx, u.ID, n.ID)
	if subs, _ = st.PushSubscriptions(ctx, u.ID); len(subs) != 0 {
		t.Fatalf("a deleted network's: %+v", subs)
	}
	st.SavePushSubscription(ctx, PushSubscription{UserID: u.ID, Endpoint: "https://push.example/c"}, 0)
	st.DeleteUser(ctx, u.ID)
	if all, _ = st.AllPushSubscriptions(ctx); len(all) != 0 {
		t.Fatalf("a deleted user's: %+v", all)
	}
}
