package telemetry

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
)

// Setup must start (no schema clashes) and push OTLP to the endpoint.
func TestSetupExportsOTLP(t *testing.T) {
	var got atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		if r.URL.Path == "/v1/metrics" && len(b) > 0 {
			got.Add(1)
		}
	}))
	defer srv.Close()
	shutdown, err := Setup(context.Background(), Config{Endpoint: srv.URL + "/v1/metrics", Interval: 50 * time.Millisecond, NodeID: "test", Version: "dev"})
	if err != nil {
		t.Fatal(err)
	}
	IRCMessages.Add(context.Background(), 1, Attrs("direction", "in", "kind", "message"))
	if err := RegisterGauges(Gauges{Links: func() []Link { return []Link{{User: "1", Network: "2", Up: true, Lag: time.Millisecond}} }}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	if err := shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got.Load() == 0 {
		t.Fatal("nothing was exported")
	}
}

// Spans go to the traces endpoint, flushed by shutdown.
func TestSetupExportsTraces(t *testing.T) {
	var got atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		if r.URL.Path == "/v1/traces" && len(b) > 0 {
			got.Add(1)
		}
	}))
	defer srv.Close()
	shutdown, err := Setup(context.Background(), Config{TracesEndpoint: srv.URL + "/v1/traces", NodeID: "test", Version: "dev"})
	if err != nil {
		t.Fatal(err)
	}
	_, span := otel.Tracer("evrd/test").Start(context.Background(), "Test.Span")
	span.End()
	if err := shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got.Load() == 0 {
		t.Fatal("no spans were exported")
	}
}

func TestNoEndpointIsANoop(t *testing.T) {
	shutdown, err := Setup(context.Background(), Config{})
	if err != nil {
		t.Fatal(err)
	}
	shutdown(context.Background())
}

func TestExportSendsURLCredentialsAsBasicAuth(t *testing.T) {
	got := make(chan string, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, _ := r.BasicAuth()
		select {
		case got <- r.URL.Path + " " + user + ":" + pass:
		default:
		}
	}))
	defer srv.Close()
	u, _ := url.Parse(srv.URL + "/opentelemetry/v1/metrics")
	u.User = url.UserPassword("evrd", "s3cret")
	stop, err := Setup(context.Background(), Config{Endpoint: u.String(), Interval: 50 * time.Millisecond, NodeID: "t", Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	defer stop(context.Background())
	select {
	case g := <-got:
		if g != "/opentelemetry/v1/metrics evrd:s3cret" {
			t.Fatalf("got %q", g)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("nothing exported")
	}
}
