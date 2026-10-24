package ircsignin

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/4evrd/evrd/internal/accounts"
	"github.com/4evrd/evrd/internal/config"
	"github.com/4evrd/evrd/internal/store"
)

func newService(t *testing.T) (*Service, store.Store, *accounts.Service, *time.Time) {
	t.Helper()
	dir, err := os.MkdirTemp("", "evrd-signin-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	st, err := store.Open("sqlite:" + filepath.Join(dir, "evrd.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	acc := accounts.New(st, config.Limits{})
	now := time.Now()
	s := New(st, acc).(*Service)
	s.now = func() time.Time { return now }
	return s, st, acc, &now
}

func TestLinkAllowAndRemember(t *testing.T) {
	s, _, acc, now := newService(t)
	ctx := context.Background()
	alice, _ := acc.Create(accounts.CreateInput{Username: "alice", Password: "alice-password"})
	bob, _ := acc.Create(accounts.CreateInput{Username: "bob", Password: "bob-password"})

	id, token, err := s.Begin(ctx, Request{Username: "Alice", Network: "libera", Nick: "al", IP: "192.0.2.1"})
	if err != nil || len(token) < 40 {
		t.Fatalf("begin: %v %q", err, token)
	}
	p, err := s.Lookup(ctx, token)
	if err != nil || p.ID != id || p.Username != "alice" || p.Network != "libera" || p.State != Pending || p.TokenHash == token {
		t.Fatalf("lookup: %+v %v", p, err)
	}
	if _, err := s.Lookup(ctx, token+"x"); !errors.Is(err, ErrGone) {
		t.Fatalf("a wrong token: %v", err)
	}
	// only alice may allow it; anyone may turn it away
	if _, err := s.Allow(ctx, token, bob, RememberNone, time.Hour); !errors.Is(err, ErrNotYours) {
		t.Fatalf("bob allows alice's: %v", err)
	}
	if _, err := s.Allow(ctx, token, alice, RememberSecret, time.Hour); !errors.Is(err, ErrNoSecret) {
		t.Fatalf("remember a secret it didn't send: %v", err)
	}
	secret, err := s.Allow(ctx, token, alice, RememberPassword, 30*24*time.Hour)
	if err != nil || secret == "" {
		t.Fatalf("allow: %q %v", secret, err)
	}
	if p, _ := s.Status(ctx, id); p.State != Allowed || p.AllowedUser != alice.ID {
		t.Fatalf("status: %+v", p)
	}
	if _, err := s.Allow(ctx, token, alice, RememberPassword, time.Hour); !errors.Is(err, ErrGone) {
		t.Fatalf("allowed twice: %v", err)
	}
	// the remembered device's password works, until it expires
	if u, dev, err := acc.IRCLogin("alice", secret); err != nil || u.ID != alice.ID || dev == "" {
		t.Fatalf("remembered password: %v", err)
	}
	cps, _ := acc.ListClientPasswords(ctx, alice.ID)
	if len(cps) != 1 || !cps[0].Remembered || cps[0].ExpiresAt == nil || cps[0].ExpiresAt.Before(now.Add(29*24*time.Hour)) {
		t.Fatalf("client passwords: %+v", cps)
	}

	// a long secret the client sent becomes its password
	sent := "a-long-random-secret-from-the-client"
	_, token, _ = s.Begin(ctx, Request{IP: "192.0.2.2", Secret: sent})
	if _, _, err := acc.IRCLogin("bob", sent); err == nil {
		t.Fatal("works before it's allowed")
	}
	if _, err := s.Allow(ctx, token, bob, RememberSecret, time.Hour); err != nil {
		t.Fatal(err)
	}
	if u, _, err := acc.IRCLogin("bob", sent); err != nil || u.ID != bob.ID {
		t.Fatalf("the sent secret: %v", err)
	}
	// a short one isn't kept
	_, token, _ = s.Begin(ctx, Request{IP: "192.0.2.2", Secret: "short"})
	if p, _ := s.Lookup(ctx, token); p.SecretHash != "" {
		t.Fatal("a short secret was kept")
	}
	_ = s.Deny(ctx, token, bob)

	// suspended accounts can't allow
	bob.Suspended = true
	_, token, _ = s.Begin(ctx, Request{IP: "192.0.2.3"})
	if _, err := s.Allow(ctx, token, bob, RememberNone, time.Hour); !errors.Is(err, ErrForbidden) {
		t.Fatalf("suspended: %v", err)
	}
}

func TestLinkDenyExpireCancelAndLimits(t *testing.T) {
	s, _, acc, now := newService(t)
	ctx := context.Background()
	alice, _ := acc.Create(accounts.CreateInput{Username: "alice", Password: "alice-password"})
	carol, _ := acc.Create(accounts.CreateInput{Username: "carol", Password: "carol-password"})

	id, token, _ := s.Begin(ctx, Request{Username: "alice", IP: "198.51.100.1"})
	if err := s.Deny(ctx, token, carol); err != nil { // sent to carol: she may turn it away
		t.Fatal(err)
	}
	if p, _ := s.Status(ctx, id); p.State != Denied {
		t.Fatalf("denied: %+v", p)
	}
	if err := s.Deny(ctx, token, alice); !errors.Is(err, ErrGone) {
		t.Fatalf("decided twice: %v", err)
	}

	// a second factor's link is only for that account
	_, token, _ = s.Begin(ctx, Request{UserID: alice.ID, Username: "alice", IP: "198.51.100.2"})
	if _, err := s.Allow(ctx, token, carol, RememberNone, time.Hour); !errors.Is(err, ErrNotYours) {
		t.Fatalf("someone else's second factor: %v", err)
	}

	// links expire, and go with their connection
	id, token, _ = s.Begin(ctx, Request{IP: "198.51.100.3"})
	*now = now.Add(LinkFor + time.Second)
	if _, err := s.Lookup(ctx, token); !errors.Is(err, ErrGone) {
		t.Fatalf("expired lookup: %v", err)
	}
	if _, err := s.Status(ctx, id); !errors.Is(err, ErrGone) {
		t.Fatalf("expired status: %v", err)
	}
	id, token, _ = s.Begin(ctx, Request{IP: "198.51.100.3"})
	if err := s.Cancel(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Lookup(ctx, token); !errors.Is(err, ErrGone) {
		t.Fatalf("cancelled: %v", err)
	}

	// at most three open per address, five per account
	for i := range maxPerIP {
		if _, _, err := s.Begin(ctx, Request{IP: "203.0.113.9"}); err != nil {
			t.Fatalf("%d: %v", i, err)
		}
	}
	if _, _, err := s.Begin(ctx, Request{IP: "203.0.113.9"}); !errors.Is(err, ErrTooMany) {
		t.Fatalf("a fourth from one address: %v", err)
	}
	for i := range maxPerUser {
		s.Begin(ctx, Request{Username: "dave", IP: "203.0.113." + string(rune('a'+i))})
	}
	if _, _, err := s.Begin(ctx, Request{Username: "DAVE", IP: "203.0.113.200"}); !errors.Is(err, ErrTooMany) {
		t.Fatalf("a sixth for one account: %v", err)
	}
	// expired ones don't count
	*now = now.Add(LinkFor + time.Second)
	if _, _, err := s.Begin(ctx, Request{IP: "203.0.113.9"}); err != nil {
		t.Fatalf("after the old ones expired: %v", err)
	}
}
