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

func TestSettingsAreReencrypted(t *testing.T) {
	t.Cleanup(func() { SetSecretKeys(nil) })
	old, nu := bytes.Repeat([]byte{3}, 32), bytes.Repeat([]byte{4}, 32)
	path := "sqlite:" + filepath.Join(t.TempDir(), "evrd.db")
	SetSecretKeys(old)
	st, _ := Open(path)
	st.SaveSettings(map[string]string{"oidc_client_secret": "shh"}, nil, "root")
	st.Close()

	SetSecretKeys(nu, old) // rotate
	st, _ = Open(path)
	if c, err := st.EncryptSecrets(); err != nil || c != 1 {
		t.Fatalf("EncryptSecrets: %d %v", c, err)
	}
	st.Close()
	SetSecretKeys(nu) // the old key is gone
	st, _ = Open(path)
	defer st.Close()
	rows, err := st.Settings()
	if err != nil || len(rows) != 1 || rows[0].Value != "shh" {
		t.Fatalf("after rotation: %+v %v", rows, err)
	}
}

func TestSecretKeysState(t *testing.T) {
	t.Cleanup(func() { SetSecretKeys(nil) })
	SetSecretKeys(nil)
	if SecretsEncrypted() {
		t.Fatal("encrypted without a key")
	}
	if err := SetSecretKeys(bytes.Repeat([]byte{1}, 32)); err != nil || !SecretsEncrypted() {
		t.Fatalf("with a key: %v %v", err, SecretsEncrypted())
	}
	// A bad old key is rejected and leaves the keys as they were.
	if err := SetSecretKeys(bytes.Repeat([]byte{2}, 32), []byte("short")); err == nil {
		t.Fatal("a short old key was accepted")
	}
	sealed, _ := sealSecret("x")
	if !strings.HasPrefix(sealed, secretPrefix+keyID(bytes.Repeat([]byte{1}, 32))+":") {
		t.Fatalf("keys changed by a failed SetSecretKeys: %q", sealed)
	}
	// Only old keys: decrypts, but stores plaintext.
	SetSecretKeys(nil, bytes.Repeat([]byte{1}, 32))
	if SecretsEncrypted() {
		t.Fatal("encrypted with no current key")
	}
	if got, err := openSecret(sealed); err != nil || got != "x" {
		t.Fatalf("old key decrypts: %q %v", got, err)
	}
	if got, _ := sealSecret("y"); got != "y" {
		t.Fatalf("sealed without a current key: %q", got)
	}
}

