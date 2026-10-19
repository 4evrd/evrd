package upstream

import (
	"testing"
	"time"
)

func TestRetryWait(t *testing.T) {
	// an ordinary failure: the jittered backoff (between half and 1.5×)
	for range 50 {
		if w, bans := retryWait(4*time.Second, "connection refused", 0); w < 2*time.Second || w >= 6*time.Second || bans != 0 {
			t.Fatalf("plain: %v, %d bans", w, bans)
		}
	}
	// told it reconnects too fast: two minutes at least, whatever the backoff
	if w, _ := retryWait(time.Second, "Closing Link: 198.51.100.1 (Reconnecting too fast, throttled.)", 0); w < throttleWait || w > throttleWait*5/4 {
		t.Fatalf("throttled: %v", w)
	}
	// banned: half an hour, doubling while bans go on, up to four hours
	bans := 0
	want := []time.Duration{30 * time.Minute, time.Hour, 2 * time.Hour, 4 * time.Hour, 4 * time.Hour}
	for i, floor := range want {
		var w time.Duration
		w, bans = retryWait(time.Second, "Closing Link: evrd (K-lined: no bouncers)", bans)
		if w < floor || w > floor*11/10 {
			t.Fatalf("ban %d: waited %v, want about %v", i+1, w, floor)
		}
	}
	if bans != 5 {
		t.Fatalf("bans: %d", bans)
	}
	for range 20 {
		_, bans = retryWait(time.Second, "You are banned from this server", bans)
	}
	if w, _ := retryWait(time.Second, "G-lined", bans); w < maxBanWait || w > maxBanWait*11/10 {
		t.Fatalf("many bans: %v", w)
	}
	// a long backoff isn't cut short by a throttle
	if w, _ := retryWait(maxBackoff, "throttled", 0); w < maxBackoff/2 {
		t.Fatalf("backoff kept: %v", w)
	}
}
