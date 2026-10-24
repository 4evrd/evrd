package client

// IRC sign-in by link (#91, internal/ircsignin). A client that comes without
// a password it can use — just "user", "user/network", or no PASS at all, or
// a long secret evrd doesn't know — is registered on no network and given a
// link to the website, where its owner signs in (any way the website
// allows, MFA included) and allows it. Then it's put on its network as if it
// had signed in. It waits five minutes at most, and its link goes when it
// does. The same link is offered instead of a code when a sign-in needs a
// second factor (mfa.go).

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/4evrd/evrd/internal/accounts"
	"github.com/4evrd/evrd/internal/bouncer"
	"github.com/4evrd/evrd/internal/ircsignin"
	"github.com/4evrd/evrd/internal/messages"
	"github.com/4evrd/evrd/internal/telemetry"
)

// linkState is the server's sign-in links.
type linkState struct {
	mu   sync.Mutex
	svc  ircsignin.IRCSignInServiceImpl // nil: no links
	on   bool                           // irc_link_signin (with a public URL)
	keep time.Duration                  // how long a remembered device lasts
}

// SetLinkSignIn has clients without a usable password sign in by link
// (on), with links from svc.
func (s *Server) SetLinkSignIn(svc ircsignin.IRCSignInServiceImpl, on bool) {
	s.link.mu.Lock()
	defer s.link.mu.Unlock()
	s.link.svc, s.link.on = svc, on
}

// SetLinkSignInOn turns sign-in by link on or off (irc_link_signin).
func (s *Server) SetLinkSignInOn(on bool) {
	s.link.mu.Lock()
	defer s.link.mu.Unlock()
	s.link.on = on
}

func (s *Server) links() ircsignin.IRCSignInServiceImpl {
	s.link.mu.Lock()
	defer s.link.mu.Unlock()
	if !s.link.on || s.publicURL == "" {
		return nil
	}
	return s.link.svc
}

func (s *Server) linkSignInOn() bool { return s.links() != nil }

// linkURL is the page that allows a sign-in.
func (s *Server) linkURL(token string) string { return s.publicURL + "/irc/approve/" + token }

// linkAfterRefusal reports whether a refused secret should get a link
// instead: a long one that matched nothing (not a typo'd password, which is
// refused as before).
func (se *session) linkAfterRefusal(secret string) bool {
	return se.srv.linkSignInOn() && len(secret) >= ircsignin.MinRememberedSecret &&
		(se.refusal == "invalid" || se.refusal == "sso_needs_client_password")
}

// waitForLink registers the client on no network and sends it a link to
// allow it on the website. login is what it said in PASS (user,
// user/network or nothing); secret what it sent as a password, if anything
// (it may become the device's password).
func (se *session) waitForLink(login, secret string) bool {
	username, network, _ := cutLogin(login)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	links := se.srv.links()
	id, token, err := links.Begin(ctx, ircsignin.Request{Username: username, Network: network, Nick: se.nick, IP: se.clientIP(), Secret: secret})
	if errors.Is(err, ircsignin.ErrTooMany) {
		se.numeric("464", ":Too many sign-in links open — try again in a few minutes")
		return false
	}
	if err != nil {
		se.srv.log.Warn("irc sign-in link", "err", err)
		se.numeric("464", ":Password required — use PASS <user>[/<network>]:<password>")
		return false
	}
	telemetry.IRCLogins.Add(context.Background(), 1, telemetry.Result("link"))
	se.srv.log.Info("irc sign-in by link", "user", username, "ip", se.clientIP())
	se.unick = orElse(se.nick, "*")
	se.welcome()
	l := &lockedSignIn{link: true, pendingID: id, url: se.srv.linkURL(token), username: username, net: networkNamed(network)}
	se.locked = l
	conn := se.conn
	l.timer = time.AfterFunc(ircsignin.LinkFor, func() {
		se.send(fmt.Sprintf("ERROR :%s", ircText(se.tr("signin.expired"))))
		conn.Close()
	})
	se.say("signin.link", "url", l.url, "minutes", int(ircsignin.LinkFor/time.Minute))
	return true
}

