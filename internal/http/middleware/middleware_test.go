package middleware

import (
	"bufio"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/4evrd/evrd/internal/auth"
	"github.com/4evrd/evrd/internal/cluster"
	"github.com/4evrd/evrd/internal/store"
)

var ok = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })

func serve(h http.Handler, r *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

func TestHSTS(t *testing.T) {
	rec := serve(HSTS(ok), httptest.NewRequest("GET", "/", nil))
	if rec.Code != http.StatusNoContent || rec.Header().Get("Strict-Transport-Security") != "max-age=31536000" {
		t.Fatalf("%d %v", rec.Code, rec.Header())
	}
}

func TestProvisionToken(t *testing.T) {
	token := ""
	h := ProvisionToken(func() string { return token }, ok)
	req := func(auth string) *http.Request {
		r := httptest.NewRequest("PUT", "/api/provision/users/x", nil)
		if auth != "" {
			r.Header.Set("Authorization", auth)
		}
		return r
	}
	if rec := serve(h, req("Bearer x")); rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "provisioning is off") {
		t.Fatalf("off: %d %s", rec.Code, rec.Body)
	}
	token = "s3cret" // read per request
	for _, a := range []string{"", "s3cret", "Bearer wrong", "Basic s3cret"} {
		if rec := serve(h, req(a)); rec.Code != http.StatusUnauthorized {
			t.Errorf("%q: %d", a, rec.Code)
		}
	}
	if rec := serve(h, req("Bearer s3cret")); rec.Code != http.StatusNoContent {
		t.Fatalf("the token: %d", rec.Code)
	}
}

func TestTraced(t *testing.T) {
	rec := tracetest.NewSpanRecorder()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec)))
	mux := http.NewServeMux()
	mux.HandleFunc("GET /boom", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		w.WriteHeader(http.StatusOK) // a second WriteHeader doesn't change what's recorded
	})
	mux.HandleFunc("GET /flush", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("x"))
		http.NewResponseController(w).Flush()
	})
	serve(Traced(false, mux), httptest.NewRequest("GET", "/boom", nil))
	serve(Traced(false, mux), httptest.NewRequest("GET", "/flush", nil))

	spans := map[string]sdktrace.ReadOnlySpan{}
	for _, s := range rec.Ended() {
		spans[s.Name()] = s
	}
	boom := spans["GET /boom"]
	if boom == nil || boom.Status().Code != codes.Error || !has(boom, attribute.Int("http.response.status_code", 502)) {
		t.Fatalf("5xx span: %v", boom)
	}
	if f := spans["GET /flush"]; f == nil || f.Status().Code == codes.Error || !has(f, attribute.Int("http.response.status_code", 200)) {
		t.Fatalf("flushed span: %v", f)
	}

	// Only the internal endpoint continues a caller's trace.
	otel.SetTextMapPropagator(propagation.TraceContext{})
	parent := "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	for _, internal := range []bool{false, true} {
		r := httptest.NewRequest("GET", "/flush", nil)
		r.Header.Set("traceparent", parent)
		serve(Traced(internal, mux), r)
		ended := rec.Ended()
		got := ended[len(ended)-1].SpanContext().TraceID().String() == "4bf92f3577b34da6a3ce929d0e0e4736"
		if got != internal {
			t.Errorf("internal %v: continued the trace %v", internal, got)
		}
	}
}

func has(s sdktrace.ReadOnlySpan, want attribute.KeyValue) bool {
	for _, a := range s.Attributes() {
		if a == want {
			return true
		}
	}
	return false
}

// hijacker is a ResponseWriter that can be hijacked, like a server's.
type hijacker struct{ *httptest.ResponseRecorder }

func (hijacker) Hijack() (net.Conn, *bufio.ReadWriter, error) { return nil, nil, nil }

func TestStatusWriterHijacks(t *testing.T) {
	sw := &statusWriter{ResponseWriter: hijacker{httptest.NewRecorder()}, status: http.StatusOK}
	if _, _, err := sw.Hijack(); err != nil {
		t.Fatal(err)
	}
	if sw.status != http.StatusSwitchingProtocols || sw.Unwrap() == nil {
		t.Fatalf("status %d", sw.status)
	}
}

// fakeCluster owns the users in owned; WaitOwner answers owner and err.
type fakeCluster struct {
	owned map[int64]bool
	owner store.Node
	err   error
}

func (f fakeCluster) Self() store.Node    { return store.Node{ID: "me"} }
func (f fakeCluster) Owns(uid int64) bool { return f.owned[uid] }
func (f fakeCluster) WaitOwner(context.Context, int64, time.Duration) (store.Node, error) {
	return f.owner, f.err
}

func TestOwner(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	owner := func(cl Cluster) *Owner {
		return &Owner{Cluster: cl, Secret: "s", ClientIP: func(*http.Request) string { return "192.0.2.1" }, Log: log}
	}
	user := func(*http.Request) int64 { return 7 }
	get := func(o *Owner, r *http.Request) *httptest.ResponseRecorder {
		return serve(o.At(user, ok), r)
	}
	req := func() *http.Request { return httptest.NewRequest("GET", "/api/me", nil) }

	for name, o := range map[string]*Owner{
		"single node":          {Log: log},
		"owned here":           owner(fakeCluster{owned: map[int64]bool{7: true}}),
		"nobody owns them":     owner(fakeCluster{err: cluster.ErrNoOwner}),
		"the lookup says here": owner(fakeCluster{owner: store.Node{ID: "me"}}),
	} {
		if rec := get(o, req()); rec.Code != http.StatusNoContent {
			t.Errorf("%s: %d", name, rec.Code)
		}
	}
	if rec := get(owner(fakeCluster{err: errors.New("db down")}), req()); rec.Code != http.StatusInternalServerError {
		t.Errorf("lookup failed: %d", rec.Code)
	}
	if rec := get(owner(fakeCluster{owner: store.Node{ID: "other"}}), req()); rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "moving") {
		t.Errorf("owner without a URL: %d %s", rec.Code, rec.Body)
	}
	if rec := get(owner(fakeCluster{owner: store.Node{ID: "other", HTTPURL: "http://127.0.0.1:1"}}), req()); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("owner unreachable: %d", rec.Code)
	}

	// A forwarded request that lands on a node that's let the user go.
	forwarded := func(h http.Handler) http.Handler { return (&auth.Manager{}).Forwarded("s", h) }
	r := req()
	r.Header.Set(auth.HeaderSecret, "s")
	if rec := serve(forwarded(owner(fakeCluster{}).At(user, ok)), r); rec.Code != http.StatusMisdirectedRequest {
		t.Errorf("forwarded, not ours: %d", rec.Code)
	}

	// Another node owns them: the request goes there, signed, without the
	// browser's cookies.
	got := make(chan *http.Request, 1)
	there := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got <- r
		w.WriteHeader(http.StatusAccepted)
	}))
	defer there.Close()
	r = req()
	r.Header.Set("Cookie", "evrd_session=x")
	if rec := get(owner(fakeCluster{owner: store.Node{ID: "other", HTTPURL: there.URL}}), r); rec.Code != http.StatusAccepted {
		t.Fatalf("forwarded: %d", rec.Code)
	}
	fr := <-got
	if fr.Header.Get(auth.HeaderSecret) != "s" || fr.Header.Get(auth.HeaderClient) != "192.0.2.1" || fr.Header.Get("Cookie") != "" || fr.URL.Path != "/api/me" {
		t.Fatalf("forwarded request: %s %v", fr.URL, fr.Header)
	}
}
