package accounts

import (
	"strings"
	"testing"
	"time"

	"github.com/4evrd/evrd/internal/config"
	"golang.org/x/crypto/bcrypt"
)

func TestRememberedDevices(t *testing.T) {
	s, _ := newService(t, config.Limits{})
	u, err := s.Create(CreateInput{Username: "alice", Password: "alice-password"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	s.now = func() time.Time { return now }

	cp, secret, err := s.RememberDevice(u.ID, "  HexChat  ", "", 24*time.Hour)
	if err != nil || len(secret) < 32 || cp.Name != "HexChat" || !cp.Remembered || !cp.ExpiresAt.Equal(now.Add(24*time.Hour)) {
		t.Fatalf("remember: %+v %q %v", cp, secret, err)
	}
	if _, dev, err := s.IRCLogin("alice", secret); err != nil || dev != "HexChat" {
		t.Fatalf("its password: %q %v", dev, err)
	}
	// a hash the client's own secret came as
	h, _ := bcrypt.GenerateFromPassword([]byte("the-clients-own-long-secret"), bcrypt.MinCost)
	if _, sec, err := s.RememberDevice(u.ID, strings.Repeat("x", 80), string(h), time.Hour); err != nil || sec != "" {
		t.Fatalf("with a hash: %q %v", sec, err)
	}
	if _, _, err := s.IRCLogin("alice", "the-clients-own-long-secret"); err != nil {
		t.Fatalf("the client's secret: %v", err)
	}
	// past its time, a remembered device's password stops working
	now = now.Add(25 * time.Hour)
	if _, _, err := s.IRCLogin("alice", secret); err == nil {
		t.Fatal("an expired device still signs in")
	}
	if _, _, err := s.IRCLogin("alice", "alice-password"); err != nil {
		t.Fatalf("the account password: %v", err)
	}
	if cp, _, _ := s.RememberDevice(u.ID, "", "", time.Hour); cp.Name != "IRC client" {
		t.Fatalf("no name: %q", cp.Name)
	}
}
