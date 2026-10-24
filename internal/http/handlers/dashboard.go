package handlers

// The overview pages: a user's dashboard (their networks and channels with
// unread state, link health, attached clients, activity, and traffic read
// back from the metrics store) and the admin overview (the cluster, every
// account's placement, what needs attention, upstream networks, storage).

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/4evrd/evrd/internal/auth"
	"github.com/4evrd/evrd/internal/bouncer"
	"github.com/4evrd/evrd/internal/messages"
	"github.com/4evrd/evrd/internal/metricsq"
	"github.com/4evrd/evrd/internal/store"
	"github.com/4evrd/evrd/internal/upstream"
	"github.com/4evrd/evrd/internal/version"
)

// Metric names as VictoriaMetrics stores them (OpenTelemetry names with
// Prometheus naming: evrd.irc.lines -> evrd_irc_lines_total).
const (
	metricLines       = "evrd_irc_lines_total"
	metricDisconnects = "evrd_upstream_disconnects_total"
	metricUp          = "evrd_upstream_up"
)

// SetMetrics sets where dashboards read metrics from (nil: no charts).
func (s *Handlers) SetMetrics(m *metricsq.Client) { s.metrics.Store(m) }

func (s *Handlers) MyClients(w http.ResponseWriter, r *http.Request) {
	u := auth.User(r)
	writeJSON(w, http.StatusOK, map[string]any{
		"clients": s.clientsDTO(u.ID), "limit": s.acc.LimitsFor(u).MaxClients,
	})
}

// dropsPeriod is how far back the IRC clients page and the admin overview
// count client disconnects.
const dropsPeriod = 24 * time.Hour

// MyClientDrops is how the user's IRC clients disconnected in the last day,
// by device: how often, mostly why, and how long they were typically gone.
func (s *Handlers) MyClientDrops(w http.ResponseWriter, r *http.Request) {
	u := auth.User(r)
	devices, err := s.b.ClientDrops(r.Context(), u.ID, time.Now().Add(-dropsPeriod))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"devices": devices, "hours": int(dropsPeriod / time.Hour)})
}

func (s *Handlers) clientsDTO(userID int64) []map[string]any {
	out := []map[string]any{}
	for _, c := range s.b.ClientsOf(userID) {
		out = append(out, map[string]any{
			"id": c.ID, "kind": c.Kind, "software": c.Software, "device": c.Device,
			"networks": c.Networks, "ip": c.IP, "since": c.Since.Unix(),
		})
	}
	return out
}

func (s *Handlers) MyEvents(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	evs, err := s.b.UserEvents(r.Context(), auth.User(r).ID, time.Now().AddDate(0, 0, -30), limit)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, eventsDTO(s.printer(r), evs))
}

func eventsDTO(p *messages.Printer, evs []store.UserEvent) []map[string]any {
	out := []map[string]any{}
	for _, e := range evs {
		title, detail := bouncer.EventText(p, e)
		d := map[string]any{"at": e.At.Unix(), "kind": e.Kind, "title": title, "detail": detail}
		if e.NetworkID != 0 {
			d["network"] = e.NetworkID
		}
		out = append(out, d)
	}
	return out
}

// Overview is everything the user's overview page shows, in one call.
// It runs on the user's owner node (live state) and reads traffic from the
// metrics store.
func (s *Handlers) Overview(w http.ResponseWriter, r *http.Request) {
	ov, err := s.overviewOf(r.Context(), s.printer(r), auth.User(r))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, ov)
}

// AdminUserOverview is the same overview of another user, for admins,
// with their web sign-ins and client passwords. Served on their owner node.
func (s *Handlers) AdminUserOverview(w http.ResponseWriter, r *http.Request) {
	u, ok := s.adminTarget(w, r)
	if !ok {
		return
	}
	ov, err := s.overviewOf(r.Context(), s.printer(r), u)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	ov["usage"] = map[string]any{"networks": len(ov["networks"].([]map[string]any))}
	ov["sessions"] = s.sessionsDTO(u.ID, "")
	cps, _ := s.acc.ListClientPasswords(r.Context(), u.ID)
	pws := make([]map[string]any, 0, len(cps))
	for _, c := range cps {
		pws = append(pws, map[string]any{"name": c.Name, "createdAt": c.CreatedAt.Unix(), "lastUsedAt": unix(c.LastUsedAt)})
	}
	ov["clientPasswords"] = pws
	writeJSON(w, http.StatusOK, ov)
}