func TestOpenSecret(t *testing.T) {
	t.Cleanup(func() { SetSecretKeys(nil) })
	key := bytes.Repeat([]byte{5}, 32)
	SetSecretKeys(key)
	id := keyID(key)
	sealed, err := sealSecret("hunter2")
	if err != nil {
		t.Fatal(err)
	}
	if again, _ := sealSecret("hunter2"); again == sealed {
		t.Fatal("the nonce isn't random")
	}
	if s, _ := sealSecret(""); s != "" {
		t.Fatalf("empty sealed to %q", s)
	}
	if got, err := openSecret(sealed); err != nil || got != "hunter2" {
		t.Fatalf("round trip: %q %v", got, err)
	}
	// Plaintext from before a key was set passes through.
	if got, err := openSecret("legacy"); err != nil || got != "legacy" {
		t.Fatalf("plaintext: %q %v", got, err)
	}
	b64 := strings.TrimPrefix(sealed, secretPrefix+id+":")
	flipped := []byte(b64)
	if flipped[len(flipped)-1] == 'A' {
		flipped[len(flipped)-1] = 'B'
	} else {
		flipped[len(flipped)-1] = 'A'
	}
	for name, bad := range map[string]string{
		"no key id":      secretPrefix + "abcdef",
		"unknown key":    secretPrefix + "00000000:" + b64,
		"bad base64":     secretPrefix + id + ":!!!",
		"too short":      secretPrefix + id + ":AAAA",
		"tampered":       secretPrefix + id + ":" + string(flipped),
		"other key's id": secretPrefix + keyID(bytes.Repeat([]byte{6}, 32)) + ":" + b64,
	} {
		if _, err := openSecret(bad); !errors.Is(err, ErrSecretKey) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestCurrent(t *testing.T) {
	t.Cleanup(func() { SetSecretKeys(nil) })
	k1, k2 := bytes.Repeat([]byte{1}, 32), bytes.Repeat([]byte{2}, 32)
	SetSecretKeys(k1)
	s1, _ := sealSecret("x")
	SetSecretKeys(k2, k1)
	s2, _ := sealSecret("x")
	for _, c := range []struct {
		stored string
		want   bool
	}{{"", true}, {"plain", false}, {s1, false}, {s2, true}} {
		if got := current(c.stored); got != c.want {
			t.Errorf("current(%q) with a key = %v", c.stored, got)
		}
	}
	SetSecretKeys(nil, k1, k2)
	for _, c := range []struct {
		stored string
		want   bool
	}{{"", true}, {"plain", true}, {s1, false}} {
		if got := current(c.stored); got != c.want {
			t.Errorf("current(%q) without a key = %v", c.stored, got)
		}
	}
}

func TestSerializersScan(t *testing.T) {
	st := open(t)
	db := st.(*gormStore).db
	n, err := st.CreateNetwork(Network{UserID: 1, Name: "x", Password: "pw", Servers: []Server{{Host: "h", Password: "sp"}}})
	if err != nil {
		t.Fatal(err)
	}
	// NULL columns read as empty.
	db.Exec("UPDATE networks SET password = NULL, servers = NULL WHERE id = ?", n.ID)
	got, err := st.NetworkByID(n.ID)
	if err != nil || got.Password != "" || got.Servers != nil {
		t.Fatalf("NULL: %+v %v", got, err)
	}
	// Stored as blobs ([]byte from the driver) they read the same.
	db.Exec("UPDATE networks SET password = CAST('pw' AS BLOB), servers = CAST(? AS BLOB) WHERE id = ?", `[{"host":"h","port":1,"tls":false,"password":"sp"}]`, n.ID)
	got, err = st.NetworkByID(n.ID)
	if err != nil || got.Password != "pw" || len(got.Servers) != 1 || got.Servers[0].Password != "sp" {
		t.Fatalf("blobs: %+v %v", got, err)
	}
	// An empty server list.
	db.Exec("UPDATE networks SET servers = '' WHERE id = ?", n.ID)
	if got, err := st.NetworkByID(n.ID); err != nil || len(got.Servers) != 0 {
		t.Fatalf("empty servers: %+v %v", got.Servers, err)
	}
	// Servers that aren't a JSON list are an error, not an empty list.
	db.Exec("UPDATE networks SET servers = 42 WHERE id = ?", n.ID)
	if _, err := st.NetworkByID(n.ID); err == nil {
		t.Fatal("numeric servers were accepted")
	}
	db.Exec("UPDATE networks SET servers = '[{' WHERE id = ?", n.ID)
	if _, err := st.NetworkByID(n.ID); err == nil {
		t.Fatal("damaged servers JSON was accepted")
	}
	// A server password sealed under an unknown key is ErrSecretKey.
	db.Exec("UPDATE networks SET servers = ? WHERE id = ?", `[{"host":"h","password":"enc:v1:00000000:AAAA"}]`, n.ID)
	if _, err := st.NetworkByID(n.ID); !errors.Is(err, ErrSecretKey) {
		t.Fatalf("server password under an unknown key: %v", err)
	}
}

func TestEncryptSecretsCoversServersSettingsAndCerts(t *testing.T) {
	t.Cleanup(func() { SetSecretKeys(nil) })
	SetSecretKeys(nil)
	path := "sqlite:" + filepath.Join(t.TempDir(), "evrd.db")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	// Only a server password is secret here; another network has no secrets.
	n, _ := st.CreateNetwork(Network{UserID: 1, Name: "x", Servers: []Server{{Host: "h", Password: "server-secret"}}})
	st.CreateNetwork(Network{UserID: 1, Name: "plain", Servers: []Server{{Host: "h"}}})
	st.PutCertCache("acme", "PRIVATE KEY")
	st.SaveSettings(map[string]string{"s": "v", "empty": ""}, nil, "root")
	if c, _ := st.EncryptSecrets(); c != 0 {
		t.Fatalf("rewrote %d rows without a key", c)
	}
	st.Close()

	SetSecretKeys(bytes.Repeat([]byte{7}, 32))
	st, _ = Open(path)
	defer st.Close()
	if c, err := st.EncryptSecrets(); err != nil || c != 3 {
		t.Fatalf("EncryptSecrets: %d %v", c, err)
	}
	db := st.(*gormStore).db
	_, servers := rawSecrets(t, st, n.ID)
	var data, value string
	db.Table("cert_cache_entries").Select("data").Where("key = ?", "acme").Scan(&data)
	db.Table("settings").Select("value").Where("key = ?", "s").Scan(&value)
	if strings.Contains(servers, "server-secret") || !strings.HasPrefix(data, secretPrefix) || !strings.HasPrefix(value, secretPrefix) {
		t.Fatalf("not encrypted: %q %q %q", servers, data, value)
	}
	if got, _ := st.CertCache("acme"); got != "PRIVATE KEY" {
		t.Fatalf("cert after encryption: %q", got)
	}
	if got, _ := st.NetworkByID(n.ID); got.Servers[0].Password != "server-secret" {
		t.Fatalf("server password after encryption: %+v", got.Servers)
	}
	if c, _ := st.EncryptSecrets(); c != 0 {
		t.Fatalf("second run rewrote %d", c)
	}

	// A secret no key opens stops the rewrite with an error.
	db.Exec("UPDATE cert_cache_entries SET data = 'enc:v1:00000000:AAAA'")
	if _, err := st.EncryptSecrets(); !errors.Is(err, ErrSecretKey) {
		t.Fatalf("cert under an unknown key: %v", err)
	}
	db.Exec("UPDATE settings SET value = 'enc:v1:00000000:AAAA' WHERE key = 's'")
	if _, err := st.EncryptSecrets(); !errors.Is(err, ErrSecretKey) {
		t.Fatalf("setting under an unknown key: %v", err)
	}
	db.Exec("UPDATE networks SET password = 'enc:v1:00000000:AAAA' WHERE id = ?", n.ID)
	if _, err := st.EncryptSecrets(); !errors.Is(err, ErrSecretKey) {
		t.Fatalf("network under an unknown key: %v", err)
	}
}
