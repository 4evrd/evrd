package api

import (
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/4evrd/evrd/internal/store"
)

// TestRequestsAreTraced: a request's span is named by its route, and the
// service it calls is a child of it.
func TestRequestsAreTraced(t *testing.T) {
	rec := tracetest.NewSpanRecorder()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec)))
	e := newEnv(t)
	alice := e.user("alice", store.RoleUser)
	c := e.login("alice")
	if code, _ := c.do("GET", "/api/networks", nil); code != 200 {
		t.Fatalf("list networks: %d", code)
	}
	if code, _ := c.do("GET", "/api/nope", nil); code != 404 {
		t.Fatalf("unknown route: %d", code)
	}

	spans := map[string]sdktrace.ReadOnlySpan{}
	for _, s := range rec.Ended() {
		spans[s.Name()] = s
	}
	req, ok := spans["GET /api/networks"]
	if !ok {
		t.Fatalf("no request span in %v", names(rec.Ended()))
	}
	if !hasAttr(req, attribute.String("http.route", "GET /api/networks")) || !hasAttr(req, attribute.Int("http.response.status_code", 200)) {
		t.Fatalf("request span attributes %v", req.Attributes())
	}
	svc, ok := spans["NetworkService.ListNetworks"]
	if !ok {
		t.Fatalf("no service span in %v", names(rec.Ended()))
	}
	if svc.Parent().SpanID() != req.SpanContext().SpanID() || !hasAttr(svc, attribute.Int64("user.id", alice.ID)) {
		t.Fatalf("service span parent %v, attributes %v", svc.Parent().SpanID(), svc.Attributes())
	}
	if s, ok := spans["GET"]; !ok || !hasAttr(s, attribute.Int("http.response.status_code", 404)) {
		t.Fatalf("unmatched request: %v", names(rec.Ended()))
	}
}

func names(spans []sdktrace.ReadOnlySpan) []string {
	var out []string
	for _, s := range spans {
		out = append(out, s.Name())
	}
	return out
}

func hasAttr(s sdktrace.ReadOnlySpan, want attribute.KeyValue) bool {
	for _, a := range s.Attributes() {
		if a == want {
			return true
		}
	}
	return false
}
