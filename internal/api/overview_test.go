package api

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/4evrd/evrd/internal/metricsq"
	"github.com/4evrd/evrd/internal/store"
)

// fakeUserVM answers a user's traffic queries: 10 lines in every whole hour
// for network nid (and for a deleted network), 5 in the hour in progress,
// 3 reconnects and 42 minutes offline.
func fakeUserVM(t *testing.T, nid int64) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("query")
		if r.URL.Path == "/api/v1/query_range" {
			start, _ := strconv.ParseInt(r.URL.Query().Get("start"), 10, 64)
			end, _ := strconv.ParseInt(r.URL.Query().Get("end"), 10, 64)
			step, _ := strconv.ParseInt(r.URL.Query().Get("step"), 10, 64)
			val := "10"
			if !strings.Contains(q, "[1h]") {
				val = "5" // the hour in progress
			}
			series := func(net string) string {
				var pts []string
				for ts := start; ts <= end; ts += step {
					pts = append(pts, fmt.Sprintf(`[%d,%q]`, ts, val))
				}
				return fmt.Sprintf(`{"metric":{"network":%q},"values":[%s]}`, net, strings.Join(pts, ","))
			}
			fmt.Fprintf(w, `{"status":"success","data":{"resultType":"matrix","result":[%s,%s]}}`, series(strconv.FormatInt(nid, 10)), series("99999"))
			return
		}
		v := "3"
		if strings.Contains(q, "evrd_upstream_up") {
			v = "42"
		}
		fmt.Fprintf(w, `{"status":"success","data":{"resultType":"vector","result":[{"metric":{},"value":[1,%q]}]}}`, v)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestOverviewTraffic(t *testing.T) {
	e := newEnv(t)
	e.user("fay", store.RoleUser)
	c := e.login("fay")
	_, n := c.do("POST", "/api/networks", map[string]any{"name": "lib", "host": "irc.example.org"})
	nid := int64(n["id"].(float64))
	e.api.SetMetrics(metricsq.New(fakeUserVM(t, nid).URL))

	code, ov := c.do("GET", "/api/overview", nil)
	if code != 200 {
		t.Fatalf("overview: %d %v", code, ov)
	}
	tr := ov["traffic"].(map[string]any)
	if tr["available"] != true || tr["error"] != "" || tr["step"] != 3600.0 {
		t.Fatalf("traffic: %v", tr)
	}
	series := tr["series"].([]any)
	if len(series) != 1 {
		t.Fatalf("only live networks have series: %v", series)
	}
	s := series[0].(map[string]any)
	pts := s["points"].([]any)
	if s["name"] != "lib" || len(pts) != 25 || pts[0] != 10.0 || pts[23] != 10.0 || pts[24] != 5.0 {
		t.Fatalf("series: %v", s)
	}
	// The first point covers the hour before the window: 23×10 + 5.
	if tr["total24h"] != 235.0 {
		t.Fatalf("total: %v", tr["total24h"])
	}
	h := ov["health"].(map[string]any)
	if h["reconnects24h"] != 3.0 || h["offlineMin24h"] != 42.0 {
		t.Fatalf("health: %v", h)
	}
	net0 := ov["networks"].([]any)[0].(map[string]any)
	if net0["traffic24h"] != 235.0 || net0["trafficPct"] != 100.0 || net0["readPct"] != 100.0 {
		t.Fatalf("network traffic: %v", net0)
	}
}

func TestOverviewMetricsErrors(t *testing.T) {
	e := newEnv(t)
	e.user("fay", store.RoleUser)
	c := e.login("fay")
	calls := 0
	vm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 2 { // the hour in progress
			fmt.Fprint(w, `{"status":"error","error":"too slow"}`)
			return
		}
		fmt.Fprint(w, `{"status":"success","data":{"resultType":"matrix","result":[]}}`)
	}))
	defer vm.Close()
	e.api.SetMetrics(metricsq.New(vm.URL))
	_, ov := c.do("GET", "/api/overview", nil)
	if tr := ov["traffic"].(map[string]any); tr["available"] != false || !strings.Contains(tr["error"].(string), "too slow") {
		t.Fatalf("traffic: %v", tr)
	}
	calls = 1 // fail the first query next time
	_, ov = c.do("GET", "/api/overview", nil)
	if tr := ov["traffic"].(map[string]any); tr["available"] != false {
		t.Fatalf("traffic: %v", tr)
	}
}

