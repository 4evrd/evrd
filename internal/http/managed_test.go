package http

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/4evrd/evrd/internal/managed"
)

// In managed mode the public API is the managed API alone.
func TestManagedHandler(t *testing.T) {
	s := newServer(t)
	s.SetManaged(&managed.Provisioner{})
	h := s.Handler()
	for path, want := range map[string]int{
		"/api/managed/health": http.StatusNotFound, // no managed_token: off
		"/api/config":         http.StatusNotFound,
		"/auth/login":         http.StatusNotFound,
		"/":                   http.StatusNotFound,
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		if rec.Code != want {
			t.Errorf("%s: %d, want %d", path, rec.Code, want)
		}
	}
	s.SetManaged(nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/api/config", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("managed mode off: /api/config %d", rec.Code)
	}
}