// overviewOf is everything a user's overview page shows. It needs the
// user's live state, so it runs on their owner node.
// p is the language to say evrd's own words in.
func (s *Handlers) overviewOf(ctx context.Context, p *messages.Printer, u store.User) (map[string]any, error) {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	nets, err := s.acc.ListNetworks(ctx, u.ID)
	if err != nil {
		return nil, err
	}
	lim := s.acc.LimitsFor(u)

	traffic := s.userTraffic(ctx, u.ID, nets)
	var total float64
	for _, v := range traffic.perNet {
		total += v
	}

	var lagSum, lagN, mentions int64
	backlogFill, backlogN := 0.0, 0
	netsOut := make([]map[string]any, 0, len(nets))
	now := time.Now()
	for _, n := range nets {
		d := s.networkDTO(p, n)
		conn, live := s.b.ConnFor(n.ID)
		if l, ok := d["lag"].(int64); ok {
			lagSum += l
			lagN++
		}
		var bufs []map[string]any
		unread := 0
		netFill, netFillN := 0.0, 0
		for _, b := range s.b.Buffers(n.ID) {
			unread += b.Unread
			mentions += int64(b.Mentions)
			if lim.BacklogLines > 0 {
				if c, err := s.b.History().Count(n.ID, b.Name, lim.BacklogLines); err == nil {
					netFill += float64(c) / float64(lim.BacklogLines)
					netFillN++
				}
			}
			if !isChannelName(b.Name) || (live && !conn.Joined(b.Name)) {
				continue
			}
			bd := map[string]any{"name": b.Name, "unread": b.Unread, "mentions": b.Mentions, "rate": s.channelRate(n.ID, b.Name, now)}
			if live {
				bd["members"] = conn.MemberCount(b.Name)
			}
			bufs = append(bufs, bd)
		}
		if live {
			// Joined channels with nothing recorded yet.
			for _, ch := range conn.Channels() {
				if !conn.Joined(ch) {
					continue
				}
				found := false
				for _, b := range bufs {
					if strings.EqualFold(b["name"].(string), ch) {
						found = true
					}
				}
				if !found {
					bufs = append(bufs, map[string]any{"name": ch, "unread": 0, "mentions": 0, "rate": 0, "members": conn.MemberCount(ch)})
				}
			}
		}
		if bufs == nil {
			bufs = []map[string]any{}
		}
		d["buffers"] = bufs
		t := traffic.perNet[n.ID]
		d["traffic24h"] = int64(math.Round(t))
		d["trafficPct"] = pct(t, total)
		if netFillN > 0 {
			d["backlogPct"] = int(math.Round(100 * netFill / float64(netFillN)))
			backlogFill += netFill
			backlogN += netFillN
		} else {
			d["backlogPct"] = 0
		}
		switch {
		case t > 0:
			d["readPct"] = int(math.Round(100 * (1 - math.Min(float64(unread), t)/t)))
		case unread == 0:
			d["readPct"] = 100
		default:
			d["readPct"] = 0
		}
		netsOut = append(netsOut, d)
	}

	health := map[string]any{"mentions": mentions, "backlogPct": 0}
	if lagN > 0 {
		health["avgLag"] = lagSum / lagN
	}
	if backlogN > 0 {
		health["backlogPct"] = int(math.Round(100 * backlogFill / float64(backlogN)))
	}
	if traffic.available {
		health["reconnects24h"] = traffic.reconnects
		health["offlineMin24h"] = traffic.offlineMin
	}

	evs, _ := s.b.UserEvents(ctx, u.ID, now.Add(-24*time.Hour), 20)
	return map[string]any{
		"networks": netsOut,
		"clients":  s.clientsDTO(u.ID),
		"events":   eventsDTO(p, evs),
		"health":   health,
		"limits":   lim,
		"traffic": map[string]any{
			"available": traffic.available, "error": metricsErr(p, traffic.err),
			"start": traffic.start, "step": 3600, "series": traffic.series, "total24h": int64(math.Round(total)),
		},
	}, nil
}

// attachedAccounts counts the accounts with an IRC client attached, and the
// web chats open (online counts both, per user; clients only IRC).
func attachedAccounts(online, clients map[int64]int) (accounts, web int) {
	for uid, n := range online {
		if clients[uid] > 0 {
			accounts++
		}
		web += max(n-clients[uid], 0)
	}
	return accounts, web
}

