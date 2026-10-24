// Package ircsignin is IRC sign-in by link (#91): an IRC client that comes
// without a password it can use gets a link to evrd's website, where its
// owner signs in (with a password, or their identity provider and its MFA)
// and allows it. The same link is the alternative to a code when an IRC
// sign-in needs a second factor.
//
// A link is good for five minutes, once, for the one connection that asked:
// its token (256 random bits) is kept only as a SHA-256, and the pending
// sign-in goes when the connection does. Pending sign-ins live in the
// database, so the website and the IRC client may be on different nodes.
package ircsignin

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/crypto/bcrypt"

	"github.com/4evrd/evrd/internal/accounts"
	"github.com/4evrd/evrd/internal/messages"
	"github.com/4evrd/evrd/internal/store"
)

var tracer = otel.Tracer("evrd/ircsignin")

// LinkFor is how long a link works.
const LinkFor = 5 * time.Minute

// MinRememberedSecret is how long the secret a client sent must be to be
// remembered as its device's password (anything shorter may be a typo'd
// password, or guessable).
const MinRememberedSecret = 24

// Limits on open links, against a client asking for one after another.
const (
	maxPerIP   = 3
	maxPerUser = 5
)

// States of a pending sign-in.
const (
	Pending = "pending"
	Allowed = "allowed"
	Denied  = "denied"
)

// How to remember an allowed device: not at all, with a new client
// password (shown once), or with the secret it sent.
const (
	RememberNone     = ""
	RememberPassword = "password"
	RememberSecret   = "secret"
)

// Errors, said in words.
var (
	ErrTooMany   = messages.Errorf("signin.tooMany")
	ErrGone      = messages.Errorf("signin.gone")
	ErrNotYours  = messages.Errorf("signin.notYours")
	ErrNoSecret  = messages.Errorf("signin.noSecret")
	ErrForbidden = messages.Errorf("signin.forbidden")
)

// Request is what an IRC client asking for a link said about itself.
// UserID is set when it's a second factor for a signed-in account.
type Request struct {
	Username, Network, Nick, IP, Software string
	Secret                                string // what it sent as its password, if anything
	UserID                                int64
}

// IRCSignInServiceImpl is IRC sign-in by link.
type IRCSignInServiceImpl interface {
	// Begin opens a link for a client: its id (for Status and Cancel) and
	// the token that goes in the link.
	Begin(ctx context.Context, r Request) (id, token string, err error)
	// Status is a pending sign-in as it is now (ErrGone once expired).
	Status(ctx context.Context, id string) (store.PendingSignIn, error)
	// Cancel drops it (the client went away).
	Cancel(ctx context.Context, id string) error
	// Lookup is the sign-in behind a link, for its page.
	Lookup(ctx context.Context, token string) (store.PendingSignIn, error)
	// Allow lets u's client in, remembering the device for ttl as
	// remember says; the new client password when one was made.
	Allow(ctx context.Context, token string, u store.User, remember string, ttl time.Duration) (string, error)
	// Deny turns it away.
	Deny(ctx context.Context, token string, u store.User) error
}

// Service keeps pending sign-ins in their repository; accounts makes the
// remembered devices' passwords.
type Service struct {
	Repository store.PendingSignInRepositoryImpl
	Accounts   accounts.AccountServiceImpl
	now        func() time.Time
}

// New is sign-in by link on repo.
func New(repo store.PendingSignInRepositoryImpl, acc accounts.AccountServiceImpl) IRCSignInServiceImpl {
	return &Service{Repository: repo, Accounts: acc, now: time.Now}
}

