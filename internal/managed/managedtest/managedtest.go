// Package managedtest is a fake access-token issuer for managed-mode tests:
// an OIDC discovery document and JWKS over HTTP, and Ed25519-signed JWTs
// like Boson's.
package managedtest

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// Audience is the "aud" the issuer's tokens carry by default.
const Audience = "boson"

// Issuer signs tokens and serves its keys.
type Issuer struct {
	URL  string // the issuer, and where its discovery document is
	key  ed25519.PrivateKey
	kid  string
	stop func()
}

// New starts an issuer until the test ends.
func New(t testing.TB) *Issuer {
	t.Helper()
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	is := &Issuer{key: key, kid: "key_test"}
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	is.URL, is.stop = srv.URL, srv.Close
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"issuer": is.URL, "jwks_uri": is.URL + "/.well-known/jwks.json",
			"id_token_signing_alg_values_supported": []string{"EdDSA"},
		})
	})
	mux.HandleFunc("/.well-known/jwks.json", func(w http.ResponseWriter, r *http.Request) {
		pub := key.Public().(ed25519.PublicKey)
		json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{
			"kty": "OKP", "crv": "Ed25519", "x": b64(pub), "kid": is.kid, "alg": "EdDSA", "use": "sig",
		}}})
	})
	t.Cleanup(srv.Close)
	return is
}

// JWKS is where the issuer's keys are.
func (is *Issuer) JWKS() string { return is.URL + "/.well-known/jwks.json" }

// Token is an access token for sub (named name), valid for a while, for
// Audience.
func (is *Issuer) Token(sub, name string) string {
	return is.Sign(map[string]any{"sub": sub, "name": name, "aud": Audience})
}

// Sign signs claims, adding iss, iat and exp unless they're there.
func (is *Issuer) Sign(claims map[string]any) string {
	now := time.Now()
	set := func(k string, v any) {
		if _, ok := claims[k]; !ok {
			claims[k] = v
		}
	}
	set("iss", is.URL)
	set("iat", now.Unix())
	set("exp", now.Add(15*time.Minute).Unix())
	h, _ := json.Marshal(map[string]string{"alg": "EdDSA", "kid": is.kid, "typ": "at+jwt"})
	c, _ := json.Marshal(claims)
	in := b64(h) + "." + b64(c)
	return in + "." + b64(ed25519.Sign(is.key, []byte(in)))
}

// Close stops serving the keys (the issuer going down).
func (is *Issuer) Close() { is.stop() }

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }
