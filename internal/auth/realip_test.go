package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// The forwarding node's word on the client's address counts only on a
// request that came with the cluster secret.
func TestForwardedClientAddress(t *testing.T) {
	m := &Manager{}
	var got string
	h := m.Forwarded("s3cret", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { got = m.ClientIP(r) }))
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set(HeaderSecret, "s3cret")
	r.Header.Set(HeaderClient, "198.51.100.7")
	h.ServeHTTP(httptest.NewRecorder(), r)
	if got != "198.51.100.7" {
		t.Fatalf("forwarded: %q", got)
	}
	got = ""
	r.Header.Set(HeaderSecret, "wrong")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized || got != "" {
		t.Fatalf("wrong secret: %d %q", w.Code, got)
	}
}
