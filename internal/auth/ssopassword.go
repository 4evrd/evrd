package auth

// SSO passwords over IRC (#91): an IRC client can't go through the
// provider's sign-in page, and OpenID Connect itself has no way for a server
// to check a user's password, so evrd checks it some other way, chosen by
// sso_irc_password:
//
//	password_grant  OAuth2's resource-owner password grant at the OIDC
//	                provider (Keycloak, Okta, Auth0, Entra ID, Dex…)
//	ldap            a bind to the directory as the user (OpenLDAP, Active
//	                Directory, FreeIPA, LLDAP, an authentik LDAP outpost…)
//	logto           Logto's Management API
//
// Each is a PasswordVerifier; a CachedVerifier in front remembers right
// passwords for a while.

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/4evrd/evrd/internal/config"
	"github.com/4evrd/evrd/internal/store"
)

// Why an SSO password was refused.
var (
	ErrSSOWrongPassword = errors.New("wrong SSO password")
	ErrSSONoPassword    = errors.New("the SSO account has no password")
	ErrSSOSuspended     = errors.New("the SSO account is suspended or disabled")
	ErrSSOUnavailable   = errors.New("the identity provider can't check the password right now")
	ErrSSONotLinked     = errors.New("the account isn't linked to an SSO user")
)

// Login is who a password is checked for: the account's SSO subject, its
// evrd user name and email. Each method uses what its provider knows the
// user by.
type Login struct {
	Subject  string
	Username string
	Email    string
}

// PasswordVerifier checks an SSO user's password with their identity
// provider: nil when it's right, else one of the ErrSSO* errors (wrapped).
type PasswordVerifier interface {
	VerifyPassword(ctx context.Context, who Login, password string) error
}

// SSOPasswordServiceImpl is IRC sign-in with the SSO password.
type SSOPasswordServiceImpl interface {
	// SSOPasswordsOn reports whether SSO accounts may use their provider
	// password in IRC clients (sso_irc_password, set up).
	SSOPasswordsOn() bool
	// SSOPasswordMethod is how they're checked: password_grant, ldap or
	// logto ("" when off).
	SSOPasswordMethod() string
	// CheckSSOPassword checks u's SSO password.
	CheckSSOPassword(ctx context.Context, u store.User, password string) error
}

// ssoPasswords is SSOPasswordServiceImpl over a verifier that follows the
// settings.
type ssoPasswords struct {
	mu       sync.Mutex
	method   string // "" when off
	key      string // the settings the verifier was built from
	verifier PasswordVerifier
	client   *http.Client
	provider *oidc.Provider // the OIDC provider (password_grant), once discovered
}

// newSSOPasswords is the SSO password check for cfg.
func newSSOPasswords(cfg config.Auth, hc *http.Client) *ssoPasswords {
	s := &ssoPasswords{client: hc}
	s.apply(cfg)
	return s
}

// setProvider gives password_grant the discovered OIDC provider.
func (s *ssoPasswords) setProvider(p *oidc.Provider, cfg config.Auth) {
	s.mu.Lock()
	s.provider, s.key = p, ""
	s.mu.Unlock()
	s.apply(cfg)
}

