package auth

// sso_irc_password password_grant: OAuth2's resource-owner password grant
// (RFC 6749 §4.3) at the OIDC provider's token endpoint, as the sign-in
// client (or one of its own allowed the grant). The answer's ID token has
// to be the account's: same subject as the one it's linked to.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
)

// PasswordGrant checks passwords with the password grant.
type PasswordGrant struct {
	tokenURL string
	id       string
	secret   string
	login    string // "username" or "email": what the provider knows the user by
	verifier *oidc.IDTokenVerifier
	client   *http.Client
}

// NewPasswordGrant asks tokenURL as client id/secret, sending the user's
// login ("username" or "email"); verifier checks the ID token it answers
// with.
func NewPasswordGrant(tokenURL, id, secret, login string, verifier *oidc.IDTokenVerifier, hc *http.Client) *PasswordGrant {
	if hc == nil {
		hc = &http.Client{Timeout: 10 * time.Second}
	}
	return &PasswordGrant{tokenURL: tokenURL, id: id, secret: secret, login: login, verifier: verifier, client: hc}
}

func (g *PasswordGrant) VerifyPassword(ctx context.Context, who Login, password string) error {
	name := who.Username
	if g.login == "email" {
		if who.Email == "" {
			return fmt.Errorf("%w (the account has no email to sign in with)", ErrSSOWrongPassword)
		}
		name = who.Email
	}
	// the client's credentials go in the body (client_secret_post): every
	// provider takes that form, Entra ID only that one
	form := url.Values{"grant_type": {"password"}, "username": {name}, "password": {password}, "scope": {"openid"}, "client_id": {g.id}}
	if g.secret != "" {
		form.Set("client_secret", g.secret)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("%w: %v", ErrSSOUnavailable, err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := g.client.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrSSOUnavailable, err)
	}
	defer resp.Body.Close()
	var out struct {
		IDToken     string `json:"id_token"`
		Error       string `json:"error"`
		Description string `json:"error_description"`
	}
	_ = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out)
	if resp.StatusCode != http.StatusOK {
		return grantError(resp.StatusCode, out.Error, out.Description)
	}
	if out.IDToken == "" {
		return fmt.Errorf("%w: no ID token in the answer (is the openid scope allowed for this client?)", ErrSSOUnavailable)
	}
	tok, err := g.verifier.Verify(ctx, out.IDToken)
	if err != nil {
		return fmt.Errorf("%w: the ID token didn't verify: %v", ErrSSOUnavailable, err)
	}
	if tok.Subject != who.Subject {
		// the password is someone's, but not this account's provider user
		return fmt.Errorf("%w (the provider answered for another user)", ErrSSOWrongPassword)
	}
	return nil
}

// grantError is what a refused password grant means.
func grantError(status int, code, description string) error {
	d := strings.ToLower(description)
	switch {
	case code == "invalid_grant" && (strings.Contains(d, "disabled") || strings.Contains(d, "locked") || strings.Contains(d, "suspended") || strings.Contains(d, "blocked")):
		return fmt.Errorf("%w: %s", ErrSSOSuspended, description)
	case code == "invalid_grant" && (strings.Contains(d, "not fully set up") || strings.Contains(d, "no password")):
		return fmt.Errorf("%w: %s", ErrSSONoPassword, description)
	case code == "invalid_grant" || code == "invalid_user" || code == "access_denied" && status == http.StatusForbidden:
		return ErrSSOWrongPassword
	case code == "unsupported_grant_type" || code == "unauthorized_client" || code == "invalid_client" || code == "invalid_scope":
		return fmt.Errorf("%w: the provider refused the password grant (%s: %s) — allow it for this client (sso_password_client_id)", ErrSSOUnavailable, code, description)
	default:
		return fmt.Errorf("%w: the token endpoint answered %d %s", ErrSSOUnavailable, status, code)
	}
}
