// Package mfa is second factors for signing in from IRC clients (#91): an
// authenticator app (TOTP, RFC 6238) with one-time recovery codes, and the
// policy that says when one is needed.
//
// The admin's irc_mfa decides: off (never), optional (each user decides
// with their "Require a second factor for IRC" toggle) or required (every
// password sign-in, whatever the user set). IRC client passwords are
// per-device, random and revocable, so they're left out unless
// irc_mfa_client_passwords is on.
package mfa

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/4evrd/evrd/internal/config"
	"net/url"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"rsc.io/qr"

	"github.com/4evrd/evrd/internal/messages"
	"github.com/4evrd/evrd/internal/store"
)

var tracer = otel.Tracer("evrd/mfa")

// The admin's irc_mfa.
const (
	PolicyOff      = "off"
	PolicyOptional = "optional"
	PolicyRequired = "required"
)

// Policy is when IRC sign-ins need a second factor.
type Policy struct {
	Mode            string // PolicyOff, PolicyOptional or PolicyRequired
	ClientPasswords bool   // irc_mfa_client_passwords: client passwords too
}

// PolicyOf is the policy the settings set (irc_mfa, irc_mfa_client_passwords).
func PolicyOf(a config.Auth) Policy {
	return Policy{Mode: a.IRCMFA, ClientPasswords: a.IRCMFAClientPasswords}
}

// UserChoice is what the user's toggle can be: "hidden" (irc_mfa off),
// "locked" (required, on whatever they chose) or "free".
func (p Policy) UserChoice() string {
	switch p.Mode {
	case PolicyOff:
		return "hidden"
	case PolicyRequired:
		return "locked"
	}
	return "free"
}

// Needed reports whether a sign-in needs a second factor: userWants is the
// user's toggle, clientPassword whether the secret was an IRC client
// password. The admin's required wins over the user's toggle; off hides it.
func (p Policy) Needed(userWants, clientPassword bool) bool {
	switch {
	case p.Mode == PolicyOff:
		return false
	case clientPassword && !p.ClientPasswords:
		return false
	case p.Mode == PolicyRequired:
		return true
	default:
		return userWants
	}
}

// Errors, said in words (internal/messages).
var (
	ErrWrongCode   = messages.Errorf("mfa.wrongCode")
	ErrNoFactor    = messages.Errorf("mfa.noFactor")
	ErrNotStarted  = messages.Errorf("mfa.notStarted")
	ErrAlreadySet  = messages.Errorf("mfa.alreadySet")
	ErrNeedsFactor = messages.Errorf("mfa.needsFactor")
)

// RecoveryCodeCount is how many recovery codes a user gets.
const RecoveryCodeCount = 10

// Status is a user's second factors.
type Status struct {
	TOTP          bool `json:"totp"`          // an authenticator app is set up
	RecoveryCodes int  `json:"recoveryCodes"` // unused recovery codes
}

// Enrolment is an authenticator app being set up: the secret to type in,
// the otpauth:// URL and a QR code (SVG) of it.
type Enrolment struct {
	Secret string `json:"secret"`
	URL    string `json:"url"`
	QR     string `json:"qr"`
}

// MFAServiceImpl is a user's second factors.
type MFAServiceImpl interface {
	Status(ctx context.Context, userID int64) (Status, error)
	// StartTOTP makes a new secret for u (replacing one not confirmed yet);
	// issuer names the server in the app.
	StartTOTP(ctx context.Context, u store.User, issuer string) (Enrolment, error)
	// ConfirmTOTP takes the first code from the app, switching it on, and
	// returns new recovery codes (shown once).
	ConfirmTOTP(ctx context.Context, userID int64, code string) ([]string, error)
	// DisableTOTP removes the app and the recovery codes.
	DisableTOTP(ctx context.Context, userID int64) error
	// NewRecoveryCodes replaces the user's recovery codes.
	NewRecoveryCodes(ctx context.Context, userID int64) ([]string, error)
	// Verify checks a code from the app or a recovery code (used up).
	Verify(ctx context.Context, userID int64, code string) error
}

// Service keeps second factors in their repository.
type Service struct {
	Repository store.MFARepositoryImpl
	now        func() time.Time
}

// New is the second-factor service on repo.
func New(repo store.MFARepositoryImpl) MFAServiceImpl {
	return &Service{Repository: repo, now: time.Now}
}

