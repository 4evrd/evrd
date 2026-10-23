package handlers

import (
	"testing"
	"time"

	"github.com/4evrd/evrd/internal/metricsq"
)

func TestWithLatestAddsNowToTheMatchingSeries(t *testing.T) {
	now := time.Unix(10_000, 0)
	ss := []metricsq.Series{{Labels: map[string]string{"status": "connected"}, Points: [][2]float64{{9_000, 3}}}}
	cur := []metricsq.Series{
		{Labels: map[string]string{"status": "connected"}, Points: [][2]float64{{10_000, 4}}},
		{Labels: map[string]string{"status": "reconnecting"}, Points: [][2]float64{{10_000, 1}}}, // only just appeared
	}
	got := withLatest(ss, cur, now)
	if len(got) != 2 || len(got[0].Points) != 2 || got[0].Points[1] != [2]float64{10_000, 4} {
		t.Fatalf("connected: %+v", got)
	}
	if got[1].Labels["status"] != "reconnecting" || got[1].Points[0] != [2]float64{10_000, 1} {
		t.Fatalf("a series that's only in the instant result: %+v", got)
	}
}