func TestOverviewBacklogAndUnread(t *testing.T) {
	e := newEnv(t)
	e.user("fay", store.RoleUser)
	c := e.login("fay")
	path, irc, _ := e.liveNetwork(c, "fake")
	irc.send(":z!z@h PRIVMSG fay :one")
	irc.send(":z!z@h PRIVMSG fay :two")
	eventually(t, "messages", func() bool { _, n := c.do("GET", path, nil); return n["unread"] == 2.0 })
	_, ov := c.do("GET", "/api/overview", nil)
	n := ov["networks"].([]any)[0].(map[string]any)
	// 2 of 100 backlog lines; nothing read and no traffic figures.
	if n["backlogPct"] != 2.0 || n["readPct"] != 0.0 || len(n["buffers"].([]any)) != 0 {
		t.Fatalf("network: %v", n)
	}
	h := ov["health"].(map[string]any)
	if h["mentions"] != 2.0 || h["backlogPct"] != 2.0 {
		t.Fatalf("health: %v", h)
	}
}

func TestMyEvents(t *testing.T) {
	e := newEnv(t)
	u := e.user("fay", store.RoleUser)
	c := e.login("fay")
	for i := range 3 {
		e.api.b.AddEvent(u.ID, "connected", "Connected "+strconv.Itoa(i), "detail")
	}
	eventually(t, "events", func() bool {
		_, out := c.doAny("GET", "/api/me/events", nil)
		return len(out.([]any)) == 3
	})
	_, out := c.doAny("GET", "/api/me/events?limit=2", nil)
	evs := out.([]any)
	if len(evs) != 2 || evs[0].(map[string]any)["kind"] != "connected" || evs[0].(map[string]any)["at"] == nil {
		t.Fatalf("limited events: %v", evs)
	}
	if _, out := c.doAny("GET", "/api/me/events?limit=5000", nil); len(out.([]any)) != 3 {
		t.Fatalf("an over-large limit: %v", out)
	}
}

// The admin overview flags accounts at their network limit, networks that
// can't connect, and inactive accounts still holding connections.
func TestAdminOverviewAttention(t *testing.T) {
	e := newEnv(t)
	e.user("root", store.RoleAdmin)
	full := e.user("full", store.RoleUser)
	idle := e.user("idle", store.RoleUser)
	e.user("broke", store.RoleUser)
	admin := e.login("root")

	fc := e.login("full")
	fc.do("POST", "/api/networks", map[string]any{"name": "a", "host": "irc.a.example"})
	fc.do("POST", "/api/networks", map[string]any{"name": "b", "host": "irc.b.example"})

	ic := e.login("idle")
	e.liveNetwork(ic, "fake")
	old := time.Now().Add(-20 * 24 * time.Hour)
	iu, _ := e.st.UserByID(idle.ID)
	iu.LastSeenAt = &old
	e.st.UpdateUser(iu)

	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	closed := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	bc := e.login("broke")
	_, bn := bc.do("POST", "/api/networks", map[string]any{"name": "Gone", "host": "localhost", "port": closed, "tls": false})
	bpath := "/api/networks/" + strconv.Itoa(int(bn["id"].(float64)))
	t.Cleanup(func() { e.api.b.Disconnect(int64(bn["id"].(float64))) })
	bc.do("POST", bpath+"/connect", nil)
	eventually(t, "the connection to fail", func() bool { _, n := bc.do("GET", bpath, nil); return n["lastError"] != "" })

	code, ov := admin.do("GET", "/api/admin/overview", nil)
	if code != 200 {
		t.Fatalf("overview: %d %v", code, ov)
	}
	kinds := map[string]map[string]any{}
	for _, a := range ov["attention"].([]any) {
		am := a.(map[string]any)
		kinds[am["kind"].(string)] = am
	}
	if a := kinds["limit"]; a == nil || a["userId"] != float64(full.ID) || a["detail"] != "2 of 2 networks" {
		t.Errorf("limit: %v", kinds)
	}
	if a := kinds["inactive"]; a == nil || a["userId"] != float64(idle.ID) || !strings.HasPrefix(a["detail"].(string), "2 weeks") {
		t.Errorf("inactive: %v", kinds)
	}
	if a := kinds["network"]; a == nil || a["title"] != "Gone unreachable" || !strings.HasPrefix(a["detail"].(string), "broke · ") {
		t.Errorf("network: %v", kinds)
	}
	issues := map[string]string{}
	for _, p := range ov["placement"].([]any) {
		pm := p.(map[string]any)
		issue, _ := pm["issue"].(string)
		issues[pm["username"].(string)] = issue
	}
	if issues["full"] != "at network limit" || issues["broke"] != "Gone down" || issues["idle"] != "inactive 2 weeks" {
		t.Errorf("issues: %v", issues)
	}
	if ov["linksUp"] != 1.0 || ov["linksTotal"] != 2.0 || ov["networks"] != 4.0 {
		t.Errorf("links: up %v of %v, networks %v", ov["linksUp"], ov["linksTotal"], ov["networks"])
	}
}