func pct(part, total float64) int {
	if total <= 0 {
		return 0
	}
	return int(math.Round(100 * part / total))
}

// channelRate is a 0–10 activity level from the last hour's lines.
func (s *Handlers) channelRate(networkID int64, channel string, now time.Time) int {
	msgs, err := s.b.History().Recent(networkID, channel, 120)
	if err != nil {
		return 0
	}
	n := 0
	for _, m := range msgs {
		if m.Time.After(now.Add(-time.Hour)) {
			n++
		}
	}
	switch {
	case n == 0:
		return 0
	case n >= 100:
		return 10
	default:
		return 1 + n/11
	}
}

type userTraffic struct {
	available  bool
	err        string
	start      int64
	series     []map[string]any // per network: {network, name, points: [lines per hour ×25]}
	perNet     map[int64]float64
	reconnects int64
	offlineMin int64
}

// userTraffic reads a user's last 24 hours from the metrics store.
func (s *Handlers) userTraffic(ctx context.Context, userID int64, nets []store.Network) userTraffic {
	out := userTraffic{perNet: map[int64]float64{}, series: []map[string]any{}}
	mq := s.metrics.Load()
	if mq == nil {
		out.err = noMetrics
		return out
	}
	// The rest of the page doesn't wait on a slow metrics store.
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	user := metricsq.Quote(strconv.FormatInt(userID, 10))
	end := time.Now().Truncate(time.Hour).Add(time.Hour)
	start := end.Add(-24 * time.Hour)
	out.start = start.Unix()
	series, err := hourlyByNetwork(ctx, mq, func(window string) string {
		return fmt.Sprintf(`sum by (network) (increase_pure(%s{user=%s}[%s]))`, metricLines, user, window)
	}, end)
	if err != nil {
		out.err = err.Error()
		return out
	}
	out.available = true
	names := map[string]string{}
	for _, n := range nets {
		names[strconv.FormatInt(n.ID, 10)] = n.Name
	}
	for _, se := range series {
		id := se.Labels["network"]
		name, ok := names[id]
		if !ok {
			continue // a deleted network
		}
		pts := hourSlots(se, start)
		var sum float64
		for _, v := range pts[1:] { // the first point covers the hour before the window
			sum += v
		}
		nid, _ := strconv.ParseInt(id, 10, 64)
		out.perNet[nid] = sum
		out.series = append(out.series, map[string]any{"network": nid, "name": name, "points": pts})
	}
	if v, err := mq.Scalar(ctx, fmt.Sprintf(`sum(increase_pure(%s{user=%s}[24h]))`, metricDisconnects, user)); err == nil {
		out.reconnects = int64(math.Round(v))
	}
	// Minutes in the last day with a network down, summed over networks.
	if v, err := mq.Scalar(ctx, fmt.Sprintf(`sum(count_over_time((%s{user=%s} == 0)[24h:1m]))`, metricUp, user)); err == nil {
		out.offlineMin = int64(math.Round(v))
	}
	return out
}

// hourlyByNetwork reads 25 hourly points per network for the day ending at
// end (a whole hour, in the future): query(window) sums by network over a
// window. Point i covers the hour ending at end-24h+i hours. The last one
// ends in the future, where VictoriaMetrics has nothing, so the hour in
// progress is an instant query at now over the part of it that has passed.
func hourlyByNetwork(ctx context.Context, mq *metricsq.Client, query func(window string) string, end time.Time) ([]metricsq.Series, error) {
	start := end.Add(-24 * time.Hour)
	series, err := mq.Range(ctx, query("1h"), start, end.Add(-time.Hour), time.Hour)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	elapsed := max(now.Sub(end.Add(-time.Hour)).Truncate(time.Second), time.Minute)
	current, err := mq.Range(ctx, query(fmt.Sprintf("%ds", int(elapsed.Seconds()))), now, now, time.Minute)
	if err != nil {
		return nil, err
	}
	for _, c := range current {
		if len(c.Points) == 0 {
			continue
		}
		p := [2]float64{float64(end.Unix()), c.Points[len(c.Points)-1][1]}
		found := false
		for i := range series {
			if series[i].Labels["network"] == c.Labels["network"] {
				series[i].Points = append(series[i].Points, p)
				found = true
			}
		}
		if !found {
			series = append(series, metricsq.Series{Labels: c.Labels, Points: [][2]float64{p}})
		}
	}
	return series, nil
}

