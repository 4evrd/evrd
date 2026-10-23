package handlers

// The admin metrics page: every metric evrd exports, across all users and
// nodes, read back from the metrics store over a chosen range.

import (
	"context"
	"fmt"
	"maps"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/4evrd/evrd/internal/metricsq"
)

// metricRanges are the ranges the page offers: how far back, and the step
// between points (counters are counted per step).
var metricRanges = map[string]struct{ span, step time.Duration }{
	"6h":  {6 * time.Hour, 5 * time.Minute},
	"24h": {24 * time.Hour, 15 * time.Minute},
	"7d":  {7 * 24 * time.Hour, time.Hour},
}

// metricPanel is one chart: a title, a unit, and one query per line. A
// query's series are named by label (or by its own name when label is
// empty); names maps label values to something readable.
type metricPanel struct {
	ID, Title, Help, Unit string
	Counter               bool // lines are counts per step
	Queries               []metricQuery
}

type metricQuery struct {
	Name, Label, Expr string
	Scale             float64 // multiply values (0: 1)
	Names             map[string]string
	Prefix            string // before label values not in Names
}

// Gauges are reported by every evrd process with its version, so a restart
// into a new version starts new series: take the latest value per node.
const perNode = "max by (%s service_instance_id) (last_over_time(%s[%s]))"

func gaugeBy(labels, metric, step string) string {
	l := ""
	if labels != "" {
		l = labels + ","
	}
	return fmt.Sprintf("sum by (%s) ("+perNode+")", labels, l, metric, step)
}

func adminPanels(step string) []metricPanel {
	inc := func(metric, by string) string {
		return fmt.Sprintf("sum by (%s) (increase_pure(%s[%s]))", by, metric, step)
	}
	return []metricPanel{
		{ID: "messages", Title: "Messages relayed", Help: "IRC messages and channel events through the bouncer", Counter: true, Queries: []metricQuery{
			{Label: "direction", Expr: inc("evrd_irc_messages_total", "direction"), Names: map[string]string{"in": "from networks", "out": "from users"}},
		}},
		{ID: "links", Title: "Upstream connections", Help: "Connections to IRC networks, by state", Queries: []metricQuery{
			{Label: "status", Expr: gaugeBy("status", "evrd_upstream_connections", step)},
		}},
		{ID: "clients", Title: "Attached clients", Help: "IRC clients and open web chat sockets", Queries: []metricQuery{
			{Name: "IRC clients", Expr: gaugeBy("", "evrd_irc_clients", step)},
			{Name: "web sockets", Expr: gaugeBy("", "evrd_web_streams", step)},
		}},
		{ID: "lag", Title: "Upstream lag", Help: "Round-trip time to the IRC servers", Unit: "ms", Queries: []metricQuery{
			{Name: "average", Expr: fmt.Sprintf("avg(max by (user, network) (last_over_time(evrd_upstream_lag_seconds[%s])))", step), Scale: 1000},
			{Name: "worst", Expr: fmt.Sprintf("max(max by (user, network) (last_over_time(evrd_upstream_lag_seconds[%s])))", step), Scale: 1000},
		}},
		{ID: "connects", Title: "Connects and drops", Help: "Connection attempts to IRC networks, and connections lost", Counter: true, Queries: []metricQuery{
			{Label: "result", Expr: inc("evrd_upstream_connects_total", "result"), Names: map[string]string{"ok": "connected"}},
			{Name: "lost", Expr: fmt.Sprintf("sum(increase_pure(evrd_upstream_disconnects_total[%s]))", step)},
		}},
		{ID: "logins", Title: "Sign-ins", Help: "IRC client attaches and web app sign-ins, by result", Counter: true, Queries: []metricQuery{
			{Label: "result", Expr: inc("evrd_irc_logins_total", "result"), Prefix: "IRC "},
			{Label: "result", Expr: inc("evrd_web_logins_total", "result"), Prefix: "web "},
		}},
		{ID: "cluster", Title: "Users per node", Help: "Accounts each node runs", Queries: []metricQuery{
			{Label: "service_instance_id", Expr: fmt.Sprintf("max by (service_instance_id) (last_over_time(evrd_cluster_users[%s]))", step)},
		}},
	}
}

