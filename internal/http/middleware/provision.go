package middleware

import (
	"crypto/subtle"
	"net/http"
	"strings"
)

// BearerToken lets a request through with the bearer token token returns
// (managed_token): 404 while there's none, 401 for a wrong one.
func BearerToken(token func() string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		want := token()
		got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if want == "" {
			writeErr(w, http.StatusNotFound, "not found")
			return
		}
		if !ok || subtle.ConstantTimeCompare([]byte(got), []byte(want)) != 1 {
			writeErr(w, http.StatusUnauthorized, "bad token")
			return
		}
		next(w, r)
	}
}

// ProvisionToken lets through requests bearing the provisioning token
// (provision_token, read per request: settings change at runtime). With
// none set, provisioning is off.
func ProvisionToken(token func() string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		want := token()
		got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if want == "" {
			writeErr(w, http.StatusNotFound, "provisioning is off (set provision_token)")
			return
		}
		if !ok || subtle.ConstantTimeCompare([]byte(got), []byte(want)) != 1 {
			writeErr(w, http.StatusUnauthorized, "bad provisioning token")
			return
		}
		next(w, r)
	}
}
