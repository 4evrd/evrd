// Package accounts holds evrd's user-management rules, shared by the web API,
// the IRC listener and evrctl: username and password policy, limits, IRC
// client passwords, and the guards that keep at least one admin around.
package accounts

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"errors"
	"fmt"
	"net/netip"
	"path"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/crypto/bcrypt"

	"github.com/4evrd/evrd/internal/config"
	"github.com/4evrd/evrd/internal/messages"
	"github.com/4evrd/evrd/internal/store"
)

// MinPasswordLen is the shortest password accepted.
const MinPasswordLen = 10

var (
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrSuspended          = errors.New("account suspended")
	ErrLastAdmin          = messages.Errorf("err.lastAdmin")
	ErrUsernameTaken      = messages.Errorf("err.usernameTaken")
	// ErrNeedsClientPassword is a failed IRC sign-in to an account with no
	// password of its own (it signs in with SSO): only client passwords work.
	ErrNeedsClientPassword = fmt.Errorf("%w: the account signs in with SSO", ErrInvalidCredentials)
)

// ValidationError is a rule the input broke; Message is safe to show, and
// Text is it in words to put in the reader's language (zero: Message only).
type ValidationError struct {
	Message string
	Text    messages.Text
}

func (e *ValidationError) Error() string { return e.Message }

// MessageText is e in words (messages.Texter); zero for one with only a
// Message.
func (e *ValidationError) MessageText() messages.Text { return e.Text }

// invalid is a ValidationError saying irc.err.<key> (internal/messages),
// with name/value pairs of arguments.
func invalid(key string, args ...any) error {
	t := messages.T("err."+key, args...)
	return &ValidationError{Message: t.English(), Text: t}
}

var usernameRe = regexp.MustCompile(`^[a-z][a-z0-9_-]{1,31}$`)

// NormalizeUsername lowercases and checks a username: 2–32 characters,
// starting with a letter, then letters, digits, - and _.
func NormalizeUsername(s string) (string, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	if !usernameRe.MatchString(s) {
		return "", invalid("usernameRules")
	}
	return s, nil
}

// CheckPassword applies the password policy.
func CheckPassword(pw string) error {
	switch {
	case !utf8.ValidString(pw):
		return invalid("passwordText")
	case utf8.RuneCountInString(pw) < MinPasswordLen:
		return invalid("passwordShort", "min", MinPasswordLen)
	case len(pw) > 72:
		return invalid("passwordLong")
	case strings.IndexFunc(pw, unicode.IsControl) >= 0:
		return invalid("passwordControl")
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

// AccountServiceImpl is users and their limits, passwords and networks, for
// the web API, the IRC listener and evrctl.
type AccountServiceImpl interface {
	NetworkServiceImpl

	SetBindHosts(hosts []string)
	SetDefaults(l config.Limits)
	BindHosts() []string
	CheckBindHost(u store.User, bind string) error
	Defaults() Limits
	LimitsFor(u store.User) Limits
	Create(in CreateInput) (store.User, error)
	SetPassword(u *store.User, pw string) error
	SetTemporaryPassword(u *store.User, pw string) (string, error)
	ChangePassword(u *store.User, current, next string) error
	PasswordLogin(username, password string) (store.User, error)
	IRCLogin(username, secret string) (store.User, string, error)
	NewClientPassword(userID int64, name string) (store.ClientPassword, string, error)
	CanCreateNetwork(u store.User) error
	GuardAdminChange(before store.User, stillAdmin bool) error
	CheckNetworkName(userID int64, name string, exceptID int64) error

	UserByID(ctx context.Context, id int64) (store.User, error)
	UserByUsername(ctx context.Context, username string) (store.User, error)
	ListUsers(ctx context.Context) ([]store.User, error)
	UpdateUser(ctx context.Context, u store.User) error
	DeleteUser(ctx context.Context, id int64) error
	ListClientPasswords(ctx context.Context, userID int64) ([]store.ClientPassword, error)
	DeleteClientPassword(ctx context.Context, userID, id int64) error
}

// Repository is what the account service keeps its records in.
type Repository interface {
	store.UserRepositoryImpl
	store.ClientPasswordRepositoryImpl
	store.NetworkRepositoryImpl
}

// Service applies the rules against the repositories.
type Service struct {
	NetworkServiceImpl // users' networks

	users     store.UserRepositoryImpl
	passwords store.ClientPasswordRepositoryImpl
	now       func() time.Time

	mu        sync.RWMutex // settings that can change at runtime
	defaults  config.Limits
	bindHosts []string
}

// SetBindHosts sets the bind hosts users may choose from.
func (s *Service) SetBindHosts(hosts []string) {
	s.mu.Lock()
	s.bindHosts = append([]string(nil), hosts...)
	s.mu.Unlock()
}

// SetDefaults sets the server-wide limits.
func (s *Service) SetDefaults(l config.Limits) {
	s.mu.Lock()
	s.defaults = l
	s.mu.Unlock()
}

// BindHosts are the bind hosts users may choose from.
func (s *Service) BindHosts() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]string(nil), s.bindHosts...)
}