// hourSlots puts a series' points into the 25 hourly slots from start.
func hourSlots(se metricsq.Series, start time.Time) []float64 {
	pts := make([]float64, 25)
	for _, p := range se.Points {
		i := int((int64(p[0]) - start.Unix()) / 3600)
		if i >= 0 && i < len(pts) && !math.IsNaN(p[1]) {
			pts[i] = math.Round(p[1])
		}
	}
	return pts
}

// --- admin overview ------------------------------------------------------------

// AdminOverview is the admin dashboard: cluster nodes and where each
// account lives, what needs attention, upstream networks across all users,
// and the stores' health. Live numbers come from every node.
func (s *Handlers) AdminOverview(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	pr := s.printer(r) // evrd's own words, in the admin's language
	users, err := s.acc.ListUsers(ctx)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	allNets, err := s.acc.AllNetworks(ctx)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	now := time.Now()
	live := s.clusterUsage(ctx)
	leases, _ := s.state.Leases(ctx, now)
	owner := map[int64]string{}
	for _, l := range leases {
		owner[l.UserID] = l.NodeID
	}

	// links by network id (live state, from whichever node runs them)
	links := map[int64]linkInfo{}
	for _, l := range live.Links {
		links[l.Network] = l
	}
	netsBy := map[int64][]store.Network{}
	for _, n := range allNets {
		netsBy[n.UserID] = append(netsBy[n.UserID], n)
	}

	var admins, suspended, sso, online int
	type placed struct {
		ID       int64  `json:"id"`
		Username string `json:"username"`
		Node     string `json:"node"`
		Status   string `json:"status"` // online | offline | suspended
		Nets     int    `json:"nets"`
		Up       int    `json:"up"`
		Clients  int    `json:"clients"`
		Issue    string `json:"issue,omitempty"`
	}
	var placements []placed
	var attention []map[string]any
	for _, u := range users {
		if u.IsAdmin() {
			admins++
		}
		if u.OIDCSubject != nil {
			sso++
		}
		p := placed{ID: u.ID, Username: u.Username, Node: owner[u.ID], Nets: len(netsBy[u.ID]), Clients: live.Online[u.ID]}
		var down []string
		for _, n := range netsBy[u.ID] {
			if l, ok := links[n.ID]; ok {
				if l.Status == string(upstream.StatusConnected) {
					p.Up++
				} else if l.LastError != "" {
					down = append(down, n.Name)
				}
			}
		}
		lim := s.acc.LimitsFor(u)
		switch {
		case u.Suspended:
			p.Status, p.Issue = "suspended", "suspended"
			suspended++
		case p.Clients > 0:
			p.Status = "online"
			online++
		default:
			p.Status = "offline"
		}
		inactive := u.LastSeenAt != nil && now.Sub(*u.LastSeenAt) > 14*24*time.Hour
		switch {
		case u.Suspended:
		case len(down) > 0:
			p.Issue = pr.T(messages.Area+"admin.issueDown", "networks", strings.Join(down, ", "))
		case lim.MaxNetworks > 0 && p.Nets >= lim.MaxNetworks:
			p.Issue = pr.T(messages.Area + "admin.issueLimit")
			attention = append(attention, map[string]any{
				"kind": "limit", "user": u.Username, "userId": u.ID,
				"title":  pr.T(messages.Area+"admin.atLimit", "user", u.Username),
				"detail": pr.T(messages.Area+"admin.atLimitDetail", "count", p.Nets, "max", lim.MaxNetworks),
			})
		case inactive:
			p.Issue = pr.T(messages.Area+"admin.issueInactive", "ago", ago(*u.LastSeenAt))
			if p.Up > 0 {
				attention = append(attention, map[string]any{
					"kind": "inactive", "user": u.Username, "userId": u.ID,
					"title":  pr.T(messages.Area+"admin.inactive", "user", u.Username),
					"detail": pr.T(messages.Area+"admin.inactiveDetail", "ago", ago(*u.LastSeenAt), "count", p.Up),
				})
			}
		}
		if u.Suspended {
			attention = append(attention, map[string]any{
				"kind": "suspended", "user": u.Username, "userId": u.ID,
				"title": pr.T(messages.Area+"admin.suspended", "user", u.Username), "detail": orDash(u.SuspendedReason),
			})
		}
		placements = append(placements, p)
	}

	// Upstream networks across all users, grouped by server host.
	type upRow struct {
		Host     string   `json:"host"`
		Name     string   `json:"name"`
		Users    int      `json:"users"`
		Up       int      `json:"up"`
		Enabled  int      `json:"enabled"`
		Down     []string `json:"down"`
		ErrSince int64    `json:"errSince,omitempty"`
		Err      string   `json:"error,omitempty"`
	}
	ups := map[string]*upRow{}
	userName := map[int64]string{}
	for _, u := range users {
		userName[u.ID] = u.Username
	}
	for _, n := range allNets {
		key := strings.ToLower(n.Host)
		row := ups[key]
		if row == nil {
			row = &upRow{Host: n.Host, Name: n.Name, Down: []string{}}
			ups[key] = row
		}
		row.Users++
		if n.Enabled {
			row.Enabled++
		}
		if l, ok := links[n.ID]; ok {
			if l.Status == string(upstream.StatusConnected) {
				row.Up++
			} else if l.LastError != "" {
				row.Down = append(row.Down, userName[n.UserID])
				row.Err = orText(pr, l.LastErrorText, l.LastError)
				if row.ErrSince == 0 || (l.LastErrorAt > 0 && l.LastErrorAt < row.ErrSince) {
					row.ErrSince = l.LastErrorAt
				}
			}
		}
	}
	upList := make([]*upRow, 0, len(ups))
	for _, row := range ups {
		upList = append(upList, row)
		if len(row.Down) > 0 {
			attention = append([]map[string]any{{
				"kind": "network", "title": pr.T(messages.Area+"admin.unreachable", "network", row.Name), "host": row.Host,
				"detail": strings.Join(row.Down, ", ") + " · " + row.Err, "since": row.ErrSince,
			}}, attention...)
		}
	}
	sort.Slice(upList, func(i, j int) bool {
		if upList[i].Users != upList[j].Users {
			return upList[i].Users > upList[j].Users
		}
		return upList[i].Host < upList[j].Host
	})

	// Cluster nodes.
	var nodes []map[string]any
	nodeList := []store.Node{}
	if s.cl != nil {
		nodeList = s.cl.Nodes()
	} else if live, err := s.state.LiveNodes(ctx, now.Add(-2*time.Minute)); err == nil {
		// Not clustered: still one node, heartbeating and holding everyone.
		nodeList = live
	}
	for _, n := range nodeList {
		held, up := 0, 0
		for _, l := range leases {
			if l.NodeID == n.ID {
				held++
			}
		}
		for _, l := range live.Links {
			// Not clustered: links aren't tagged with a node — all are this one's.
			if (l.Node == n.ID || s.cl == nil) && l.Status == string(upstream.StatusConnected) {
				up++
			}
		}
		host := n.HTTPURL
		if pu, err := url.Parse(n.HTTPURL); err == nil && pu.Host != "" {
			host = pu.Hostname()
		}
		nodes = append(nodes, map[string]any{
			"id": n.ID, "host": host, "startedAt": n.StartedAt.Unix(), "heartbeatAt": n.HeartbeatAt.Unix(),
			"users": held, "linksUp": up,
		})
	}
	if nodes == nil {
		nodes = []map[string]any{}
	}

	linksTotal, linksUp := 0, 0
	for _, n := range allNets {
		if n.Enabled {
			linksTotal++
		}
	}
	for _, l := range live.Links {
		if l.Status == string(upstream.StatusConnected) {
			linksUp++
		}
	}
	accountsWith, web := attachedAccounts(live.Online, live.Clients)

	if attention == nil {
		attention = []map[string]any{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"users": len(users), "admins": admins, "suspended": suspended, "sso": sso, "online": online,
		"networks": len(allNets), "linksTotal": linksTotal, "linksUp": linksUp,
		"connectedNetworks": live.Connected, "attachedClients": live.Attached, "accountsAttached": accountsWith, "webAttached": web,
		"defaults": s.acc.Defaults(), "nodes": nodes, "placement": placements, "clustered": s.cl != nil,
		// the server's clock: the page compares heartbeats against it, so a
		// browser (or server) whose clock is off doesn't call a live node stale
		"now":       now.Unix(),
		"attention": attention, "upstreams": upList, "storage": s.storageHealth(),
		"ircHost": s.cfg().IRCHost,
		"version": version.Version, "holder": s.holderStatus(ctx),
		"clientDrops": s.clientDropSummary(ctx),
	})
}

