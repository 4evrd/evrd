package client

// draft/webpush: a client registers a push endpoint for its account, and
// evrd pushes the user's DMs and highlights there while none of their
// clients is attached (internal/webpush). The command and its replies are
// Ergo's, so clients work against both.
//
// https://github.com/ircv3/ircv3-specifications/pull/471

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/4evrd/evrd/internal/store"
	"github.com/4evrd/evrd/internal/webpush"
)

// SetPush turns on draft/webpush (nil: off).
func (s *Server) SetPush(p *webpush.Service) { s.push = p }

// webpush answers WEBPUSH REGISTER <endpoint> <keys> and WEBPUSH UNREGISTER
// <endpoint>.
func (se *session) webpush(p []string) {
	if len(p) < 2 {
		se.numeric("461", "WEBPUSH :Not enough parameters")
		return
	}
	sub, endpoint := strings.ToUpper(p[0]), p[1]
	fail := func(code, sub, text string) {
		se.send(fmt.Sprintf(":%s FAIL WEBPUSH %s %s %s :%s", se.serverName(), code, sub, safeParam(endpoint), text))
	}
	if !se.srv.push.Enabled() {
		fail("FORBIDDEN", sub, "Web push is disabled")
		return
	}
	if !se.pushAllowed() {
		fail("FORBIDDEN", sub, "You must be logged in to receive push messages")
		return
	}
	if webpush.CheckEndpoint(endpoint) != nil {
		fail("INVALID_PARAMS", sub, "Invalid web push URL")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), webpush.Timeout+5*time.Second)
	defer cancel()
	switch sub {
	case "REGISTER":
		if len(p) < 3 {
			fail("INVALID_PARAMS", sub, "Insufficient parameters for WEBPUSH REGISTER")
			return
		}
		keys, err := webpush.ParseKeys(p[2])
		if err != nil {
			fail("INVALID_PARAMS", sub, "Invalid subscription keys for WEBPUSH REGISTER")
			return
		}
		switch err := se.srv.push.Register(ctx, se.uid, se.net.ID, endpoint, keys); {
		case err == nil:
			se.pushEndpoint = endpoint
			se.send(fmt.Sprintf(":%s WEBPUSH REGISTER %s %s", se.serverName(), endpoint, p[2]))
		case errors.Is(err, store.ErrPushLimit):
			fail("MAX_REGISTRATIONS", "REGISTER", "You have too many push subscriptions already")
		case errors.Is(err, webpush.ErrTestFailed):
			fail("INVALID_PARAMS", "REGISTER", "Test push message failed to send")
		default:
			se.srv.log.Error("webpush: register", "user", se.uid, "err", err)
			fail("INTERNAL_ERROR", "REGISTER", "An error occurred")
		}
	case "UNREGISTER":
		// always answered as done, as Ergo does
		if err := se.srv.push.Unregister(ctx, se.uid, se.net.ID, endpoint); err != nil {
			se.srv.log.Warn("webpush: unregister", "user", se.uid, "err", err)
		}
		if se.pushEndpoint == endpoint {
			se.pushEndpoint = ""
		}
		se.send(fmt.Sprintf(":%s WEBPUSH UNREGISTER %s", se.serverName(), endpoint))
	default:
		fail("INVALID_PARAMS", sub, "Unknown subcommand")
	}
}

// pushAllowed reports whether this client may register for push: signed in
// to an account (not waiting for a second factor), and in managed mode a
// Boson account's client, signed in with its access token — never a guest.
func (se *session) pushAllowed() bool {
	if se.uid == 0 || se.att == nil || se.locked != nil || se.guest || se.acct.Guest {
		return false
	}
	if se.isManaged() {
		return isDevice(se.att.Info().Device, tokenDevice)
	}
	return true
}

// safeParam is a parameter fit to quote back in a reply (Ergo's
// SafeErrorParam): "*" for one that would break the line.
func safeParam(p string) string {
	if p == "" || strings.HasPrefix(p, ":") || strings.ContainsAny(p, " \r\n") {
		return "*"
	}
	return p
}
