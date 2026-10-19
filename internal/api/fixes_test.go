package api

// Tests for bugs found going through the dashboard.

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/4evrd/evrd/internal/certs"
	"github.com/4evrd/evrd/internal/config"
	"github.com/4evrd/evrd/internal/metricsq"
	"github.com/4evrd/evrd/internal/store"
)

// A metrics store that doesn't answer mustn't hold the overview up for
// long: it answers without the chart.
func TestOverviewDoesntWaitOnASlowMetricsStore(t *testing.T) {
	e := newEnv(t)
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(20 * time.Second):
		}
	}))
	defer slow.Close()
	e.api.SetMetrics(metricsq.New(slow.URL))
	e.user("fay", store.RoleUser)
	c := e.login("fay")
	start := time.Now()
	code, ov := c.do("GET", "/api/overview", nil)
	if code != 200 {
		t.Fatalf("overview: %d %v", code, ov)
	}
	if d := time.Since(start); d > 6*time.Second {
		t.Fatalf("the overview took %v", d)
	}
	if tr := ov["traffic"].(map[string]any); tr["available"] != false {
		t.Fatalf("traffic: %v", tr)
	}
}

// fakeVM answers PromQL queries: a constant for every range query, instant
// queries with now's value, and the busiest-networks query with one live
// network and one that's been deleted.
func fakeVM(t *testing.T, liveNet int64) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("query")
		labels := `{}`
		switch {
		case strings.Contains(q, "evrd_irc_logins_total"):
			labels = `{"result":"too_many_clients"}`
		case strings.Contains(q, "by (direction)"):
			labels = `{"direction":"in"}`
		}
		if r.URL.Path == "/api/v1/query_range" {
			start, _ := strconv.ParseFloat(r.URL.Query().Get("start"), 64)
			end, _ := strconv.ParseFloat(r.URL.Query().Get("end"), 64)
			step, _ := strconv.ParseFloat(r.URL.Query().Get("step"), 64)
			// like VictoriaMetrics: the end rounded down to a whole step
			var pts []string
			for ts := start; ts <= end-step; ts += step {
				pts = append(pts, fmt.Sprintf(`[%d,"1"]`, int64(ts)))
			}
			fmt.Fprintf(w, `{"status":"success","data":{"resultType":"matrix","result":[{"metric":%s,"values":[%s]}]}}`, labels, strings.Join(pts, ","))
			return
		}
		if strings.HasPrefix(q, "topk(") {
			fmt.Fprintf(w, `{"status":"success","data":{"resultType":"vector","result":[`+
				`{"metric":{"user":"1","network":"%d"},"value":[1,"50"]},`+
				`{"metric":{"user":"1","network":"99999"},"value":[1,"80"]}]}}`, liveNet)
			return
		}
		fmt.Fprintf(w, `{"status":"success","data":{"resultType":"vector","result":[{"metric":%s,"value":[1,"9"]}]}}`, labels)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestAdminMetrics(t *testing.T) {
	e := newEnv(t)
	e.user("root", store.RoleAdmin)
	admin := e.login("root")
	_, n := admin.do("POST", "/api/networks", map[string]any{"name": "lib", "host": "irc.example.org", "port": 6697, "tls": true, "nick": "root", "enabled": false})
	nid := int64(n["id"].(float64))
	e.api.SetMetrics(metricsq.New(fakeVM(t, nid).URL))

	code, m := admin.do("GET", "/api/admin/metrics?range=6h", nil)
	if code != 200 || m["available"] != true {
		t.Fatalf("metrics: %d %v", code, m)
	}
	if m["step"] != 300.0 || m["range"] != "6h" {
		t.Fatalf("6h: step %v range %v", m["step"], m["range"])
	}
	panels := map[string]map[string]any{}
	for _, p := range m["panels"].([]any) {
		panels[p.(map[string]any)["id"].(string)] = p.(map[string]any)
	}
	// the newest point is there although the store's range stops a step early
	msgs := panels["messages"]["series"].([]any)[0].(map[string]any)
	pts := msgs["points"].([]any)
	if pts[len(pts)-1] != 9.0 || pts[len(pts)-2] != 1.0 {
		t.Fatalf("latest points: %v", pts[len(pts)-3:])
	}
	if msgs["name"] != "from networks" {
		t.Fatalf("series name %v", msgs["name"])
	}
	// label values read as words
	if name := panels["logins"]["series"].([]any)[0].(map[string]any)["name"]; name != "IRC too many clients" {
		t.Fatalf("login series %v", name)
	}
	// deleted networks aren't among the busiest
	busy := m["busiest"].([]any)
	if len(busy) != 1 || busy[0].(map[string]any)["network"] != "lib" {
		t.Fatalf("busiest: %v", busy)
	}

	// not for users
	e.user("fay", store.RoleUser)
	if code, _ := e.login("fay").do("GET", "/api/admin/metrics", nil); code != 403 {
		t.Fatalf("a user got %d", code)
	}
}