// holderStatus is the connection holder for the overview, with whether it
// keeps IRC clients connected through evrd's restarts.
func (s *Handlers) holderStatus(ctx context.Context) map[string]any {
	h := s.b.Holder(ctx)
	return map[string]any{
		"configured": h.Configured, "reachable": h.Reachable, "protocol": h.Protocol,
		"hash": h.Hash, "version": h.Version, "older": h.Older,
		"holdsClients": h.Reachable && h.Protocol >= 2 && !s.cfg().KeepClientsOut,
	}
}

// storageHealth checks the account database and the history store.
func (s *Handlers) storageHealth() map[string]any {
	accounts := map[string]any{"kind": dbKind(s.cfg().DB), "addr": dbAddr(s.cfg().DB), "up": s.b.Ping(context.Background()) == nil}
	hist := map[string]any{"kind": dbKind(s.cfg().History), "addr": dbAddr(s.cfg().History), "up": s.b.History().Ping() == nil}
	if p, ok := s.b.History().(interface{ Pending() int64 }); ok {
		hist["queue"] = p.Pending()
	}
	return map[string]any{"accounts": accounts, "history": hist}
}

func dbKind(dsn string) string {
	switch {
	case strings.HasPrefix(dsn, "postgres"):
		return "PostgreSQL"
	case strings.HasPrefix(dsn, "scylla://"), strings.HasPrefix(dsn, "cassandra://"):
		return "ScyllaDB"
	case strings.HasPrefix(dsn, "pebble:") || !strings.Contains(dsn, ":") && dsn != "":
		return "Pebble"
	default:
		return "SQLite"
	}
}