// apply follows changed settings: a new verifier (and an empty cache) when
// the method or its settings changed.
func (s *ssoPasswords) apply(cfg config.Auth) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !cfg.SSOPasswordsOverIRC() || cfg.SSOIRCPassword == "password_grant" && s.provider == nil {
		s.method, s.key, s.verifier = "", "", nil
		return
	}
	a := cfg
	key := strings.Join([]string{a.SSOIRCPassword, a.OIDCIssuer, a.OIDCClientID, a.OIDCClientSecret,
		a.SSOPasswordClientID, a.SSOPasswordClientSecret, a.SSOPasswordLogin,
		a.LDAPURL, fmtBool(a.LDAPInsecure), a.LDAPUserDN, a.LDAPBindDN, a.LDAPBindPassword, a.LDAPBaseDN, a.LDAPUserFilter, a.LDAPLogin,
		a.LogtoM2MClientID, a.LogtoM2MClientSecret, a.LogtoM2MResource}, "\x00")
	if key == s.key && s.verifier != nil {
		return
	}
	var v PasswordVerifier
	switch cfg.SSOIRCPassword {
	case "password_grant":
		id, secret := a.SSOPasswordClientID, a.SSOPasswordClientSecret
		if id == "" {
			id, secret = a.OIDCClientID, a.OIDCClientSecret
		}
		v = NewPasswordGrant(s.provider.Endpoint().TokenURL, id, secret, a.SSOPasswordLogin,
			s.provider.Verifier(&oidc.Config{ClientID: id}), s.client)
	case "ldap":
		v = NewLDAP(LDAPConfig{URL: a.LDAPURL, Insecure: a.LDAPInsecure, UserDN: a.LDAPUserDN, BindDN: a.LDAPBindDN,
			BindPassword: a.LDAPBindPassword, BaseDN: a.LDAPBaseDN, Filter: a.LDAPUserFilter, Login: a.LDAPLogin})
	case "logto":
		v = NewLogto(a.OIDCIssuer, a.LogtoM2MClientID, a.LogtoM2MClientSecret, a.LogtoM2MResource, s.client)
	}
	s.method, s.key = cfg.SSOIRCPassword, key
	s.verifier = NewCachedVerifier(v, ssoCacheFor)
}

func fmtBool(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

func (s *ssoPasswords) SSOPasswordsOn() bool { return s.SSOPasswordMethod() != "" }

func (s *ssoPasswords) SSOPasswordMethod() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.method
}

func (s *ssoPasswords) CheckSSOPassword(ctx context.Context, u store.User, password string) error {
	ctx, span := tracer.Start(ctx, "SSOPasswordService.CheckSSOPassword", trace.WithAttributes(attribute.Int64("user.id", u.ID)))
	defer span.End()
	s.mu.Lock()
	v, method := s.verifier, s.method
	s.mu.Unlock()
	span.SetAttributes(attribute.String("sso.method", method))
	if v == nil || u.OIDCSubject == nil || *u.OIDCSubject == "" {
		return ErrSSONotLinked
	}
	return v.VerifyPassword(ctx, Login{Subject: *u.OIDCSubject, Username: u.Username, Email: u.Email}, password)
}

// --- the success cache -------------------------------------------------------

// ssoCacheFor is how long a right SSO password is taken without asking the
// provider again, so a client reconnecting doesn't ask it each time.
const ssoCacheFor = 10 * time.Minute

// maxSSOCache bounds the cache; past it, the oldest half is forgotten.
const maxSSOCache = 10000

// CachedVerifier remembers right passwords for a while. Only a keyed hash
// of the user and password is kept, and never a refusal: a wrong password
// is always asked about (and counts toward the lockout).
type CachedVerifier struct {
	next PasswordVerifier
	ttl  time.Duration
	key  []byte // HMAC key, new each run
	now  func() time.Time

	mu   sync.Mutex
	good map[string]time.Time // HMAC(user, password) -> until
}

// NewCachedVerifier caches next's successes for ttl.
func NewCachedVerifier(next PasswordVerifier, ttl time.Duration) *CachedVerifier {
	k := make([]byte, 32)
	_, _ = rand.Read(k)
	return &CachedVerifier{next: next, ttl: ttl, key: k, now: time.Now, good: map[string]time.Time{}}
}

func (c *CachedVerifier) VerifyPassword(ctx context.Context, who Login, password string) error {
	m := hmac.New(sha256.New, c.key)
	m.Write([]byte(who.Subject + "\x00" + who.Username + "\x00" + who.Email + "\x00" + password))
	id := string(m.Sum(nil))
	now := c.now()
	c.mu.Lock()
	until, ok := c.good[id]
	c.mu.Unlock()
	if ok && now.Before(until) {
		return nil
	}
	if err := c.next.VerifyPassword(ctx, who, password); err != nil {
		c.mu.Lock()
		delete(c.good, id)
		c.mu.Unlock()
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.good) >= maxSSOCache {
		for k, t := range c.good {
			if !now.Before(t) || len(c.good) >= maxSSOCache/2 {
				delete(c.good, k)
			}
		}
	}
	c.good[id] = now.Add(c.ttl)
	return nil
}
