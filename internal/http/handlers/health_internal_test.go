package handlers

import "testing"

func TestHealthSummaryWithoutData(t *testing.T) {
	h := healthMetrics{uptime: make([]*float64, 3), lagAvg: make([]*float64, 3), lagMax: make([]*float64, 3), lines: []float64{1, 2, 3}}
	s := h.summary()
	if _, ok := s["uptimePct"]; ok {
		t.Fatalf("no uptime without data: %v", s)
	}
	if _, ok := s["lagPeak"]; ok || s["lines"] != 6.0 {
		t.Fatalf("summary: %v", s)
	}
}