// dbAddr is a DSN's host part, never its credentials.
func dbAddr(dsn string) string {
	if strings.HasPrefix(dsn, "sqlite:") || strings.HasPrefix(dsn, "pebble:") {
		return strings.SplitN(dsn, ":", 2)[1]
	}
	if !strings.Contains(dsn, ":") {
		return dsn // a bare path: a Pebble directory
	}
	rest := dsn
	if i := strings.Index(rest, "://"); i >= 0 {
		rest = rest[i+3:]
	}
	if i := strings.LastIndex(rest, "@"); i >= 0 {
		rest = rest[i+1:]
	}
	host, _, _ := strings.Cut(rest, "/")
	return host
}

// ago is how long ago t was, roughly, in words.
func ago(t time.Time) messages.Text {
	d := time.Since(t)
	switch {
	case d >= 14*24*time.Hour:
		return messages.T("ago.weeks", "count", int(d.Hours()/24/7))
	case d >= 48*time.Hour:
		return messages.T("ago.days", "count", int(d.Hours()/24))
	case d >= time.Hour:
		return messages.T("ago.hours", "count", int(d.Hours()))
	default:
		return messages.T("ago.minutes", "count", int(d.Minutes()))
	}
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

// --- node usage --------------------------------------------------------------

// linkInfo is one upstream connection's state, reported by the node running it.
type linkInfo struct {
	Node        string `json:"node"`
	User        int64  `json:"user"`
	Network     int64  `json:"network"`
	Status      string `json:"status"`
	LastError   string `json:"lastError,omitempty"`
	LastErrorAt int64  `json:"lastErrorAt,omitempty"`
	// LastErrorText is LastError in words, to show in the reader's language.
	LastErrorText messages.Text `json:"lastErrorText,omitzero"`
}

// usage is one node's live numbers.
type usage struct {
	Clients   map[int64]int `json:"clients"` // attached IRC clients per user
	Online    map[int64]int `json:"online"`  // attached IRC clients and web chats per user
	Links     []linkInfo    `json:"links"`
	Connected int           `json:"connected"`
	Attached  int           `json:"attached"`
}

func (s *Handlers) localUsage() usage {
	u := s.b.Usage()
	node := ""
	if s.cl != nil {
		node = s.cl.Self().ID
	}
	out := usage{Clients: s.b.ClientsByUser(), Online: map[int64]int{}, Links: []linkInfo{}, Connected: u.ConnectedNetworks, Attached: u.AttachedClients}
	for _, l := range s.b.LinkStates() {
		li := linkInfo{Node: node, User: l.UserID, Network: l.NetworkID, Status: string(l.Health.Status), LastError: l.Health.LastError, LastErrorText: l.Health.LastErrorText}
		if !l.Health.LastErrorAt.IsZero() {
			li.LastErrorAt = l.Health.LastErrorAt.Unix()
		}
		out.Links = append(out.Links, li)
	}
	for uid, n := range s.b.OnlineByUser() {
		out.Online[uid] = n
	}
	return out
}

func (s *Handlers) InternalUsage(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.localUsage())
}

