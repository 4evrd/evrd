package api

// The reconnects page: which of a user's networks lost their link or
// couldn't connect in the last day, how often and why. Counts and offline
// time come from the metrics store; the reasons from the activity feed and
// each network's live state.

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/4evrd/evrd/internal/auth"
	"github.com/4evrd/evrd/internal/metricsq"
	"github.com/4evrd/evrd/internal/store"
)

// linkEventKinds are the activity entries about a network's link.
var linkEventKinds = map[string]bool{"disconnected": true, "failed": true, "connected": true, "netsplit": true}

func (s *Server) handleReconnects(w http.ResponseWriter, r *http.Request) {
	out, err := s.reconnectsOf(r.Context(), auth.User(r).ID)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// handleAdminUserReconnects is the same page for another user, for admins.
func (s *Server) handleAdminUserReconnects(w http.ResponseWriter, r *http.Request) {
	u, ok := s.adminTarget(w, r)
	if !ok {
		return
	}
	out, err := s.reconnectsOf(r.Context(), u.ID)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// reconnectsOf is a user's last day of link trouble, per network: links
// dropped, connection attempts that failed, minutes offline, the hours it
// happened in, and the live state with the last error; then the events.
func (s *Server) reconnectsOf(ctx context.Context, userID int64) (map[string]any, error) {
	nets, err := s.st().ListNetworks(ctx, userID)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	end := now.Truncate(time.Hour).Add(time.Hour)
	start := end.Add(-24 * time.Hour)
	m := s.reconnectMetrics(ctx, userID, end)

	netsOut := make([]map[string]any, 0, len(nets))
	for _, n := range nets {
		id := strconv.FormatInt(n.ID, 10)
		d := s.networkDTO(n)
		row := map[string]any{"id": n.ID, "name": n.Name, "enabled": n.Enabled}
		for _, k := range []string{"status", "lastError", "lastErrorAt", "hint", "attempts", "retryIn", "connectedAt", "server"} {
			if v, ok := d[k]; ok {
				row[k] = v
			}
		}
		if m.available {
			dropped, failed := m.counts[id]["dropped"], m.counts[id]["failed"]
			// Counted before the kind was recorded: neither.
			other := m.counts[id][""]
			row["dropped"], row["failed"] = dropped, failed
			row["total"] = dropped + failed + other
			row["offlineMin"] = m.offline[id]
			hourly := m.hourly[id]
			if hourly == nil {
				hourly = make([]float64, 25)
			}
			row["hourly"] = hourly
		}
		netsOut = append(netsOut, row)
	}
	// The most trouble first.
	sort.SliceStable(netsOut, func(i, j int) bool {
		a, _ := netsOut[i]["total"].(int64)
		b, _ := netsOut[j]["total"].(int64)
		return a > b
	})

	evs, err := s.st().UserEvents(ctx, userID, now.Add(-24*time.Hour), 500)
	if err != nil {
		return nil, err
	}
	var link []store.UserEvent
	for _, e := range evs {
		if linkEventKinds[e.Kind] {
			link = append(link, e)
		}
	}
	return map[string]any{
		"available": m.available, "error": m.err,
		"start": start.Unix(), "step": 3600,
		"networks": netsOut,
		"events":   eventsDTO(link),
	}, nil
}

type reconnectMetrics struct {
	available bool
	err       string
	counts    map[string]map[string]int64 // network → kind → count
	offline   map[string]int64            // network → minutes
	hourly    map[string][]float64        // network → 25 hourly counts
}

// reconnectMetrics reads a user's last day of disconnects from the metrics
// store, by network.
func (s *Server) reconnectMetrics(ctx context.Context, userID int64, end time.Time) reconnectMetrics {
	out := reconnectMetrics{counts: map[string]map[string]int64{}, offline: map[string]int64{}, hourly: map[string][]float64{}}
	mq := s.metrics.Load()
	if mq == nil {
		out.err = "metrics aren't configured"
		return out
	}
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	user := metricsq.Quote(strconv.FormatInt(userID, 10))
	counts, err := mq.Instant(ctx, fmt.Sprintf(`sum by (network, kind) (increase_pure(%s{user=%s}[24h]))`, metricDisconnects, user))
	if err != nil {
		out.err = err.Error()
		return out
	}
	for _, c := range counts {
		net := c.Labels["network"]
		if out.counts[net] == nil {
			out.counts[net] = map[string]int64{}
		}
		out.counts[net][c.Labels["kind"]] += int64(math.Round(c.Points[0][1]))
	}
	offline, err := mq.Instant(ctx, fmt.Sprintf(`sum by (network) (count_over_time((%s{user=%s} == 0)[24h:1m]))`, metricUp, user))
	if err != nil {
		out.err = err.Error()
		return out
	}
	for _, o := range offline {
		out.offline[o.Labels["network"]] = int64(math.Round(o.Points[0][1]))
	}
	hourly, err := hourlyByNetwork(ctx, mq, func(window string) string {
		return fmt.Sprintf(`sum by (network) (increase_pure(%s{user=%s}[%s]))`, metricDisconnects, user, window)
	}, end)
	if err != nil {
		out.err = err.Error()
		return out
	}
	for _, se := range hourly {
		out.hourly[se.Labels["network"]] = hourSlots(se, end.Add(-24*time.Hour))
	}
	out.available = true
	return out
}
