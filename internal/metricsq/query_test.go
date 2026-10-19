package metricsq

import (
	"context"
	"errors"
	"io"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// answer serves body for every request and records the last request.
func answer(t *testing.T, status int, body string) (*httptest.Server, *atomic.Pointer[http.Request]) {
	t.Helper()
	var last atomic.Pointer[http.Request]
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		last.Store(r)
		w.WriteHeader(status)
		io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv, &last
}

func TestNewOff(t *testing.T) {
	if New("") != nil {
		t.Fatal("New(\"\") isn't nil")
	}
	var c *Client
	ctx := context.Background()
	if _, err := c.Scalar(ctx, "up"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Scalar: %v", err)
	}
	if _, err := c.Instant(ctx, "up"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Instant: %v", err)
	}
	if _, err := c.Range(ctx, "up", time.Now(), time.Now(), time.Minute); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Range: %v", err)
	}
}

func TestTransportIsHTTP11WithoutKeepAlive(t *testing.T) {
	tr := New("http://vm:8428").hc.Transport.(*http.Transport)
	if !tr.DisableKeepAlives {
		t.Error("keep-alives on")
	}
	if got := tr.TLSClientConfig.NextProtos; !reflect.DeepEqual(got, []string{"http/1.1"}) {
		t.Errorf("ALPN %v", got)
	}
	if tr.TLSNextProto == nil || len(tr.TLSNextProto) != 0 {
		t.Error("TLSNextProto must be a non-nil empty map to turn HTTP/2 off")
	}
}

