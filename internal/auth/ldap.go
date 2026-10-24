package auth

// sso_irc_password ldap: the password is right when the directory lets the
// user bind with it. The user's DN comes from a template
// (uid={username},ou=people,…) or a search as a service account. TLS is
// required: ldaps://, or ldap:// with StartTLS, unless ldap_insecure.

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/go-ldap/ldap/v3"
)

// LDAPConfig is how to find and bind a user (the ldap_* settings).
type LDAPConfig struct {
	URL          string
	Insecure     bool // ldap:// without StartTLS
	UserDN       string
	BindDN       string
	BindPassword string
	BaseDN       string
	Filter       string // with {username}
	Login        string // "username" or "subject": what {username} is
	Timeout      time.Duration
}

// ldapConn is the part of *ldap.Conn evrd uses (tests fake it).
type ldapConn interface {
	StartTLS(*tls.Config) error
	Bind(username, password string) error
	Search(*ldap.SearchRequest) (*ldap.SearchResult, error)
	Close() error
}

// LDAP checks passwords by binding as the user.
type LDAP struct {
	cfg  LDAPConfig
	dial func(ctx context.Context, rawURL string, timeout time.Duration) (ldapConn, error)
}

// NewLDAP checks passwords against the directory cfg describes.
func NewLDAP(cfg LDAPConfig) *LDAP {
	if cfg.Timeout == 0 {
		cfg.Timeout = 10 * time.Second
	}
	if cfg.Filter == "" {
		cfg.Filter = "(uid={username})"
	}
	return &LDAP{cfg: cfg, dial: dialLDAP}
}

func dialLDAP(ctx context.Context, rawURL string, timeout time.Duration) (ldapConn, error) {
	d := &net.Dialer{Timeout: timeout}
	c, err := ldap.DialURL(rawURL, ldap.DialWithDialer(d))
	if err != nil {
		return nil, err
	}
	c.SetTimeout(timeout)
	if dl, ok := ctx.Deadline(); ok && time.Until(dl) < timeout {
		c.SetTimeout(time.Until(dl))
	}
	return c, nil
}

func (l *LDAP) VerifyPassword(ctx context.Context, who Login, password string) error {
	if password == "" {
		// an empty password is an unauthenticated bind, which succeeds
		return ErrSSOWrongPassword
	}
	name := who.Username
	if l.cfg.Login == "subject" {
		name = who.Subject
	}
	u, err := url.Parse(l.cfg.URL)
	if err != nil || (u.Scheme != "ldap" && u.Scheme != "ldaps") {
		return fmt.Errorf("%w: ldap_url isn't an ldap:// or ldaps:// URL", ErrSSOUnavailable)
	}
	c, err := l.dial(ctx, l.cfg.URL, l.cfg.Timeout)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrSSOUnavailable, err)
	}
	defer c.Close()
	if u.Scheme == "ldap" && !l.cfg.Insecure {
		if err := c.StartTLS(&tls.Config{ServerName: u.Hostname(), MinVersion: tls.VersionTLS12}); err != nil {
			return fmt.Errorf("%w: StartTLS: %v (set ldap_insecure only on a trusted network)", ErrSSOUnavailable, err)
		}
	}
	dn := ""
	if l.cfg.UserDN != "" {
		dn = strings.ReplaceAll(l.cfg.UserDN, "{username}", ldap.EscapeDN(name))
	} else {
		if dn, err = l.find(c, name); err != nil {
			return err
		}
	}
	if err := c.Bind(dn, password); err != nil {
		return bindError(err)
	}
	return nil
}

// find is the DN of the user whose filter matches name: a search as the
// bind DN (or anonymous), which must find exactly one.
func (l *LDAP) find(c ldapConn, name string) (string, error) {
	if l.cfg.BindDN != "" {
		if err := c.Bind(l.cfg.BindDN, l.cfg.BindPassword); err != nil {
			return "", fmt.Errorf("%w: the search bind (ldap_bind_dn) failed: %v", ErrSSOUnavailable, err)
		}
	}
	filter := strings.ReplaceAll(l.cfg.Filter, "{username}", ldap.EscapeFilter(name))
	res, err := c.Search(ldap.NewSearchRequest(l.cfg.BaseDN, ldap.ScopeWholeSubtree, ldap.NeverDerefAliases,
		2, int(l.cfg.Timeout/time.Second), false, filter, []string{"dn"}, nil))
	switch {
	case err != nil && ldap.IsErrorWithCode(err, ldap.LDAPResultSizeLimitExceeded):
		return "", fmt.Errorf("%w: ldap_user_filter matches more than one entry", ErrSSOUnavailable)
	case err != nil:
		return "", fmt.Errorf("%w: search: %v", ErrSSOUnavailable, err)
	case len(res.Entries) == 0:
		return "", fmt.Errorf("%w (no such user in the directory)", ErrSSOWrongPassword)
	case len(res.Entries) > 1:
		return "", fmt.Errorf("%w: ldap_user_filter matches more than one entry", ErrSSOUnavailable)
	}
	return res.Entries[0].DN, nil
}

// bindError is what a refused bind means. Active Directory says why in its
// diagnostic ("data 533": disabled, 775: locked out).
func bindError(err error) error {
	var le *ldap.Error
	if !errors.As(err, &le) {
		return fmt.Errorf("%w: %v", ErrSSOUnavailable, err)
	}
	msg := ""
	if le.Err != nil {
		msg = le.Err.Error()
	}
	switch {
	case le.ResultCode == ldap.LDAPResultInvalidCredentials && (strings.Contains(msg, "data 533") || strings.Contains(msg, "data 775")):
		return ErrSSOSuspended
	case le.ResultCode == ldap.LDAPResultInvalidCredentials, le.ResultCode == ldap.LDAPResultNoSuchObject:
		return ErrSSOWrongPassword
	case le.ResultCode == ldap.LDAPResultUnwillingToPerform || le.ResultCode == ldap.LDAPResultInappropriateAuthentication:
		return fmt.Errorf("%w: %v", ErrSSOSuspended, err)
	default:
		return fmt.Errorf("%w: %v", ErrSSOUnavailable, err)
	}
}
