package auth

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/4evrd/evrd/internal/accounts"
	"github.com/4evrd/evrd/internal/config"
	"github.com/4evrd/evrd/internal/store"
)

func newManager(t *testing.T, a config.Auth) (*Manager, store.Store, *accounts.Service) {
	t.Helper()
	st, err := store.Open("sqlite:" + filepath.Join(t.TempDir(), "evrd.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	acc := accounts.New(st, config.Limits{})
	m, err := New(context.Background(), st, acc, a, "", slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return m, st, acc
}

func TestProvisionCreatesAndRelinksBySubject(t *testing.T) {
	m, _, _ := newManager(t, config.Auth{OIDCAutoCreate: true})
	u, code := m.provision(Claims{Subject: "s1", Username: "Alice", Email: "a@example.org"})
	if code != "" || u.Username != "alice" || u.Email != "a@example.org" || u.Role != store.RoleUser {
		t.Fatalf("first sign-in: %q %+v", code, u)
	}
	// A renamed provider account still lands on the same evrd account.
	again, code := m.provision(Claims{Subject: "s1", Username: "alice2", Email: "new@example.org"})
	if code != "" || again.ID != u.ID || again.Email != "new@example.org" {
		t.Fatalf("second sign-in: %q %+v", code, again)
	}
}

func TestProvisionNeverTakesOverALocalAccount(t *testing.T) {
	m, _, acc := newManager(t, config.Auth{OIDCAutoCreate: true})
	acc.Create(accounts.CreateInput{Username: "alice", Password: "local-password"})
	if _, code := m.provision(Claims{Subject: "s1", Username: "alice"}); code != "taken" {
		t.Fatalf("got %q, want taken", code)
	}
}

func TestProvisionLinkExisting(t *testing.T) {
	m, _, acc := newManager(t, config.Auth{OIDCAutoCreate: true, OIDCLinkExisting: true})
	local, _ := acc.Create(accounts.CreateInput{Username: "alice", Password: "local-password"})
	u, code := m.provision(Claims{Subject: "s1", Username: "alice"})
	if code != "" || u.ID != local.ID || u.OIDCSubject == nil || *u.OIDCSubject != "s1" {
		t.Fatalf("link: %q %+v", code, u)
	}
}

func TestProvisionWithoutAutoCreate(t *testing.T) {
	m, _, _ := newManager(t, config.Auth{})
	if _, code := m.provision(Claims{Subject: "s1", Username: "alice"}); code != "no_account" {
		t.Fatalf("got %q, want no_account", code)
	}
}

func TestProvisionSyncsAdminRole(t *testing.T) {
	m, _, _ := newManager(t, config.Auth{OIDCAutoCreate: true, OIDCAdminRole: "evrd-admin"})
	a, _ := m.provision(Claims{Subject: "a", Username: "alice", Roles: []string{"evrd-admin"}})
	b, _ := m.provision(Claims{Subject: "b", Username: "bob", Roles: []string{"evrd-admin"}})
	if !a.IsAdmin() || !b.IsAdmin() {
		t.Fatal("role holders should be admins")
	}
	if b, _ = m.provision(Claims{Subject: "b", Username: "bob"}); b.IsAdmin() {
		t.Fatal("losing the role should demote")
	}
	// alice is now the only admin; losing the role must not leave none.
	if a, _ = m.provision(Claims{Subject: "a", Username: "alice"}); !a.IsAdmin() {
		t.Fatal("the last admin was demoted")
	}
}

func TestProvisionRefusesSuspended(t *testing.T) {
	m, st, _ := newManager(t, config.Auth{OIDCAutoCreate: true})
	u, _ := m.provision(Claims{Subject: "s1", Username: "alice"})
	u.Suspended = true
	st.UpdateUser(t.Context(), u)
	if _, code := m.provision(Claims{Subject: "s1", Username: "alice"}); code != "suspended" {
		t.Fatalf("got %q, want suspended", code)
	}
}

func TestProvisionClaimsAnAccountMadeForIt(t *testing.T) {
	m, st, acc := newManager(t, config.Auth{OIDCAutoCreate: true})
	made, err := acc.Create(accounts.CreateInput{Username: "alice", Email: "Alice@Example.org", SSOClaimable: true})
	if err != nil {
		t.Fatal(err)
	}
	// Same username, different email: someone else's look-alike — refused.
	if _, code := m.provision(Claims{Subject: "evil", Username: "alice", Email: "mallory@example.org"}); code != "taken" {
		t.Fatalf("look-alike: %q", code)
	}
	// The owner: same username and email.
	u, code := m.provision(Claims{Subject: "s1", Username: "alice", Email: "alice@example.org"})
	if code != "" || u.ID != made.ID {
		t.Fatalf("claim: %q %+v", code, u)
	}
	got, _ := st.UserByID(t.Context(), made.ID)
	if got.SSOClaimable || got.OIDCSubject == nil || *got.OIDCSubject != "s1" {
		t.Fatalf("after claim: %+v", got)
	}
	// Claimed once: another identity can't take it.
	if _, code := m.provision(Claims{Subject: "s2", Username: "alice", Email: "alice@example.org"}); code != "taken" {
		t.Fatalf("second claim: %q", code)
	}
}
