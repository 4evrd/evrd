package client

import (
	"bufio"
	"context"
	"errors"
	"net"
	"regexp"
	"testing"
	"time"

	"github.com/4evrd/evrd/internal/accounts"
	"github.com/4evrd/evrd/internal/ircsignin"
	"github.com/4evrd/evrd/internal/mfa"
)

// connect is a client that sends pass (none when "") and registers.
func (r *rig) connect(pass string) *ircClient {
	r.t.Helper()
	c, err := net.Dial("tcp", r.addr)
	if err != nil {
		r.t.Fatal(err)
	}
	r.t.Cleanup(func() { c.Close() })
	ic := &ircClient{t: r.t, c: c, sc: bufio.NewScanner(c)}
	if pass != "" {
		ic.send("PASS " + pass)
	}
	ic.send("NICK alice")
	ic.send("USER alice 0 * :alice")
	return ic
}

var linkToken = regexp.MustCompile(`https://evrd\.example\.org/irc/approve/([A-Za-z0-9_-]+)`)

// token is the sign-in link's token in a *status line.
func token(t *testing.T, line string) string {
	t.Helper()
	m := linkToken.FindStringSubmatch(line)
	if m == nil {
		t.Fatalf("no link in %q", line)
	}
	return m[1]
}

func linkRig(t *testing.T) (*rig, ircsignin.IRCSignInServiceImpl) {
	r := newRig(t)
	r.srv.SetPublicURL("https://evrd.example.org")
	svc := ircsignin.New(r.st, r.acc)
	r.srv.SetLinkSignIn(svc, true)
	return r, svc
}

func TestSignInByLink(t *testing.T) {
	r, svc := linkRig(t)
	ctx := context.Background()
	alice, _ := r.st.UserByID(ctx, r.user.ID)

	// no PASS at all: a link, and nothing else works until it's used
	ic := r.connect("")
	ic.expect(" 001 alice ")
	tok := token(t, ic.expect("Sign in on the website to connect: https://evrd.example.org/irc/approve/"))
	ic.send("JOIN #one")
	ic.expect("Waiting to be allowed on the website")
	if _, err := svc.Allow(ctx, tok, alice, ircsignin.RememberNone, time.Hour); err != nil {
		t.Fatal(err)
	}
	ic.expect("Allowed on the website")
	ic.expect("JOIN #one") // the first network
	eventually(t, "attached as allowed by link", func() bool {
		for _, c := range r.b.ClientsOf(alice.ID) {
			if isDevice(c.Device, linkDevice) {
				return true
			}
		}
		return false
	})
	ic.c.Close()
	eventually(t, "the client is gone", func() bool { return len(r.b.ClientsOf(alice.ID)) == 0 })

	// user/network: that network; turned away, it's closed
	ic = r.connect("alice/two")
	tok = token(t, ic.expect("/irc/approve/"))
	if err := svc.Deny(ctx, tok, alice); err != nil {
		t.Fatal(err)
	}
	ic.expect("ERROR :The sign-in was turned away on the website.")

	// a link for alice can't be allowed by bob
	bob, _ := r.acc.Create(accounts.CreateInput{Username: "bob", Password: "bob-password"})
	ic = r.connect("alice/two")
	tok = token(t, ic.expect("/irc/approve/"))
	if _, err := svc.Allow(ctx, tok, bob, ircsignin.RememberNone, time.Hour); !errors.Is(err, ircsignin.ErrNotYours) {
		t.Fatalf("bob: %v", err)
	}
	if _, err := svc.Allow(ctx, tok, alice, ircsignin.RememberNone, time.Hour); err != nil {
		t.Fatal(err)
	}
	ic.expect("JOIN #two")
	ic.c.Close()
	eventually(t, "the client is gone", func() bool { return len(r.b.ClientsOf(alice.ID)) == 0 })

	// a long secret evrd doesn't know gets a link, and can be remembered
	sent := "my-client-config-has-this-long-secret"
	ic = r.connect("alice/one:" + sent)
	tok = token(t, ic.expect("/irc/approve/"))
	if _, err := svc.Allow(ctx, tok, alice, ircsignin.RememberSecret, time.Hour); err != nil {
		t.Fatal(err)
	}
	ic.expect("JOIN #one")
	ic.c.Close()
	eventually(t, "the client is gone", func() bool { return len(r.b.ClientsOf(alice.ID)) == 0 })
	ic = r.connect("alice/one:" + sent) // remembered: straight in
	ic.expect("JOIN #one")
	ic.c.Close()
	eventually(t, "the client is gone", func() bool { return len(r.b.ClientsOf(alice.ID)) == 0 })
	// a short wrong password is refused as before
	r.connect("alice:wrong").expect("464 alice :Invalid credentials")

	// a link goes when its connection does
	ic = r.connect("alice")
	tok = token(t, ic.expect("/irc/approve/"))
	ic.c.Close()
	eventually(t, "the link is gone", func() bool {
		_, err := svc.Lookup(ctx, tok)
		return errors.Is(err, ircsignin.ErrGone)
	})

	// and the client goes when its link does (expired, or dropped)
	ic = r.connect("alice")
	tok = token(t, ic.expect("/irc/approve/"))
	p, _ := svc.Lookup(ctx, tok)
	svc.Cancel(ctx, p.ID)
	ic.expect("ERROR :The sign-in link expired")

	// off: as before
	r.srv.SetLinkSignInOn(false)
	r.connect("alice").expect("464 alice :Password required")
}

func TestSecondFactorOnTheWebsite(t *testing.T) {
	r, svc := linkRig(t)
	ctx := context.Background()
	r.srv.SetSecondFactors(mfa.New(r.st), mfa.Policy{Mode: mfa.PolicyRequired})
	alice, _ := r.st.UserByID(ctx, r.user.ID)

	// no authenticator app: the website is the second factor
	ic := r.attach("alice/one:alice-password")
	tok := token(t, ic.expect("Allow this sign-in on the website before you're on one: "))
	if _, err := svc.Allow(ctx, tok, alice, ircsignin.RememberNone, time.Hour); err != nil {
		t.Fatal(err)
	}
	ic.expect("Second factor accepted.")
	ic.expect("JOIN #one")
	ic.c.Close()
	eventually(t, "the client is gone", func() bool { return len(r.b.ClientsOf(alice.ID)) == 0 })

	// with an app: either, and a code makes the link go
	secret, _ := enrol(t, mfa.New(r.st), alice)
	ic = r.attach("alice/one:alice-password")
	tok = token(t, ic.expect("/msg *status otp <code>, or allow this sign-in on the website: "))
	ic.send("PRIVMSG *status :otp " + mfa.TOTPCode(secret, time.Now()))
	ic.expect("JOIN #one")
	eventually(t, "the link is gone", func() bool {
		_, err := svc.Lookup(ctx, tok)
		return errors.Is(err, ircsignin.ErrGone)
	})
}
