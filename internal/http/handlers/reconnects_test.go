package handlers_test

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

// fakeReconnectsVM answers the reconnects page's queries for network nid:
// 2 links dropped, 5 failed attempts and 1 counted before kinds; 12 minutes
// offline; 1 disconnect in every hour, 2 in the hour in progress.
func fakeReconnectsVM(t *testing.T, nid int64) *httptest.Server {
	t.Helper()
	id := strconv.FormatInt(nid, 10)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("query")
		if r.URL.Path == "/api/v1/query_range" {
			start, _ := strconv.ParseInt(r.URL.Query().Get("start"), 10, 64)
			end, _ := strconv.ParseInt(r.URL.Query().Get("end"), 10, 64)
			step, _ := strconv.ParseInt(r.URL.Query().Get("step"), 10, 64)
			val := "1"
			if !strings.Contains(q, "[1h]") {
				val = "2"
			}
			var pts []string
			for ts := start; ts <= end; ts += step {
				pts = append(pts, fmt.Sprintf(`[%d,%q]`, ts, val))
			}
			fmt.Fprintf(w, `{"status":"success","data":{"resultType":"matrix","result":[{"metric":{"network":%q},"values":[%s]}]}}`, id, strings.Join(pts, ","))
			return
		}
		vec := func(rows ...string) {
			fmt.Fprintf(w, `{"status":"success","data":{"resultType":"vector","result":[%s]}}`, strings.Join(rows, ","))
		}
		row := func(labels, v string) string { return fmt.Sprintf(`{"metric":{%s},"value":[1,%q]}`, labels, v) }
		switch {
		case strings.Contains(q, "evrd_upstream_up"):
			vec(row(`"network":"`+id+`"`, "12"))
		case strings.Contains(q, "by (network, kind)"):
			vec(row(`"network":"`+id+`","kind":"dropped"`, "2"), row(`"network":"`+id+`","kind":"failed"`, "5"), row(`"network":"`+id+`"`, "1"))
		default:
			t.Errorf("unexpected query %q", q)
			vec()
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestReconnects(t *testing.T) {
	e := newEnv(t)
	fay := e.user("fay", store.RoleUser)
	c := e.login("fay")
	_, quiet := c.do("POST", "/api/networks", map[string]any{"name": "quiet", "host": "irc.example.org"})
	_, n := c.do("POST", "/api/networks", map[string]any{"name": "flaky", "host": "irc.example.org"})
	nid := int64(n["id"].(float64))
	qid := int64(quiet["id"].(float64))
	e.api.SetMetrics(metricsq.New(fakeReconnectsVM(t, nid).URL))
	now := time.Now()
	for _, ev := range []store.UserEvent{
		{UserID: fay.ID, NetworkID: nid, At: now.Add(-time.Hour), Kind: "failed", Title: "flaky: couldn't connect: connection refused", Detail: "nothing listens"},
		{UserID: fay.ID, NetworkID: nid, At: now.Add(-2 * time.Hour), Kind: "disconnected", Title: "flaky ping timeout"},
		{UserID: fay.ID, At: now.Add(-time.Minute), Kind: "attached", Title: "irssi attached"},
		{UserID: fay.ID, NetworkID: nid, At: now.Add(-48 * time.Hour), Kind: "disconnected", Title: "long ago"},
	} {
		if err := e.st.AddUserEvent(t.Context(), ev); err != nil {
			t.Fatal(err)
		}
	}

	code, out := c.do("GET", "/api/reconnects", nil)
	if code != 200 || out["available"] != true || out["error"] != "" || out["step"] != 3600.0 {
		t.Fatalf("reconnects: %d %v", code, out)
	}
	nets := out["networks"].([]any)
	if len(nets) != 2 {
		t.Fatalf("networks: %v", nets)
	}
	first := nets[0].(map[string]any)
	if first["id"] != float64(nid) || first["dropped"] != 2.0 || first["failed"] != 5.0 || first["total"] != 8.0 || first["offlineMin"] != 12.0 {
		t.Fatalf("the network with the most trouble comes first, counted: %v", first)
	}
	if h := first["hourly"].([]any); len(h) != 25 || h[0] != 1.0 || h[24] != 2.0 {
		t.Fatalf("hourly: %v", h)
	}
	if first["status"] != "disconnected" {
		t.Fatalf("live state: %v", first)
	}
	second := nets[1].(map[string]any)
	if second["id"] != float64(qid) || second["total"] != 0.0 || len(second["hourly"].([]any)) != 25 {
		t.Fatalf("a network without trouble: %v", second)
	}
	evs := out["events"].([]any)
	if len(evs) != 2 {
		t.Fatalf("only the last day's link events: %v", evs)
	}
	ev := evs[0].(map[string]any)
	if ev["kind"] != "failed" || ev["network"] != float64(nid) || ev["detail"] != "nothing listens" {
		t.Fatalf("event: %v", ev)
	}

	// admins see the same for any user; users can't
	if code, _ := c.do("GET", fmt.Sprintf("/api/admin/users/%d/reconnects", fay.ID), nil); code != 403 {
		t.Fatalf("a user on the admin endpoint: %d", code)
	}
	e.user("root", store.RoleAdmin)
	code, adm := e.login("root").do("GET", fmt.Sprintf("/api/admin/users/%d/reconnects", fay.ID), nil)
	if code != 200 || len(adm["networks"].([]any)) != 2 || adm["networks"].([]any)[0].(map[string]any)["total"] != 8.0 {
		t.Fatalf("admin: %d %v", code, adm)
	}
	if code, _ := e.login("root").do("GET", "/api/admin/users/99999/reconnects", nil); code != 404 {
		t.Fatalf("no such user: %d", code)
	}
}

func TestReconnectsWithoutMetrics(t *testing.T) {
	e := newEnv(t)
	e.user("fay", store.RoleUser)
	c := e.login("fay")
	c.do("POST", "/api/networks", map[string]any{"name": "lib", "host": "irc.example.org"})
	code, out := c.do("GET", "/api/reconnects", nil)
	if code != 200 || out["available"] != false || out["error"] != "metrics aren't configured" {
		t.Fatalf("%d %v", code, out)
	}
	n := out["networks"].([]any)[0].(map[string]any)
	if _, ok := n["total"]; ok || n["name"] != "lib" {
		t.Fatalf("no counts without metrics, still the live state: %v", n)
	}

	// a metrics store that fails says why
	vm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "down", http.StatusServiceUnavailable)
	}))
	t.Cleanup(vm.Close)
	e.api.SetMetrics(metricsq.New(vm.URL))
	if _, out := c.do("GET", "/api/reconnects", nil); out["available"] != false || out["error"] == "" {
		t.Fatalf("failing store: %v", out)
	}
}