func TestRangeEncodesAndParsesAMatrix(t *testing.T) {
	srv, last := answer(t, 200, `{"status":"success","data":{"resultType":"matrix","result":[
		{"metric":{"network":"libera"},"values":[[1700000000,"1.5"],[1700000060,"NaN"]]},
		{"metric":{"network":"efnet"},"values":[[1700000000,"2"]]}
	]}}`)
	start := time.Unix(1700000000, 0)
	got, err := New(srv.URL+"/").Range(context.Background(), `sum(rate(x{user="a b"}[5m]))`, start, start.Add(time.Hour), 90*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	r := last.Load()
	if r.URL.Path != "/api/v1/query_range" {
		t.Fatalf("path %q (trailing slash not trimmed?)", r.URL.Path)
	}
	q := r.URL.Query()
	want := url.Values{"query": {`sum(rate(x{user="a b"}[5m]))`}, "start": {"1700000000"}, "end": {"1700003600"}, "step": {"90"}}
	if !reflect.DeepEqual(q, want) {
		t.Fatalf("query %v, want %v", q, want)
	}
	// sorted by labels: efnet before libera
	if len(got) != 2 || got[0].Labels["network"] != "efnet" || got[1].Labels["network"] != "libera" {
		t.Fatalf("series %+v", got)
	}
	if !reflect.DeepEqual(got[0].Points, [][2]float64{{1700000000, 2}}) {
		t.Fatalf("efnet points %v", got[0].Points)
	}
	p := got[1].Points
	if len(p) != 2 || p[0] != [2]float64{1700000000, 1.5} || p[1][0] != 1700000060 || !math.IsNaN(p[1][1]) {
		t.Fatalf("libera points %v", p)
	}
}

func TestRangeEmpty(t *testing.T) {
	srv, _ := answer(t, 200, `{"status":"success","data":{"resultType":"matrix","result":[]}}`)
	got, err := New(srv.URL).Range(context.Background(), "up", time.Now(), time.Now(), time.Minute)
	if err != nil || got == nil || len(got) != 0 {
		t.Fatalf("got %#v, %v", got, err)
	}
}

func TestInstantParsesAVector(t *testing.T) {
	srv, last := answer(t, 200, `{"status":"success","data":{"resultType":"vector","result":[
		{"metric":{"network":"libera"},"value":[1700000000.5,"3"]},
		{"metric":{"network":"efnet"},"value":[1700000000.5,"+Inf"]}
	]}}`)
	got, err := New(srv.URL).Instant(context.Background(), "up")
	if err != nil {
		t.Fatal(err)
	}
	if r := last.Load(); r.URL.Path != "/api/v1/query" || r.URL.Query().Get("query") != "up" {
		t.Fatalf("request %s", r.URL)
	}
	if len(got) != 2 || got[0].Labels["network"] != "libera" || got[0].Points[0] != [2]float64{1700000000.5, 3} {
		t.Fatalf("got %+v", got)
	}
	if !math.IsInf(got[1].Points[0][1], 1) {
		t.Fatalf("+Inf parsed as %v", got[1].Points[0][1])
	}
}

func TestScalarSumsSeriesAndIsZeroWhenEmpty(t *testing.T) {
	srv, _ := answer(t, 200, `{"status":"success","data":{"resultType":"vector","result":[
		{"metric":{"a":"1"},"value":[0,"1.25"]},{"metric":{"a":"2"},"value":[0,"2"]},{"metric":{"a":"3"},"value":[0,"junk"]}]}}`)
	if v, err := New(srv.URL).Scalar(context.Background(), "x"); err != nil || v != 3.25 {
		t.Fatalf("got %v, %v", v, err)
	}
	srv, _ = answer(t, 200, `{"status":"success","data":{"resultType":"vector","result":[]}}`)
	if v, err := New(srv.URL).Scalar(context.Background(), "x"); err != nil || v != 0 {
		t.Fatalf("empty: %v, %v", v, err)
	}
}

func TestScalarNaN(t *testing.T) {
	srv, _ := answer(t, 200, `{"status":"success","data":{"resultType":"vector","result":[{"metric":{},"value":[0,"NaN"]}]}}`)
	if v, err := New(srv.URL).Scalar(context.Background(), "x"); err != nil || !math.IsNaN(v) {
		t.Fatalf("got %v, %v", v, err)
	}
}

func TestErrors(t *testing.T) {
	for _, c := range []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{"error status", 400, `{"status":"error","errorType":"bad_data","error":"parse error at char 3"}`, "metrics: parse error at char 3"},
		{"not JSON", 502, `<html>Bad Gateway</html>`, "metrics: 502 Bad Gateway: invalid character"},
		{"empty body", 200, ``, "metrics: 200 OK: EOF"},
	} {
		t.Run(c.name, func(t *testing.T) {
			srv, _ := answer(t, c.status, c.body)
			_, err := New(srv.URL).Instant(context.Background(), "up")
			if err == nil || !strings.HasPrefix(err.Error(), c.want) {
				t.Fatalf("got %v, want %q", err, c.want)
			}
			if _, err := New(srv.URL).Range(context.Background(), "up", time.Now(), time.Now(), time.Minute); err == nil {
				t.Fatal("Range: no error")
			}
		})
	}
}

func TestBadBaseURL(t *testing.T) {
	if _, err := New("http://vm\x7f:8428").Scalar(context.Background(), "up"); err == nil {
		t.Fatal("no error for a bad URL")
	}
}

func TestUnreachableIsTriedTwiceThenFails(t *testing.T) {
	var accepts atomic.Int32
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			accepts.Add(1)
			c.Close()
		}
	}()
	_, err = New("http://"+ln.Addr().String()).Scalar(context.Background(), "up")
	if err == nil || !strings.HasPrefix(err.Error(), "metrics: ") {
		t.Fatalf("got %v", err)
	}
	if n := accepts.Load(); n != 2 {
		t.Fatalf("%d connections, want 2", n)
	}
}

func TestCancelledContextIsNotRetried(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
	}))
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := New(srv.URL).Scalar(ctx, "up"); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
	if calls.Load() != 0 {
		t.Fatalf("%d calls", calls.Load())
	}
}

func TestQuote(t *testing.T) {
	for in, want := range map[string]string{
		"libera":    `"libera"`,
		`a"b`:       `"a\"b"`,
		`back\nick`: `"back\\nick"`,
	} {
		if got := Quote(in); got != want {
			t.Errorf("Quote(%q) = %s, want %s", in, got, want)
		}
	}
}