func random(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func (s *Service) Begin(ctx context.Context, r Request) (string, string, error) {
	ctx, span := tracer.Start(ctx, "IRCSignInService.Begin", trace.WithAttributes(attribute.String("user.name", r.Username)))
	defer span.End()
	now := s.now()
	_ = s.Repository.DeleteExpiredSignIns(ctx, now)
	user := strings.ToLower(r.Username)
	byIP, byUser, err := s.Repository.CountPendingSignIns(ctx, r.IP, user, now)
	if err != nil {
		return "", "", err
	}
	if byIP >= maxPerIP || byUser >= maxPerUser {
		return "", "", ErrTooMany
	}
	p := store.PendingSignIn{ID: random(16), Username: user, UserID: r.UserID, Network: trim(r.Network, 64), Nick: trim(r.Nick, 64),
		IP: r.IP, Software: trim(r.Software, 64), CreatedAt: now, ExpiresAt: now.Add(LinkFor), State: Pending}
	if len(r.Secret) >= MinRememberedSecret {
		h, err := bcrypt.GenerateFromPassword([]byte(r.Secret), bcrypt.DefaultCost)
		if err != nil {
			return "", "", err
		}
		p.SecretHash = string(h)
	}
	token := random(32)
	p.TokenHash = hashToken(token)
	if err := s.Repository.CreatePendingSignIn(ctx, p); err != nil {
		return "", "", err
	}
	return p.ID, token, nil
}

func trim(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

func (s *Service) Status(ctx context.Context, id string) (store.PendingSignIn, error) {
	p, err := s.Repository.PendingSignIn(ctx, id)
	return s.live(p, err)
}

// live is p if it's still open (or decided), ErrGone once it expired.
func (s *Service) live(p store.PendingSignIn, err error) (store.PendingSignIn, error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		return store.PendingSignIn{}, ErrGone
	case err != nil:
		return store.PendingSignIn{}, err
	case p.State == Pending && !s.now().Before(p.ExpiresAt):
		return store.PendingSignIn{}, ErrGone
	}
	return p, nil
}

func (s *Service) Cancel(ctx context.Context, id string) error {
	return s.Repository.DeletePendingSignIn(ctx, id)
}

func (s *Service) Lookup(ctx context.Context, token string) (store.PendingSignIn, error) {
	ctx, span := tracer.Start(ctx, "IRCSignInService.Lookup")
	defer span.End()
	p, err := s.Repository.PendingSignInByToken(ctx, hashToken(token))
	return s.live(p, err)
}

// decidable is the pending sign-in behind token if u may decide it.
func (s *Service) decidable(ctx context.Context, token string, u store.User) (store.PendingSignIn, error) {
	p, err := s.Lookup(ctx, token)
	switch {
	case err != nil:
		return p, err
	case p.State != Pending:
		return p, ErrGone
	case p.UserID != 0 && p.UserID != u.ID, p.Username != "" && !strings.EqualFold(p.Username, u.Username):
		return p, ErrNotYours
	case u.Suspended:
		return p, ErrForbidden
	}
	return p, nil
}

func (s *Service) Allow(ctx context.Context, token string, u store.User, remember string, ttl time.Duration) (string, error) {
	ctx, span := tracer.Start(ctx, "IRCSignInService.Allow", trace.WithAttributes(attribute.Int64("user.id", u.ID), attribute.String("remember", remember)))
	defer span.End()
	p, err := s.decidable(ctx, token, u)
	if err != nil {
		return "", err
	}
	if remember == RememberSecret && p.SecretHash == "" {
		return "", ErrNoSecret
	}
	// decided first, so a second Allow can't make a second device
	if ok, err := s.Repository.DecidePendingSignIn(ctx, p.ID, Allowed, u.ID, ""); err != nil || !ok {
		if err == nil {
			err = ErrGone
		}
		return "", err
	}
	name := p.Software
	if name == "" {
		name = "IRC client"
	}
	switch remember {
	case RememberPassword:
		_, secret, err := s.Accounts.RememberDevice(u.ID, name, "", ttl)
		return secret, err
	case RememberSecret:
		_, _, err := s.Accounts.RememberDevice(u.ID, name, p.SecretHash, ttl)
		return "", err
	}
	return "", nil
}

func (s *Service) Deny(ctx context.Context, token string, u store.User) error {
	ctx, span := tracer.Start(ctx, "IRCSignInService.Deny", trace.WithAttributes(attribute.Int64("user.id", u.ID)))
	defer span.End()
	p, err := s.decidable(ctx, token, u)
	if errors.Is(err, ErrNotYours) {
		err = nil // anyone signed in may turn away a link that was sent to them
	}
	if err != nil {
		return err
	}
	if ok, err := s.Repository.DecidePendingSignIn(ctx, p.ID, Denied, u.ID, ""); err != nil || !ok {
		if err == nil {
			err = ErrGone
		}
		return err
	}
	return nil
}
