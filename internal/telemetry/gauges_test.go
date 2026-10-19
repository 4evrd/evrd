package telemetry

import (
	"context"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

// Every gauge with a function set reports its value at collection.
func TestGaugesReportTheirValues(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	defer mp.Shutdown(context.Background())
	old := meter
	meter = mp.Meter("test") // RegisterGauges registers on the package meter
	defer func() { meter = old }()

	n := int64(0)
	next := func() int64 { n++; return n }
	err := RegisterGauges(Gauges{
		Upstreams: func() map[string]int64 { return map[string]int64{"connected": 2, "reconnecting": 1} },
		Links: func() []Link {
			return []Link{{User: "1", Network: "2", Up: true, Lag: 1500 * time.Millisecond}, {User: "1", Network: "3", Lag: -1}}
		},
		Clients: next, Streams: next, Users: next, Nodes: next, Queue: next,
	})
	if err != nil {
		t.Fatal(err)
	}
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatal(err)
	}
	ints := map[string]int64{}
	var lags []float64
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			switch d := m.Data.(type) {
			case metricdata.Gauge[int64]:
				for _, p := range d.DataPoints {
					key := m.Name
					for _, kv := range p.Attributes.ToSlice() {
						key += " " + string(kv.Key) + "=" + kv.Value.AsString()
					}
					ints[key] = p.Value
				}
			case metricdata.Gauge[float64]:
				for _, p := range d.DataPoints {
					lags = append(lags, p.Value)
				}
			}
		}
	}
	for key, want := range map[string]int64{
		"evrd.upstream.connections status=connected":    2,
		"evrd.upstream.connections status=reconnecting": 1,
		"evrd.upstream.up network=2 user=1":             1,
		"evrd.upstream.up network=3 user=1":             0,
	} {
		if got, ok := ints[key]; !ok || got != want {
			t.Errorf("%s = %d (%v), want %d", key, got, ok, want)
		}
	}
	sum := int64(0)
	for _, name := range []string{"evrd.irc.clients", "evrd.web.streams", "evrd.cluster.users", "evrd.cluster.nodes", "evrd.history.queue"} {
		v, ok := ints[name]
		if !ok {
			t.Errorf("%s not reported", name)
		}
		sum += v
	}
	if sum != 1+2+3+4+5 {
		t.Errorf("each gauge reads its own function once: sum %d", sum)
	}
	if len(lags) != 1 || lags[0] != 1.5 {
		t.Errorf("lag is reported only while known, in seconds: %v", lags)
	}
}

func TestAttrs(t *testing.T) {
	cfg := metric.NewAddConfig([]metric.AddOption{Attrs("a", "1", "b", "2")})
	set := cfg.Attributes()
	if v, ok := set.Value(attribute.Key("b")); !ok || v.AsString() != "2" || set.Len() != 2 {
		t.Fatalf("attrs: %v", set.ToSlice())
	}
	cfg = metric.NewAddConfig([]metric.AddOption{Result("error")})
	res := cfg.Attributes()
	if v, _ := res.Value("result"); v.AsString() != "error" {
		t.Fatal("Result")
	}
	defer func() {
		if recover() == nil {
			t.Fatal("an odd number of arguments should panic")
		}
	}()
	Attrs("lonely")
}
