package client

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/4evrd/evrd/internal/accounts"
	"github.com/4evrd/evrd/internal/mfa"
	"github.com/4evrd/evrd/internal/store"
)

// enrol sets up an authenticator app for u, returning its secret and the
// recovery codes. The confirming code is the previous step's, so the
// current step's is still unused.
func enrol(t *testing.T, svc mfa.MFAServiceImpl, u store.User) (string, []string) {
	t.Helper()
	ctx := context.Background()
	e, err := svc.StartTOTP(ctx, u, "test")
	if err != nil {
		t.Fatal(err)
	}
	secret := strings.ReplaceAll(e.Secret, " ", "")
	codes, err := svc.ConfirmTOTP(ctx, u.ID, mfa.TOTPCode(secret, time.Now().Add(-30*time.Second)))
	if err != nil {
		t.Fatal(err)
	}
	return secret, codes
}

func TestSecondFactorOverIRC(t *testing.T) {
	r := newRig(t)
	// try signs in with pass, waits for want, and hangs up (the rig allows
	// five clients at once)
	try := func(pass, want string) {
		t.Helper()
		ic := r.attach(pass)
		ic.expect(want)
		ic.c.Close()
		eventually(t, "the client is gone", func() bool { return len(r.b.ClientsOf(r.user.ID)) == 0 })
	}
	svc := mfa.New(r.st)
	r.srv.SetSecondFactors(svc, mfa.Policy{Mode: mfa.PolicyRequired})
	secret, codes := enrol(t, svc, r.user)

	// no code: signed in, on no network, only *status answering
	ic := r.attach("alice/one:alice-password")
	ic.expect(" 001 alice ")
	ic.expect("A second factor is needed before you're on one: /msg *status otp <code>")
	ic.send("JOIN #elsewhere")
	ic.expect("Send your second factor first")
	ic.send("PRIVMSG *status :networks")
	ic.expect("Send your second factor first")
	ic.send("PRIVMSG *status :otp")
	ic.expect("Usage: otp <code>")
	ic.send("PRIVMSG *status :otp 000000")
	ic.expect("That code isn't right — 4 tries left.")
	ic.send("PRIVMSG *status :otp " + mfa.TOTPCode(secret, time.Now()))
	ic.expect("Second factor accepted.")
	ic.expect("JOIN #one") // on its network now
	ic.send("PRIVMSG *status :networks")
	ic.expect("one")
	ic.c.Close()
	eventually(t, "the client is gone", func() bool { return len(r.b.ClientsOf(r.user.ID)) == 0 })

	// a code after the password goes straight through (a recovery code here)
	try("alice/one:alice-password:"+codes[0], "JOIN #one")
	// …and a wrong one is refused
	try("alice/one:alice-password:"+codes[0], "464 alice :Wrong second-factor code")

	// five wrong codes end it
	ic = r.attach("alice:alice-password")
	ic.expect("otp <code>")
	for range 4 {
		ic.send("PRIVMSG *status :otp 000000")
		ic.expect("isn't right")
	}
	ic.send("PRIVMSG *status :otp 000000")
	ic.expect("ERROR :Too many wrong codes")
	eventually(t, "the client is gone", func() bool { return len(r.b.ClientsOf(r.user.ID)) == 0 })

	// so does waiting too long
	otpWait = 300 * time.Millisecond
	t.Cleanup(func() { otpWait = 2 * time.Minute })
	try("alice:alice-password", "ERROR :No second factor within 2 minutes")
	otpWait = 2 * time.Minute

	// an account without an app can't sign in while it's required
	r.acc.Create(accounts.CreateInput{Username: "bob", Password: "bob-password"})
	r.attach("bob:bob-password").expect("464 alice :This account needs a second factor for IRC — set up an authenticator app on")

	// client passwords are left out, unless the admin says otherwise
	_, cp, _ := r.acc.NewClientPassword(r.user.ID, "laptop")
	try("alice/one:"+cp, "JOIN #one")
	r.srv.SetMFAPolicy(mfa.Policy{Mode: mfa.PolicyRequired, ClientPasswords: true})
	try("alice/one:"+cp, "A second factor is needed")

	// optional: only for users who asked for it
	r.srv.SetMFAPolicy(mfa.Policy{Mode: mfa.PolicyOptional})
	try("alice/one:alice-password", "JOIN #one")
	u, _ := r.st.UserByID(t.Context(), r.user.ID)
	u.IRCRequireMFA = true
	r.st.UpdateUser(t.Context(), u)
	try("alice/one:alice-password", "A second factor is needed")
	r.srv.SetMFAPolicy(mfa.Policy{Mode: mfa.PolicyOff})
	try("alice/one:alice-password", "JOIN #one")
}

func TestLockedClientsAreNotHeldOver(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	se := &session{att: nil}
	if se.heldOver(ctx) {
		t.Fatal("held over without a holder")
	}
	se.locked = &lockedSignIn{}
	se.saveState() // returns before touching the (missing) holder
}
