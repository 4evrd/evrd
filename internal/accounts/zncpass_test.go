package accounts

import (
	"crypto/md5"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"

	"golang.org/x/crypto/argon2"

	"github.com/4evrd/evrd/internal/config"
)

func TestZNCPasswords(t *testing.T) {
	pw := "znc pass" // shorter than evrd's minimum: ZNC's still work
	sha := sha256.Sum256([]byte(pw + "s4lt"))
	md := md5.Sum([]byte(pw + "s4lt"))
	unsalted := md5.Sum([]byte(pw))
	salt := []byte("0123456789abcdef")
	key := argon2.IDKey([]byte(pw), salt, 2, 1024, 1, 32)
	phc := fmt.Sprintf("$argon2id$v=19$m=1024,t=2,p=1$%s$%s", base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key))
	cases := []struct{ method, hash, salt string }{
		{"sha256", hex.EncodeToString(sha[:]), "s4lt"},
		{"SHA256", strings.ToUpper(hex.EncodeToString(sha[:])), "s4lt"},
		{"md5", hex.EncodeToString(md[:]), "s4lt"},
		{"md5", hex.EncodeToString(unsalted[:]), ""},
		{"Argon2id", phc, ""},
		{"plain", pw, ""},
	}
	for _, c := range cases {
		stored, err := ZNCPasswordHash(c.method, c.hash, c.salt)
		if err != nil {
			t.Errorf("%s: %v", c.method, err)
			continue
		}
		if ok, _ := matchHash(stored, pw); !ok {
			t.Errorf("%s: the right password doesn't match", c.method)
		}
		if ok, _ := matchHash(stored, pw+"x"); ok {
			t.Errorf("%s: a wrong password matches", c.method)
		}
		if _, upgrade := matchHash(stored, pw); upgrade != IsZNCHash(stored) {
			t.Errorf("%s: upgrade %v", c.method, upgrade)
		}
	}
	for _, bad := range []struct{ method, hash, salt string }{
		{"sha256", "nothex", ""}, {"md5", hex.EncodeToString(sha[:]), ""}, {"crypt", "x", ""},
		{"argon2id", "$argon2i$v=19$m=1,t=1,p=1$a$b", ""}, {"plain", "", ""}, {"sha256", hex.EncodeToString(sha[:]), "a$b"},
	} {
		if _, err := ZNCPasswordHash(bad.method, bad.hash, bad.salt); err == nil {
			t.Errorf("%+v accepted", bad)
		}
	}
}

func TestZNCPasswordSignInUpgrades(t *testing.T) {
	s, _ := newService(t, config.Limits{})
	sum := sha256.Sum256([]byte("old znc password" + "salt"))
	h, _ := ZNCPasswordHash("sha256", hex.EncodeToString(sum[:]), "salt")
	u, err := s.Create(CreateInput{Username: "zed", PasswordHash: h})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.PasswordLogin("zed", "wrong"); err != ErrInvalidCredentials {
		t.Fatalf("wrong password: %v", err)
	}
	if _, _, err := s.IRCLogin("zed", "old znc password"); err != nil {
		t.Fatalf("IRC sign-in with the ZNC password: %v", err)
	}
	u, _ = s.UserByID(t.Context(), u.ID)
	if IsZNCHash(u.PasswordHash) || !strings.HasPrefix(u.PasswordHash, "$2") {
		t.Fatalf("not upgraded to bcrypt: %q", u.PasswordHash)
	}
	if _, err := s.PasswordLogin("zed", "old znc password"); err != nil {
		t.Errorf("after the upgrade: %v", err)
	}
}
