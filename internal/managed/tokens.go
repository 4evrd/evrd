package managed

// Access tokens: an IRC client in managed mode signs in with the access
// token the managing service issued it (a JWT from the OIDC issuer), by
// SASL OAUTHBEARER or IRCV3BEARER, or as the password. The token is
// checked against the issuer's published keys; its "sub" is the account's
// OIDC subject.

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
)

// ErrBadToken is a token that doesn't hold: badly signed, expired, for
// another audience or issuer.
var ErrBadToken = errors.New("invalid access token")

// ErrTokensUnavailable is the issuer's keys being out of reach.
var ErrTokensUnavailable = errors.New("can't check access tokens right now")

// Claims is what evrd reads from a token.
type Claims struct {
	Subject string
	// Name is the user's name at the issuer ("name", else
	// "preferred_username").
	Name string
}

// signingAlgs are the signatures accepted: Boson signs with EdDSA, most
// providers with RS256 or ES256.
var signingAlgs = []string{oidc.RS256, oidc.RS384, oidc.RS512, oidc.ES256, oidc.ES384, oidc.ES512, oidc.PS256, oidc.PS384, oidc.PS512, oidc.EdDSA}

// Verifier checks access tokens from one issuer.
type Verifier struct {
	issuer, audience, jwks string

	mu sync.Mutex
	v  *oidc.IDTokenVerifier
	// ctx is the context keys are fetched in (tests give it their client).
	ctx context.Context
}

// NewVerifier checks tokens from issuer with audience ("*": any), reading
// the keys from jwks, or (jwks "") from the issuer's discovery document.
// Nothing is fetched until the first token comes.
func NewVerifier(issuer, audience, jwks string) *Verifier {
	return &Verifier{issuer: issuer, audience: audience, jwks: jwks, ctx: context.Background()}
}

// verifier is the token verifier, set up on first use (discovery may fail
// while the issuer is down: it's tried again with the next token).
func (v *Verifier) verifier() (*oidc.IDTokenVerifier, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.v != nil {
		return v.v, nil
	}
	conf := &oidc.Config{ClientID: v.audience, SkipClientIDCheck: v.audience == "*", SupportedSigningAlgs: signingAlgs}
	jwks := v.jwks
	if jwks == "" {
		dctx, cancel := context.WithTimeout(v.ctx, 10*time.Second)
		defer cancel()
		p, err := oidc.NewProvider(dctx, v.issuer)
		if err != nil {
			return nil, errors.Join(ErrTokensUnavailable, err)
		}
		var meta struct {
			JWKS string `json:"jwks_uri"`
		}
		if err := p.Claims(&meta); err != nil || meta.JWKS == "" {
			return nil, errors.Join(ErrTokensUnavailable, errors.New("the issuer publishes no jwks_uri"))
		}
		jwks = meta.JWKS
	}
	v.v = oidc.NewVerifier(v.issuer, oidc.NewRemoteKeySet(v.ctx, jwks), conf)
	return v.v, nil
}

// Verify checks token and returns its claims.
func (v *Verifier) Verify(ctx context.Context, token string) (Claims, error) {
	ver, err := v.verifier()
	if err != nil {
		return Claims{}, err
	}
	t, err := ver.Verify(ctx, token)
	if err != nil {
		if strings.Contains(err.Error(), "fetching keys") {
			return Claims{}, errors.Join(ErrTokensUnavailable, err)
		}
		return Claims{}, errors.Join(ErrBadToken, err)
	}
	var c struct {
		Name              string `json:"name"`
		PreferredUsername string `json:"preferred_username"`
	}
	_ = t.Claims(&c)
	name := c.Name
	if name == "" {
		name = c.PreferredUsername
	}
	if t.Subject == "" {
		return Claims{}, errors.Join(ErrBadToken, errors.New("no subject"))
	}
	return Claims{Subject: t.Subject, Name: name}, nil
}

// LooksLikeToken reports whether s has a JWT's shape (three base64url
// parts, a JSON header), so a password isn't sent off to be checked as one.
func LooksLikeToken(s string) bool {
	parts := strings.Split(s, ".")
	if len(parts) != 3 || !strings.HasPrefix(s, "eyJ") || len(s) < 32 {
		return false
	}
	for _, p := range parts {
		if p == "" || strings.Trim(p, "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_") != "" {
			return false
		}
	}
	return true
}