func TestAttachedAccountsCountsIRCClientsOnly(t *testing.T) {
	// user 1: an IRC client and a web chat; user 2: only the web app open
	accounts, web := attachedAccounts(map[int64]int{1: 2, 2: 1}, map[int64]int{1: 1})
	if accounts != 1 || web != 2 {
		t.Fatalf("accounts %d web %d — want 1 and 2", accounts, web)
	}
}

// The IRC clients page tells people to accept a self-signed certificate
// only when the server has one.
func TestSelfSignedHint(t *testing.T) {
	dir := t.TempDir()
	caKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Test CA", Organization: []string{"Test"}},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	caDER, _ := x509.CreateCertificate(rand.Reader, ca, ca, &caKey.PublicKey, caKey)
	caCert, _ := x509.ParseCertificate(caDER)
	write := func(name string, self bool) (string, string) {
		key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		tpl := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "irc.example.org"}, DNSNames: []string{"irc.example.org"},
			NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
		parent, signer := caCert, any(caKey)
		if self {
			parent, signer = tpl, key
		}
		der, err := x509.CreateCertificate(rand.Reader, tpl, parent, &key.PublicKey, signer)
		if err != nil {
			t.Fatal(err)
		}
		kb, _ := x509.MarshalECPrivateKey(key)
		c, k := filepath.Join(dir, name+".crt"), filepath.Join(dir, name+".key")
		os.WriteFile(c, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600)
		os.WriteFile(k, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb}), 0o600)
		return c, k
	}
	e := newEnv(t)
	hint := func() any {
		_, cfg := e.anon().do("GET", "/api/config", nil)
		return cfg["ircSelfSigned"]
	}
	m := certs.New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	e.api.SetCerts(m)
	use := func(c, k string) {
		cfg := config.Default()
		cfg.TLSCert, cfg.TLSKey = c, k
		if err := m.Apply(cfg); err != nil {
			t.Fatal(err)
		}
	}
	use(write("ca", false))
	if hint() != false {
		t.Fatal("a CA-signed certificate was called self-signed")
	}
	use(write("self", true))
	if hint() != true {
		t.Fatal("a self-signed certificate wasn't flagged")
	}
}

func TestNetworkLogIsTheOwnersOnly(t *testing.T) {
	e := newEnv(t)
	e.user("fay", store.RoleUser)
	e.user("gus", store.RoleUser)
	fay := e.login("fay")
	_, n := fay.do("POST", "/api/networks", map[string]any{"name": "lib", "host": "irc.example.invalid", "port": 6697, "tls": true, "nick": "fay", "enabled": false})
	id := strconv.FormatInt(int64(n["id"].(float64)), 10)
	fay.do("POST", "/api/networks/"+id+"/connect", nil)
	defer fay.do("POST", "/api/networks/"+id+"/disconnect", nil)

	code, log := fay.doAny("GET", "/api/networks/"+id+"/log", nil)
	entries, _ := log.([]any)
	if code != 200 || len(entries) == 0 || entries[0].(map[string]any)["text"] != "Switched on" {
		t.Fatalf("owner: %d %v", code, log)
	}
	if code, _ := e.login("gus").doAny("GET", "/api/networks/"+id+"/log", nil); code != 404 {
		t.Fatalf("someone else got %d", code)
	}
}

