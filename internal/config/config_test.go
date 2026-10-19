package config

import (
	"testing"
	"time"
)

func TestExportIntervalDefaultsTo15s(t *testing.T) {
	c := Default()
	if c.Metrics.Interval != 15*time.Second {
		t.Fatalf("default otel_interval %v", c.Metrics.Interval)
	}
	s, _ := SpecFor("otel_interval")
	if got := s.get(c); got != "15s" {
		t.Fatalf("settings page shows %q", got)
	}
}
