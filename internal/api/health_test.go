package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/4evrd/evrd/internal/metricsq"
	"github.com/4evrd/evrd/internal/store"
)

// fakeHealthVM answers the health page's queries for user uid's network
// nid. Every past slot: connected the whole time, 50 ms lag on average and
// 200 at worst, 10 lines, 1 link dropped, 2 failed attempts and 1 counted
// before kinds. The slot in progress: connected half of it, 100 and 300 ms,
// 4 lines, nothing lost. It records the range queries' steps.
func fakeHealthVM(t *testing.T, uid, nid int64) (*httptest.Server, *[]int64) {
	t.Helper()
	sel := fmt.Sprintf(`{user="%d",network="%d"}`, uid, nid)
	var steps []int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("query")
		if !strings.Contains(q, sel) {
			t.Errorf("query without the network's labels: %q", q)
		}
		past := r.URL.Path == "/api/v1/query_range"
		pick := func(old, now string) string {
			if past {
				return old
			}
			return now
		}
		var rows [][2]string // labels, value
		switch {
		case strings.Contains(q, metricUp):
			rows = [][2]string{{"", pick("1", "0.5")}}
		case strings.HasPrefix(q, "avg_over_time") && strings.Contains(q, metricLag):
			rows = [][2]string{{"", pick("0.05", "0.1")}}
		case strings.HasPrefix(q, "max_over_time") && strings.Contains(q, metricLag):
			rows = [][2]string{{"", pick("0.2", "0.3")}}
		case strings.Contains(q, metricLines):
			rows = [][2]string{{"", pick("10", "4")}}
		case strings.Contains(q, metricDisconnects):
			rows = [][2]string{{`"kind":"dropped"`, pick("1", "0")}, {`"kind":"failed"`, pick("2", "0")}, {"", pick("1", "0")}}
		default:
			t.Errorf("unexpected query %q", q)
		}
		var out []string
		if past {
			start, _ := strconv.ParseInt(r.URL.Query().Get("start"), 10, 64)
			end, _ := strconv.ParseInt(r.URL.Query().Get("end"), 10, 64)
			step, _ := strconv.ParseInt(r.URL.Query().Get("step"), 10, 64)
			steps = append(steps, step)
			for _, row := range rows {
				var pts []string
				for ts := start; ts <= end; ts += step {
					pts = append(pts, fmt.Sprintf(`[%d,%q]`, ts, row[1]))
				}
				out = append(out, fmt.Sprintf(`{"metric":{%s},"values":[%s]}`, row[0], strings.Join(pts, ",")))
			}
			fmt.Fprintf(w, `{"status":"success","data":{"resultType":"matrix","result":[%s]}}`, strings.Join(out, ","))
			return
		}
		for _, row := range rows {
			out = append(out, fmt.Sprintf(`{"metric":{%s},"value":[1,%q]}`, row[0], row[1]))
		}
		fmt.Fprintf(w, `{"status":"success","data":{"resultType":"vector","result":[%s]}}`, strings.Join(out, ","))
	}))
	t.Cleanup(srv.Close)
	return srv, &steps
}

func nums(t *testing.T, v any) []any {
	t.Helper()
	l, ok := v.([]any)
	if !ok {
		t.Fatalf("not a list: %v", v)
	}
	return l
}