func (s *Service) span(ctx context.Context, name string, userID int64) (context.Context, trace.Span) {
	return tracer.Start(ctx, "MFAService."+name, trace.WithAttributes(attribute.Int64("user.id", userID)))
}

func (s *Service) Status(ctx context.Context, userID int64) (Status, error) {
	ctx, span := s.span(ctx, "Status", userID)
	defer span.End()
	var st Status
	t, err := s.Repository.TOTP(ctx, userID)
	switch {
	case err == nil:
		st.TOTP = t.Confirmed
	case !errors.Is(err, store.ErrNotFound):
		return Status{}, err
	}
	codes, err := s.Repository.RecoveryCodes(ctx, userID)
	if err != nil {
		return Status{}, err
	}
	for _, c := range codes {
		if c.UsedAt == nil {
			st.RecoveryCodes++
		}
	}
	return st, nil
}

func (s *Service) StartTOTP(ctx context.Context, u store.User, issuer string) (Enrolment, error) {
	ctx, span := s.span(ctx, "StartTOTP", u.ID)
	defer span.End()
	if t, err := s.Repository.TOTP(ctx, u.ID); err == nil && t.Confirmed {
		return Enrolment{}, ErrAlreadySet
	}
	b := make([]byte, 20)
	if _, err := rand.Read(b); err != nil {
		return Enrolment{}, err
	}
	secret := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b)
	if err := s.Repository.PutTOTP(ctx, store.UserTOTP{UserID: u.ID, Secret: secret, CreatedAt: s.now()}); err != nil {
		return Enrolment{}, err
	}
	link := OTPAuthURL(issuer, u.Username, secret)
	svg, err := QRCodeSVG(link)
	if err != nil {
		return Enrolment{}, err
	}
	return Enrolment{Secret: groups(secret), URL: link, QR: svg}, nil
}

func (s *Service) ConfirmTOTP(ctx context.Context, userID int64, code string) ([]string, error) {
	ctx, span := s.span(ctx, "ConfirmTOTP", userID)
	defer span.End()
	t, err := s.Repository.TOTP(ctx, userID)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return nil, ErrNotStarted
	case err != nil:
		return nil, err
	case t.Confirmed:
		return nil, ErrAlreadySet
	}
	step, ok := matchTOTP(t.Secret, code, s.now())
	if !ok {
		return nil, ErrWrongCode
	}
	t.Confirmed, t.LastStep = true, step
	if err := s.Repository.PutTOTP(ctx, t); err != nil {
		return nil, err
	}
	return s.NewRecoveryCodes(ctx, userID)
}

func (s *Service) DisableTOTP(ctx context.Context, userID int64) error {
	ctx, span := s.span(ctx, "DisableTOTP", userID)
	defer span.End()
	if err := s.Repository.DeleteTOTP(ctx, userID); err != nil {
		return err
	}
	return s.Repository.ReplaceRecoveryCodes(ctx, userID, nil)
}

func (s *Service) NewRecoveryCodes(ctx context.Context, userID int64) ([]string, error) {
	ctx, span := s.span(ctx, "NewRecoveryCodes", userID)
	defer span.End()
	if t, err := s.Repository.TOTP(ctx, userID); err != nil || !t.Confirmed {
		return nil, ErrNoFactor
	}
	codes := make([]string, RecoveryCodeCount)
	hashes := make([]string, RecoveryCodeCount)
	for i := range codes {
		b := make([]byte, 5)
		if _, err := rand.Read(b); err != nil {
			return nil, err
		}
		c := strings.ToLower(base32.StdEncoding.EncodeToString(b)) // 8 characters
		codes[i] = c[:4] + "-" + c[4:]
		hashes[i] = hashCode(c)
	}
	if err := s.Repository.ReplaceRecoveryCodes(ctx, userID, hashes); err != nil {
		return nil, err
	}
	return codes, nil
}

