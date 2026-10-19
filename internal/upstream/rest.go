package upstream

// Servers that refuse this address rest: a refusal (or a throttle) is per
// server, so the network's other servers are tried meanwhile, and evrd only
// waits when every server is resting.

import (
	"fmt"
	"math/rand/v2"
	"net"
	"strconv"
	"time"

	"github.com/4evrd/evrd/internal/store"
)

// serverRest is how long a server rests, and the refusals in a row from it
// (each doubling its rest).
type serverRest struct {
	until  time.Time
	bans   int
	banned bool // the last rest was for a refusal, not a throttle
}

// rests holds the resting servers by host:port. It lives on the Conn, in
// memory: a restart forgets it.
type rests map[string]serverRest

func serverKey(sv store.Server) string { return net.JoinHostPort(sv.Host, strconv.Itoa(sv.Port)) }

// jitter spreads d between half and 1.5×, so connections dropped together
// don't all retry together.
func jitter(d time.Duration) time.Duration { return d/2 + time.Duration(rand.Int64N(int64(d))) }

// ended updates the server at key's rest when a connection to it ended
// with msg: a link that stayed up (stable) forgets its bans, and a refusal
// or throttle rests it. It returns how long the server rests (0: it doesn't).
func (r rests) ended(key, msg string, stable bool, now time.Time) time.Duration {
	if stable && !bannedRE.MatchString(msg) {
		delete(r, key)
	}
	return r.record(key, msg, now)
}

// record rests the server at key when msg says it refused or throttled this
// address, and returns how long (0 when it didn't).
func (r rests) record(key, msg string, now time.Time) time.Duration {
	s := r[key]
	var d time.Duration
	switch {
	case bannedRE.MatchString(msg):
		s.bans = min(s.bans+1, 8) // (the rest stops growing well before)
		floor := min(banWait<<(s.bans-1), maxBanWait)
		d = floor + time.Duration(rand.Int64N(int64(floor/10)))
		s.banned = true
	case throttledRE.MatchString(msg):
		d = throttleWait + time.Duration(rand.Int64N(int64(throttleWait/4)))
		s.banned = false
	default:
		return 0
	}
	s.until = now.Add(d)
	r[key] = s
	return d
}

// pick is the index of the next server to try from start on: the first
// that isn't resting, or (all resting) the one whose rest ends first.
func (r rests) pick(servers []store.Server, start int, now time.Time) int {
	best := -1
	for i := range servers {
		j := (start + i) % len(servers)
		s, ok := r[serverKey(servers[j])]
		if !ok || !s.until.After(now) {
			return j
		}
		if best < 0 || s.until.Before(r[serverKey(servers[best])].until) {
			best = j
		}
	}
	return best
}

// allResting reports when the first rest ends if every server is resting,
// and whether they were all refusals.
func (r rests) allResting(servers []store.Server, now time.Time) (time.Time, bool, bool) {
	var first time.Time
	banned := true
	for _, sv := range servers {
		s, ok := r[serverKey(sv)]
		if !ok || !s.until.After(now) {
			return time.Time{}, false, false
		}
		if first.IsZero() || s.until.Before(first) {
			first = s.until
		}
		banned = banned && s.banned
	}
	return first, banned, len(servers) > 0
}

// retryWait is how long to wait before the next try, given the backoff:
// the jittered backoff while some server isn't resting, else until the
// first rest ends (a long backoff isn't cut short).
func (r rests) retryWait(backoff time.Duration, servers []store.Server, now time.Time) time.Duration {
	wait := jitter(backoff)
	if first, _, all := r.allResting(servers, now); all {
		wait = max(wait, first.Sub(now))
	}
	return wait
}

// restNote is the connection log's line on what evrd does next: with
// servers of the network to spare, which one rests and for how long.
func restNote(network, server, msg string, rest, wait time.Duration, servers int, allRest, allBanned bool) string {
	switch {
	case allRest && servers > 1:
		why := "refuse this address"
		if !allBanned {
			why = "refuse or limit connections from this address"
		}
		return fmt.Sprintf("All servers of %s %s: trying again in %s", network, why, roundDur(wait))
	case rest > 0 && servers > 1:
		host, _, err := net.SplitHostPort(server)
		if err != nil {
			host = server
		}
		why := "refuses this address"
		if !bannedRE.MatchString(msg) {
			why = "limits connections from this address"
		}
		return fmt.Sprintf("%s %s: resting it for %s, trying the network's other servers", host, why, roundDur(rest))
	}
	return "Trying again in " + roundDur(wait)
}
