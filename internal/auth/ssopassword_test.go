package auth

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-ldap/ldap/v3"

	"github.com/4evrd/evrd/internal/config"
	"github.com/4evrd/evrd/internal/store"
)

// --- logto ---------------------------------------------------------------

// fakeLogto is a Logto Management API with one user per id.
type fakeLogto struct {
	srv      *httptest.Server
	tokens   atomic.Int32 // token requests
	verifies atomic.Int32 // password checks
	refuse   atomic.Bool  // answer 401 to the next API call
	down     atomic.Bool  // answer 500
	users    map[string]fakeLogtoUser
}

type fakeLogtoUser struct {
	password  string
	suspended bool
}

func newFakeLogto(t *testing.T) *fakeLogto {
	f := &fakeLogto{users: map[string]fakeLogtoUser{}}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if f.down.Load() {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		if r.URL.Path == "/oidc/token" {
			id, secret, ok := r.BasicAuth()
			r.ParseForm()
			if !ok || id != "m2m-id" || secret != "m2m-test-secret" || r.Form.Get("grant_type") != "client_credentials" || r.Form.Get("resource") != "https://default.logto.app/api" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			f.tokens.Add(1)
			json.NewEncoder(w).Encode(map[string]any{"access_token": "tok", "expires_in": 3600})
			return
		}
		if r.Header.Get("Authorization") != "Bearer tok" || f.refuse.Swap(false) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		rest, ok := strings.CutPrefix(r.URL.Path, "/api/users/")
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		id, verify := strings.CutSuffix(rest, "/password/verify")
		u, known := f.users[id]
		switch {
		case !known:
			w.WriteHeader(http.StatusNotFound)
		case verify:
			f.verifies.Add(1)
			var in struct{ Password string }
			json.NewDecoder(r.Body).Decode(&in)
			if in.Password != u.password {
				w.WriteHeader(http.StatusUnprocessableEntity)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			json.NewEncoder(w).Encode(map[string]any{"id": id, "isSuspended": u.suspended, "hasPassword": u.password != ""})
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeLogto) verifier() *Logto {
	return NewLogto(f.srv.URL+"/oidc", "m2m-id", "m2m-test-secret", "", f.srv.Client())
}

func sub(s string) Login { return Login{Subject: s} }

func TestLogtoVerifyPassword(t *testing.T) {
	f := newFakeLogto(t)
	f.users["alice"] = fakeLogtoUser{password: "right horse"}
	f.users["bob"] = fakeLogtoUser{password: "pw", suspended: true}
	f.users["carol"] = fakeLogtoUser{} // social sign-in only
	l := f.verifier()
	ctx := context.Background()
	for _, c := range []struct {
		sub, pw string
		want    error
	}{
		{"alice", "right horse", nil},
		{"alice", "wrong", ErrSSOWrongPassword},
		{"bob", "pw", ErrSSOSuspended},
		{"carol", "x", ErrSSONoPassword},
		{"nobody", "x", ErrSSOWrongPassword},
	} {
		if err := l.VerifyPassword(ctx, sub(c.sub), c.pw); !errors.Is(err, c.want) || (c.want == nil) != (err == nil) {
			t.Errorf("%s/%s: %v, want %v", c.sub, c.pw, err, c.want)
		}
	}
	if n := f.tokens.Load(); n != 1 {
		t.Errorf("token fetched %d times, want once (kept until it expires)", n)
	}

	// a refused token is fetched again, once
	f.refuse.Store(true)
	if err := l.VerifyPassword(ctx, sub("alice"), "right horse"); err != nil || f.tokens.Load() != 2 {
		t.Errorf("after a 401: %v, %d tokens", err, f.tokens.Load())
	}
	// an expired token too
	l.now = func() time.Time { return time.Now().Add(2 * time.Hour) }
	if err := l.VerifyPassword(ctx, sub("alice"), "right horse"); err != nil || f.tokens.Load() != 3 {
		t.Errorf("after expiry: %v, %d tokens", err, f.tokens.Load())
	}

	f.down.Store(true)
	if err := l.VerifyPassword(ctx, sub("alice"), "right horse"); !errors.Is(err, ErrSSOUnavailable) {
		t.Errorf("provider down: %v", err)
	}
	f.down.Store(false)
	bad := NewLogto(f.srv.URL+"/oidc", "m2m-id", "not-the-secret", "", f.srv.Client())
	if err := bad.VerifyPassword(ctx, sub("alice"), "right horse"); !errors.Is(err, ErrSSOUnavailable) || !strings.Contains(err.Error(), "logto_m2m_client_id") {
		t.Errorf("wrong app secret: %v", err)
	}
	gone := NewLogto("http://127.0.0.1:1/oidc", "m2m-id", "m2m-test-secret", "", nil)
	if err := gone.VerifyPassword(ctx, sub("alice"), "x"); !errors.Is(err, ErrSSOUnavailable) {
		t.Errorf("unreachable: %v", err)
	}
}

func TestLogtoResource(t *testing.T) {
	for issuer, want := range map[string]string{
		"https://abc123.logto.app/oidc": "https://abc123.logto.app/api",
		"https://id.4evr.sh/oidc":       "https://default.logto.app/api",
		"https://x.y.logto.app/oidc":    "https://default.logto.app/api",
	} {
		if got := NewLogto(issuer, "", "", "", nil).resource; got != want {
			t.Errorf("%s: %s, want %s", issuer, got, want)
		}
	}
	if got := NewLogto("https://id.example.org/oidc", "", "", "https://custom/api", nil); got.resource != "https://custom/api" || got.base != "https://id.example.org" {
		t.Errorf("set: %+v", got)
	}
}

// --- password grant --------------------------------------------------------

func TestPasswordGrant(t *testing.T) {
	p := newFakeIdP(t)
	p.passwords["alice"] = pwUser{password: "alices-pw", sub: "sub-alice"}
	p.passwords["alice@example.org"] = pwUser{password: "alices-pw", sub: "sub-alice"}
	p.passwords["mallory"] = pwUser{password: "mallorys-pw", sub: "sub-mallory"}
	p.passwords["dave"] = pwUser{error: "invalid_grant", describe: "Account disabled"}
	p.passwords["erin"] = pwUser{error: "invalid_grant", describe: "Account is not fully set up"}
	p.passwords["frank"] = pwUser{error: "unauthorized_client", describe: "Client not allowed for direct access grants"}
	m, _, _ := newOIDCManager(t, p, config.Auth{SSOIRCPassword: "password_grant"})
	if m.SSOPasswordMethod() != "password_grant" {
		t.Fatalf("method %q", m.SSOPasswordMethod())
	}
	ctx := context.Background()
	user := func(name, subject string) store.User {
		return store.User{Username: name, Email: name + "@example.org", OIDCSubject: &subject}
	}
	for _, c := range []struct {
		u    store.User
		pw   string
		want error
	}{
		{user("alice", "sub-alice"), "alices-pw", nil},
		{user("alice", "sub-alice"), "wrong", ErrSSOWrongPassword},
		// mallory's own password, but the account is linked to someone else
		{user("mallory", "sub-alice"), "mallorys-pw", ErrSSOWrongPassword},
		{user("dave", "sub-dave"), "x", ErrSSOSuspended},
		{user("erin", "sub-erin"), "x", ErrSSONoPassword},
		{user("frank", "sub-frank"), "x", ErrSSOUnavailable},
	} {
		if err := m.CheckSSOPassword(ctx, c.u, c.pw); !errors.Is(err, c.want) || (c.want == nil) != (err == nil) {
			t.Errorf("%s/%s: %v, want %v", c.u.Username, c.pw, err, c.want)
		}
	}

	// by email, with the client of its own
	a := m.conf()
	a.SSOPasswordLogin = "email"
	m.SetConfig(a)
	if err := m.CheckSSOPassword(ctx, user("alice", "sub-alice"), "alices-pw"); err != nil {
		t.Fatalf("by email: %v", err)
	}
	if err := m.CheckSSOPassword(ctx, store.User{Username: "x", OIDCSubject: &[]string{"s"}[0]}, "pw"); !errors.Is(err, ErrSSOWrongPassword) {
		t.Fatalf("no email: %v", err)
	}
	a.SSOPasswordClientID, a.SSOPasswordClientSecret = "other-client", "other-secret"
	m.SetConfig(a)
	if err := m.CheckSSOPassword(ctx, user("alice", "sub-alice"), "alices-pw"); !errors.Is(err, ErrSSOUnavailable) {
		t.Fatalf("a client the provider doesn't know: %v", err)
	}

	// the provider gone
	g := NewPasswordGrant("http://127.0.0.1:1/token", "c", "", "username", nil, nil)
	if err := g.VerifyPassword(ctx, Login{Username: "a"}, "x"); !errors.Is(err, ErrSSOUnavailable) {
		t.Fatalf("unreachable: %v", err)
	}
}

func TestGrantError(t *testing.T) {
	for _, c := range []struct {
		status    int
		code, why string
		want      error
	}{
		{400, "invalid_grant", "Invalid user credentials", ErrSSOWrongPassword},
		{400, "invalid_grant", "User is locked", ErrSSOSuspended},
		{400, "unsupported_grant_type", "", ErrSSOUnavailable},
		{401, "invalid_client", "", ErrSSOUnavailable},
		{403, "access_denied", "", ErrSSOWrongPassword},
		{500, "", "", ErrSSOUnavailable},
	} {
		if err := grantError(c.status, c.code, c.why); !errors.Is(err, c.want) {
			t.Errorf("%d %s %q: %v", c.status, c.code, c.why, err)
		}
	}
}

// --- ldap --------------------------------------------------------------------

// fakeDirectory is an LDAP server's answers: DN -> password, and the
// entries a search finds.
type fakeDirectory struct {
	passwords map[string]string
	disabled  map[string]bool
	found     []string // DNs the search answers with
	filters   []string // the filters searched for
	binds     []string // DNs bound as
	startTLS  int
	down      bool
}

func (d *fakeDirectory) dial(ctx context.Context, rawURL string, timeout time.Duration) (ldapConn, error) {
	if d.down {
		return nil, errors.New("connection refused")
	}
	return &fakeLDAPConn{d: d}, nil
}

type fakeLDAPConn struct{ d *fakeDirectory }

func (c *fakeLDAPConn) StartTLS(*tls.Config) error { c.d.startTLS++; return nil }
func (c *fakeLDAPConn) Close() error               { return nil }
func (c *fakeLDAPConn) Bind(dn, password string) error {
	c.d.binds = append(c.d.binds, dn)
	if c.d.disabled[dn] {
		return &ldap.Error{ResultCode: ldap.LDAPResultInvalidCredentials, Err: errors.New("80090308: LdapErr: DSID-0C09044E, comment: AcceptSecurityContext error, data 533, v4563")}
	}
	if pw, ok := c.d.passwords[dn]; !ok || pw != password {
		return &ldap.Error{ResultCode: ldap.LDAPResultInvalidCredentials, Err: errors.New("invalid credentials")}
	}
	return nil
}
func (c *fakeLDAPConn) Search(r *ldap.SearchRequest) (*ldap.SearchResult, error) {
	c.d.filters = append(c.d.filters, r.Filter)
	res := &ldap.SearchResult{}
	for _, dn := range c.d.found {
		res.Entries = append(res.Entries, &ldap.Entry{DN: dn})
	}
	return res, nil
}

func TestLDAPTemplate(t *testing.T) {
	d := &fakeDirectory{passwords: map[string]string{"uid=alice,ou=people,dc=example,dc=org": "alices-pw"},
		disabled: map[string]bool{"uid=dave,ou=people,dc=example,dc=org": true}}
	l := NewLDAP(LDAPConfig{URL: "ldap://ldap.example.org", UserDN: "uid={username},ou=people,dc=example,dc=org"})
	l.dial = d.dial
	ctx := context.Background()
	if err := l.VerifyPassword(ctx, Login{Username: "alice"}, "alices-pw"); err != nil || d.startTLS != 1 {
		t.Fatalf("right: %v (StartTLS %d)", err, d.startTLS)
	}
	for _, c := range []struct {
		name, pw string
		want     error
	}{
		{"alice", "wrong", ErrSSOWrongPassword},
		{"alice", "", ErrSSOWrongPassword}, // never an unauthenticated bind
		{"dave", "x", ErrSSOSuspended},
	} {
		if err := l.VerifyPassword(ctx, Login{Username: c.name}, c.pw); !errors.Is(err, c.want) {
			t.Errorf("%s/%q: %v", c.name, c.pw, err)
		}
	}
	// a name can't change the DN's shape
	l.VerifyPassword(ctx, Login{Username: "x,ou=admins"}, "pw")
	if last := d.binds[len(d.binds)-1]; last != `uid=x\,ou=admins,ou=people,dc=example,dc=org` {
		t.Fatalf("not escaped: %s", last)
	}
	// the subject instead of the user name
	l.cfg.Login = "subject"
	if err := l.VerifyPassword(ctx, Login{Username: "nope", Subject: "alice"}, "alices-pw"); err != nil {
		t.Fatalf("by subject: %v", err)
	}
	// ldaps:// and ldap_insecure: no StartTLS
	before := d.startTLS
	l.cfg.URL = "ldaps://ldap.example.org"
	l.VerifyPassword(ctx, Login{Subject: "alice"}, "alices-pw")
	l.cfg.URL, l.cfg.Insecure = "ldap://ldap.example.org", true
	l.VerifyPassword(ctx, Login{Subject: "alice"}, "alices-pw")
	if d.startTLS != before {
		t.Fatal("StartTLS on ldaps:// or with ldap_insecure")
	}
	l.cfg.URL = "http://nope"
	if err := l.VerifyPassword(ctx, Login{Subject: "alice"}, "x"); !errors.Is(err, ErrSSOUnavailable) {
		t.Fatalf("bad URL: %v", err)
	}
	l.cfg.URL, d.down = "ldaps://ldap.example.org", true
	if err := l.VerifyPassword(ctx, Login{Subject: "alice"}, "x"); !errors.Is(err, ErrSSOUnavailable) {
		t.Fatalf("down: %v", err)
	}
}

func TestLDAPSearch(t *testing.T) {
	d := &fakeDirectory{passwords: map[string]string{"cn=evrd,dc=example,dc=org": "svc-pw", "uid=alice,ou=people,dc=example,dc=org": "alices-pw"}}
	l := NewLDAP(LDAPConfig{URL: "ldaps://ldap.example.org", BindDN: "cn=evrd,dc=example,dc=org", BindPassword: "svc-pw",
		BaseDN: "ou=people,dc=example,dc=org", Filter: "(&(objectClass=person)(uid={username}))"})
	l.dial = d.dial
	ctx := context.Background()
	d.found = []string{"uid=alice,ou=people,dc=example,dc=org"}
	if err := l.VerifyPassword(ctx, Login{Username: "alice"}, "alices-pw"); err != nil {
		t.Fatalf("right: %v", err)
	}
	if d.binds[0] != "cn=evrd,dc=example,dc=org" || d.filters[0] != "(&(objectClass=person)(uid=alice))" {
		t.Fatalf("binds %v filters %v", d.binds, d.filters)
	}
	l.VerifyPassword(ctx, Login{Username: "*)(uid=*"}, "x")
	if f := d.filters[len(d.filters)-1]; f != `(&(objectClass=person)(uid=\2a\29\28uid=\2a))` {
		t.Fatalf("filter not escaped: %s", f)
	}
	d.found = nil
	if err := l.VerifyPassword(ctx, Login{Username: "nobody"}, "x"); !errors.Is(err, ErrSSOWrongPassword) {
		t.Fatalf("not found: %v", err)
	}
	d.found = []string{"uid=a,dc=x", "uid=b,dc=x"}
	if err := l.VerifyPassword(ctx, Login{Username: "a"}, "x"); !errors.Is(err, ErrSSOUnavailable) {
		t.Fatalf("two found: %v", err)
	}
	l.cfg.BindPassword = "wrong"
	if err := l.VerifyPassword(ctx, Login{Username: "alice"}, "alices-pw"); !errors.Is(err, ErrSSOUnavailable) || !strings.Contains(err.Error(), "ldap_bind_dn") {
		t.Fatalf("service bind refused: %v", err)
	}
}

func TestBindError(t *testing.T) {
	if err := bindError(errors.New("eof")); !errors.Is(err, ErrSSOUnavailable) {
		t.Errorf("not an LDAP error: %v", err)
	}
	if err := bindError(&ldap.Error{ResultCode: ldap.LDAPResultUnwillingToPerform, Err: errors.New("account locked")}); !errors.Is(err, ErrSSOSuspended) {
		t.Errorf("unwilling: %v", err)
	}
	if err := bindError(&ldap.Error{ResultCode: ldap.LDAPResultBusy, Err: errors.New("busy")}); !errors.Is(err, ErrSSOUnavailable) {
		t.Errorf("busy: %v", err)
	}
}

// --- the cache and the settings ---------------------------------------------

// countingVerifier says yes to "good" and counts what it's asked.
type countingVerifier struct{ asked int }

func (c *countingVerifier) VerifyPassword(_ context.Context, _ Login, password string) error {
	c.asked++
	if password == "good" {
		return nil
	}
	return ErrSSOWrongPassword
}

func TestCachedVerifier(t *testing.T) {
	next := &countingVerifier{}
	c := NewCachedVerifier(next, time.Minute)
	now := time.Unix(1000, 0)
	c.now = func() time.Time { return now }
	ctx := context.Background()
	for range 3 {
		if err := c.VerifyPassword(ctx, sub("alice"), "good"); err != nil {
			t.Fatal(err)
		}
	}
	if next.asked != 1 {
		t.Fatalf("asked %d times, want once", next.asked)
	}
	for range 2 {
		c.VerifyPassword(ctx, sub("alice"), "bad")
	}
	if next.asked != 3 {
		t.Fatalf("refusals are cached: asked %d", next.asked)
	}
	c.VerifyPassword(ctx, sub("bob"), "good") // another user is asked about
	now = now.Add(2 * time.Minute)
	c.VerifyPassword(ctx, sub("alice"), "good")
	if next.asked != 5 {
		t.Fatalf("after expiry: asked %d", next.asked)
	}
	for k := range c.good {
		if strings.Contains(k, "good") || strings.Contains(k, "alice") {
			t.Fatal("the cache keeps the password or user in the clear")
		}
	}
	// a full cache forgets old entries rather than growing
	for i := range maxSSOCache + 5 {
		c.VerifyPassword(ctx, sub(fmt.Sprint(i)), "good")
	}
	if len(c.good) > maxSSOCache {
		t.Fatalf("cache grew to %d", len(c.good))
	}
}

func TestSSOPasswordsFollowSettings(t *testing.T) {
	f := newFakeLogto(t)
	f.users["sub-1"] = fakeLogtoUser{password: "pw"}
	cfg := config.Auth{OIDCIssuer: f.srv.URL + "/oidc", SSOIRCPassword: "logto", LogtoM2MClientID: "m2m-id", LogtoM2MClientSecret: "m2m-test-secret"}
	s := newSSOPasswords(config.Auth{}, f.srv.Client())
	if s.SSOPasswordsOn() {
		t.Fatal("on without settings")
	}
	s.apply(cfg)
	subject := "sub-1"
	linked := store.User{ID: 1, OIDCSubject: &subject}
	ctx := context.Background()
	if s.SSOPasswordMethod() != "logto" || s.CheckSSOPassword(ctx, linked, "pw") != nil {
		t.Fatal("logto: the right password is refused")
	}
	if err := s.CheckSSOPassword(ctx, store.User{ID: 2}, "pw"); !errors.Is(err, ErrSSONotLinked) {
		t.Fatalf("a local account: %v", err)
	}
	v := s.verifier
	s.apply(cfg)
	if s.verifier != v {
		t.Fatal("the same settings built a new verifier (and lost the cache)")
	}
	cfg.LogtoM2MClientSecret = "rotated-test-secret"
	s.apply(cfg)
	if s.verifier == v {
		t.Fatal("a new secret kept the old verifier")
	}

	// ldap needs a server and a DN template or a search base
	cfg.SSOIRCPassword = "ldap"
	s.apply(cfg)
	if s.SSOPasswordsOn() {
		t.Fatal("ldap on without ldap_url")
	}
	cfg.LDAPURL, cfg.LDAPUserDN = "ldaps://ldap.example.org", "uid={username},dc=example"
	s.apply(cfg)
	if s.SSOPasswordMethod() != "ldap" {
		t.Fatal("ldap set up but off")
	}
	// password_grant waits for the provider's discovery
	cfg.SSOIRCPassword, cfg.OIDCClientID = "password_grant", "client"
	s.apply(cfg)
	if s.SSOPasswordsOn() {
		t.Fatal("password_grant on before the provider is known")
	}
	cfg.SSOIRCPassword = "off"
	s.apply(cfg)
	if s.SSOPasswordsOn() || !errors.Is(s.CheckSSOPassword(ctx, linked, "pw"), ErrSSONotLinked) {
		t.Fatal("off: still checks")
	}
}
