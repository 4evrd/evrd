package client

// Managed mode's guests (internal/managed/guests.go): a client that signs
// in without credentials, while guests are on, gets an ephemeral session
// of its own. And WEBIRC from the web gateway in front of evrd, so a
// guest (or an account) reaches the IRC server with its own address.

import (
	"context"
	"crypto/subtle"
	"errors"
	"net/netip"
	"time"

	"github.com/4evrd/evrd/internal/irc"
	"github.com/4evrd/evrd/internal/managed"
	"github.com/4evrd/evrd/internal/store"
	"github.com/4evrd/evrd/internal/telemetry"
)

// SetGuests lets guests in through g (nil: none). Call it before serving.
func (s *Server) SetGuests(g *managed.Guests) { s.guests = g }

// guestsOn reports whether a client without credentials may come in as a
// guest.
func (se *session) guestsOn() bool { return se.isManaged() && se.srv.guests.On() }

// guestDevice is how a guest's client shows among its account's clients.
const guestDevice = "guest"

// guestRefusals are what a refused guest is told (neutral words: managed
// mode names nobody).
var guestRefusals = map[error]string{
	managed.ErrGuestsOff:   "Password required",
	managed.ErrGuestsFull:  "Too many guests right now, try again later",
	managed.ErrGuestsPerIP: "Too many guests from your address",
	managed.ErrGuestsRate:  "Too many new guest sessions from your address, try again in a minute",
}

// guestSignIn is the guest account for this client, or false after
// telling it why not.
func (se *session) guestSignIn() (store.User, string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	ip := se.clientIP()
	u, resumed, err := se.srv.guests.SignIn(ctx, managed.GuestInput{IP: ip, Nick: se.nick, User: se.user, Realname: se.realname})
	if err != nil {
		why := ""
		for e, text := range guestRefusals {
			if errors.Is(err, e) {
				why = text
			}
		}
		if why == "" {
			se.srv.log.Warn("guest sign-in", "ip", ip, "err", err)
			why = se.srv.managed.Text("unavailable", se.nick)
		} else {
			se.srv.log.Info("guest refused", "ip", ip, "why", err)
		}
		telemetry.IRCLogins.Add(context.Background(), 1, telemetry.Result("guest_refused"))
		se.refusal = "guest_refused"
		se.numeric("464", ":"+why)
		return store.User{}, "", false
	}
	se.guest = true
	if resumed {
		se.srv.log.Info("guest back", "guest", u.Username, "ip", ip)
	}
	return u, sharedDevice(guestDevice, ip), true
}

// guestBound lets the guests follow this guest's nick on the network (a
// refresh picks the session up again by it).
func (se *session) guestBound() {
	if se.guest && se.up != nil {
		se.srv.guests.Track(se.uid, se.up.Nick)
	}
}

// webIRC takes a web gateway's WEBIRC (WEBIRC <password> <gateway> <host>
// <ip>) while registering: with managed_client_webirc_password, the
// client's address is the one the gateway gives. A wrong password or a
// bad address is ignored (and logged).
func (se *session) webIRC(m irc.Message) {
	m2 := se.srv.managed
	if m2 == nil || se.internal || len(m.Params) < 4 {
		return
	}
	pw := m2.Config().ClientWebIRCPassword
	if pw == "" || subtle.ConstantTimeCompare([]byte(m.Params[0]), []byte(pw)) != 1 {
		se.srv.log.Warn("refused WEBIRC from a client", "from", remoteHost(se.conn))
		return
	}
	a, err := netip.ParseAddr(m.Params[3])
	if err != nil {
		se.srv.log.Warn("WEBIRC with a bad address", "from", remoteHost(se.conn), "addr", m.Params[3])
		return
	}
	se.fromIP = a.Unmap().String()
}
