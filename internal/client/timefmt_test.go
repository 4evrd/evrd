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
		"%y %F %R %j":   "26 2026-03-07 14:05 066",
		"%B %A %h %Z":   "March Saturday Mar UTC",
		"%z %s":         "+0000 1772892309",
		"trailing %":    "trailing %",
	} {
		if got := strftime(f, ts); got != want {
			t.Errorf("%q: got %q, want %q", f, got, want)
		}
	}
	// 12-hour clock around midnight
	for f, want := range map[string]string{
		"%I %p": "12 AM",
		"%d %e": "01  1",
	} {
		if got := strftime(f, time.Date(2026, 1, 1, 0, 30, 0, 0, time.UTC)); got != want {
			t.Errorf("midnight %q: got %q, want %q", f, got, want)
		}
	}
}
