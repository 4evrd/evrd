package telemetry

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
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

func TestNoEndpointIsANoop(t *testing.T) {
	shutdown, err := Setup(context.Background(), Config{})
	if err != nil {
		t.Fatal(err)
	}
	shutdown(context.Background())
}
