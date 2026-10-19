package metricsq

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
)

func TestQueriesSendURLCredentialsAsBasicAuth(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, _ := r.BasicAuth()
		got = user + ":" + pass
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[{"metric":{},"value":[0,"7"]}]}}`))
	}))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)
	u.User = url.UserPassword("evrd", "s3cret")
	v, err := New(u.String()).Scalar(context.Background(), "up")
	if err != nil || v != 7 || got != "evrd:s3cret" {
		t.Fatalf("v=%v err=%v auth=%q", v, err, got)
	}
}

func TestADroppedConnectionIsRetried(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			// the network drops the first connection: no answer, just EOF
			c, _, _ := w.(http.Hijacker).Hijack()
			c.Close()
			return
		}
		io.WriteString(w, `{"status":"success","data":{"resultType":"vector","result":[{"metric":{},"value":[1,"7"]}]}}`)
	}))
	defer srv.Close()
	v, err := New(srv.URL).Scalar(context.Background(), "up")
	if err != nil || v != 7 {
		t.Fatalf("got %v, %v", v, err)
	}
	if calls.Load() != 2 {
		t.Fatalf("%d calls", calls.Load())
	}
}

// The metrics store usually sits behind a proxy that speaks HTTP/2 over TLS
// (Traefik). The client must agree on a protocol it actually speaks.
func TestHTTPSWithHTTP2OnOffer(t *testing.T) {
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"status":"success","data":{"resultType":"vector","result":[{"metric":{},"value":[1,"7"]}]}}`)
	}))
	srv.EnableHTTP2 = true
	srv.StartTLS()
	defer srv.Close()
	c := New(srv.URL)
	c.hc.Transport.(*http.Transport).TLSClientConfig.RootCAs = srv.Client().Transport.(*http.Transport).TLSClientConfig.RootCAs
	for i := 0; i < 3; i++ {
		if v, err := c.Scalar(context.Background(), "up"); err != nil || v != 7 {
			t.Fatalf("query %d: %v, %v", i, v, err)
		}
	}
}
