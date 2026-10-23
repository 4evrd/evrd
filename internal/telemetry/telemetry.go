// Package telemetry exports evrd's metrics with OpenTelemetry (OTLP over
// HTTP), e.g. straight into VictoriaMetrics at
// http://victoriametrics:8428/opentelemetry/v1/metrics.
//
// The instruments are package variables, so any package can record without
// plumbing: until Setup installs a provider (or when no endpoint is
// configured) they are no-ops. Every series carries the node it came from
// (service.instance.id), so a cluster's nodes can be told apart or summed.
//
// Names follow OpenTelemetry conventions (evrd.irc.messages); VictoriaMetrics
// started with -opentelemetry.usePrometheusNaming stores them as
// evrd_irc_messages_total and so on.
package telemetry

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/runtime"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
)

// Config is where and how often to export.
type Config struct {
	// Endpoint is the full OTLP/HTTP metrics URL; empty turns export off.
	Endpoint string
	Interval time.Duration // default 15s
	NodeID   string
	Version  string
}

// Setup starts exporting. The returned shutdown flushes and stops it.
func Setup(ctx context.Context, cfg Config) (shutdown func(context.Context) error, err error) {
	if cfg.Endpoint == "" {
		return func(context.Context) error { return nil }, nil
	}
	if cfg.Interval <= 0 {
		cfg.Interval = 15 * time.Second
	}
	// Credentials in the URL (https://user:pass@host/…) become basic auth:
	// the exporter ignores them otherwise, and a proxy in front of the
	// metrics store (like vmwrite behind Traefik) may want them.
	opts := []otlpmetrichttp.Option{}
	endpoint := cfg.Endpoint
	if u, err := url.Parse(endpoint); err == nil && u.User != nil {
		pass, _ := u.User.Password()
		opts = append(opts, otlpmetrichttp.WithHeaders(map[string]string{
			"Authorization": "Basic " + base64.StdEncoding.EncodeToString([]byte(u.User.Username()+":"+pass)),
		}))
		u.User = nil
		endpoint = u.String()
	}
	opts = append(opts, otlpmetrichttp.WithEndpointURL(endpoint))
	exp, err := otlpmetrichttp.New(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("telemetry: %w", err)
	}
	// Schemaless, so it merges with the SDK's defaults whatever semantic
	// conventions version those use.
	res, err := resource.Merge(resource.Default(), resource.NewSchemaless(
		attribute.String("service.name", "evrd"),
		attribute.String("service.version", cfg.Version),
		attribute.String("service.instance.id", cfg.NodeID),
	))
	if err != nil {
		return nil, fmt.Errorf("telemetry: %w", err)
	}
	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithResource(res),
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(exp, sdkmetric.WithInterval(cfg.Interval))),
	)
	otel.SetMeterProvider(mp)
	if err := runtime.Start(runtime.WithMeterProvider(mp)); err != nil {
		return nil, fmt.Errorf("telemetry: runtime metrics: %w", err)
	}
	return mp.Shutdown, nil
}

var meter = otel.Meter("github.com/4evrd/evrd")

func counter(name, desc, unit string) metric.Int64Counter {
	c, err := meter.Int64Counter(name, metric.WithDescription(desc), metric.WithUnit(unit))
	if err != nil {
		panic(err)
	}
	return c
}

// Counters. Attributes are low-cardinality labels, except Lines and
// UpstreamDisconnects, which carry user and network IDs (never names) so the
// web app can chart a user's own traffic and link health.
var (
	// IRCMessages counts lines through the bouncer: direction (in = from the
	// IRC server, out = sent by a user) and kind (message, notice, action,
	// event).
	IRCMessages = counter("evrd.irc.messages", "IRC messages and channel events relayed", "{message}")
	// UpstreamConnects counts connection attempts to IRC servers, by result.
	UpstreamConnects = counter("evrd.upstream.connects", "Connection attempts to IRC networks", "{connect}")
	// Lines counts chat lines per user and network (direction in/out), for the
	// web app's traffic chart.
	Lines = counter("evrd.irc.lines", "Chat lines relayed, per user and network", "{line}")
	// UpstreamDisconnects counts dropped upstream connections, per user and
	// network.
	UpstreamDisconnects = counter("evrd.upstream.disconnects", "Upstream links lost (kind=dropped) or connection attempts failed (kind=failed)", "{disconnect}")
	// IRCLogins counts native client attaches, by result.
	IRCLogins = counter("evrd.irc.logins", "IRC client sign-ins", "{login}")
	// WebLogins counts web sign-ins, by method and result.
	WebLogins = counter("evrd.web.logins", "Web app sign-ins", "{login}")
	// Forwarded counts requests and connections handed to another node, by
	// kind (http, irc).
	Forwarded = counter("evrd.cluster.forwarded", "Requests and connections forwarded to the owning node", "{request}")
	// HistoryWrites counts history writes, by result.
	HistoryWrites = counter("evrd.history.writes", "Lines written to the history store", "{line}")

	// HTTPDuration is API request latency, by route, method and status class.
	HTTPDuration = func() metric.Float64Histogram {
		h, err := meter.Float64Histogram("evrd.http.server.duration",
			metric.WithDescription("API request duration"), metric.WithUnit("s"),
			metric.WithExplicitBucketBoundaries(0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10))
		if err != nil {
			panic(err)
		}
		return h
	}()

	// DBDuration is database query latency, by table, operation (create,
	// query, update, delete, row, raw) and result.
	DBDuration = func() metric.Float64Histogram {
		h, err := meter.Float64Histogram("evrd.db.duration",
			metric.WithDescription("Database query duration"), metric.WithUnit("s"),
			metric.WithExplicitBucketBoundaries(0.0005, 0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1))
		if err != nil {
			panic(err)
		}
		return h
	}()
)

