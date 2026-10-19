package upstream

import (
	"strings"
	"testing"
	"time"

	"github.com/4evrd/evrd/internal/store"
)

var (
	duSe   = store.Server{Host: "irc.du.se", Port: 6667}
	colo   = store.Server{Host: "irc.colosolutions.net", Port: 9999}
	twoSvs = []store.Server{duSe, colo}
	t0     = time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
)

const (
	notAuthorised = "Closing Link: admin.floretos.com (You are not authorised to use this server)"
	kLined        = "Closing Link: evrd (K-lined: no bouncers)"
	throttled     = "Closing Link: 198.51.100.1 (Reconnecting too fast, throttled.)"
)

func TestRetryWaitWithoutRests(t *testing.T) {
	// an ordinary failure: the jittered backoff (between half and 1.5×)
	r := rests{}
	for range 50 {
		if d := r.record(serverKey(duSe), "connection refused", t0); d != 0 {
			t.Fatalf("a plain failure rested the server: %v", d)
		}
		if w := r.retryWait(4*time.Second, twoSvs, t0); w < 2*time.Second || w >= 6*time.Second {
			t.Fatalf("plain: %v", w)
		}
	}
	if len(r) != 0 {
		t.Fatalf("rests: %+v", r)
	}
}

func TestARefusingServerRestsWhileTheOthersAreTried(t *testing.T) {
	r := rests{}
	// irc.du.se refuses: it rests half an hour, the next try goes to the
	// other server after the ordinary backoff
	d := r.record(serverKey(duSe), notAuthorised, t0)
	if d < banWait || d > banWait*11/10 {
		t.Fatalf("rest: %v", d)
	}
	if i := r.pick(twoSvs, 0, t0); i != 1 {
		t.Fatalf("picked %d, want the other server", i)
	}
	if i := r.pick(twoSvs, 1, t0); i != 1 {
		t.Fatalf("picked %d from the other server on", i)
	}
	if w := r.retryWait(time.Second, twoSvs, t0); w >= 1500*time.Millisecond {
		t.Fatalf("waited %v with a server to spare", w)
	}
	if _, _, all := r.allResting(twoSvs, t0); all {
		t.Fatal("all resting with one to spare")
	}
	// the other refuses too, a little later: wait until the first comes back
	later := t0.Add(time.Minute)
	r.record(serverKey(colo), "the server refused the connection: *** Banned: Please use another server.", later)
	// (the rests are jittered: pin them, so irc.du.se's surely ends first)
	for k, at := range map[string]time.Time{serverKey(duSe): t0.Add(banWait), serverKey(colo): later.Add(banWait)} {
		sr := r[k]
		sr.until = at
		r[k] = sr
	}
	first, banned, all := r.allResting(twoSvs, later)
	if !all || !banned || !first.Equal(r[serverKey(duSe)].until) {
		t.Fatalf("all resting: %v %v %v", first, banned, all)
	}
	if w := r.retryWait(time.Second, twoSvs, later); w != first.Sub(later) {
		t.Fatalf("waited %v, want %v", w, first.Sub(later))
	}
	if i := r.pick(twoSvs, 1, later); i != 0 {
		t.Fatalf("picked %d, want the one whose rest ends first", i)
	}
	// once its rest is over, it's tried again
	back := first.Add(time.Second)
	if i := r.pick(twoSvs, 1, back); i != 0 {
		t.Fatalf("picked %d after the rest", i)
	}
	if w := r.retryWait(time.Second, twoSvs, back); w >= 1500*time.Millisecond {
		t.Fatalf("waited %v after the rest", w)
	}
	// a long backoff isn't cut short by a short rest
	r.record(serverKey(duSe), throttled, back)
	if w := r.retryWait(maxBackoff, twoSvs, back); w < maxBackoff/2 {
		t.Fatalf("backoff kept: %v", w)
	}
}

func TestBansDoublePerServer(t *testing.T) {
	r := rests{}
	now := t0
	want := []time.Duration{30 * time.Minute, time.Hour, 2 * time.Hour, 4 * time.Hour, 4 * time.Hour}
	for i, floor := range want {
		d := r.record(serverKey(duSe), kLined, now)
		if d < floor || d > floor*11/10 {
			t.Fatalf("ban %d: rested %v, want about %v", i+1, d, floor)
		}
		now = now.Add(d)
	}
	if r[serverKey(duSe)].bans != 5 {
		t.Fatalf("bans: %+v", r[serverKey(duSe)])
	}
	for range 20 {
		r.record(serverKey(duSe), "You are banned from this server", now)
	}
	if d := r.record(serverKey(duSe), "G-lined", now); d < maxBanWait || d > maxBanWait*11/10 {
		t.Fatalf("many bans: %v", d)
	}
	// the other server's count is its own
	if d := r.record(serverKey(colo), kLined, now); d > banWait*11/10 {
		t.Fatalf("the other server's first ban: %v", d)
	}
}

