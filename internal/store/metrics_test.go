package store

import (
	"context"
	"errors"
	"testing"

	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

// Every query is timed, by table, operation and result.
func TestQueriesAreMeasured(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	defer mp.Shutdown(context.Background())
	otel.SetMeterProvider(mp) // telemetry's instruments follow the global provider

	st := open(t)
	ctx := t.Context()
	u, err := st.CreateUser(ctx, User{Username: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	st.UserByID(ctx, u.ID)
	st.UserByID(ctx, u.ID+100) // not found: still "ok"

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(ctx, &rm); err != nil {
		t.Fatal(err)
	}
	got := map[string]uint64{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != "evrd.db.duration" {
				continue
			}
			for _, dp := range m.Data.(metricdata.Histogram[float64]).DataPoints {
				table, _ := dp.Attributes.Value("table")
				op, _ := dp.Attributes.Value("op")
				res, _ := dp.Attributes.Value("result")
				got[table.AsString()+" "+op.AsString()+" "+res.AsString()] += dp.Count
			}
		}
	}
	if got["users create ok"] != 1 || got["users query ok"] != 2 {
		t.Fatalf("measured: %v", got)
	}
}

// The caller's context reaches the query: a cancelled one stops it.
func TestQueriesTakeTheCallersContext(t *testing.T) {
	st := open(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := st.ListUsers(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("ListUsers on a cancelled context: %v", err)
	}
}