// clusterUsage adds up every live node's numbers. A node that doesn't answer
// in time is left out.
func (s *Handlers) clusterUsage(ctx context.Context) usage {
	total := s.localUsage()
	if s.cl == nil {
		return total
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, n := range s.cl.Nodes() {
		if n.ID == s.cl.Self().ID || n.HTTPURL == "" {
			continue
		}
		wg.Add(1)
		go func(n store.Node) {
			defer wg.Done()
			req, _ := http.NewRequestWithContext(ctx, http.MethodGet, n.HTTPURL+"/internal/usage", nil)
			req.Header.Set(auth.HeaderSecret, s.secret)
			res, err := http.DefaultClient.Do(req)
			if err != nil {
				s.log.Warn("usage from node", "node", n.ID, "err", err)
				return
			}
			defer res.Body.Close()
			var u usage
			if res.StatusCode != http.StatusOK || decodeJSON(res, &u) != nil {
				return
			}
			mu.Lock()
			for uid, c := range u.Clients {
				total.Clients[uid] += c
			}
			for uid, c := range u.Online {
				total.Online[uid] += c
			}
			total.Links = append(total.Links, u.Links...)
			total.Connected += u.Connected
			total.Attached += u.Attached
			mu.Unlock()
		}(n)
	}
	wg.Wait()
	return total
}

// AdminClientDrops is every user's IRC client disconnects over ?range=
// (24h, 7d or 30d): by reason, how long devices were gone, and the devices
// that dropped most, with whose they are (?sort= drops or a reason,
// ?limit= up to 100).
func (s *Handlers) AdminClientDrops(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	// the network health page's ranges and slots, so the charts read alike
	key, span, step := healthRange(q.Get("range"))
	n := int(span / step)
	start := time.Now().Truncate(step).Add(-time.Duration(n-1) * step)
	by := q.Get("sort")
	if !slices.Contains(bouncer.DropReasons, by) {
		by = "drops"
	}
	limit := 25
	if n, err := strconv.Atoi(q.Get("limit")); err == nil && n > 0 {
		limit = min(n, 100)
	}
	// ?user= narrows everything to one account (Admin → Users links here)
	var only map[string]any
	slots := bouncer.Slots{Start: start, Step: step, N: n}
	if id, err := strconv.ParseInt(q.Get("user"), 10, 64); err == nil && id > 0 {
		u, err := s.acc.UserByID(r.Context(), id)
		if err != nil {
			writeErr(w, http.StatusNotFound, "no such user")
			return
		}
		slots.UserID, only = id, map[string]any{"id": u.ID, "username": u.Username}
	}
	all, err := s.b.AllClientDrops(r.Context(), slots, by, limit)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	users, _ := s.acc.ListUsers(r.Context())
	names := map[int64]string{}
	for _, u := range users {
		names[u.ID] = u.Username
	}
	top := make([]map[string]any, 0, len(all.Top))
	for _, t := range all.Top {
		top = append(top, map[string]any{"userId": t.UserID, "user": orDeleted(names[t.UserID]), "device": t.Device,
			"software": t.Software, "network": t.Network, "drops": t.Drops, "reasons": t.Reasons, "mainReason": t.MainReason,
			"medianGap": t.MedianGap, "last": t.Last.Unix(), "connected": t.Connected})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"range": key, "sort": by, "order": bouncer.DropReasons, "bucketBounds": bouncer.GapBuckets,
		"drops": all.Drops, "accounts": all.Accounts, "devices": all.Devices, "reasons": all.Reasons,
		"gaps": all.Gaps, "buckets": all.Buckets, "top": top,
		"start": start.Unix(), "step": int64(step / time.Second), "points": n, "timeline": all.Timeline, "user": only,
	})
}

// clientDropSummary is every IRC client disconnect in the last day, for the
// admin overview (nil when it can't be read).
func (s *Handlers) clientDropSummary(ctx context.Context) any {
	sum, err := s.b.ClientDropSummary(ctx, time.Now().Add(-dropsPeriod))
	if err != nil {
		return nil
	}
	return sum
}
