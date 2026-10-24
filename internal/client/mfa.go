package client

// A second factor for IRC sign-ins (#91). A client whose sign-in needs one
// and didn't send a code after its password (PASS user:password:123456) is
// registered on no network, with only *status answering, until it sends
// /msg *status otp <code>; then it's put on its network as if it had just
// signed in. It has two minutes and five tries, and isn't held over an evrd
// restart (it signs in again).

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/4evrd/evrd/internal/bouncer"
	"github.com/4evrd/evrd/internal/irc"
	"github.com/4evrd/evrd/internal/mfa"
	"github.com/4evrd/evrd/internal/store"
	"github.com/4evrd/evrd/internal/telemetry"
)

// SecondFactors checks users' second factors (mfa.Service).
type SecondFactors interface {
	Status(ctx context.Context, userID int64) (mfa.Status, error)
	Verify(ctx context.Context, userID int64, code string) error
}

// mfaState is the server's second-factor checks and the admin's policy.
type mfaState struct {
	mu      sync.Mutex
	factors SecondFactors // nil: no second factors (tests)
	policy  mfa.Policy
}

// SetSecondFactors has IRC sign-ins ask for a second factor where policy
// says, checked with f.
func (s *Server) SetSecondFactors(f SecondFactors, policy mfa.Policy) {
	s.mfa.mu.Lock()
	defer s.mfa.mu.Unlock()
	s.mfa.factors, s.mfa.policy = f, policy
}

// SetMFAPolicy changes when a second factor is needed (irc_mfa).
func (s *Server) SetMFAPolicy(policy mfa.Policy) {
	s.mfa.mu.Lock()
	defer s.mfa.mu.Unlock()
	s.mfa.policy = policy
}

func (s *Server) secondFactors() (SecondFactors, mfa.Policy) {
	s.mfa.mu.Lock()
	defer s.mfa.mu.Unlock()
	return s.mfa.factors, s.mfa.policy
}

// otpWait is how long a client has to send its second factor, and
// otpTries how many wrong codes end the connection.
var (
	otpWait  = 2 * time.Minute
	otpTries = 5
)

// lockedSignIn is a client waiting for its second factor.
type lockedSignIn struct {
	net      store.Network // where it goes once verified
	unbound  bool          // a bouncer connection: on no network either way
	username string
	device   string
	tries    int
	timer    *time.Timer
}

// clientPassword reports whether device is an IRC client password's name
// (not the account or SSO password).
func clientPassword(device string) bool { return device != "" && device != ssoDevice }

