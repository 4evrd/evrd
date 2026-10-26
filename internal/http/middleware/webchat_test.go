package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWebChatCORS(t *testing.T) {
	c := WebChat{
		Allowed: func(o string) bool { return o == "https://chat.example" },
		Route:   func(method, path string) bool { return method == http.MethodGet && path == "/api/networks" },
	}
	reached := false
	h := c.CORS(SameOriginOr(c.Exempt, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached = true; w.WriteHeader(http.StatusOK) })))
	serve := func(method, path string, hdr map[string]string) *httptest.ResponseRecorder {
		reached = false
		r := httptest.NewRequest(method, "https://evrd.example.org"+path, nil)
		for k, v := range hdr {
			r.Header.Set(k, v)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		return rec
	}
	site := map[string]string{"Origin": "https://chat.example", "Sec-Fetch-Site": "cross-site"}

	if rec := serve("GET", "/api/networks", site); !reached || rec.Header().Get("Access-Control-Allow-Origin") != "https://chat.example" || rec.Header().Get("Vary") != "Origin" {
		t.Errorf("allowed route: %v %v", reached, rec.Header())
	}
	if rec := serve("GET", "/api/me/sessions", site); !reached || rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Errorf("another route: %v", rec.Header())
	}
	if rec := serve("GET", "/api/networks", map[string]string{"Origin": "https://evil.example"}); rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Errorf("another site: %v", rec.Header())
	}
	if rec := serve("GET", "/api/networks", nil); !reached || rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Errorf("no origin: %v", rec.Header())
	}

	pre := func(want string) map[string]string {
		return map[string]string{"Origin": "https://chat.example", "Access-Control-Request-Method": want}
	}
	if rec := serve("OPTIONS", "/api/networks", pre("GET")); reached || rec.Code != http.StatusNoContent || rec.Header().Get("Access-Control-Allow-Methods") != "GET, POST" || rec.Header().Get("Access-Control-Max-Age") != "600" {
		t.Errorf("preflight: %d %v", rec.Code, rec.Header())
	}
	if rec := serve("OPTIONS", "/api/networks", pre("DELETE")); reached || rec.Code != http.StatusForbidden || rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Errorf("preflight for another method: %d %v", rec.Code, rec.Header())
	}

	// Cross-site writes: only a web chat's, with its token, on its route.
	c.Route = func(method, path string) bool { return path == "/api/networks/1/read" }
	h = c.CORS(SameOriginOr(c.Exempt, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached = true })))
	withToken := map[string]string{"Origin": "https://chat.example", "Sec-Fetch-Site": "cross-site", "Authorization": "Bearer t"}
	if serve("POST", "/api/networks/1/read", withToken); !reached {
		t.Error("a web chat's POST was refused")
	}
	if rec := serve("POST", "/api/networks/1/read", site); reached || rec.Code != http.StatusForbidden {
		t.Error("a cross-site POST without a token got through")
	}
	if serve("POST", "/api/me/password", withToken); reached {
		t.Error("a token got a cross-site POST to another route through")
	}
	withToken["Origin"] = "https://evil.example"
	if serve("POST", "/api/networks/1/read", withToken); reached {
		t.Error("another site's POST got through")
	}
}
