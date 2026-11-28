package accounts

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/4evrd/evrd/internal/config"
)

func TestParseFingerprint(t *testing.T) {
	want := strings.Repeat("ab", 32)
	for _, in := range []string{
		want,
		strings.ToUpper(want),
		strings.TrimSuffix(strings.Repeat("AB:", 32), ":"), // openssl, ZNC
		" " + strings.Repeat("ab ", 32),
		"sha256 Fingerprint=" + strings.TrimSuffix(strings.Repeat("AB:", 32), ":"), // openssl's whole line
	} {
		if got, ok := ParseFingerprint(in); !ok || got != want {
			t.Errorf("%q: %q %v", in, got, ok)
		}
	}
	for _, bad := range []string{"", "abcd", strings.Repeat("ab", 20) /* SHA-1 */, strings.Repeat("zz", 32), want + "ab"} {
		if got, ok := ParseFingerprint(bad); ok {
			t.Errorf("%q: took it as %q", bad, got)
		}
	}
}

func TestClientCerts(t *testing.T) {
	svc, st := newService(t, config.Limits{})
	certs := NewClientCerts(st, st)
	ctx := context.Background()
	alice, _ := svc.Create(CreateInput{Username: "alice", Password: "alice-password"})
	bob, _ := svc.Create(CreateInput{Username: "bob", Password: "bob-password1"})
	fp1, fp2 := strings.Repeat("1a", 32), strings.Repeat("2b", 32)

	c, err := certs.AddClientCert(ctx, alice.ID, strings.ToUpper(fp1), "  laptop ")
	if err != nil || c.Fingerprint != fp1 || c.Label != "laptop" {
		t.Fatalf("add: %+v %v", c, err)
	}
	for _, bad := range []struct{ fp, label string }{
		{"nope", ""},                   // not a fingerprint
		{fp1, ""},                      // already there
		{fp2, strings.Repeat("x", 65)}, // label too long
	} {
		if _, err := certs.AddClientCert(ctx, alice.ID, bad.fp, bad.label); !isValidation(err) {
			t.Errorf("%+v: %v", bad, err)
		}
	}

	// a certificate signs in its account; the account's name, if given,
	// must be its
	u, got, err := certs.CertUser(ctx, "", fp1)
	if err != nil || u.ID != alice.ID || got.ID != c.ID {
		t.Fatalf("sign in: %+v %+v %v", u, got, err)
	}
	if u, _, err := certs.CertUser(ctx, "Alice", "", fp1); err != nil || u.ID != alice.ID {
		t.Fatalf("named: %+v %v", u, err)
	}
	list, _ := certs.ListClientCerts(ctx, alice.ID)
	if len(list) != 1 || list[0].LastUsedAt == nil {
		t.Fatalf("not marked used: %+v", list)
	}
	for _, try := range []struct {
		user string
		fps  []string
	}{{"", []string{fp2}}, {"bob", []string{fp1}}, {"nobody", []string{fp1}}, {"", nil}} {
		if _, _, err := certs.CertUser(ctx, try.user, try.fps...); !errors.Is(err, ErrInvalidCredentials) {
			t.Errorf("%+v: %v", try, err)
		}
	}
	// the key's fingerprint works as well as the certificate's
	if _, err := certs.AddClientCert(ctx, bob.ID, fp2, ""); err != nil {
		t.Fatal(err)
	}
	if u, _, err := certs.CertUser(ctx, "", fp1+"-not-it", fp2); err != nil || u.ID != bob.ID {
		t.Fatalf("by key: %+v %v", u, err)
	}

	// on two accounts: only with a name
	if _, err := certs.AddClientCert(ctx, bob.ID, fp1, ""); err != nil {
		t.Fatal(err)
	}
	if _, _, err := certs.CertUser(ctx, "", fp1); !errors.Is(err, ErrCertAmbiguous) {
		t.Fatalf("two accounts: %v", err)
	}
	if u, _, err := certs.CertUser(ctx, "bob", fp1); err != nil || u.ID != bob.ID {
		t.Fatalf("two accounts, named: %+v %v", u, err)
	}

	// a suspended account
	bob.Suspended = true
	st.UpdateUser(ctx, bob)
	if _, _, err := certs.CertUser(ctx, "", fp2); !errors.Is(err, ErrSuspended) {
		t.Fatalf("suspended: %v", err)
	}

	if err := certs.DeleteClientCert(ctx, alice.ID, c.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := certs.CertUser(ctx, "alice", fp1); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("deleted: %v", err)
	}
}

func TestClientCertLimit(t *testing.T) {
	svc, st := newService(t, config.Limits{})
	certs := NewClientCerts(st, st)
	u, _ := svc.Create(CreateInput{Username: "alice", Password: "alice-password"})
	for i := range MaxClientCerts {
		fp := strings.Repeat("0", 62) + string("0123456789abcdef"[i/16]) + string("0123456789abcdef"[i%16])
		if _, err := certs.AddClientCert(context.Background(), u.ID, fp, ""); err != nil {
			t.Fatal(i, err)
		}
	}
	if _, err := certs.AddClientCert(context.Background(), u.ID, strings.Repeat("f", 64), ""); !isValidation(err) {
		t.Fatalf("over the limit: %v", err)
	}
}
