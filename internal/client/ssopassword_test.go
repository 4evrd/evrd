package client

import (
	"bufio"
	"context"
	"encoding/base64"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/4evrd/evrd/internal/accounts"
	"github.com/4evrd/evrd/internal/auth"
	"github.com/4evrd/evrd/internal/store"
)

// fakeSignIns is the web sign-in's half of IRC sign-in: a lockout, and SSO
// passwords from a map of subject -> password (or error).
type fakeSignIns struct {
	fails *auth.FailureLimiter
	on    bool
	pw    map[string]string
	err   error // answered for every check when set
	asked int
}

func (f *fakeSignIns) Failures() *auth.FailureLimiter { return f.fails }
func (f *fakeSignIns) SSOPasswordsOn() bool           { return f.on }
func (f *fakeSignIns) SSOPasswordMethod() string      { return "ldap" }
func (f *fakeSignIns) CheckSSOPassword(_ context.Context, u store.User, password string) error {
	f.asked++
	if f.err != nil {
		return f.err
	}
	if u.OIDCSubject == nil || f.pw[*u.OIDCSubject] != password {
		return auth.ErrSSOWrongPassword
	}
	return nil
}

func TestSignInWithTheSSOPassword(t *testing.T) {
	r := newRig(t)
	sam, err := r.acc.Create(accounts.CreateInput{Username: "sam"}) // SSO: no password of its own
	if err != nil {
		t.Fatal(err)
	}
	sub := "logto-sam"
	sam.OIDCSubject = &sub
	r.st.UpdateUser(t.Context(), sam)
	si := &fakeSignIns{fails: auth.NewFailureLimiter(10, time.Minute), pw: map[string]string{sub: "sams-sso-password"}}
	r.srv.SetSignIns(si)

	// off: the SSO account is told to use a client password, and the
	// provider isn't asked
	r.attach("sam:sams-sso-password").expect("464 alice :Invalid credentials — this account signs in with SSO, so use an IRC client password")
	if si.asked != 0 {
		t.Fatal("asked the provider while it's off")
	}

	si.on = true
	r.attach("sam:sams-sso-password").expect("No networks configured") // signed in
	r.attach("sam:wrong").expect("464 alice :Invalid credentials")

	// SASL PLAIN takes it too
	c, err := net.Dial("tcp", r.addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	ic := &ircClient{t: t, c: c, sc: bufio.NewScanner(c)}
	ic.send("CAP LS 302")
	ic.send("CAP REQ :sasl")
	ic.send("AUTHENTICATE PLAIN")
	ic.expect("AUTHENTICATE +")
	ic.send("AUTHENTICATE " + base64.StdEncoding.EncodeToString([]byte("\x00sam\x00sams-sso-password")))
	ic.expect("903 ")

	// a client password or the account's own password is tried first
	before := si.asked
	r.attach("alice:alice-password").expect(" 001 ")
	if si.asked != before {
		t.Fatal("asked the provider about a local account's password")
	}

	// the provider's refusals
	si.err = auth.ErrSSOSuspended
	r.attach("sam:sams-sso-password").expect("465 alice :This account is suspended")
	si.err = auth.ErrSSONoPassword
	r.attach("sam:x").expect("464 alice :Your SSO account has no password")
	si.err = auth.ErrSSOUnavailable
	for range 12 { // not the user's doing: no lockout
		r.attach("sam:sams-sso-password").expect("464 alice :Can't check your SSO password right now — use an IRC client password")
	}
	si.err = nil
	r.attach("sam:sams-sso-password").expect("No networks configured")

	// suspended in evrd
	sam.Suspended = true
	r.st.UpdateUser(t.Context(), sam)
	r.attach("sam:sams-sso-password").expect("465 alice :This account is suspended")
}

func TestIRCSignInLockout(t *testing.T) {
	r := newRig(t)
	si := &fakeSignIns{fails: auth.NewFailureLimiter(3, time.Minute)}
	r.srv.SetSignIns(si)
	for range 3 {
		r.attach("alice:wrong").expect("464 alice :Invalid credentials")
	}
	// locked: even the right password is refused for a while, and it's
	// shared with the web sign-in (the same limiter)
	r.attach("alice:alice-password").expect("464 alice :Too many failed sign-ins — try again in a few minutes")
	if !si.fails.Blocked("alice") {
		t.Fatal("not blocked")
	}
	si.fails.Reset("alice")
	r.attach("ALICE:alice-password").expect(" 001 ")
	if si.fails.Blocked("alice") || strings.Contains(r.logs.String(), "alice-password") {
		t.Fatal("a sign-in left failures, or the password was logged")
	}
}
