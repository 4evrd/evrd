package managedtest

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestIssuerServesKeysAndSigns(t *testing.T) {
	is := New(t)
	for _, path := range []string{"/.well-known/openid-configuration", "/.well-known/jwks.json"} {
		resp, err := http.Get(is.URL + path)
		if err != nil || resp.StatusCode != 200 {
			t.Fatalf("%s: %v %v", path, resp, err)
		}
		resp.Body.Close()
	}
	if is.JWKS() != is.URL+"/.well-known/jwks.json" {
		t.Error(is.JWKS())
	}
	tok := is.Token("usr_1", "alice")
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		t.Fatal(tok)
	}
	b, _ := base64.RawURLEncoding.DecodeString(parts[1])
	var c map[string]any
	json.Unmarshal(b, &c)
	if c["sub"] != "usr_1" || c["aud"] != Audience || c["iss"] != is.URL || c["exp"] == nil {
		t.Errorf("claims %v", c)
	}
	if c := is.Sign(map[string]any{"iss": "other"}); !strings.Contains(c, ".") {
		t.Error(c)
	}
	is.Close()
	if _, err := http.Get(is.URL + "/.well-known/jwks.json"); err == nil {
		t.Error("still serving after Close")
	}
}
