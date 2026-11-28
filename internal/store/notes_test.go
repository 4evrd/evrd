package store

import (
	"fmt"
	"testing"
	"time"
)

func TestNotes(t *testing.T) {
	st := open(t)
	ctx := t.Context()
	u, _ := st.CreateUser(ctx, User{Username: "alice"})
	bob, _ := st.CreateUser(ctx, User{Username: "bob"})
	if !u.ShowNotesOnLogin {
		t.Error("notes aren't shown on login by default")
	}
	for _, n := range []UserNote{{UserID: u.ID, Key: "wifi", Text: "hunter2"}, {UserID: u.ID, Key: "door", Text: "1234"}, {UserID: bob.ID, Key: "wifi", Text: "bob's"}} {
		if err := st.PutNote(ctx, n); err != nil {
			t.Fatal(err)
		}
	}
	// the same key again replaces it
	if err := st.PutNote(ctx, UserNote{UserID: u.ID, Key: "wifi", Text: "hunter3"}); err != nil {
		t.Fatal(err)
	}
	notes, _ := st.Notes(ctx, u.ID)
	if len(notes) != 2 || notes[0].Key != "door" || notes[1].Text != "hunter3" || notes[1].UpdatedAt.IsZero() {
		t.Fatalf("notes: %+v", notes)
	}
	if ok, err := st.DeleteNote(ctx, u.ID, "door"); !ok || err != nil {
		t.Errorf("delete: %v %v", ok, err)
	}
	if ok, _ := st.DeleteNote(ctx, u.ID, "door"); ok {
		t.Error("deleted twice")
	}
	if notes, _ := st.Notes(ctx, bob.ID); len(notes) != 1 || notes[0].Text != "bob's" {
		t.Errorf("bob's: %+v", notes)
	}

	// they go with the account
	if err := st.DeleteUser(ctx, u.ID); err != nil {
		t.Fatal(err)
	}
	if notes, _ := st.Notes(ctx, u.ID); len(notes) != 0 {
		t.Errorf("left over: %+v", notes)
	}
}

func TestSeeClientIP(t *testing.T) {
	st := open(t)
	ctx := t.Context()
	u, _ := st.CreateUser(ctx, User{Username: "alice"})
	t0 := time.Now().Add(-time.Hour)
	see := func(ip string, at time.Time) bool {
		t.Helper()
		fresh, err := st.SeeClientIP(ctx, u.ID, ip, at, 3)
		if err != nil {
			t.Fatal(err)
		}
		return fresh
	}
	if !see("192.0.2.1", t0) || see("192.0.2.1", t0.Add(time.Minute)) {
		t.Error("an address is new once")
	}
	for i := 2; i <= 4; i++ {
		if !see(fmt.Sprintf("192.0.2.%d", i), t0.Add(time.Duration(i)*time.Minute)) {
			t.Errorf("192.0.2.%d isn't new", i)
		}
	}
	// only the newest 3 are kept: .1 was forgotten
	if !see("192.0.2.1", t0.Add(time.Hour)) {
		t.Error("the oldest address was kept")
	}
	if see("192.0.2.4", t0.Add(time.Hour)) {
		t.Error("a recent address was forgotten")
	}
	if err := st.DeleteUser(ctx, u.ID); err != nil {
		t.Fatal(err)
	}
	u2, _ := st.CreateUser(ctx, User{Username: "alice"})
	if fresh, _ := st.SeeClientIP(ctx, u2.ID, "192.0.2.4", time.Now(), 3); !fresh {
		t.Error("addresses outlived the account")
	}
}
