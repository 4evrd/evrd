package middleware

import (
	"net/http"
	"net/url"
	"strings"
)

// SameOrigin refuses cross-site state-changing requests (CSRF), on top of the
// SameSite=Lax cookie.
func SameOrigin(next http.Handler) http.Handler { return SameOriginOr(nil, next) }

// SameOriginOr is SameOrigin letting through the cross-site requests exempt
// reports (nil: none) — ones that don't ride the cookie.
func SameOriginOr(exempt func(*http.Request) bool, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
		default:
			if !sameOrigin(r) && (exempt == nil || !exempt(r)) {
				writeErr(w, http.StatusForbidden, "cross-site request refused")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// CheckWebSocketOrigin is the upgrader's origin check: a browser opening a
// socket from another site must not ride the session cookie.
func CheckWebSocketOrigin(r *http.Request) bool { return sameOrigin(r) }

func sameOrigin(r *http.Request) bool {
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" {
		return site == "same-origin" || site == "none"
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true // not a browser cross-site request
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	host := r.Header.Get("X-Forwarded-Host")
	if host == "" {
		host = r.Host
	}
	return strings.EqualFold(u.Host, host)
}
