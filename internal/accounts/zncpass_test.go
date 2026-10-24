package accounts

import (
	"crypto/md5"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/argon2"

	"github.com/4evrd/evrd/internal/config"
	"github.com/4evrd/evrd/internal/store"
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

func TestZNCPasswordShortMustChange(t *testing.T) {
	s, _ := newService(t, config.Limits{})
	sum := sha256.Sum256([]byte("short" + "salt"))
	h, _ := ZNCPasswordHash("sha256", hex.EncodeToString(sum[:]), "salt")
	u, _ := s.Create(CreateInput{Username: "shorty", PasswordHash: h})
	if _, err := s.PasswordLogin("shorty", "short"); err != nil {
		t.Fatal(err)
	}
	u, _ = s.UserByID(t.Context(), u.ID)
	if !u.MustChangePassword || IsZNCHash(u.PasswordHash) {
		t.Errorf("a short ZNC password: must change %v, hash %q", u.MustChangePassword, u.PasswordHash)
	}
	long, _ := s.Create(CreateInput{Username: "longer", PasswordHash: mustZNC(t, "a long enough password")})
	s.PasswordLogin("longer", "a long enough password")
	if long, _ = s.UserByID(t.Context(), long.ID); long.MustChangePassword {
		t.Error("a long ZNC password must be changed")
	}
}

func mustZNC(t *testing.T, pw string) string {
	sum := sha256.Sum256([]byte(pw + "s"))
	h, err := ZNCPasswordHash("sha256", hex.EncodeToString(sum[:]), "s")
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func TestZNCPasswordsExpire(t *testing.T) {
	s, _ := newService(t, config.Limits{})
	s.SetZNCPasswordDays(90)
	old, _ := s.Create(CreateInput{Username: "unused", PasswordHash: mustZNC(t, "old znc password")})
	fresh, _ := s.Create(CreateInput{Username: "fresh", PasswordHash: mustZNC(t, "old znc password")})
	own, _ := s.Create(CreateInput{Username: "ownpw", Password: "an evrd password"})
	now := time.Now()
	s.now = func() time.Time { return now.Add(91 * 24 * time.Hour) }
	if _, err := s.PasswordLogin("unused", "old znc password"); err != ErrInvalidCredentials {
		t.Errorf("an expired ZNC password still signs in: %v", err)
	}
	if u, _ := s.UserByID(t.Context(), old.ID); u.PasswordHash != "" {
		t.Errorf("an expired ZNC hash wasn't cleared at sign-in: %q", u.PasswordHash)
	}
	// the sweep clears the one no one signed in to, and only that
	if n, err := s.ExpireZNCPasswords(t.Context()); err != nil || n != 1 {
		t.Errorf("sweep: %d %v", n, err)
	}
	if u, _ := s.UserByID(t.Context(), fresh.ID); u.PasswordHash != "" {
		t.Errorf("not swept: %q", u.PasswordHash)
	}
	if n, _ := s.ExpireZNCPasswords(t.Context()); n != 0 {
		t.Errorf("swept again: %d", n)
	}
	s.now = func() time.Time { return now.Add(89 * 24 * time.Hour) }
	if s.zncExpired(store.User{PasswordHash: "znc$md5$$00", CreatedAt: now}) {
		t.Error("expired before its time")
	}
	if u, _ := s.UserByID(t.Context(), own.ID); u.PasswordHash == "" {
		t.Error("the sweep cleared an evrd password")
	}
	s.SetZNCPasswordDays(0)
	if s.zncExpired(store.User{PasswordHash: "znc$md5$$00", CreatedAt: time.Time{}}) {
		t.Error("expired with no limit")
	}
}
