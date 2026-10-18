// Package accounts holds evrd's user-management rules, shared by the web API,
// the IRC listener and evrctl: username and password policy, limits, IRC
// client passwords, and the guards that keep at least one admin around.
package accounts

import (
	"crypto/rand"
	"encoding/base32"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/crypto/bcrypt"

	"code.ircforever.org/ircforever/evrd/internal/config"
	"code.ircforever.org/ircforever/evrd/internal/store"
)

// MinPasswordLen is the shortest password accepted.
const MinPasswordLen = 8

var (
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrSuspended          = errors.New("account suspended")
	ErrLastAdmin          = errors.New("this is the last admin — make someone else an admin first")
	ErrUsernameTaken      = errors.New("that username is taken")
)

// ValidationError is a rule the input broke; Message is safe to show.
type ValidationError struct{ Message string }

func (e *ValidationError) Error() string { return e.Message }

func invalid(format string, a ...any) error {
	return &ValidationError{fmt.Sprintf(format, a...)}
}

var usernameRe = regexp.MustCompile(`^[a-z][a-z0-9_-]{1,31}$`)

// NormalizeUsername lowercases and checks a username: 2–32 characters,
// starting with a letter, then letters, digits, - and _.
func NormalizeUsername(s string) (string, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	if !usernameRe.MatchString(s) {
		return "", invalid("usernames are 2–32 characters: a–z, 0–9, - and _, starting with a letter")
	}
	return s, nil
}

// CheckPassword applies the password policy.
func CheckPassword(pw string) error {
	switch {
	case !utf8.ValidString(pw):
		return invalid("passwords must be valid text")
	case utf8.RuneCountInString(pw) < MinPasswordLen:
		return invalid("use at least %d characters", MinPasswordLen)
	case len(pw) > 72:
		return invalid("passwords are limited to 72 bytes")
	case strings.IndexFunc(pw, unicode.IsControl) >= 0:
		return invalid("passwords can't contain line breaks or control characters")
	}
	return nil
}

// HashPassword checks the policy and returns a bcrypt hash.
func HashPassword(pw string) (string, error) {
	if err := CheckPassword(pw); err != nil {
		return "", err
	}
	h, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
	return string(h), err
}

// Service applies the rules against the store.
type Service struct {
	st       store.Store
	defaults config.Limits
	now      func() time.Time
}

func New(st store.Store, defaults config.Limits) *Service {
	return &Service{st: st, defaults: defaults, now: time.Now}
}

// Limits are a user's effective limits; 0 means unlimited.
type Limits struct {
	MaxNetworks  int `json:"maxNetworks"`
	MaxClients   int `json:"maxClients"`
	BacklogLines int `json:"backlogLines"`
}

// Defaults are the server-wide limits.
func (s *Service) Defaults() Limits {
	return Limits{s.defaults.MaxNetworks, s.defaults.MaxClients, s.defaults.BacklogLines}
}

// LimitsFor resolves a user's overrides against the server defaults.
func (s *Service) LimitsFor(u store.User) Limits {
	pick := func(override *int, def int) int {
		if override != nil {
			return *override
		}
		return def
	}
	return Limits{
		MaxNetworks:  pick(u.MaxNetworks, s.defaults.MaxNetworks),
		MaxClients:   pick(u.MaxClients, s.defaults.MaxClients),
		BacklogLines: pick(u.BacklogLines, s.defaults.BacklogLines),
	}
}

// CreateInput describes a new account.
type CreateInput struct {
	Username string
	Email    string
	Password string // optional: empty means SSO / client passwords only
	Role     string
	Subject  string // optional OIDC subject to link
}

// Create validates and stores a new user.
func (s *Service) Create(in CreateInput) (store.User, error) {
	name, err := NormalizeUsername(in.Username)
	if err != nil {
		return store.User{}, err
	}
	if _, err := s.st.UserByUsername(name); err == nil {
		return store.User{}, ErrUsernameTaken
	} else if !errors.Is(err, store.ErrNotFound) {
		return store.User{}, err
	}
	role := in.Role
	if role == "" {
		role = store.RoleUser
	}
	if role != store.RoleUser && role != store.RoleAdmin {
		return store.User{}, invalid("role must be user or admin")
	}
	u := store.User{Username: name, Email: strings.TrimSpace(in.Email), Role: role, DefaultNick: name}
	if in.Password != "" {
		if u.PasswordHash, err = HashPassword(in.Password); err != nil {
			return store.User{}, err
		}
	}
	if in.Subject != "" {
		sub := in.Subject
		u.OIDCSubject = &sub
	}
	return s.st.CreateUser(u)
}

