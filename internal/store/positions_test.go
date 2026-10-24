package store

import (
	"testing"
	"time"
)

func TestDevicePositions(t *testing.T) {
	st := open(t)
	ctx := t.Context()
	u, _ := st.CreateUser(ctx, User{Username: "alice"})
	n, _ := st.CreateNetwork(ctx, Network{UserID: u.ID, Name: "one", Nick: "a", Host: "h"})
	if _, ok, err := st.DevicePosition(ctx, n.ID, "password:phone"); ok || err != nil {
		t.Fatalf("none yet: %v %v", ok, err)
	}
	t1 := time.Now().Add(-time.Minute).Truncate(time.Millisecond)
	t2 := t1.Add(30 * time.Second)
	st.SetDevicePosition(ctx, n.ID, "password:phone", t1)
	st.SetDevicePosition(ctx, n.ID, "password:phone", t2) // moves on
	st.SetDevicePosition(ctx, n.ID, "password:laptop", t1)
	if at, ok, _ := st.DevicePosition(ctx, n.ID, "password:phone"); !ok || !at.Equal(t2) {
		t.Fatalf("phone: %v %v", at, ok)
	}
	if at, _, _ := st.DevicePosition(ctx, n.ID, "password:laptop"); !at.Equal(t1) {
		t.Fatalf("laptop: %v", at)
	}
	if err := st.DeleteNetwork(ctx, u.ID, n.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := st.DevicePosition(ctx, n.ID, "password:phone"); ok {
		t.Fatal("a deleted network's positions go with it")
	}
	n2, _ := st.CreateNetwork(ctx, Network{UserID: u.ID, Name: "two", Nick: "a", Host: "h"})
	st.SetDevicePosition(ctx, n2.ID, "password:phone", t1)
	st.DeleteUser(ctx, u.ID)
	if _, ok, _ := st.DevicePosition(ctx, n2.ID, "password:phone"); ok {
		t.Fatal("a deleted user's positions go with them")
	}
}
