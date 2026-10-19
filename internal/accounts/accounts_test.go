package accounts

import (
	"errors"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/4evrd/evrd/internal/config"
	"github.com/4evrd/evrd/internal/store"
)

func newService(t *testing.T, l config.Limits) (*Service, store.Store) {
	t.Helper()
	st, err := store.Open("sqlite:" + filepath.Join(t.TempDir(), "evrd.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return New(st, l), st
}

func isValidation(err error) bool {
	var v *ValidationError
	return errors.As(err, &v)
}

func intp(n int) *int { return &n }

func TestNormalizeUsername(t *testing.T) {
	for in, want := range map[string]string{
		"Alice":                       "alice",
		"  bob_2 ":                    "bob_2",
		"c-d":                         "c-d",
		"ab":                          "ab",
		"a" + strings.Repeat("b", 31): "a" + strings.Repeat("b", 31),
	} {
		if got, err := NormalizeUsername(in); err != nil || got != want {
			t.Errorf("%q: got %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "a", "1abc", "_abc", "al ice", "alice!", "a" + strings.Repeat("b", 32), "ålice"} {
		if _, err := NormalizeUsername(bad); !isValidation(err) {
			t.Errorf("%q: got %v, want a validation error", bad, err)
		}
	}
}

func TestCheckPassword(t *testing.T) {
	for _, tc := range []struct {
		pw   string
		want string // substring of the message; "" = accepted
	}{
		{"correct horse", ""},
		{strings.Repeat("é", 10), ""}, // 10 runes, 20 bytes
		{strings.Repeat("x", 72), ""},
		{"short", "at least 10"},
		{strings.Repeat("é", 9), "at least 10"},
		{strings.Repeat("x", 73), "72 bytes"},
		{"line\nbreak-pw", "control characters"},
		{"tab\there-pw", "control characters"},
		{"bad\xffbytes-here", "valid text"},
	} {
		err := CheckPassword(tc.pw)
		switch {
		case tc.want == "" && err != nil:
			t.Errorf("%q: unexpected %v", tc.pw, err)
		case tc.want != "" && (!isValidation(err) || !strings.Contains(err.Error(), tc.want)):
			t.Errorf("%q: got %v, want %q", tc.pw, err, tc.want)
		}
	}
	if _, err := HashPassword("short"); !isValidation(err) {
		t.Errorf("HashPassword applies the policy: %v", err)
	}
}

func TestCreate(t *testing.T) {
	s, st := newService(t, config.Limits{})
	u, err := s.Create(CreateInput{Username: " Alice ", Email: " a@example.org ", Password: "correct horse", Temporary: true, Subject: "sub-1"})
	if err != nil {
		t.Fatal(err)
	}
	if u.Username != "alice" || u.DefaultNick != "alice" || u.Email != "a@example.org" || u.Role != store.RoleUser {
		t.Fatalf("created: %+v", u)
	}
	if !u.MustChangePassword || u.PasswordHash == "" || u.PasswordHash == "correct horse" {
		t.Fatalf("password: must-change %v hash %q", u.MustChangePassword, u.PasswordHash)
	}
	if got, err := st.UserBySubject("sub-1"); err != nil || got.ID != u.ID {
		t.Fatalf("subject link: %v %+v", err, got)
	}

	// Without a password, Temporary means nothing.
	sso, err := s.Create(CreateInput{Username: "bob", Temporary: true, Role: store.RoleAdmin})
	if err != nil || sso.PasswordHash != "" || sso.MustChangePassword || !sso.IsAdmin() || sso.OIDCSubject != nil {
		t.Fatalf("sso-only admin: %v %+v", err, sso)
	}

	for _, tc := range []struct {
		name string
		in   CreateInput
		want func(error) bool
	}{
		{"taken, any case", CreateInput{Username: "ALICE"}, func(err error) bool { return errors.Is(err, ErrUsernameTaken) }},
		{"bad username", CreateInput{Username: "9lives"}, isValidation},
		{"bad role", CreateInput{Username: "carol", Role: "root"}, isValidation},
		{"weak password", CreateInput{Username: "dave", Password: "short"}, isValidation},
	} {
		if _, err := s.Create(tc.in); !tc.want(err) {
			t.Errorf("%s: got %v", tc.name, err)
		}
	}
	if users, _ := st.ListUsers(); len(users) != 2 {
		t.Fatalf("refused creates stored users: %d", len(users))
	}
}

func TestPasswordLogin(t *testing.T) {
	s, st := newService(t, config.Limits{})
	u, _ := s.Create(CreateInput{Username: "alice", Password: "correct horse"})
	s.Create(CreateInput{Username: "sso"}) // no local password

	if got, err := s.PasswordLogin(" Alice ", "correct horse"); err != nil || got.ID != u.ID {
		t.Fatalf("right password: %v %+v", err, got)
	}
	for _, tc := range []struct{ user, pw string }{
		{"alice", "wrong horse"},
		{"alice", ""},
		{"nobody", "correct horse"},
		{"sso", ""},
		{"sso", "anything-at-all"},
	} {
		if _, err := s.PasswordLogin(tc.user, tc.pw); !errors.Is(err, ErrInvalidCredentials) {
			t.Errorf("%s/%q: got %v, want invalid credentials", tc.user, tc.pw, err)
		}
	}

	u.Suspended = true
	st.UpdateUser(u)
	if _, err := s.PasswordLogin("alice", "correct horse"); !errors.Is(err, ErrSuspended) {
		t.Fatalf("suspended: %v", err)
	}
	// A wrong password doesn't reveal that the account is suspended.
	if _, err := s.PasswordLogin("alice", "wrong horse"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("suspended, wrong password: %v", err)
	}
}

func TestSetAndChangePassword(t *testing.T) {
	s, st := newService(t, config.Limits{})
	u, _ := s.Create(CreateInput{Username: "alice", Password: "first password", Temporary: true})

	if err := s.ChangePassword(&u, "not it at all", "second password"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("wrong current: %v", err)
	}
	if err := s.ChangePassword(&u, "first password", "short"); !isValidation(err) {
		t.Fatalf("weak new: %v", err)
	}
	if err := s.ChangePassword(&u, "first password", "second password"); err != nil {
		t.Fatal(err)
	}
	got, _ := st.UserByID(u.ID)
	if got.MustChangePassword {
		t.Fatal("changing the password clears must-change")
	}
	if _, err := s.PasswordLogin("alice", "first password"); err == nil {
		t.Fatal("the old password still works")
	}
	if _, err := s.PasswordLogin("alice", "second password"); err != nil {
		t.Fatalf("the new password: %v", err)
	}

	// An SSO-only account has no current password to prove.
	sso, _ := s.Create(CreateInput{Username: "bob"})
	if err := s.ChangePassword(&sso, "", "some new password"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("no current password: %v", err)
	}
}

func TestSetTemporaryPassword(t *testing.T) {
	s, st := newService(t, config.Limits{})
	u, _ := s.Create(CreateInput{Username: "alice", Password: "first password"})

	pw, err := s.SetTemporaryPassword(&u, "chosen by admin")
	if err != nil || pw != "chosen by admin" {
		t.Fatalf("given: %q %v", pw, err)
	}
	if got, _ := st.UserByID(u.ID); !got.MustChangePassword {
		t.Fatal("a temporary password must be changed")
	}
	if _, err := s.PasswordLogin("alice", "chosen by admin"); err != nil {
		t.Fatalf("temporary password works: %v", err)
	}

	gen, err := s.SetTemporaryPassword(&u, "")
	if err != nil || !regexp.MustCompile(`^[a-z2-9]{5}(-[a-z2-9]{5}){3}$`).MatchString(gen) {
		t.Fatalf("generated: %q %v", gen, err)
	}
	if _, err := s.PasswordLogin("alice", gen); err != nil {
		t.Fatalf("generated password works: %v", err)
	}
	if got, _ := st.UserByID(u.ID); !got.MustChangePassword {
		t.Fatal("a generated password must be changed")
	}

	if _, err := s.SetTemporaryPassword(&u, "short"); !isValidation(err) {
		t.Fatalf("weak temporary: %v", err)
	}
}

func TestRandomPassword(t *testing.T) {
	seen := map[string]bool{}
	for range 20 {
		pw, err := RandomPassword()
		if err != nil {
			t.Fatal(err)
		}
		if strings.ContainsAny(pw, "01ilo") {
			t.Fatalf("look-alike characters: %q", pw)
		}
		if err := CheckPassword(pw); err != nil {
			t.Fatalf("%q fails the policy: %v", pw, err)
		}
		if seen[pw] {
			t.Fatalf("repeated: %q", pw)
		}
		seen[pw] = true
	}
}

func TestIRCLoginAndClientPasswords(t *testing.T) {
	s, st := newService(t, config.Limits{})
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	s.now = func() time.Time { return at }
	u, _ := s.Create(CreateInput{Username: "alice", Password: "account password"})

	for _, bad := range []string{"", "   ", strings.Repeat("x", 65)} {
		if _, _, err := s.NewClientPassword(u.ID, bad); !isValidation(err) {
			t.Errorf("name %q: %v", bad, err)
		}
	}
	cp, secret, err := s.NewClientPassword(u.ID, "  laptop ")
	if err != nil {
		t.Fatal(err)
	}
	if cp.Name != "laptop" || cp.Hash == secret || !regexp.MustCompile(`^[a-z2-7]{4}(-[a-z2-7]{1,4})+$`).MatchString(secret) {
		t.Fatalf("client password: %+v %q", cp, secret)
	}
	_, other, _ := s.NewClientPassword(u.ID, "phone")
	if other == secret {
		t.Fatal("client passwords repeat")
	}

	if got, dev, err := s.IRCLogin("Alice", "account password"); err != nil || got.ID != u.ID || dev != "" {
		t.Fatalf("account password: %v %q", err, dev)
	}
	got, dev, err := s.IRCLogin("alice", secret)
	if err != nil || got.ID != u.ID || dev != "laptop" {
		t.Fatalf("client password: %v %q", err, dev)
	}
	cps, _ := st.ListClientPasswords(u.ID)
	for _, c := range cps {
		used := c.LastUsedAt != nil && c.LastUsedAt.Equal(at)
		if used != (c.Name == "laptop") {
			t.Errorf("%s last used: %v", c.Name, c.LastUsedAt)
		}
	}

	for _, tc := range []struct{ user, secret string }{
		{"alice", "wrong"},
		{"alice", ""},
		{"nobody", secret},
	} {
		if _, _, err := s.IRCLogin(tc.user, tc.secret); !errors.Is(err, ErrInvalidCredentials) {
			t.Errorf("%s/%q: %v", tc.user, tc.secret, err)
		}
	}
	// Another user's client password doesn't open this account.
	s.Create(CreateInput{Username: "bob"})
	if _, _, err := s.IRCLogin("bob", secret); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("someone else's client password: %v", err)
	}

	u.Suspended = true
	st.UpdateUser(u)
	for _, pw := range []string{"account password", secret} {
		if _, _, err := s.IRCLogin("alice", pw); !errors.Is(err, ErrSuspended) {
			t.Errorf("suspended (%q): %v", pw, err)
		}
	}
}

func TestLimits(t *testing.T) {
	s, _ := newService(t, config.Limits{MaxNetworks: 3, MaxClients: 2, BacklogLines: 500})
	if d := s.Defaults(); d != (Limits{3, 2, 500}) {
		t.Fatalf("defaults: %+v", d)
	}
	u := store.User{MaxNetworks: intp(0), BacklogLines: intp(1000)}
	if l := s.LimitsFor(u); l != (Limits{MaxNetworks: 0, MaxClients: 2, BacklogLines: 1000}) {
		t.Fatalf("overrides win, unset follow defaults: %+v", l)
	}
	s.SetDefaults(config.Limits{MaxClients: 7})
	if l := s.LimitsFor(u); l.MaxClients != 7 || l.BacklogLines != 1000 {
		t.Fatalf("after SetDefaults: %+v", l)
	}
}

func TestCanCreateNetwork(t *testing.T) {
	s, st := newService(t, config.Limits{MaxNetworks: 2})
	u, _ := s.Create(CreateInput{Username: "alice"})
	for i, name := range []string{"one", "two"} {
		if err := s.CanCreateNetwork(u); err != nil {
			t.Fatalf("network %d: %v", i+1, err)
		}
		if _, err := st.CreateNetwork(store.Network{UserID: u.ID, Name: name, Nick: "alice", Host: "irc.example.org"}); err != nil {
			t.Fatal(err)
		}
	}
	err := s.CanCreateNetwork(u)
	if !isValidation(err) || !strings.Contains(err.Error(), "limit of 2") {
		t.Fatalf("at the limit: %v", err)
	}
	u.MaxNetworks = intp(0) // unlimited for this user
	if err := s.CanCreateNetwork(u); err != nil {
		t.Fatalf("unlimited override: %v", err)
	}
	u.MaxNetworks = intp(3)
	if err := s.CanCreateNetwork(u); err != nil {
		t.Fatalf("raised override: %v", err)
	}
}

func TestGuardAdminChange(t *testing.T) {
	s, st := newService(t, config.Limits{})
	a, _ := s.Create(CreateInput{Username: "alice", Role: store.RoleAdmin})
	user, _ := s.Create(CreateInput{Username: "bob"})

	if err := s.GuardAdminChange(user, false); err != nil {
		t.Fatalf("users aren't guarded: %v", err)
	}
	if err := s.GuardAdminChange(a, true); err != nil {
		t.Fatalf("staying admin: %v", err)
	}
	if err := s.GuardAdminChange(a, false); !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("the only admin: %v", err)
	}
	b, _ := s.Create(CreateInput{Username: "carol", Role: store.RoleAdmin})
	if err := s.GuardAdminChange(a, false); err != nil {
		t.Fatalf("with a second admin: %v", err)
	}
	// A suspended admin doesn't count as the other one.
	b.Suspended = true
	st.UpdateUser(b)
	if err := s.GuardAdminChange(a, false); !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("other admin suspended: %v", err)
	}
}

func TestBindHosts(t *testing.T) {
	s, _ := newService(t, config.Limits{})
	in := []string{"192.0.2.1"}
	s.SetBindHosts(in)
	in[0] = "changed"
	got := s.BindHosts()
	if len(got) != 1 || got[0] != "192.0.2.1" {
		t.Fatalf("SetBindHosts keeps its own copy: %q", got)
	}
	got[0] = "changed"
	if s.BindHosts()[0] != "192.0.2.1" {
		t.Fatal("BindHosts returns a copy")
	}
	if err := s.CheckBindHost(store.User{}, ""); err != nil {
		t.Fatalf("no bind host is always fine: %v", err)
	}
}

func TestNormalizeAllowedHosts(t *testing.T) {
	got, err := NormalizeAllowedHosts([]string{" 10.0.0.0/8 ", "", "2001:db8::1", "192.168.*", "fe80::?", "10.0.[1-3].*"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, " ") != "10.0.0.0/8 2001:db8::1 192.168.* fe80::? 10.0.[1-3].*" {
		t.Fatalf("got %q", got)
	}
	if got, err := NormalizeAllowedHosts(nil); err != nil || got == nil || len(got) != 0 {
		t.Fatalf("empty: %v %#v", err, got)
	}
	for _, bad := range []string{"evil.example.com", "192.168.1", "*.example.org", "10.0.0.0/33"} {
		if _, err := NormalizeAllowedHosts([]string{bad}); !isValidation(err) {
			t.Errorf("%q: %v", bad, err)
		}
	}
}

func TestHostAllowedEdgeCases(t *testing.T) {
	u := store.User{AllowedHosts: []string{" 10.0.0.0/8 ", "fe80::*"}}
	for ip, want := range map[string]bool{
		"10.9.9.9":  true,  // entries are trimmed
		"fe80::1":   true,  // IPv6 wildcard
		"FE80::A":   true,  // case-insensitive
		"not-an-ip": false, // CIDRs can't match garbage
		"":          false,
	} {
		if got := HostAllowed(u, ip); got != want {
			t.Errorf("%q: got %v, want %v", ip, got, want)
		}
	}
}
