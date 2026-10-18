package client

import (
	"testing"
	"time"
)

func TestStrftime(t *testing.T) {
	ts := time.Date(2026, 3, 7, 14, 5, 9, 0, time.UTC)
	for f, want := range map[string]string{
		"[%H:%M:%S]":    "[14:05:09]",
		"%Y-%m-%d %I%p": "2026-03-07 02PM",
		"%a %b %e":      "Sat Mar  7",
		"100%% %T":      "100% 14:05:09",
		"%Q stays":      "%Q stays",
	} {
		if got := strftime(f, ts); got != want {
			t.Errorf("%q: got %q, want %q", f, got, want)
		}
	}
}