func TestNetworkHealth(t *testing.T) {
	e := newEnv(t)
	gil := e.user("gil", store.RoleUser)
	c := e.login("gil")
	_, n := c.do("POST", "/api/networks", map[string]any{
		"name": "libera", "host": "irc.libera.chat", "port": 6697, "tls": true, "username": "gilly", "channels": []string{"#evrd", "#go"},
	})
	nid := int64(n["id"].(float64))
	_, other := c.do("POST", "/api/networks", map[string]any{"name": "oftc", "host": "irc.oftc.net"})
	vm, steps := fakeHealthVM(t, gil.ID, nid)
	e.api.SetMetrics(metricsq.New(vm.URL))
	now := time.Now()
	for _, ev := range []store.UserEvent{
		{UserID: gil.ID, NetworkID: nid, At: now.Add(-time.Hour), Kind: "disconnected", Title: "libera ping timeout"},
		{UserID: gil.ID, NetworkID: int64(other["id"].(float64)), At: now.Add(-time.Hour), Kind: "failed", Title: "oftc refused"},
		{UserID: gil.ID, At: now.Add(-time.Minute), Kind: "attached", Title: "irssi attached"},
		{UserID: gil.ID, NetworkID: nid, At: now.Add(-3 * 24 * time.Hour), Kind: "netsplit", Title: "three days ago"},
	} {
		if err := e.st.AddUserEvent(t.Context(), ev); err != nil {
			t.Fatal(err)
		}
	}
	path := "/api/networks/" + strconv.FormatInt(nid, 10) + "/health"

	code, out := c.do("GET", path, nil)
	if code != 200 || out["metrics"] != true || out["range"] != "24h" || out["step"] != 3600.0 || out["points"] != 24.0 {
		t.Fatalf("health: %d %v", code, out)
	}
	if st := int64(out["start"].(float64)); st%3600 != 0 || time.Unix(st, 0).After(now.Add(-23*time.Hour)) {
		t.Fatalf("the range starts a day back on a whole hour: %v", st)
	}
	net := out["network"].(map[string]any)
	if net["name"] != "libera" || net["tls"] != true || net["port"] != 6697.0 || net["username"] != "gilly" || net["nick"] != "gil" || net["authMethod"] == "" || net["servers"] != 1.0 {
		t.Fatalf("network: %v", net)
	}
	if live := out["live"].(map[string]any); live["status"] != "disconnected" || live["nick"] != "gil" {
		t.Fatalf("live: %v", live)
	}
	se := out["series"].(map[string]any)
	up := nums(t, se["uptime"])
	if len(up) != 24 || up[0] != 1.0 || up[22] != 1.0 || up[23] != 0.5 {
		t.Fatalf("uptime: %v", up)
	}
	if l := nums(t, se["lagAvg"]); len(l) != 24 || l[0] != 50.0 || l[23] != 100.0 {
		t.Fatalf("lag: %v", l)
	}
	if l := nums(t, se["lagMax"]); l[0] != 200.0 || l[23] != 300.0 {
		t.Fatalf("worst lag: %v", l)
	}
	if l := nums(t, se["lines"]); len(l) != 24 || l[0] != 10.0 || l[23] != 4.0 {
		t.Fatalf("lines: %v", l)
	}
	if d := nums(t, se["dropped"]); d[0] != 2.0 || d[23] != 0.0 {
		t.Fatalf("dropped, with those counted before kinds: %v", d)
	}
	if f := nums(t, se["failed"]); f[0] != 2.0 || f[23] != 0.0 {
		t.Fatalf("failed: %v", f)
	}
	sum := out["summary"].(map[string]any)
	// 23 slots up, one half: 23.5/24
	if sum["uptimePct"] != 97.9 || sum["lagAvg"] != 52.0 || sum["lagPeak"] != 300.0 || sum["lines"] != 234.0 || sum["dropped"] != 46.0 || sum["failed"] != 46.0 {
		t.Fatalf("summary: %v", sum)
	}
	evs := nums(t, out["events"])
	if len(evs) != 1 || evs[0].(map[string]any)["title"] != "libera ping timeout" {
		t.Fatalf("only this network's events in the range: %v", evs)
	}
	chans := nums(t, out["channels"])
	if len(chans) != 2 || chans[0].(map[string]any)["name"] != "#evrd" || chans[1].(map[string]any)["unread"] != 0.0 {
		t.Fatalf("saved channels while offline: %v", chans)
	}
	if _, ok := chans[0].(map[string]any)["members"]; ok {
		t.Fatalf("no members without a link: %v", chans[0])
	}

	// the week is hourly, the month daily; anything else is a day
	for _, tc := range []struct {
		rng    string
		points float64
		step   int64
	}{{"7d", 168, 3600}, {"30d", 30, 86400}, {"1y", 24, 3600}} {
		*steps = nil
		_, out := c.do("GET", path+"?range="+tc.rng, nil)
		if out["points"] != tc.points || out["step"] != float64(tc.step) || len(nums(t, out["series"].(map[string]any)["lines"])) != int(tc.points) {
			t.Fatalf("%s: %v", tc.rng, out)
		}
		for _, s := range *steps {
			if s != tc.step {
				t.Fatalf("%s: queried with step %d", tc.rng, s)
			}
		}
	}
	if _, out := c.do("GET", path+"?range=30d", nil); int64(out["start"].(float64))%86400 != 0 || out["range"] != "30d" {
		t.Fatalf("days start at midnight UTC: %v", out["start"])
	}

	// someone else's network, or none
	e.user("hal", store.RoleUser)
	hal := e.login("hal")
	for _, p := range []string{path, "/api/networks/99999/health", "/api/networks/nope/health"} {
		if code, _ := hal.do("GET", p, nil); code != 404 {
			t.Fatalf("hal GET %s: %d", p, code)
		}
	}

	// admins see any user's; users can't
	admPath := fmt.Sprintf("/api/admin/users/%d/networks/%d/health", gil.ID, nid)
	if code, _ := c.do("GET", admPath, nil); code != 403 {
		t.Fatalf("a user on the admin endpoint: %d", code)
	}
	e.user("root", store.RoleAdmin)
	root := e.login("root")
	code, adm := root.do("GET", admPath+"?range=7d", nil)
	if code != 200 || adm["metrics"] != true || adm["points"] != 168.0 || adm["network"].(map[string]any)["name"] != "libera" {
		t.Fatalf("admin: %d %v", code, adm)
	}
	for _, p := range []string{
		fmt.Sprintf("/api/admin/users/%d/networks/99999/health", gil.ID),
		fmt.Sprintf("/api/admin/users/%d/networks/nope/health", gil.ID),
		fmt.Sprintf("/api/admin/users/99999/networks/%d/health", nid),
	} {
		if code, _ := root.do("GET", p, nil); code != 404 {
			t.Fatalf("admin GET %s: %d", p, code)
		}
	}
	// a network of another user's isn't under this one
	halID := e.login("hal")
	_, hn := halID.do("POST", "/api/networks", map[string]any{"name": "hals", "host": "irc.example.org"})
	if code, _ := root.do("GET", fmt.Sprintf("/api/admin/users/%d/networks/%d/health", gil.ID, int64(hn["id"].(float64))), nil); code != 404 {
		t.Fatalf("admin, wrong user's network: %d", code)
	}
}