func (s *Service) limits() config.Limits {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.defaults
}

// CheckBindHost reports whether u may connect from bind ("" always may).
// Admins may use any address; users pick from the server's list unless
// their bind host is locked (DenySetBindHost).
func (s *Service) CheckBindHost(u store.User, bind string) error {
	switch {
	case bind == "" || u.IsAdmin():
		return nil
	case u.DenySetBindHost:
		return invalid("bindHostByAdmin")
	case len(s.BindHosts()) == 0:
		return invalid("noBindHosts")
	}
	for _, h := range s.BindHosts() {
		if strings.EqualFold(h, bind) {
			return nil
		}
	}
	return invalid("notABindHost", "host", bind, "hosts", strings.Join(s.BindHosts(), ", "))
}

// New is the account service on repo. It's the concrete type, for main and
// tests; everything else takes an AccountServiceImpl.
func New(repo Repository, defaults config.Limits) *Service {
	return &Service{NetworkServiceImpl: NewNetworkService(repo), users: repo, passwords: repo, defaults: defaults, now: time.Now}
}

// Limits are a user's effective limits; 0 means unlimited.
type Limits struct {
	MaxNetworks  int `json:"maxNetworks"`
	MaxClients   int `json:"maxClients"`
	BacklogLines int `json:"backlogLines"`
}

// Defaults are the server-wide limits.
func (s *Service) Defaults() Limits {
	d := s.limits()
	return Limits{d.MaxNetworks, d.MaxClients, d.BacklogLines}
}

// LimitsFor resolves a user's overrides against the server defaults.
func (s *Service) LimitsFor(u store.User) Limits {
	pick := func(override *int, def int) int {
		if override != nil {
			return *override
		}
		return def
	}
	d := s.limits()
	return Limits{
		MaxNetworks:  pick(u.MaxNetworks, d.MaxNetworks),
		MaxClients:   pick(u.MaxClients, d.MaxClients),
		BacklogLines: pick(u.BacklogLines, d.BacklogLines),
	}
}

// CreateInput describes a new account.
type CreateInput struct {
	Username string
	Email    string
	Password string // optional: empty means SSO / client passwords only
	// Temporary makes the password one the user must change at first sign-in.
	Temporary bool
	Role      string
	Subject   string // optional OIDC subject to link
	// SSOClaimable lets the owner's first SSO sign-in (same username and
	// email) take over the account; see store.User.
	SSOClaimable bool
}

// Create validates and stores a new user.
func (s *Service) Create(in CreateInput) (store.User, error) {
	name, err := NormalizeUsername(in.Username)
	if err != nil {
		return store.User{}, err
	}
	if _, err := s.users.UserByUsername(context.Background(), name); err == nil {
		return store.User{}, ErrUsernameTaken
	} else if !errors.Is(err, store.ErrNotFound) {
		return store.User{}, err
	}
	role := in.Role
	if role == "" {
		role = store.RoleUser
	}
	if role != store.RoleUser && role != store.RoleAdmin {
		return store.User{}, invalid("role")
	}
	u := store.User{Username: name, Email: strings.TrimSpace(in.Email), Role: role, DefaultNick: name, SSOClaimable: in.SSOClaimable}
	if in.Password != "" {
		if u.PasswordHash, err = HashPassword(in.Password); err != nil {
			return store.User{}, err
		}
		u.MustChangePassword = in.Temporary
	}
	if in.Subject != "" {
		sub := in.Subject
		u.OIDCSubject = &sub
	}
	return s.users.CreateUser(context.Background(), u)
}

// SetPassword replaces a user's local password (and clears a pending
// must-change).
func (s *Service) SetPassword(u *store.User, pw string) error {
	h, err := HashPassword(pw)
	if err != nil {
		return err
	}
	u.PasswordHash, u.MustChangePassword = h, false
	return s.users.UpdateUser(context.Background(), *u)
}

// SetTemporaryPassword gives a user a password they must change at their
// next web sign-in. An empty pw generates one, which is returned.
func (s *Service) SetTemporaryPassword(u *store.User, pw string) (string, error) {
	if pw == "" {
		var err error
		if pw, err = RandomPassword(); err != nil {
			return "", err
		}
	}
	if err := s.SetPassword(u, pw); err != nil {
		return "", err
	}
	u.MustChangePassword = true
	return pw, s.users.UpdateUser(context.Background(), *u)
}