// AdminMetrics returns every panel for ?range= (6h, 24h or 7d), plus
// the busiest users and networks over it.
func (s *Handlers) AdminMetrics(w http.ResponseWriter, r *http.Request) {
	key := r.URL.Query().Get("range")
	rg, ok := metricRanges[key]
	if !ok {
		key, rg = "24h", metricRanges["24h"]
	}
	mq := s.metrics.Load()
	if mq == nil {
		writeJSON(w, http.StatusOK, map[string]any{"available": false, "error": "metrics aren't configured", "range": key})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()

	// Up to now: the newest points are the ones an admin looks for.
	end := time.Now()
	start := end.Add(-rg.span)
	step := fmt.Sprintf("%ds", int(rg.step.Seconds()))
	panels := adminPanels(step)

	type result struct {
		series []metricsq.Series
		err    error
	}
	results := make([][]result, len(panels))
	var wg sync.WaitGroup
	for i, p := range panels {
		results[i] = make([]result, len(p.Queries))
		for j, q := range p.Queries {
			wg.Add(1)
			go func() {
				defer wg.Done()
				ss, err := mq.Range(ctx, q.Expr, start, end, rg.step)
				if err == nil {
					// The store rounds a range's end down to a whole step, so
					// the latest minutes come from an instant query at now.
					if cur, err := mq.Instant(ctx, q.Expr); err == nil {
						ss = withLatest(ss, cur, end)
					}
				}
				results[i][j] = result{ss, err}
			}()
		}
	}
	var top []metricsq.Series
	var topErr error
	wg.Add(1)
	go func() {
		defer wg.Done()
		top, topErr = mq.Instant(ctx, fmt.Sprintf("topk(30, sum by (user, network) (increase_pure(evrd_irc_lines_total[%ds])))", int(rg.span.Seconds())))
	}()
	wg.Wait()

	n := int(rg.span/rg.step) + 1
	out := make([]map[string]any, 0, len(panels))
	var firstErr error
	for i, p := range panels {
		var lines []map[string]any
		for j, q := range p.Queries {
			res := results[i][j]
			if res.err != nil {
				if firstErr == nil {
					firstErr = res.err
				}
				continue
			}
			scale := q.Scale
			if scale == 0 {
				scale = 1
			}
			for _, se := range res.series {
				name := q.Name
				if q.Label != "" {
					v := se.Labels[q.Label]
					name = q.Prefix + strings.ReplaceAll(v, "_", " ")
					if nm, ok := q.Names[v]; ok {
						name = nm
					}
					if name == "" {
						name = q.Label + " unknown"
					}
				}
				pts := make([]*float64, n) // nil where there's no data
				for _, pt := range se.Points {
					k := int((int64(pt[0]) - start.Unix()) / int64(rg.step.Seconds()))
					if k >= 0 && k < n && !math.IsNaN(pt[1]) {
						v := math.Round(pt[1]*scale*100) / 100
						pts[k] = &v
					}
				}
				lines = append(lines, map[string]any{"name": name, "points": pts})
			}
		}
		if lines == nil {
			lines = []map[string]any{}
		}
		out = append(out, map[string]any{
			"id": p.ID, "title": p.Title, "help": p.Help, "unit": p.Unit, "counter": p.Counter, "series": lines,
		})
	}
	if firstErr != nil && topErr == nil {
		topErr = firstErr
	}

	// The busiest networks, with their owners' and networks' names.
	users, _ := s.acc.ListUsers(context.Background())
	nets, _ := s.acc.AllNetworks(context.Background())
	uname := map[string]string{}
	for _, u := range users {
		uname[strconv.FormatInt(u.ID, 10)] = u.Username
	}
	nname := map[string]string{}
	for _, nw := range nets {
		nname[strconv.FormatInt(nw.ID, 10)] = nw.Name
	}
	busiest := []map[string]any{}
	for _, se := range top {
		uid, nid := se.Labels["user"], se.Labels["network"]
		lines := 0.0
		if len(se.Points) > 0 {
			lines = se.Points[0][1]
		}
		if math.IsNaN(lines) || lines <= 0 {
			continue
		}
		if nname[nid] == "" {
			continue // a deleted network
		}
		id, _ := strconv.ParseInt(uid, 10, 64)
		busiest = append(busiest, map[string]any{
			"userId": id, "user": orDeleted(uname[uid]), "network": nname[nid], "lines": int64(math.Round(lines)),
		})
	}
	sort.Slice(busiest, func(i, j int) bool { return busiest[i]["lines"].(int64) > busiest[j]["lines"].(int64) })
	if len(busiest) > 10 {
		busiest = busiest[:10]
	}

	errText := ""
	if topErr != nil {
		errText = topErr.Error()
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"available": firstErr == nil || len(out) > 0, "error": errText, "range": key,
		"start": start.Unix(), "step": int(rg.step.Seconds()), "points": n,
		"panels": out, "busiest": busiest,
	})
}

// withLatest adds each instant result as the point at now to the series
// with the same labels (or as a series of its own).
func withLatest(ss, cur []metricsq.Series, now time.Time) []metricsq.Series {
	for _, c := range cur {
		if len(c.Points) == 0 {
			continue
		}
		p := [2]float64{float64(now.Unix()), c.Points[0][1]}
		found := false
		for i := range ss {
			if maps.Equal(ss[i].Labels, c.Labels) {
				ss[i].Points = append(ss[i].Points, p)
				found = true
				break
			}
		}
		if !found {
			ss = append(ss, metricsq.Series{Labels: c.Labels, Points: [][2]float64{p}})
		}
	}
	return ss
}

func orDeleted(s string) string {
	if s == "" {
		return "(deleted)"
	}
	return s
}