func (s *Service) Verify(ctx context.Context, userID int64, code string) error {
	ctx, span := s.span(ctx, "Verify", userID)
	defer span.End()
	t, err := s.Repository.TOTP(ctx, userID)
	switch {
	case errors.Is(err, store.ErrNotFound) || err == nil && !t.Confirmed:
		return ErrNoFactor
	case err != nil:
		return err
	}
	if step, ok := matchTOTP(t.Secret, code, s.now()); ok {
		fresh, err := s.Repository.UseTOTPStep(ctx, userID, step)
		if err != nil {
			return err
		}
		if !fresh { // that code (or a later one) was taken already
			return ErrWrongCode
		}
		return nil
	}
	if rc := normalizeRecovery(code); len(rc) == 8 {
		codes, err := s.Repository.RecoveryCodes(ctx, userID)
		if err != nil {
			return err
		}
		h := hashCode(rc)
		for _, c := range codes {
			if c.UsedAt == nil && subtle.ConstantTimeCompare([]byte(c.Hash), []byte(h)) == 1 {
				if ok, err := s.Repository.UseRecoveryCode(ctx, c.ID, s.now()); err != nil || ok {
					return err
				}
			}
		}
	}
	return ErrWrongCode
}

// --- TOTP --------------------------------------------------------------------

// step is TOTP's time step; codes from one step either side are taken, for
// clocks a little off.
const step = 30 * time.Second

// TOTPCode is the 6-digit code for secret (base32) at time t.
func TOTPCode(secret string, t time.Time) string {
	return hotp(secret, t.Unix()/int64(step/time.Second))
}

func hotp(secret string, counter int64) string {
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(secret))
	if err != nil {
		return ""
	}
	m := hmac.New(sha1.New, key)
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(counter))
	m.Write(msg[:])
	sum := m.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	n := binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff
	return fmt.Sprintf("%06d", n%1_000_000)
}

// matchTOTP reports whether code is secret's at now (or a step either
// side), and which step.
func matchTOTP(secret, code string, now time.Time) (int64, bool) {
	code = strings.Map(func(r rune) rune {
		if r == ' ' || r == '-' {
			return -1
		}
		return r
	}, code)
	if len(code) != 6 {
		return 0, false
	}
	cur := now.Unix() / int64(step/time.Second)
	for _, s := range []int64{cur, cur - 1, cur + 1} {
		if c := hotp(secret, s); c != "" && subtle.ConstantTimeCompare([]byte(c), []byte(code)) == 1 {
			return s, true
		}
	}
	return 0, false
}

// LooksLikeCode reports whether s could be a second-factor code after a
// password: 6 digits (the app's) or a recovery code as shown, xxxx-xxxx
// (the dash keeps an 8-letter word from passing for one).
func LooksLikeCode(s string) bool {
	if len(s) == 6 && strings.Trim(s, "0123456789") == "" {
		return true
	}
	return len(s) == 9 && s[4] == '-' && len(normalizeRecovery(s)) == 8
}

// normalizeRecovery is a recovery code as stored: lowercase, no dash or
// spaces ("" when it can't be one).
func normalizeRecovery(s string) string {
	s = strings.ToLower(strings.NewReplacer("-", "", " ", "").Replace(s))
	if strings.Trim(s, "abcdefghijklmnopqrstuvwxyz234567") != "" {
		return ""
	}
	return s
}

func hashCode(c string) string {
	sum := sha256.Sum256([]byte(c))
	return hex.EncodeToString(sum[:])
}

// groups is a secret in groups of four, for typing it in.
func groups(s string) string {
	var parts []string
	for i := 0; i < len(s); i += 4 {
		parts = append(parts, s[i:min(i+4, len(s))])
	}
	return strings.Join(parts, " ")
}

// OTPAuthURL is the otpauth:// link authenticator apps read (from the QR
// code): the account shows as "issuer: username".
func OTPAuthURL(issuer, username, secret string) string {
	label := url.PathEscape(issuer + ":" + username)
	q := url.Values{"secret": {secret}, "issuer": {issuer}, "algorithm": {"SHA1"}, "digits": {"6"}, "period": {"30"}}
	return "otpauth://totp/" + label + "?" + q.Encode()
}

// QRCodeSVG is text as a QR code, an SVG that scales to fit (black on
// white, with the quiet zone).
func QRCodeSVG(text string) (string, error) {
	c, err := qr.Encode(text, qr.M)
	if err != nil {
		return "", err
	}
	const quiet = 4
	n := c.Size + 2*quiet
	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" shape-rendering="crispEdges"><rect width="%d" height="%d" fill="#fff"/><path fill="#000" d="`, n, n, n, n)
	for y := range c.Size {
		for x := range c.Size {
			if c.Black(x, y) {
				fmt.Fprintf(&b, "M%d %dh1v1h-1z", x+quiet, y+quiet)
			}
		}
	}
	b.WriteString(`"/></svg>`)
	return b.String(), nil
}