// secondFactor decides whether u's sign-in needs a second factor and checks
// otp, the code sent after the password, if any: locked when the client
// has to send one first, or the numeric to refuse it with and why.
func (se *session) secondFactor(u store.User, username, network, device, otp string) (locked bool, code, why string) {
	factors, policy := se.srv.secondFactors()
	if factors == nil || !policy.Needed(u.IRCRequireMFA, clientPassword(device)) {
		return false, "", ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	st, err := factors.Status(ctx, u.ID)
	if err != nil {
		se.srv.log.Warn("second factor", "user", username, "err", err)
		return false, "464", "Can't check your second factor right now — try again in a moment"
	}
	if !st.TOTP {
		telemetry.IRCLogins.Add(context.Background(), 1, telemetry.Result("no_second_factor"))
		se.loginFailed(username, network, "no_second_factor")
		return false, "464", "This account needs a second factor for IRC — set up an authenticator app on " + se.srv.securityPage() + " first"
	}
	if otp == "" {
		return true, "", ""
	}
	if err := factors.Verify(ctx, u.ID, otp); err != nil {
		se.otpFailed(username)
		telemetry.IRCLogins.Add(context.Background(), 1, telemetry.Result("wrong_second_factor"))
		se.loginFailed(username, network, "wrong_second_factor")
		return false, "464", "Wrong second-factor code"
	}
	return false, "", ""
}

// securityPage is where users set up second factors.
func (s *Server) securityPage() string {
	if s.publicURL == "" {
		return "the web app's Account page"
	}
	return s.publicURL + "/account#security"
}

// otpFailed counts a wrong code toward the sign-in lockout.
func (se *session) otpFailed(username string) {
	if se.srv.signIns != nil {
		if f := se.srv.signIns.Failures(); f != nil {
			f.Add(strings.ToLower(username))
		}
	}
}

// lock leaves the client waiting for its second factor, for otpWait.
func (se *session) lock(l lockedSignIn) {
	se.locked = &l
	conn := se.conn
	l.timer = time.AfterFunc(otpWait, func() {
		se.send(fmt.Sprintf("ERROR :%s", ircText(se.tr("mfa.timedOut"))))
		conn.Close()
	})
	se.locked.timer = l.timer
	where := l.net.Name
	if l.unbound {
		where = "evrd"
	}
	se.say("mfa.challenge", "network", where, "status", statusNick)
}

// lockedCommand handles a command from a client waiting for its second
// factor, reporting whether dispatch should carry on with it (the few a
// bouncer connection takes).
func (se *session) lockedCommand(m irc.Message) bool {
	switch m.Command {
	case "PING", "QUIT", "CAP", "PASS", "USER", "AUTHENTICATE":
		return true
	case "ZNC":
		se.lockedStatus(strings.Join(m.Params, " "))
	case "PRIVMSG", "NOTICE":
		if len(m.Params) < 2 || !strings.EqualFold(m.Params[0], statusNick) {
			se.say("mfa.verifyFirst", "status", statusNick)
			return false
		}
		text := m.Params[len(m.Params)-1]
		if m.Command == "PRIVMSG" {
			se.lockedStatus(text)
		} else if v, ok := strings.CutPrefix(text, "\x01VERSION "); ok && se.att != nil {
			se.att.Update(func(ci *bouncer.ClientInfo) { ci.Software = clientName(strings.TrimSuffix(v, "\x01")) })
		}
	default:
		se.say("mfa.verifyFirst", "status", statusNick)
	}
	return false
}

// lockedStatus is *status for a client waiting for its second factor: only
// otp (and help, which says what to do).
func (se *session) lockedStatus(text string) {
	f := strings.Fields(text)
	if len(f) == 0 || !strings.EqualFold(f[0], "otp") {
		se.say("mfa.verifyFirst", "status", statusNick)
		return
	}
	if len(f) != 2 {
		se.say("usage.otp")
		return
	}
	se.verifyLocked(f[1])
}

// verifyLocked checks a second-factor code from a locked client.
func (se *session) verifyLocked(code string) {
	l := se.locked
	factors, _ := se.srv.secondFactors()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err := factors.Verify(ctx, se.uid, code)
	if err != nil {
		if !errors.Is(err, mfa.ErrWrongCode) {
			se.srv.log.Warn("second factor", "user", l.username, "err", err)
		}
		l.tries++
		se.otpFailed(l.username)
		se.loginFailed(l.username, l.net.Name, "wrong_second_factor")
		if l.tries >= otpTries {
			telemetry.IRCLogins.Add(context.Background(), 1, telemetry.Result("wrong_second_factor"))
			se.send(fmt.Sprintf("ERROR :%s", ircText(se.tr("mfa.tooManyTries"))))
			se.conn.Close()
			return
		}
		se.say("mfa.wrongCodeTries", "count", otpTries-l.tries)
		return
	}
	if se.srv.signIns != nil {
		if f := se.srv.signIns.Failures(); f != nil {
			f.Reset(strings.ToLower(l.username))
		}
	}
	se.unlock()
}

// unlock puts a verified client on its network, as attach would have.
func (se *session) unlock() {
	l := se.locked
	l.timer.Stop()
	se.locked = nil
	if !l.unbound {
		if err := se.bind(l.net); err != nil {
			se.say("mfa.networkFailed", "network", l.net.Name, "error", err)
			se.conn.Close()
			return
		}
		if nick := orElse(se.up.Nick(), l.net.Nick); nick != se.unick {
			se.send(fmt.Sprintf(":%s!%s@%s NICK %s", se.unick, se.unick, serverName, nick))
			se.unick = nick
		}
		se.att.Update(func(ci *bouncer.ClientInfo) { ci.Networks = []string{l.net.Name} })
	}
	telemetry.IRCLogins.Add(context.Background(), 1, telemetry.Result("ok"))
	se.say("mfa.verified")
	se.syncCaps()
	se.signedIn(!l.unbound, l.username, l.net.Name, l.device)
}
