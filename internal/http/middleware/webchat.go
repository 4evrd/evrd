package middleware

// Another site's web chat (web_chat_origins) calls a few routes from its own
// page, signed in by its token (internal/auth/webchat.go): CORS lets the
// browser do that from the allowed sites only, for those routes only. The
// token isn't a cookie, so a cross-site request carrying it isn't CSRF; the
// cookie is never given to those sites (no Access-Control-Allow-Credentials).

import (
	"net/http"
	"strconv"

	"github.com/4evrd/evrd/internal/auth"
)

// WebChat says which sites and routes a web chat may use.
type WebChat struct {
	// Allowed reports whether a page origin is one of web_chat_origins.
	Allowed func(origin string) bool
	// Route reports whether method and path are a web chat's route.
	Route func(method, path string) bool
}

// corsMaxAge is how long a browser may keep a preflight's answer.
const corsMaxAge = 10 * 60

// CORS answers preflights and marks responses readable by the allowed
// sites, on the web chat's routes.
func (c WebChat) CORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin == "" || !c.Allowed(origin) {
			next.ServeHTTP(w, r)
			return
		}
		h := w.Header()
		if want := r.Header.Get("Access-Control-Request-Method"); r.Method == http.MethodOptions && want != "" {
			h.Add("Vary", "Origin")
			if !c.Route(want, r.URL.Path) {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			h.Set("Access-Control-Allow-Origin", origin)
			h.Set("Access-Control-Allow-Methods", "GET, POST")
			h.Set("Access-Control-Allow-Headers", "Authorization, Content-Type, "+languageHeader)
			h.Set("Access-Control-Max-Age", strconv.Itoa(corsMaxAge))
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if c.Route(r.Method, r.URL.Path) {
			h.Add("Vary", "Origin")
			h.Set("Access-Control-Allow-Origin", origin)
		}
		next.ServeHTTP(w, r)
	})
}

// Exempt reports whether a cross-site request is a web chat's: from an
// allowed site, to its route, with its token (SameOriginOr).
func (c WebChat) Exempt(r *http.Request) bool {
	_, tok := auth.BearerToken(r)
	return tok && c.Allowed(r.Header.Get("Origin")) && c.Route(r.Method, r.URL.Path)
}

// languageHeader is handlers.LanguageHeader (the web app's language).
const languageHeader = "X-Evrd-Language"