// SetPassword replaces a user's local password.
func (s *Service) SetPassword(u *store.User, pw string) error {
	h, err := HashPassword(pw)
	if err != nil {
		return err
	}
	u.PasswordHash = h
	return s.st.UpdateUser(*u)
}

// ChangePassword is the self-service change: the current password must match.
func (s *Service) ChangePassword(u *store.User, current, next string) error {
	if u.PasswordHash == "" || bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(current)) != nil {
		return ErrInvalidCredentials
	}
	return s.SetPassword(u, next)
}

// PasswordLogin checks a username and local password for the web sign-in.
func (s *Service) PasswordLogin(username, password string) (store.User, error) {
	u, err := s.st.UserByUsername(strings.ToLower(strings.TrimSpace(username)))
	if err != nil {
		// Spend the same time as a real check so usernames can't be probed.
		bcrypt.CompareHashAndPassword(dummyHash, []byte(password))
		return store.User{}, ErrInvalidCredentials
	}
	if u.PasswordHash == "" || bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)) != nil {
		return store.User{}, ErrInvalidCredentials
	}
	if u.Suspended {
		return store.User{}, ErrSuspended
	}
	return u, nil
}

var dummyHash, _ = bcrypt.GenerateFromPassword([]byte("evrd-timing-dummy"), bcrypt.DefaultCost)

// IRCLogin checks the secret a native IRC client sent with PASS: the user's
// local password, or one of their client passwords.
func (s *Service) IRCLogin(username, secret string) (store.User, error) {
	u, err := s.st.UserByUsername(strings.ToLower(username))
	if err != nil {
		bcrypt.CompareHashAndPassword(dummyHash, []byte(secret))
		return store.User{}, ErrInvalidCredentials
	}
	ok := u.PasswordHash != "" && bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(secret)) == nil
	if !ok {
		cps, err := s.st.ListClientPasswords(u.ID)
		if err != nil {
			return store.User{}, err
		}
		for _, cp := range cps {
			if bcrypt.CompareHashAndPassword([]byte(cp.Hash), []byte(secret)) == nil {
				_ = s.st.TouchClientPassword(cp.ID, s.now())
				ok = true
				break
			}
		}
	}
	if !ok {
		return store.User{}, ErrInvalidCredentials
	}
	if u.Suspended {
		return store.User{}, ErrSuspended
	}
	return u, nil
}

// NewClientPassword creates an IRC client password and returns its secret,
// which is shown once and never stored in the clear.
func (s *Service) NewClientPassword(userID int64, name string) (store.ClientPassword, string, error) {
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > 64 {
		return store.ClientPassword{}, "", invalid("give it a name of up to 64 characters, e.g. the client or device")
	}
	b := make([]byte, 20)
	if _, err := rand.Read(b); err != nil {
		return store.ClientPassword{}, "", err
	}
	secret := strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b))
	// Grouped for reading aloud / typing: xxxx-xxxx-…
	var parts []string
	for i := 0; i < len(secret); i += 4 {
		parts = append(parts, secret[i:min(i+4, len(secret))])
	}
	secret = strings.Join(parts, "-")
	h, err := bcrypt.GenerateFromPassword([]byte(secret), bcrypt.DefaultCost)
	if err != nil {
		return store.ClientPassword{}, "", err
	}
	cp, err := s.st.CreateClientPassword(store.ClientPassword{UserID: userID, Name: name, Hash: string(h)})
	return cp, secret, err
}

// CanCreateNetwork enforces the network limit.
func (s *Service) CanCreateNetwork(u store.User) error {
	max := s.LimitsFor(u).MaxNetworks
	if max == 0 {
		return nil
	}
	n, err := s.st.CountNetworks(u.ID)
	if err != nil {
		return err
	}
	if n >= int64(max) {
		return invalid("you've reached your limit of %d networks — remove one first", max)
	}
	return nil
}

// GuardAdminChange refuses a change that would leave no admin: demoting,
// suspending or deleting the only one.
func (s *Service) GuardAdminChange(before store.User, stillAdmin bool) error {
	if !before.IsAdmin() || stillAdmin {
		return nil
	}
	n, err := s.st.CountAdmins()
	if err != nil {
		return err
	}
	if n <= 1 {
		return ErrLastAdmin
	}
	return nil
}
