package api

// A network's health page: its settings at a glance and its live link, how
// the link held up over a range (uptime, lag, chat lines and disconnects per
// hour or day, read back from the metrics store), its recent events and its
// channels.

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/4evrd/evrd/internal/metricsq"
	"github.com/4evrd/evrd/internal/store"
)

const metricLag = "evrd_upstream_lag_seconds"

// healthRanges are the ranges the page offers: how far back, and the slot
// each point covers.
var healthRanges = map[string]struct{ span, step time.Duration }{
	"24h": {24 * time.Hour, time.Hour},
	"7d":  {7 * 24 * time.Hour, time.Hour},
	"30d": {30 * 24 * time.Hour, 24 * time.Hour},
}

// healthRange is ?range= as a key, a span and a step: 24h when it's missing
// or not one of healthRanges.
func healthRange(key string) (string, time.Duration, time.Duration) {
	rg, ok := healthRanges[key]
	if !ok {
		key, rg = "24h", healthRanges["24h"]
	}
	return key, rg.span, rg.step
}

// healthEvents is how many of the network's events the page shows.
const healthEvents = 25

func (s *Server) handleNetworkHealth(w http.ResponseWriter, r *http.Request) {
	n, ok := s.ownedNetwork(w, r)
	if !ok {
		return
	}
	out, err := s.healthOf(r.Context(), n, r.URL.Query().Get("range"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// handleAdminNetworkHealth is the same page for another user's network, for
// admins (GET /api/admin/users/{id}/networks/{nid}/health).
func (s *Server) handleAdminNetworkHealth(w http.ResponseWriter, r *http.Request) {
	n, ok := s.adminNetwork(w, r)
	if !ok {
		return
	}
	out, err := s.healthOf(r.Context(), n, r.URL.Query().Get("range"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// healthOf is everything a network's health page shows. The live state
// needs the owner's node; the series come from the metrics store, and
// without one (or when it fails) metrics is false with the reason.
func (s *Server) healthOf(ctx context.Context, n store.Network, rangeKey string) (map[string]any, error) {
	key, span, step := healthRange(rangeKey)
	now := time.Now()
	// slots end on whole steps (days on UTC midnight); the last is in progress
	end := now.Truncate(step).Add(step)
	start := end.Add(-span)
	slots := int(span / step)

	evs, err := s.st().UserEvents(n.UserID, start, 500)
	if err != nil {
		return nil, err
	}
	var mine []store.UserEvent
	for _, e := range evs {
		if e.NetworkID == n.ID && len(mine) < healthEvents {
			mine = append(mine, e)
		}
	}

	d := s.networkDTO(n)
	live := map[string]any{"nick": d["currentNick"]}
	for _, k := range []string{"status", "server", "lag", "lastError", "lastErrorAt", "hint", "attempts", "retryIn", "connectedAt", "auth"} {
		if v, ok := d[k]; ok {
			live[k] = v
		}
	}
	username := n.Username
	if username == "" {
		username = n.Nick
	}
	out := map[string]any{
		"network": map[string]any{
			"id": n.ID, "name": n.Name, "host": n.Host, "port": n.Port, "tls": n.TLS, "enabled": n.Enabled,
			"nick": n.Nick, "username": username, "realname": n.Realname, "authMethod": n.Auth(),
			"servers": len(n.ServerList()),
		},
		"live":     live,
		"channels": s.healthChannels(n),
		"events":   eventsDTO(mine),
		"range":    key, "start": start.Unix(), "step": int(step.Seconds()), "points": slots,
		"now": now.Unix(),
	}

	h := s.healthMetrics(ctx, n, start, step, slots, now)
	out["metrics"] = h.err == ""
	if h.err != "" {
		out["reason"] = h.err
		return out, nil
	}
	out["series"] = map[string]any{
		"uptime": h.uptime, "lagAvg": h.lagAvg, "lagMax": h.lagMax,
		"lines": h.lines, "dropped": h.dropped, "failed": h.failed,
	}
	out["summary"] = h.summary()
	return out, nil
}

// adminNetwork loads the {nid} network of the {id} user, for admins,
// answering 404 itself.
func (s *Server) adminNetwork(w http.ResponseWriter, r *http.Request) (store.Network, bool) {
	u, ok := s.adminTarget(w, r)
	if !ok {
		return store.Network{}, false
	}
	nid, err := strconv.ParseInt(r.PathValue("nid"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusNotFound, "no such network")
		return store.Network{}, false
	}
	n, err := s.st().NetworkByID(nid)
	if err != nil || n.UserID != u.ID {
		writeErr(w, http.StatusNotFound, "no such network")
		return store.Network{}, false
	}
	return n, true
}

// healthChannels is the network's channels with their unread lines and
// mentions: the ones joined while it's connected (with members), else the
// saved ones and any with something buffered.
func (s *Server) healthChannels(n store.Network) []map[string]any {
	conn, live := s.b.ConnFor(n.ID)
	names := map[string]string{} // lower-case → as written
	add := func(name string) {
		if isChannelName(name) && names[strings.ToLower(name)] == "" {
			names[strings.ToLower(name)] = name
		}
	}
	if live {
		for _, ch := range conn.Channels() {
			if conn.Joined(ch) {
				add(ch)
			}
		}
	} else {
		for _, ch := range n.Channels {
			add(ch)
		}
	}
	bufs := map[string][2]int{}
	for _, b := range s.b.Buffers(n.ID) {
		if !live {
			add(b.Name)
		}
		bufs[strings.ToLower(b.Name)] = [2]int{b.Unread, b.Mentions}
	}
	out := make([]map[string]any, 0, len(names))
	for k, name := range names {
		row := map[string]any{"name": name, "unread": bufs[k][0], "mentions": bufs[k][1]}
		if live {
			row["members"] = conn.MemberCount(name)
		}
		out = append(out, row)
	}
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i]["name"].(string)) < strings.ToLower(out[j]["name"].(string))
	})
	return out
}

type healthMetrics struct {
	err            string
	uptime         []*float64 // fraction of each slot connected; nil: no data
	lagAvg, lagMax []*float64 // ms
	lines          []float64
	dropped        []float64
	failed         []float64
}

// healthMetrics reads a network's slots from the metrics store, the
// queries side by side.
func (s *Server) healthMetrics(ctx context.Context, n store.Network, start time.Time, step time.Duration, slots int, now time.Time) healthMetrics {
	mq := s.metrics.Load()
	if mq == nil {
		return healthMetrics{err: "metrics aren't configured"}
	}
	ctx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	sel := fmt.Sprintf("{user=%s,network=%s}", metricsq.Quote(strconv.FormatInt(n.UserID, 10)), metricsq.Quote(strconv.FormatInt(n.ID, 10)))
	// gauges are reported per process: take the highest at each minute, so a
	// restart's new series doesn't count twice
	gauge := func(fn, metric string) func(string) string {
		return func(w string) string { return fmt.Sprintf("%s((max(%s%s))[%s:1m])", fn, metric, sel, w) }
	}
	queries := []func(string) string{
		gauge("avg_over_time", metricUp),
		gauge("avg_over_time", metricLag),
		gauge("max_over_time", metricLag),
		func(w string) string { return fmt.Sprintf("sum(increase_pure(%s%s[%s]))", metricLines, sel, w) },
		func(w string) string {
			return fmt.Sprintf("sum by (kind) (increase_pure(%s%s[%s]))", metricDisconnects, sel, w)
		},
	}
	results := make([][]metricsq.Series, len(queries))
	errs := make([]error, len(queries))
	var wg sync.WaitGroup
	for i, q := range queries {
		wg.Go(func() { results[i], errs[i] = slotted(ctx, mq, q, start, step, slots, now) })
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			return healthMetrics{err: err.Error()}
		}
	}

	one := func(ss []metricsq.Series, scale float64) []*float64 {
		if len(ss) == 0 {
			return make([]*float64, slots)
		}
		return slotValues(ss[0], start, step, slots, scale)
	}
	h := healthMetrics{
		uptime: one(results[0], 1),
		lagAvg: one(results[1], 1000),
		lagMax: one(results[2], 1000),
		lines:  counts(one(results[3], 1)),
	}
	for i, v := range h.uptime {
		if v != nil {
			u := math.Round(*v*1000) / 1000
			h.uptime[i] = &u
		}
	}
	for _, l := range [][]*float64{h.lagAvg, h.lagMax} {
		for i, v := range l {
			if v != nil {
				ms := math.Round(*v)
				l[i] = &ms
			}
		}
	}
	h.dropped, h.failed = make([]float64, slots), make([]float64, slots)
	for _, se := range results[4] {
		c := counts(slotValues(se, start, step, slots, 1))
		// before kinds were recorded, only lost links were counted
		into := h.dropped
		if se.Labels["kind"] == "failed" {
			into = h.failed
		}
		for i, v := range c {
			into[i] += v
		}
	}
	return h
}

// summary is the range in a few numbers: the share of time connected (of
// the slots with data), the average and peak lag, and the totals.
func (h healthMetrics) summary() map[string]any {
	out := map[string]any{"lines": sum(h.lines), "dropped": sum(h.dropped), "failed": sum(h.failed)}
	if v, ok := mean(h.uptime); ok {
		out["uptimePct"] = math.Round(v*1000) / 10
	}
	if v, ok := mean(h.lagAvg); ok {
		out["lagAvg"] = math.Round(v)
	}
	peak := -1.0
	for _, v := range h.lagMax {
		if v != nil && *v > peak {
			peak = *v
		}
	}
	if peak >= 0 {
		out["lagPeak"] = peak
	}
	return out
}

func mean(vs []*float64) (float64, bool) {
	total, n := 0.0, 0
	for _, v := range vs {
		if v != nil {
			total += *v
			n++
		}
	}
	if n == 0 {
		return 0, false
	}
	return total / float64(n), true
}

func sum(vs []float64) float64 {
	t := 0.0
	for _, v := range vs {
		t += v
	}
	return t
}

// counts rounds a counter's slots, no data being none counted.
func counts(vs []*float64) []float64 {
	out := make([]float64, len(vs))
	for i, v := range vs {
		if v != nil {
			out[i] = math.Round(*v)
		}
	}
	return out
}

// slotted reads n slots from start, step apart: query(window) is over a
// window ending at a point. Slot i ends at start+(i+1)·step, so it's the
// point there; the last, in progress, ends in the future where the store has
// nothing, so it's an instant query at now over the part that has passed.
func slotted(ctx context.Context, mq *metricsq.Client, query func(window string) string, start time.Time, step time.Duration, n int, now time.Time) ([]metricsq.Series, error) {
	var series []metricsq.Series
	if n > 1 {
		var err error
		series, err = mq.Range(ctx, query(fmt.Sprintf("%ds", int(step.Seconds()))), start.Add(step), start.Add(time.Duration(n-1)*step), step)
		if err != nil {
			return nil, err
		}
	}
	elapsed := max(now.Sub(start.Add(time.Duration(n-1)*step)).Truncate(time.Second), time.Minute)
	current, err := mq.Instant(ctx, query(fmt.Sprintf("%ds", int(elapsed.Seconds()))))
	if err != nil {
		return nil, err
	}
	return withLatest(series, current, start.Add(time.Duration(n)*step)), nil
}

// slotValues puts a slotted series' points into its n slots, scaled; nil
// where there's no point.
func slotValues(se metricsq.Series, start time.Time, step time.Duration, n int, scale float64) []*float64 {
	out := make([]*float64, n)
	for _, p := range se.Points {
		i := int((int64(p[0])-start.Unix())/int64(step.Seconds())) - 1
		if i >= 0 && i < n && !math.IsNaN(p[1]) {
			v := p[1] * scale
			out[i] = &v
		}
	}
	return out
}