func TestNetworkHealthWithoutMetrics(t *testing.T) {
	e := newEnv(t)
	e.user("ivy", store.RoleUser)
	c := e.login("ivy")
	path, irc, id := e.liveNetwork(c, "fake")
	conn, _ := e.api.b.ConnFor(id)
	conn.SendRaw("JOIN #evrd")
	irc.expect("JOIN #evrd")
	eventually(t, "the join", func() bool { return conn.Joined("#evrd") })
	irc.send(":fake 353 ivy = #evrd :@ivy carol")
	eventually(t, "the names", func() bool { return conn.MemberCount("#evrd") == 2 })
	irc.send(":carol!c@h PRIVMSG #evrd :hello ivy")
	eventually(t, "the mention", func() bool {
		_, n := c.do("GET", path, nil)
		return n["mentions"] == 1.0
	})

	code, out := c.do("GET", path+"/health?range=7d", nil)
	if code != 200 || out["metrics"] != false || out["reason"] != "metrics aren't configured" || out["range"] != "7d" {
		t.Fatalf("%d %v", code, out)
	}
	if _, ok := out["series"]; ok {
		t.Fatalf("no series without metrics: %v", out)
	}
	live := out["live"].(map[string]any)
	if live["status"] != "connected" || live["connectedAt"] == nil || !strings.HasPrefix(live["server"].(string), "127.0.0.1:") || live["nick"] != "ivy" {
		t.Fatalf("live: %v", live)
	}
	chans := nums(t, out["channels"])
	if len(chans) != 1 {
		t.Fatalf("channels: %v", chans)
	}
	ch := chans[0].(map[string]any)
	if ch["name"] != "#evrd" || ch["members"] != 2.0 || ch["unread"] != 1.0 || ch["mentions"] != 1.0 {
		t.Fatalf("joined channel: %v", ch)
	}

	// a metrics store that fails says why, and the rest still comes
	vm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "down", http.StatusServiceUnavailable)
	}))
	t.Cleanup(vm.Close)
	e.api.SetMetrics(metricsq.New(vm.URL))
	if _, out := c.do("GET", path+"/health", nil); out["metrics"] != false || out["reason"] == "" || len(nums(t, out["channels"])) != 1 {
		t.Fatalf("failing store: %v", out)
	}
}

func TestHealthSummaryWithoutData(t *testing.T) {
	h := healthMetrics{uptime: make([]*float64, 3), lagAvg: make([]*float64, 3), lagMax: make([]*float64, 3), lines: []float64{1, 2, 3}}
	s := h.summary()
	if _, ok := s["uptimePct"]; ok {
		t.Fatalf("no uptime without data: %v", s)
	}
	if _, ok := s["lagPeak"]; ok || s["lines"] != 6.0 {
		t.Fatalf("summary: %v", s)
	}
}
