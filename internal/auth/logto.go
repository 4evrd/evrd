package auth

// sso_irc_password logto: Logto has no password grant, so evrd asks its
// Management API, as a machine-to-machine app, whether the password is
// right.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Logto checks passwords with Logto's Management API, as a
// machine-to-machine app with access to it.
type Logto struct {
	base     string // https://id.example.org (the issuer without /oidc)
	id       string
	secret   string
	resource string
	client   *http.Client

	mu      sync.Mutex
	token   string
	expires time.Time
	now     func() time.Time
}

// NewLogto is the Management API of the Logto at issuer. resource is the
// API's indicator; empty takes Logto Cloud's (https://<tenant>.logto.app/api)
// for a *.logto.app issuer and the open-source default otherwise.
func NewLogto(issuer, clientID, secret, resource string, hc *http.Client) *Logto {
	if hc == nil {
		hc = &http.Client{Timeout: 10 * time.Second}
	}
	base := strings.TrimSuffix(strings.TrimRight(issuer, "/"), "/oidc")
	return &Logto{base: base, id: clientID, secret: secret, resource: orDefault(resource, logtoResource(base)), client: hc, now: time.Now}
}

// logtoResource is the Management API's indicator for a Logto at base.
func logtoResource(base string) string {
	if u, err := url.Parse(base); err == nil {
		if tenant, ok := strings.CutSuffix(u.Hostname(), ".logto.app"); ok && tenant != "" && !strings.Contains(tenant, ".") {
			return "https://" + tenant + ".logto.app/api"
		}
	}
	return "https://default.logto.app/api"
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

func (l *Logto) VerifyPassword(ctx context.Context, who Login, password string) error {
	subject := who.Subject
	var user struct {
		IsSuspended bool `json:"isSuspended"`
		HasPassword bool `json:"hasPassword"`
	}
	code, err := l.call(ctx, http.MethodGet, "/api/users/"+url.PathEscape(subject), nil, &user)
	switch {
	case err != nil:
		return err
	case code == http.StatusNotFound:
		return fmt.Errorf("%w (no such user in the provider)", ErrSSOWrongPassword)
	case code != http.StatusOK:
		return fmt.Errorf("%w: user lookup answered %d", ErrSSOUnavailable, code)
	case user.IsSuspended:
		return ErrSSOSuspended
	case !user.HasPassword:
		return ErrSSONoPassword
	}
	code, err = l.call(ctx, http.MethodPost, "/api/users/"+url.PathEscape(subject)+"/password/verify", map[string]string{"password": password}, nil)
	switch {
	case err != nil:
		return err
	case code == http.StatusNoContent || code == http.StatusOK:
		return nil
	case code == http.StatusUnprocessableEntity || code == http.StatusBadRequest:
		return ErrSSOWrongPassword
	default:
		return fmt.Errorf("%w: password check answered %d", ErrSSOUnavailable, code)
	}
}

// call makes a Management API request, getting a token first when needed
// (and again once, should the API refuse the one it had).
func (l *Logto) call(ctx context.Context, method, path string, body, out any) (int, error) {
	for try := 0; ; try++ {
		tok, err := l.accessToken(ctx)
		if err != nil {
			return 0, err
		}
		var rd io.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			rd = bytes.NewReader(b)
		}
		req, err := http.NewRequestWithContext(ctx, method, l.base+path, rd)
		if err != nil {
			return 0, fmt.Errorf("%w: %v", ErrSSOUnavailable, err)
		}
		req.Header.Set("Authorization", "Bearer "+tok)
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := l.client.Do(req)
		if err != nil {
			return 0, fmt.Errorf("%w: %v", ErrSSOUnavailable, err)
		}
		if resp.StatusCode == http.StatusUnauthorized && try == 0 {
			resp.Body.Close()
			l.mu.Lock()
			l.token = ""
			l.mu.Unlock()
			continue
		}
		defer resp.Body.Close()
		if out != nil && resp.StatusCode == http.StatusOK {
			if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(out); err != nil {
				return 0, fmt.Errorf("%w: %v", ErrSSOUnavailable, err)
			}
		}
		return resp.StatusCode, nil
	}
}

// accessToken is a Management API token (client credentials), kept until
// shortly before it expires.
func (l *Logto) accessToken(ctx context.Context) (string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.token != "" && l.now().Before(l.expires) {
		return l.token, nil
	}
	form := url.Values{"grant_type": {"client_credentials"}, "resource": {l.resource}, "scope": {"all"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, l.base+"/oidc/token", strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrSSOUnavailable, err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(url.QueryEscape(l.id), url.QueryEscape(l.secret))
	resp, err := l.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrSSOUnavailable, err)
	}
	defer resp.Body.Close()
	var t struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%w: the token endpoint answered %d (check logto_m2m_client_id, its secret and logto_m2m_resource)", ErrSSOUnavailable, resp.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&t); err != nil || t.AccessToken == "" {
		return "", fmt.Errorf("%w: no access token in the answer", ErrSSOUnavailable)
	}
	life := time.Duration(t.ExpiresIn) * time.Second
	if life <= 0 {
		life = time.Hour
	}
	l.token, l.expires = t.AccessToken, l.now().Add(life-min(life/10, 5*time.Minute))
	return l.token, nil
}