// RandomPassword is a readable random password (xxxxx-xxxxx-xxxxx-xxxxx).
func RandomPassword() (string, error) {
	const chars = "abcdefghjkmnpqrstuvwxyz23456789"
	b := make([]byte, 20)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	var sb strings.Builder
	for i, v := range b {
		if i > 0 && i%5 == 0 {
			sb.WriteByte('-')
		}
		sb.WriteByte(chars[int(v)%len(chars)])
	}
	return sb.String(), nil
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
	u, err := s.users.UserByUsername(context.Background(), strings.ToLower(strings.TrimSpace(username)))
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
// local password, or one of their client passwords. It also returns the
// client password's name ("" for the account password).
func (s *Service) IRCLogin(username, secret string) (store.User, string, error) {
	u, err := s.users.UserByUsername(context.Background(), strings.ToLower(username))
	if err != nil {
		bcrypt.CompareHashAndPassword(dummyHash, []byte(secret))
		return store.User{}, "", ErrInvalidCredentials
	}
	device := ""
	ok := u.PasswordHash != "" && bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(secret)) == nil
	if !ok {
		cps, err := s.passwords.ListClientPasswords(context.Background(), u.ID)
		if err != nil {
			return store.User{}, "", err
		}
		for _, cp := range cps {
			if bcrypt.CompareHashAndPassword([]byte(cp.Hash), []byte(secret)) == nil {
				_ = s.passwords.TouchClientPassword(context.Background(), cp.ID, s.now())
				ok, device = true, cp.Name
				break
			}
		}
	}
	if !ok && u.PasswordHash == "" {
		return store.User{}, "", ErrNeedsClientPassword
	}
	if !ok {
		return store.User{}, "", ErrInvalidCredentials
	}
	if u.Suspended {
		return store.User{}, "", ErrSuspended
	}
	return u, device, nil
}

// NewClientPassword creates an IRC client password and returns its secret,
// which is shown once and never stored in the clear.
func (s *Service) NewClientPassword(userID int64, name string) (store.ClientPassword, string, error) {
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > 64 {
		return store.ClientPassword{}, "", invalid("clientPasswordName")
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
	cp, err := s.passwords.CreateClientPassword(context.Background(), store.ClientPassword{UserID: userID, Name: name, Hash: string(h)})
	return cp, secret, err
}

// CanCreateNetwork enforces the network limit.
func (s *Service) CanCreateNetwork(u store.User) error {
	max := s.LimitsFor(u).MaxNetworks
	if max == 0 {
		return nil
	}
	n, err := s.CountNetworks(context.Background(), u.ID)
	if err != nil {
		return err
	}
	if n >= int64(max) {
		return invalid("networkLimit", "count", max)
	}
	return nil
}

// GuardAdminChange refuses a change that would leave no admin: demoting,
// suspending or deleting the only one.
func (s *Service) GuardAdminChange(before store.User, stillAdmin bool) error {
	if !before.IsAdmin() || stillAdmin {
		return nil
	}
	n, err := s.users.CountAdmins(context.Background())
	if err != nil {
		return err
	}
	if n <= 1 {
		return ErrLastAdmin
	}
	return nil
}

// HostAllowed reports whether a client at ip may sign in as u: anywhere when
// the user's allowed list is empty, else an entry must match — an IP, a CIDR
// range, or a wildcard such as 192.168.* or 2001:db8:*.
func HostAllowed(u store.User, ip string) bool {
	if len(u.AllowedHosts) == 0 {
		return true
	}
	addr, err := netip.ParseAddr(ip)
	if err == nil {
		addr = addr.Unmap()
		ip = addr.String()
	}
	for _, h := range u.AllowedHosts {
		h = strings.TrimSpace(h)
		if p, err := netip.ParsePrefix(h); err == nil && addr.IsValid() {
			if p.Contains(addr) {
				return true
			}
			continue
		}
		if ok, _ := path.Match(strings.ToLower(h), strings.ToLower(ip)); ok {
			return true
		}
	}
	return false
}

// NormalizeAllowedHosts checks an allowed-hosts list.
func NormalizeAllowedHosts(hosts []string) ([]string, error) {
	out := []string{}
	for _, h := range hosts {
		if h = strings.TrimSpace(h); h == "" {
			continue
		}
		if _, err := netip.ParsePrefix(h); err == nil {
			out = append(out, h)
			continue
		}
		if _, err := netip.ParseAddr(h); err == nil {
			out = append(out, h)
			continue
		}
		if strings.Trim(h, "0123456789abcdefABCDEF.:*?[]-") != "" || !strings.ContainsAny(h, "*?") {
			return nil, invalid("allowedHost", "host", strconv.Quote(h))
		}
		out = append(out, h)
	}
	return out, nil
}
