package handlers_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/netip"
	"testing"

	"github.com/4evrd/evrd/internal/auth"
	"github.com/4evrd/evrd/internal/config"
	"github.com/4evrd/evrd/internal/store"
)

// signIn posts a password sign-in with extra headers, returning the status.
func signIn(t *testing.T, e *env, user string, headers map[string]string) int {
	t.Helper()
	b, _ := json.Marshal(map[string]string{"username": user, "password": user + "-password"})
	req, _ := http.NewRequest("POST", e.srv.URL+"/auth/password", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	return res.StatusCode
}

// A user's allowed hosts can't be got round with a forged X-Forwarded-For:
// it's only believed from a trusted proxy.
func TestAllowedHostsCantBeForged(t *testing.T) {
	// the test server's clients come from 127.0.0.1; here that's not a
	// trusted proxy (as for an evrd facing the internet directly)
	e := newEnv(t, func(c *config.Config) {
		c.Auth.TrustedProxies = []netip.Prefix{netip.MustParsePrefix("192.0.2.0/24")}
	})
	u := e.user("fay", store.RoleUser)
	u.AllowedHosts = []string{"10.0.0.0/8"}
	if err := e.st.UpdateUser(t.Context(), u); err != nil {
		t.Fatal(err)
	}
	if code := signIn(t, e, "fay", map[string]string{"X-Forwarded-For": "10.1.1.1"}); code == 200 {
		t.Fatal("a forged X-Forwarded-For got past the allowed hosts")
	}
	if code := signIn(t, e, "fay", map[string]string{auth.HeaderClient: "10.1.1.1"}); code == 200 {
		t.Fatal("the cluster's client header was believed from a browser")
	}
}

func TestATrustedProxysAddressIsBelieved(t *testing.T) {
	e := newEnv(t) // default: the local host is the trusted proxy
	u := e.user("fay", store.RoleUser)
	u.AllowedHosts = []string{"10.0.0.0/8"}
	e.st.UpdateUser(t.Context(), u)
	if code := signIn(t, e, "fay", map[string]string{"X-Forwarded-For": "10.1.1.1"}); code != 200 {
		t.Fatalf("through the local proxy: %d", code)
	}
	// a client behind the proxy can't slip in an address either: the proxy
	// appends the real one, and that's the one read
	if code := signIn(t, e, "fay", map[string]string{"X-Forwarded-For": "10.1.1.1, 203.0.113.9"}); code == 200 {
		t.Fatal("a client-added entry was believed")
	}
	// and straight from the proxy's own address (127.0.0.1, not in 10/8)
	if code := signIn(t, e, "fay", nil); code == 200 {
		t.Fatal("signed in from an address that isn't allowed")
	}
}