func TestAThrottledServerRests(t *testing.T) {
	r := rests{}
	d := r.record(serverKey(colo), throttled, t0)
	if d < throttleWait || d > throttleWait*5/4 {
		t.Fatalf("throttled: %v", d)
	}
	if i := r.pick(twoSvs, 1, t0); i != 0 {
		t.Fatalf("picked %d", i)
	}
	// alone, a throttled server is waited for, as before
	one := []store.Server{colo}
	if w := r.retryWait(time.Second, one, t0); w != d {
		t.Fatalf("waited %v, want %v", w, d)
	}
	if _, banned, all := r.allResting(one, t0); !all || banned {
		t.Fatalf("all %v, banned %v", all, banned)
	}
	// a throttle keeps the count of bans
	r.record(serverKey(colo), kLined, t0)
	r.record(serverKey(colo), throttled, t0)
	if s := r[serverKey(colo)]; s.bans != 1 || s.banned {
		t.Fatalf("after a throttle: %+v", s)
	}
	if _, _, all := r.allResting(nil, t0); all {
		t.Fatal("no servers are all resting")
	}
}

func TestRestNote(t *testing.T) {
	for _, tc := range []struct {
		msg            string
		rest, wait     time.Duration
		servers        int
		allRest, alBan bool
		want           string
	}{
		{notAuthorised, 30 * time.Minute, time.Second, 2, false, false, "irc.du.se refuses this address: resting it for 30m, trying the network's other servers"},
		{throttled, 2 * time.Minute, time.Second, 2, false, false, "irc.du.se limits connections from this address: resting it for 2m"},
		{notAuthorised, 30 * time.Minute, 30 * time.Minute, 2, true, true, "All servers of EFnet refuse this address: trying again in 30m"},
		{throttled, 2 * time.Minute, 2 * time.Minute, 2, true, false, "All servers of EFnet refuse or limit connections from this address: trying again in 2m"},
		{notAuthorised, 30 * time.Minute, 30 * time.Minute, 1, true, true, "Trying again in 30m"},
		{"connection refused", 0, 2 * time.Second, 2, false, false, "Trying again in 2s"},
	} {
		if got := restNote("EFnet", "irc.du.se:6667", tc.msg, tc.rest, tc.wait, tc.servers, tc.allRest, tc.alBan); !strings.HasPrefix(got, tc.want) {
			t.Errorf("%q: %q, want %q", tc.msg, got, tc.want)
		}
	}
	if got := restNote("EFnet", "odd", kLined, time.Minute, time.Second, 2, false, false); !strings.HasPrefix(got, "odd refuses") {
		t.Errorf("unsplittable server: %q", got)
	}
}

func TestAllServersRefusingWaitsForTheFirst(t *testing.T) {
	ban := "ERROR :Closing Link: 203.0.113.9 (You are not authorised to use this server)\r\n"
	h1, p1 := sayAndClose(t, ban)
	h2, p2 := sayAndClose(t, "ERROR :Closing Link: 203.0.113.9 (*** Banned: Please use another server.)\r\n")
	var rec logRec
	c := New(store.Network{Name: "EFnet", Nick: "dora", Servers: []store.Server{{Host: h1, Port: p1}, {Host: h2, Port: p2}}}, quiet, Options{OnLog: rec.add})
	run(t, c)
	eventually(t, "the first server rested", func() bool {
		return rec.has("evrd", h1+" refuses this address: resting it for 3", false)
	})
	eventually(t, "both servers resting", func() bool { return c.Health().Attempts >= 2 })
	if h := c.Health(); h.RetryIn < banWait-time.Minute || h.RetryIn > banWait*11/10 {
		t.Fatalf("retry in %v", h.RetryIn)
	}
	if !rec.has("evrd", "All servers of EFnet refuse this address: trying again in", false) {
		t.Fatalf("log: %+v", rec.all())
	}
}

func TestAStableConnectionEndsTheRest(t *testing.T) {
	r := rests{}
	r.ended(serverKey(duSe), kLined, false, t0)
	r.ended(serverKey(duSe), kLined, false, t0)
	// a link that stayed up, then dropped: the server's bans are forgotten
	if d := r.ended(serverKey(duSe), "ping timeout", true, t0); d != 0 || len(r) != 0 {
		t.Fatalf("after a stable link: rest %v, %+v", d, r)
	}
	if d := r.ended(serverKey(duSe), kLined, false, t0); d > banWait*11/10 {
		t.Fatalf("after a stable link, a ban rests %v", d)
	}
	// killed with a ban after staying up: still a ban, and counted
	if d := r.ended(serverKey(duSe), kLined, true, t0); d < time.Hour {
		t.Fatalf("a ban after a stable link rests %v", d)
	}
	// throttled after a stable link: rested, the bans forgotten
	if d := r.ended(serverKey(duSe), throttled, true, t0); d < throttleWait || r[serverKey(duSe)].bans != 0 {
		t.Fatalf("throttled after a stable link: %v, %+v", d, r[serverKey(duSe)])
	}
}