// Link is one upstream connection's state, for the per-network gauges.
type Link struct {
	User, Network string
	Up            bool
	Lag           time.Duration // < 0 while unknown
}

// Gauges are values read when exported (connections open, users owned …).
type Gauges struct {
	Upstreams func() map[string]int64 // connections by status
	Links     func() []Link           // each upstream connection
	Clients   func() int64            // attached IRC clients
	Streams   func() int64            // open web chat sockets
	Users     func() int64            // users this node owns
	Nodes     func() int64            // live cluster nodes
	Queue     func() int64            // history writes waiting (0 if not queued)
}

// RegisterGauges reports g's values at every export.
func RegisterGauges(g Gauges) error {
	gauge := func(name, desc, unit string) metric.Int64ObservableGauge {
		o, err := meter.Int64ObservableGauge(name, metric.WithDescription(desc), metric.WithUnit(unit))
		if err != nil {
			panic(err)
		}
		return o
	}
	upstreams := gauge("evrd.upstream.connections", "Connections to IRC networks, by status", "{connection}")
	clients := gauge("evrd.irc.clients", "Attached IRC clients", "{client}")
	streams := gauge("evrd.web.streams", "Open web chat sockets", "{socket}")
	users := gauge("evrd.cluster.users", "Users this node owns", "{user}")
	nodes := gauge("evrd.cluster.nodes", "Live cluster nodes", "{node}")
	queue := gauge("evrd.history.queue", "History writes waiting", "{line}")
	up := gauge("evrd.upstream.up", "1 while an upstream connection is registered, per user and network", "")
	lag, err := meter.Float64ObservableGauge("evrd.upstream.lag", metric.WithDescription("Upstream round-trip lag, per user and network"), metric.WithUnit("s"))
	if err != nil {
		return err
	}
	_, err = meter.RegisterCallback(func(_ context.Context, o metric.Observer) error {
		if g.Upstreams != nil {
			for status, n := range g.Upstreams() {
				o.ObserveInt64(upstreams, n, metric.WithAttributes(attribute.String("status", status)))
			}
		}
		if g.Links != nil {
			for _, l := range g.Links() {
				attrs := metric.WithAttributes(attribute.String("user", l.User), attribute.String("network", l.Network))
				v := int64(0)
				if l.Up {
					v = 1
				}
				o.ObserveInt64(up, v, attrs)
				if l.Lag >= 0 {
					o.ObserveFloat64(lag, l.Lag.Seconds(), attrs)
				}
			}
		}
		for inst, f := range map[metric.Int64ObservableGauge]func() int64{
			clients: g.Clients, streams: g.Streams, users: g.Users, nodes: g.Nodes, queue: g.Queue,
		} {
			if f != nil {
				o.ObserveInt64(inst, f())
			}
		}
		return nil
	}, upstreams, clients, streams, users, nodes, queue, up, lag)
	return err
}

// Result is a convenience for the common result attribute.
func Result(r string) metric.AddOption {
	return metric.WithAttributes(attribute.String("result", r))
}

// Attrs builds an option from key/value pairs.
func Attrs(kv ...string) metric.AddOption {
	if len(kv)%2 != 0 {
		panic(errors.New("telemetry.Attrs: odd number of arguments"))
	}
	as := make([]attribute.KeyValue, 0, len(kv)/2)
	for i := 0; i < len(kv); i += 2 {
		as = append(as, attribute.String(kv[i], kv[i+1]))
	}
	return metric.WithAttributes(as...)
}