func TestAdminsSeeAUsersConnectionLog(t *testing.T) {
	e := newEnv(t)
	root := e.user("root", store.RoleAdmin)
	fay := e.user("fay", store.RoleUser)
	gus := e.user("gus", store.RoleUser)
	fc := e.login("fay")
	_, n := fc.do("POST", "/api/networks", map[string]any{"name": "lib", "host": "irc.example.invalid", "port": 6697, "tls": true, "nick": "fay", "enabled": false})
	nid := strconv.FormatInt(int64(n["id"].(float64)), 10)
	fc.do("POST", "/api/networks/"+nid+"/connect", nil)
	defer fc.do("POST", "/api/networks/"+nid+"/disconnect", nil)
	id := func(u store.User) string { return strconv.FormatInt(u.ID, 10) }

	code, log := e.login("root").doAny("GET", "/api/admin/users/"+id(fay)+"/networks/"+nid+"/log", nil)
	if entries, _ := log.([]any); code != 200 || len(entries) == 0 {
		t.Fatalf("admin: %d %v", code, log)
	}
	// the network has to be that user's
	if code, _ := e.login("root").doAny("GET", "/api/admin/users/"+id(gus)+"/networks/"+nid+"/log", nil); code != 404 {
		t.Fatalf("another user's network: %d", code)
	}
	// and it's admins only
	if code, _ := e.login("gus").doAny("GET", "/api/admin/users/"+id(fay)+"/networks/"+nid+"/log", nil); code != 403 {
		t.Fatalf("a user got %d", code)
	}
	_ = root
}

func TestNetworkModules(t *testing.T) {
	e := newEnv(t)
	e.user("root", store.RoleAdmin)
	e.user("fay", store.RoleUser)
	fay := e.login("fay")
	_, n := fay.do("POST", "/api/networks", map[string]any{"name": "lib", "host": "irc.example.invalid", "port": 6697, "tls": true, "nick": "fay", "enabled": false})
	nid := strconv.FormatInt(int64(n["id"].(float64)), 10)

	code, out := fay.do("PATCH", "/api/networks/"+nid, map[string]any{"modules": map[string]any{
		"perform":    map[string]any{"enabled": true, "settings": map[string]string{"commands": "/mode %nick% +i"}},
		"kickrejoin": map[string]any{"enabled": false, "settings": map[string]string{"delay": "3"}},
	}})
	if code != 200 {
		t.Fatalf("save: %d %v", code, out)
	}
	mods := out["modules"].(map[string]any)
	if p := mods["perform"].(map[string]any); p["enabled"] != true || p["settings"].(map[string]any)["commands"] != "/mode %nick% +i" {
		t.Fatalf("modules: %v", mods)
	}
	for _, bad := range []map[string]any{
		{"nosuch": map[string]any{"enabled": true}},
		{"kickrejoin": map[string]any{"enabled": true, "settings": map[string]string{"delay": "soon"}}},
		{"perform": map[string]any{"enabled": true, "settings": map[string]string{"bogus": "x"}}},
	} {
		if code, out := fay.do("PATCH", "/api/networks/"+nid, map[string]any{"modules": bad}); code != 422 {
			t.Fatalf("%v: %d %v", bad, code, out)
		}
	}

	// the list, with an admin's off switch
	e.login("root").do("PATCH", "/api/admin/settings", map[string]any{"set": map[string]string{"modules_off": "autoop"}})
	code, list := fay.doAny("GET", "/api/modules", nil)
	got := map[string]bool{}
	for _, m := range list.([]any) {
		mm := m.(map[string]any)
		got[mm["name"].(string)] = mm["off"].(bool)
	}
	if code != 200 || len(got) != 9 || !got["autoop"] || got["perform"] {
		t.Fatalf("modules list: %d %v", code, got)
	}
}
