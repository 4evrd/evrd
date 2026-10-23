package middleware

import (
	"crypto/subtle"
	"net/http"
	"strings"
)

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
