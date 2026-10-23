package middleware

import (
	"bufio"
	"net"
	"net/http"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

var tracer = otel.Tracer("evrd/http")

// Traced gives each request a span, named by the route it matched ("GET
// /api/networks/{id}"). Only another node is trusted to continue a trace
// (internal); a browser's traceparent is ignored.
func Traced(internal bool, mux *http.ServeMux) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		if internal {
			ctx = otel.GetTextMapPropagator().Extract(ctx, propagation.HeaderCarrier(r.Header))
		}
		name := r.Method
		attrs := []attribute.KeyValue{attribute.String("http.request.method", r.Method)}
		if _, pattern := mux.Handler(r); pattern != "" {
			name = pattern
			attrs = append(attrs, attribute.String("http.route", pattern))
		}
		ctx, span := tracer.Start(ctx, name, trace.WithSpanKind(trace.SpanKindServer), trace.WithAttributes(attrs...))
		defer span.End()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		mux.ServeHTTP(sw, r.WithContext(ctx))
		span.SetAttributes(attribute.Int("http.response.status_code", sw.status))
		if sw.status >= 500 {
			span.SetStatus(codes.Error, http.StatusText(sw.status))
		}
	})
}

// statusWriter notes the response's status. It hijacks and flushes for the
// handlers that do (the websocket stream).
type statusWriter struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (w *statusWriter) WriteHeader(code int) {
	if !w.wrote {
		w.status, w.wrote = code, true
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	w.wrote = true
	return w.ResponseWriter.Write(b)
}

func (w *statusWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	w.status, w.wrote = http.StatusSwitchingProtocols, true
	return http.NewResponseController(w.ResponseWriter).Hijack()
}

func (w *statusWriter) Flush() { _ = http.NewResponseController(w.ResponseWriter).Flush() }

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
