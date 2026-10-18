package store

import (
	"bytes"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

// rawSecrets reads a network's password and servers columns as stored.
func rawSecrets(t *testing.T, st Store, id int64) (string, string) {
	t.Helper()
	var row struct{ Password, Servers string }
	st.(*gormStore).db.Table("networks").Select("password", "servers").Where("id = ?", id).Scan(&row)
	return row.Password, row.Servers
}

func TestNetworkSecretsAreEncryptedAtRest(t *testing.T) {
	t.Cleanup(func() { SetSecretKeys(nil) })
	key1 := bytes.Repeat([]byte{1}, 32)
	key2 := bytes.Repeat([]byte{2}, 32)

	// Saved before any key: plaintext.
	SetSecretKeys(nil)
	path := "sqlite:" + filepath.Join(t.TempDir(), "evrd.db")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := st.CreateUser(User{Username: "alice"})
	n, err := st.CreateNetwork(Network{UserID: u.ID, Name: "libera", Nick: "a", Host: "h", Password: "sasl-secret",
		Servers: []Server{{Host: "h", Port: 6697, Password: "server-secret"}}})
	if err != nil {
		t.Fatal(err)
	}
	if pw, _ := rawSecrets(t, st, n.ID); pw != "sasl-secret" {
		t.Fatalf("without a key, stored as is: %q", pw)
	}
	st.Close()

	// A key is set: startup encrypts what's there.
	if err := SetSecretKeys(key1); err != nil {
		t.Fatal(err)
	}
	st, _ = Open(path)
	if c, err := st.EncryptSecrets(); err != nil || c != 1 {
		t.Fatalf("EncryptSecrets: %d %v", c, err)
	}
	pw, servers := rawSecrets(t, st, n.ID)
	if !strings.HasPrefix(pw, "enc:v1:") || strings.Contains(pw, "sasl-secret") || strings.Contains(servers, "server-secret") {
		t.Fatalf("not encrypted: %q %q", pw, servers)
	}
	if c, _ := st.EncryptSecrets(); c != 0 {
		t.Fatal("already-encrypted secrets were rewritten")
	}
	got, _ := st.NetworkByID(n.ID)
	if got.Password != "sasl-secret" || got.Servers[0].Password != "server-secret" {
		t.Fatalf("decrypted: %q %+v", got.Password, got.Servers)
	}
	// New writes are encrypted too.
	got.Password = "changed"
	st.UpdateNetwork(got)
	if pw, _ := rawSecrets(t, st, n.ID); strings.Contains(pw, "changed") {
		t.Fatal("an update stored plaintext")
	}
	st.Close()

	// Rotating: the new key, with the old one still accepted.
	SetSecretKeys(key2, key1)
	st, _ = Open(path)
	if c, _ := st.EncryptSecrets(); c != 1 {
		t.Fatal("rotation didn't re-encrypt")
	}
	st.Close()
	SetSecretKeys(key2) // the old key can go now
	st, _ = Open(path)
	if got, err := st.NetworkByID(n.ID); err != nil || got.Password != "changed" {
		t.Fatalf("after rotation: %q %v", got.Password, err)
	}
	st.Close()

	// The wrong key is an error, not garbage.
	SetSecretKeys(key1)
	st, _ = Open(path)
	defer st.Close()
	if _, err := st.NetworkByID(n.ID); !errors.Is(err, ErrSecretKey) {
		t.Fatalf("wrong key: %v", err)
	}
}

func TestSecretKeyLength(t *testing.T) {
	t.Cleanup(func() { SetSecretKeys(nil) })
	if err := SetSecretKeys([]byte("short")); err == nil {
		t.Fatal("a short key was accepted")
	}
}
