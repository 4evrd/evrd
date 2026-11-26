package managed

import (
	"context"
	"errors"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/4evrd/evrd/internal/accounts"
	"github.com/4evrd/evrd/internal/config"
	"github.com/4evrd/evrd/internal/managed/managedtest"
	"github.com/4evrd/evrd/internal/store"
)

// testConfig is a managed config for the issuer is.
func testConfig(is *managedtest.Issuer) *config.Config {
	c := config.Default()
	c.Managed.On = true
	c.Managed.Network = "ExampleNet"
	c.Managed.Host, c.Managed.Port, c.Managed.TLS = "irc.example.org", 6697, true
	c.Managed.Token = "api-token"
	c.Managed.ServerName = "irc.example.org"
	if is != nil {
		c.Managed.TokenIssuer, c.Managed.TokenAudience = is.URL, managedtest.Audience
	}
	return c
}

func testAccounts(t *testing.T) (*accounts.Service, store.Store) {
	t.Helper()
	st, err := store.Open("sqlite:" + filepath.Join(t.TempDir(), "evrd.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return accounts.New(st, config.Limits{}), st
}

func TestNewIsNilWhenOff(t *testing.T) {
	var m *Mode = New(config.Default())
	if m != nil || m.On() {
		t.Fatal("managed mode on by default")
	}
	if m.WebIRC(context.Background(), "192.0.2.1") != "" {
		t.Error("WEBIRC with managed mode off")
	}
	m = New(testConfig(nil))
	if !m.On() || m.Tokens() != nil || m.ServerName() != "irc.example.org" || m.NetworkName() != "ExampleNet" {
		t.Errorf("mode %+v", m)
	}
	if got := m.Text("welcome", "alice"); got != "Welcome to the ExampleNet IRC Network alice" {
		t.Error(got)
	}
}

func TestApplySetsTheServer(t *testing.T) {
	c := testConfig(nil)
	c.Managed.ServerPassword, c.Managed.TLSVerify, c.Managed.Fingerprints = "pw", false, []string{"ab"}
	m := New(c)
	n := store.Network{Name: "old", Nick: "alice", BindHost: "10.0.0.1", TrustAllCerts: true,
		Modules: map[string]store.ModuleConfig{"perform": {Enabled: true}}}
	m.Apply(&n)
	if n.Name != "ExampleNet" || n.Host != "irc.example.org" || n.Port != 6697 || !n.TLS || n.Nick != "alice" ||
		len(n.Servers) != 1 || n.Servers[0].Password != "pw" || n.TrustPKI || n.TrustAllCerts ||
		len(n.Fingerprints) != 1 || n.BindHost != "" || n.Modules != nil {
		t.Errorf("applied %+v", n)
	}
	if !m.IsNetwork(store.Network{Name: "examplenet"}) || m.IsNetwork(store.Network{Name: "other"}) {
		t.Error("IsNetwork")
	}
}

func TestWebIRC(t *testing.T) {
	c := testConfig(nil)
	m := New(c)
	if m.WebIRC(context.Background(), "192.0.2.1") != "" {
		t.Error("WEBIRC without a password")
	}
	c.Managed.WebIRCPassword, c.Managed.WebIRCResolve = "secret", false
	m = New(c)
	for ip, want := range map[string]string{
		"192.0.2.1":        "WEBIRC secret gateway 192.0.2.1 192.0.2.1",
		"::ffff:192.0.2.1": "WEBIRC secret gateway 192.0.2.1 192.0.2.1",
		"::1":              "WEBIRC secret gateway 0::1 0::1",
		"2001:db8::1":      "WEBIRC secret gateway 2001:db8::1 2001:db8::1",
		"not an address":   "",
	} {
		if got := m.WebIRC(context.Background(), ip); got != want {
			t.Errorf("%s: %q, want %q", ip, got, want)
		}
	}

	c.Managed.WebIRCResolve = true
	m = New(c)
	names := map[string][]string{"192.0.2.1": {"host.example.", "bad name"}, "192.0.2.2": {"liar.example."}}
	ips := map[string][]net.IP{"host.example": {net.ParseIP("192.0.2.1")}, "liar.example": {net.ParseIP("192.0.2.99")}}
	m.lookupAddr = func(ctx context.Context, a string) ([]string, error) {
		if n, ok := names[a]; ok {
			return n, nil
		}
		return nil, errors.New("nxdomain")
	}
	m.lookupIP = func(ctx context.Context, h string) ([]net.IP, error) {
		if i, ok := ips[h]; ok {
			return i, nil
		}
		return nil, errors.New("nxdomain")
	}
	for ip, want := range map[string]string{
		"192.0.2.1": "WEBIRC secret gateway host.example 192.0.2.1", // forward-confirmed
		"192.0.2.2": "WEBIRC secret gateway 192.0.2.2 192.0.2.2",    // its name points elsewhere
		"192.0.2.3": "WEBIRC secret gateway 192.0.2.3 192.0.2.3",    // no name
	} {
		if got := m.WebIRC(context.Background(), ip); got != want {
			t.Errorf("%s: %q, want %q", ip, got, want)
		}
	}
	ips["host.example"] = nil
	names["192.0.2.1"] = []string{"gone.example."}
	if got := m.WebIRC(context.Background(), "192.0.2.1"); got != "WEBIRC secret gateway 192.0.2.1 192.0.2.1" {
		t.Error(got)
	}
}

func TestSignIn(t *testing.T) {
	is := managedtest.New(t)
	m := New(testConfig(is))
	acc, _ := testAccounts(t)
	ctx := context.Background()
	if _, err := New(testConfig(nil)).SignIn(ctx, acc, is.Token("usr_1", "alice"), ""); !errors.Is(err, ErrTokensUnavailable) {
		t.Errorf("no issuer: %v", err)
	}
	alice, _ := acc.Create(accounts.CreateInput{Username: "alice", Subject: "usr_1"})
	bob, _ := acc.Create(accounts.CreateInput{Username: "bob"})
	acc.Create(accounts.CreateInput{Username: "carol", Subject: "usr_other"})

	u, err := m.SignIn(ctx, acc, is.Token("usr_1", "whatever"), "")
	if err != nil || u.ID != alice.ID {
		t.Fatalf("by subject: %+v %v", u, err)
	}
	if _, err := m.SignIn(ctx, acc, is.Token("usr_1", ""), "ALICE"); err != nil {
		t.Errorf("naming the account: %v", err)
	}
	if _, err := m.SignIn(ctx, acc, is.Token("usr_1", ""), "bob"); !errors.Is(err, ErrBadToken) {
		t.Errorf("naming another account: %v", err)
	}
	// bob has no subject yet: the first token named after him links him
	u, err = m.SignIn(ctx, acc, is.Token("usr_2", "Bob"), "")
	if err != nil || u.ID != bob.ID {
		t.Fatalf("by name: %+v %v", u, err)
	}
	if u, _ := acc.UserByID(ctx, bob.ID); u.OIDCSubject == nil || *u.OIDCSubject != "usr_2" {
		t.Errorf("bob not linked: %+v", u)
	}
	for name, tok := range map[string]string{
		"carol's, by name":   is.Token("usr_3", "carol"),
		"no such account":    is.Token("usr_4", "dave"),
		"no usable name":     is.Token("usr_5", "Not A Name!"),
		"no name":            is.Token("usr_6", ""),
		"another audience":   is.Sign(map[string]any{"sub": "usr_1", "aud": "quicvoice"}),
		"expired":            is.Sign(map[string]any{"sub": "usr_1", "aud": managedtest.Audience, "exp": time.Now().Add(-time.Hour).Unix()}),
		"another issuer":     is.Sign(map[string]any{"sub": "usr_1", "aud": managedtest.Audience, "iss": "https://elsewhere"}),
		"no subject":         is.Sign(map[string]any{"aud": managedtest.Audience}),
		"not a token at all": "eyJhbGciOiJub25lIn0.e30.c2ln",
	} {
		if _, err := m.SignIn(ctx, acc, tok, ""); err == nil {
			t.Errorf("%s: signed in", name)
		}
	}
	alice.Suspended = true
	acc.UpdateUser(ctx, alice)
	if _, err := m.SignIn(ctx, acc, is.Token("usr_1", ""), ""); !errors.Is(err, accounts.ErrSuspended) {
		t.Errorf("suspended: %v", err)
	}
}

func TestVerifierDiscoveryAndJWKS(t *testing.T) {
	is := managedtest.New(t)
	ctx := context.Background()
	for name, v := range map[string]*Verifier{
		"discovery":    NewVerifier(is.URL, managedtest.Audience, ""),
		"jwks":         NewVerifier(is.URL, managedtest.Audience, is.JWKS()),
		"any audience": NewVerifier(is.URL, "*", ""),
	} {
		c, err := v.Verify(ctx, is.Token("usr_1", "alice"))
		if err != nil || c.Subject != "usr_1" || c.Name != "alice" {
			t.Errorf("%s: %+v %v", name, c, err)
		}
	}
	c, err := NewVerifier(is.URL, "*", "").Verify(ctx, is.Sign(map[string]any{"sub": "u", "aud": "x", "preferred_username": "pat"}))
	if err != nil || c.Name != "pat" {
		t.Errorf("preferred_username: %+v %v", c, err)
	}

	// the issuer down: discovery fails now, and works once it's back
	down := managedtest.New(t)
	down.Close()
	if _, err := NewVerifier(down.URL, "*", "").Verify(ctx, is.Token("u", "")); !errors.Is(err, ErrTokensUnavailable) {
		t.Errorf("issuer down: %v", err)
	}
	if _, err := NewVerifier(down.URL, "*", down.JWKS()).Verify(ctx, down.Token("u", "")); !errors.Is(err, ErrTokensUnavailable) {
		t.Errorf("keys out of reach: %v", err)
	}
}

func TestLooksLikeToken(t *testing.T) {
	is := managedtest.New(t)
	if !LooksLikeToken(is.Token("usr_1", "alice")) {
		t.Error("a token")
	}
	for _, s := range []string{"", "hunter22", "a.b.c", "eyJ.only.two", "eyJhbGciOiJFZERTQSJ9.e30.c2ln!", "eyJhbGciOiJFZERTQSJ9..c2lnaW5hdHVyZXNpZ25hdHVyZQ", "eyJhbGciOiJFZERTQSJ9.e30.c2ln.extra"} {
		if LooksLikeToken(s) {
			t.Errorf("%q looks like a token", s)
		}
	}
	if !strings.HasPrefix(colonSafe(":1"), "0:") || colonSafe("a") != "a" {
		t.Error("colonSafe")
	}
}
