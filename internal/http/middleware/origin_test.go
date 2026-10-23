package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSameOrigin(t *testing.T) {
	h := SameOrigin(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	for _, tc := range []struct {
		name   string
		method string
		hdr    map[string]string
		ok     bool
	}{
		{"GET from anywhere", http.MethodGet, map[string]string{"Sec-Fetch-Site": "cross-site"}, true},
		{"no browser headers", http.MethodPost, nil, true},
		{"same-origin fetch", http.MethodPost, map[string]string{"Sec-Fetch-Site": "same-origin"}, true},
		{"typed by the user", http.MethodPost, map[string]string{"Sec-Fetch-Site": "none"}, true},
		{"cross-site fetch", http.MethodPost, map[string]string{"Sec-Fetch-Site": "cross-site", "Origin": "https://evrd.example.org"}, false},
		{"same-site subdomain", http.MethodDelete, map[string]string{"Sec-Fetch-Site": "same-site"}, false},
		{"matching Origin", http.MethodPost, map[string]string{"Origin": "https://evrd.example.org"}, true},
		{"Origin, other case", http.MethodPut, map[string]string{"Origin": "https://EVRD.example.org"}, true},
		{"foreign Origin", http.MethodPost, map[string]string{"Origin": "https://evil.example"}, false},
		{"Origin with another port", http.MethodPost, map[string]string{"Origin": "https://evrd.example.org:8443"}, false},
		{"unparsable Origin", http.MethodPost, map[string]string{"Origin": "://\x7f"}, false},
		{"behind a proxy", http.MethodPost, map[string]string{"Origin": "https://public.example", "X-Forwarded-Host": "public.example"}, true},
	} {
		r := httptest.NewRequest(tc.method, "https://evrd.example.org/api/x", nil)
		for k, v := range tc.hdr {
			r.Header.Set(k, v)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		if got := rec.Code == http.StatusNoContent; got != tc.ok {
			t.Errorf("%s: %d", tc.name, rec.Code)
		}
		if tc.method == http.MethodPost {
			if got := CheckWebSocketOrigin(r); got != tc.ok {
				t.Errorf("%s: websocket origin %v", tc.name, got)
			}
		}
	}
}
