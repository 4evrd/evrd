package mfa

import (
	"context"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/4evrd/evrd/internal/config"
	"github.com/4evrd/evrd/internal/store"
)

func newService(t *testing.T) (*Service, store.Store, *time.Time) {
	t.Helper()
	dir, err := os.MkdirTemp("", "evrd-mfa-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	st, err := store.Open("sqlite:" + filepath.Join(dir, "evrd.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	s := New(st).(*Service)
	s.now = func() time.Time { return now }
	return s, st, &now
}

// secretOf is the plain secret behind an enrolment's grouped one.
func secretOf(e Enrolment) string { return strings.ReplaceAll(e.Secret, " ", "") }

func TestTOTPMatchesRFC6238(t *testing.T) {
	// RFC 6238's SHA-1 test key, "12345678901234567890", in base32
	secret := "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"
	for unix, want := range map[int64]string{59: "287082", 1111111109: "081804", 1234567890: "005924", 2000000000: "279037"} {
		if got := TOTPCode(secret, time.Unix(unix, 0)); got != want {
			t.Errorf("%d: %s, want %s", unix, got, want)
		}
	}
	if TOTPCode("not base32!", time.Now()) != "" {
		t.Error("a bad secret made a code")
	}
}

func TestEnrolConfirmVerify(t *testing.T) {
	s, st, now := newService(t)
	ctx := context.Background()
	u, err := st.CreateUser(ctx, store.User{Username: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	if st, _ := s.Status(ctx, u.ID); st.TOTP || st.RecoveryCodes != 0 {
		t.Fatalf("status before: %+v", st)
	}
	if _, err := s.ConfirmTOTP(ctx, u.ID, "123456"); !errors.Is(err, ErrNotStarted) {
		t.Fatalf("confirm before start: %v", err)
	}
	if err := s.Verify(ctx, u.ID, "123456"); !errors.Is(err, ErrNoFactor) {
		t.Fatalf("verify with no app: %v", err)
	}
	e, err := s.StartTOTP(ctx, u, "bnc.example.org")
	if err != nil {
		t.Fatal(err)
	}
	link, _ := url.Parse(e.URL)
	if link.Scheme != "otpauth" || link.Query().Get("secret") != secretOf(e) || link.Query().Get("issuer") != "bnc.example.org" ||
		!strings.Contains(e.URL, "bnc.example.org:alice") || !strings.HasPrefix(e.QR, "<svg") {
		t.Fatalf("enrolment: %+v", e)
	}
	// not on until confirmed
	if st, _ := s.Status(ctx, u.ID); st.TOTP {
		t.Fatal("on before it's confirmed")
	}
	if _, err := s.ConfirmTOTP(ctx, u.ID, "000000"); !errors.Is(err, ErrWrongCode) {
		t.Fatalf("a wrong first code: %v", err)
	}
	codes, err := s.ConfirmTOTP(ctx, u.ID, TOTPCode(secretOf(e), *now))
	if err != nil || len(codes) != RecoveryCodeCount {
		t.Fatalf("confirm: %v %v", codes, err)
	}
	if _, err := s.StartTOTP(ctx, u, "x"); !errors.Is(err, ErrAlreadySet) {
		t.Fatalf("a second app: %v", err)
	}
	if _, err := s.ConfirmTOTP(ctx, u.ID, "123456"); !errors.Is(err, ErrAlreadySet) {
		t.Fatalf("confirm twice: %v", err)
	}

	// the confirming code was used up; the next step's code works once,
	// and so does one step either side for clocks a bit off
	if err := s.Verify(ctx, u.ID, TOTPCode(secretOf(e), *now)); !errors.Is(err, ErrWrongCode) {
		t.Fatalf("the confirming code again: %v", err)
	}
	*now = now.Add(30 * time.Second)
	code := TOTPCode(secretOf(e), *now)
	if err := s.Verify(ctx, u.ID, code[:3]+" "+code[3:]); err != nil {
		t.Fatalf("a fresh code: %v", err)
	}
	if err := s.Verify(ctx, u.ID, code); !errors.Is(err, ErrWrongCode) {
		t.Fatalf("a replayed code: %v", err)
	}
	*now = now.Add(2 * time.Minute)
	if err := s.Verify(ctx, u.ID, TOTPCode(secretOf(e), now.Add(30*time.Second))); err != nil {
		t.Fatalf("a step ahead: %v", err)
	}
	if err := s.Verify(ctx, u.ID, TOTPCode(secretOf(e), now.Add(2*time.Minute))); !errors.Is(err, ErrWrongCode) {
		t.Fatalf("far ahead: %v", err)
	}

	// recovery codes: any form, once each
	if err := s.Verify(ctx, u.ID, strings.ToUpper(strings.ReplaceAll(codes[0], "-", ""))); err != nil {
		t.Fatalf("a recovery code: %v", err)
	}
	if err := s.Verify(ctx, u.ID, codes[0]); !errors.Is(err, ErrWrongCode) {
		t.Fatalf("a used recovery code: %v", err)
	}
	if st, _ := s.Status(ctx, u.ID); !st.TOTP || st.RecoveryCodes != RecoveryCodeCount-1 {
		t.Fatalf("status: %+v", st)
	}
	fresh, err := s.NewRecoveryCodes(ctx, u.ID)
	if err != nil || len(fresh) != RecoveryCodeCount {
		t.Fatal(err)
	}
	if err := s.Verify(ctx, u.ID, codes[1]); !errors.Is(err, ErrWrongCode) {
		t.Fatal("an old recovery code works after new ones")
	}
	if err := s.DisableTOTP(ctx, u.ID); err != nil {
		t.Fatal(err)
	}
	if st, _ := s.Status(ctx, u.ID); st.TOTP || st.RecoveryCodes != 0 {
		t.Fatalf("after removing it: %+v", st)
	}
	if _, err := s.NewRecoveryCodes(ctx, u.ID); !errors.Is(err, ErrNoFactor) {
		t.Fatalf("recovery codes without an app: %v", err)
	}
	// deleting the account takes them too
	e, _ = s.StartTOTP(ctx, u, "x")
	if err := st.DeleteUser(ctx, u.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.TOTP(ctx, u.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("the secret outlived the account: %v", err)
	}
}

func TestPolicy(t *testing.T) {
	for _, c := range []struct {
		p                    Policy
		wants, clientPw, out bool
	}{
		{Policy{Mode: PolicyOff}, true, false, false},
		{Policy{Mode: PolicyOptional}, false, false, false},
		{Policy{Mode: PolicyOptional}, true, false, true},
		{Policy{Mode: PolicyRequired}, false, false, true},                       // the admin wins
		{Policy{Mode: PolicyRequired}, true, true, false},                        // client passwords are left out…
		{Policy{Mode: PolicyRequired, ClientPasswords: true}, false, true, true}, // …unless
		{Policy{Mode: PolicyOptional, ClientPasswords: true}, true, true, true},
	} {
		if got := c.p.Needed(c.wants, c.clientPw); got != c.out {
			t.Errorf("%+v wants=%v clientPassword=%v: %v", c.p, c.wants, c.clientPw, got)
		}
	}
	for mode, want := range map[string]string{PolicyOff: "hidden", PolicyOptional: "free", PolicyRequired: "locked"} {
		if got := (Policy{Mode: mode}).UserChoice(); got != want {
			t.Errorf("%s: %s", mode, got)
		}
	}
	if p := PolicyOf(config.Auth{IRCMFA: "required", IRCMFAClientPasswords: true}); p.Mode != PolicyRequired || !p.ClientPasswords {
		t.Errorf("PolicyOf: %+v", p)
	}
}

func TestLooksLikeCode(t *testing.T) {
	for s, want := range map[string]bool{
		"123456": true, "abcd-efgh": true, "ABCD-EFGH": true, "abcdefgh": false, "12345": false, "1234567": false,
		"password": false, "abcd-efg1": false, "": false, "abcd-efgh-ijkl": false,
	} {
		if LooksLikeCode(s) != want {
			t.Errorf("%q: %v", s, !want)
		}
	}
}

func TestQRCodeSVG(t *testing.T) {
	svg, err := QRCodeSVG("otpauth://totp/x:y?secret=ABC")
	if err != nil || !strings.HasPrefix(svg, `<svg xmlns="http://www.w3.org/2000/svg"`) || !strings.Contains(svg, "h1v1h-1z") {
		t.Fatalf("%v %.80s", err, svg)
	}
	if _, err := QRCodeSVG(strings.Repeat("x", 5000)); err == nil {
		t.Fatal("too long to encode, but no error")
	}
}