// checkLink looks whether a waiting client's link was used: allowed, it
// goes on; turned away or expired, it's closed.
func (se *session) checkLink() {
	l := se.locked
	links := se.srv.links()
	if l == nil || l.pendingID == "" || links == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	p, err := links.Status(ctx, l.pendingID)
	switch {
	case errors.Is(err, ircsignin.ErrGone):
		se.send(fmt.Sprintf("ERROR :%s", ircText(se.tr("signin.expired"))))
		se.conn.Close()
	case err != nil:
		se.srv.log.Warn("irc sign-in link", "err", err)
	case p.State == ircsignin.Denied:
		telemetry.IRCLogins.Add(context.Background(), 1, telemetry.Result("link_denied"))
		se.send(fmt.Sprintf("ERROR :%s", ircText(se.tr("signin.denied"))))
		se.conn.Close()
	case p.State == ircsignin.Allowed && !l.link:
		se.unlock() // a second factor, given on the website
	case p.State == ircsignin.Allowed:
		se.finishLink(p.AllowedUser)
	}
}

// finishLink signs a client allowed on the website in as userID and puts
// it on its network.
func (se *session) finishLink(userID int64) {
	l := se.locked
	u, err := se.srv.acc.UserByID(context.Background(), userID)
	if err != nil || u.Suspended {
		se.send(fmt.Sprintf("ERROR :%s", ircText(se.tr("signin.denied"))))
		se.conn.Close()
		return
	}
	se.uid, se.acct = u.ID, u
	fail := func(why string) {
		se.send(fmt.Sprintf("ERROR :%s", ircText(why)))
		se.conn.Close()
	}
	if ip := se.clientIP(); ip != "" && !accounts.HostAllowed(u, ip) {
		se.loginFailed(u.Username, l.net.Name, "host_denied")
		fail("Signing in from " + ip + " isn't allowed for this account")
		return
	}
	if !se.srv.b.Owns(u.ID) {
		// this node can't serve them: a remembered device signs in again
		// on the right one
		fail(se.tr("signin.otherNode"))
		return
	}
	chosen, unbound, code, why := se.chooseNetwork(u, l.net.Name)
	if code != "" {
		fail(why)
		return
	}
	var bound []string
	if !unbound {
		bound = []string{chosen.Name}
	}
	att, err := se.srv.b.AttachClient(u.ID, se.srv.acc.LimitsFor(u).MaxClients,
		bouncer.ClientInfo{Device: linkDevice, Networks: bound, IP: se.clientIP()}, func() { se.conn.Close() })
	if err != nil {
		fail(fmt.Sprintf("Too many clients attached (limit %d) — detach one first", se.srv.acc.LimitsFor(u).MaxClients))
		return
	}
	se.att, se.release = att, att.Release
	l.timer.Stop()
	se.locked = nil
	if !unbound {
		if err := se.bind(chosen); err != nil {
			fail("Network unavailable: " + err.Error())
			return
		}
		if nick := orElse(se.up.Nick(), chosen.Nick); nick != se.unick {
			se.send(fmt.Sprintf(":%s!%s@%s NICK %s", se.unick, se.unick, serverName, nick))
			se.unick = nick
		}
	}
	telemetry.IRCLogins.Add(context.Background(), 1, telemetry.Result("ok"))
	se.srv.log.Info("irc sign-in allowed by link", "user", u.Username, "ip", se.clientIP())
	se.say("signin.allowed")
	se.syncCaps()
	se.signedIn(!unbound, u.Username, chosen.Name, linkDevice)
}

// linkDevice is how a client allowed by link shows among the user's
// clients.
const linkDevice = "allowed by link"

// dropLink forgets a waiting client's link (it went away, or got in).
func (se *session) dropLink() {
	l := se.locked
	if l == nil || l.pendingID == "" {
		return
	}
	if l.timer != nil {
		l.timer.Stop()
	}
	if links := se.srv.links(); links != nil {
		_ = links.Cancel(context.Background(), l.pendingID)
	}
}

// eventTitle is the activity entry for a client signing in with device.
func eventTitle(device, client, method string) messages.Text {
	switch device {
	case ssoDevice:
		return messages.T("event.attachedSSO", "client", client, "method", messages.T("ssoMethod."+method))
	case linkDevice:
		return messages.T("event.attachedLink", "client", client)
	}
	return messages.T("event.attached", "client", client)
}
