package handlers

import (
	"net/http"
	"testing"

	"github.com/4evrd/evrd/internal/auth"
)

func TestBrowserNameAndClientAddr(t *testing.T) {
	for ua, want := range map[string]string{
		"Mozilla/5.0 (Windows NT 10.0) AppleWebKit Chrome/120 Safari/537 Edg/120": "Edge on Windows",
		"Mozilla/5.0 (X11; Linux x86_64; rv:120.0) Gecko Firefox/120.0":           "Firefox on Linux",
		"Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) Safari/604.1":     "Safari on iOS",
		"Mozilla/5.0 (Linux; Android 14) Chrome/120 Mobile Safari/537":            "Chrome on Android",
		"curl/8.0": "Web",
	} {
		if got := browserName(ua); got != want {
			t.Errorf("browserName(%q) = %q, want %q", ua, got, want)
		}
	}
	s := &Handlers{auth: &auth.Manager{}}
	r, _ := http.NewRequest("GET", "/", nil)
	r.RemoteAddr = "192.0.2.1:5555"
	if got := s.clientAddr(r); got != "192.0.2.1" {
		t.Errorf("clientAddr: %q", got)
	}
	r.RemoteAddr = "pipe"
	if got := s.clientAddr(r); got != "pipe" {
		t.Errorf("clientAddr without a port: %q", got)
	}
	// X-Forwarded-For counts only from a trusted proxy (loopback by default).
	r.Header.Set("X-Forwarded-For", " 198.51.100.7 , 10.0.0.1")
	r.RemoteAddr = "192.0.2.1:5555"
	if got := s.clientAddr(r); got != "192.0.2.1" {
		t.Errorf("clientAddr with a spoofed X-Forwarded-For: %q", got)
	}
	r.Header.Set("X-Forwarded-For", " 198.51.100.7 , 127.0.0.1")
	r.RemoteAddr = "127.0.0.1:5555"
	if got := s.clientAddr(r); got != "198.51.100.7" {
		t.Errorf("clientAddr behind a proxy: %q", got)
	}
}
