package store

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestClientCerts(t *testing.T) {
	st := open(t)
	ctx := t.Context()
	alice, _ := st.CreateUser(ctx, User{Username: "alice"})
	bob, _ := st.CreateUser(ctx, User{Username: "bob"})
	fp1, fp2 := strings.Repeat("a", 64), strings.Repeat("b", 64)

	c1, err := st.CreateClientCert(ctx, ClientCert{UserID: alice.ID, Fingerprint: fp1, Label: "laptop"})
	if err != nil || c1.ID == 0 {
		t.Fatalf("create: %+v %v", c1, err)
	}
	// the same certificate twice on one account: refused
	if _, err := st.CreateClientCert(ctx, ClientCert{UserID: alice.ID, Fingerprint: fp1}); err == nil {
		t.Fatal("a duplicate was kept")
	}
	// on another account it's that account's (sign-in sorts it out)
	if _, err := st.CreateClientCert(ctx, ClientCert{UserID: bob.ID, Fingerprint: fp1}); err != nil {
		t.Fatal(err)
	}
	c2, _ := st.CreateClientCert(ctx, ClientCert{UserID: alice.ID, Fingerprint: fp2})

	list, _ := st.ListClientCerts(ctx, alice.ID)
	if len(list) != 2 || list[0].Label != "laptop" || list[1].ID != c2.ID {
		t.Fatalf("list: %+v", list)
	}
	found, _ := st.ClientCertsByFingerprint(ctx, []string{fp1, "nope"})
	if len(found) != 2 || found[0].UserID != alice.ID || found[1].UserID != bob.ID {
		t.Fatalf("by fingerprint: %+v", found)
	}
	if found, _ := st.ClientCertsByFingerprint(ctx, nil); len(found) != 0 {
		t.Fatalf("none asked: %+v", found)
	}

	at := time.Now().Truncate(time.Second)
	if err := st.TouchClientCert(ctx, c1.ID, at); err != nil {
		t.Fatal(err)
	}
	list, _ = st.ListClientCerts(ctx, alice.ID)
	if list[0].LastUsedAt == nil || !list[0].LastUsedAt.Equal(at) {
		t.Fatalf("touched: %+v", list[0])
	}

	// only the owner deletes it
	if err := st.DeleteClientCert(ctx, bob.ID, c2.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("someone else's: %v", err)
	}
	if err := st.DeleteClientCert(ctx, alice.ID, c2.ID); err != nil {
		t.Fatal(err)
	}
	// and they go with the account
	if err := st.DeleteUser(ctx, alice.ID); err != nil {
		t.Fatal(err)
	}
	if found, _ := st.ClientCertsByFingerprint(ctx, []string{fp1, fp2}); len(found) != 1 || found[0].UserID != bob.ID {
		t.Fatalf("after deleting alice: %+v", found)
	}
}
