package handlers_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/4evrd/evrd/internal/auth"
	"github.com/4evrd/evrd/internal/cluster"
	"github.com/4evrd/evrd/internal/http/middleware"
	"github.com/4evrd/evrd/internal/store"
)

const clusterSecret = "cluster-secret"

// placement is where each user runs, shared by every node of a fake
// cluster. Users not in it have no owner.
type placement struct {
	mu    sync.Mutex
	owner map[int64]store.Node
	err   error // WaitOwner's error, when set
}

func (p *placement) place(uid int64, n store.Node) {
	p.mu.Lock()
	p.owner[uid] = n
	p.mu.Unlock()
}

func (p *placement) fail(err error) {
	p.mu.Lock()
	p.err = err
	p.mu.Unlock()
}

// fakeCluster is one node's view of a placement.
type fakeCluster struct {
	self   store.Node
	p      *placement
	nodes  []store.Node
	disown bool // Owns says no even for this node's users (a lease being handed over)
}

func (f *fakeCluster) Self() store.Node { return f.self }
func (f *fakeCluster) Owns(uid int64) bool {
	f.p.mu.Lock()
	defer f.p.mu.Unlock()
	n, ok := f.p.owner[uid]
	return !f.disown && ok && n.ID == f.self.ID
}
func (f *fakeCluster) WaitOwner(_ context.Context, uid int64, _ time.Duration) (store.Node, error) {
	f.p.mu.Lock()
	defer f.p.mu.Unlock()
	if f.p.err != nil {
		return store.Node{}, f.p.err
	}
	n, ok := f.p.owner[uid]
	if !ok {
		return store.Node{}, cluster.ErrNoOwner
	}
	return n, nil
}
func (f *fakeCluster) Nodes() []store.Node { return f.nodes }

// twoNodes is a cluster of a front node (where browsers arrive) and a back
// node, sharing one database. Users placed on back are served there.
type twoNodes struct {
	front, back   *env
	p             *placement
	frontN, backN store.Node
}

func newTwoNodes(t *testing.T) *twoNodes {
	t.Helper()
	front := newEnv(t)
	back := newEnvOn(t, front.st)
	internal := httptest.NewServer(nil)
	t.Cleanup(internal.Close)
	frontN := store.Node{ID: "front", HTTPURL: "http://front.invalid", StartedAt: time.Now(), HeartbeatAt: time.Now()}
	backN := store.Node{ID: "back", HTTPURL: internal.URL, StartedAt: time.Now(), HeartbeatAt: time.Now()}
	p := &placement{owner: map[int64]store.Node{}}
	nodes := []store.Node{frontN, backN}
	front.api.SetCluster(&fakeCluster{self: frontN, p: p, nodes: nodes}, clusterSecret)
	back.api.SetCluster(&fakeCluster{self: backN, p: p, nodes: nodes}, clusterSecret)
	internal.Config.Handler = back.api.InternalHandler()
	return &twoNodes{front: front, back: back, p: p, frontN: frontN, backN: backN}
}

func TestRequestsAreServedByTheOwner(t *testing.T) {
	cl := newTwoNodes(t)
	e := cl.front
	root := e.user("root", store.RoleAdmin)
	alice := e.user("alice", store.RoleUser)
	cl.p.place(alice.ID, cl.backN)
	cl.p.place(root.ID, cl.frontN)
	ac, admin := e.login("alice"), e.login("root")

	if code, me := ac.do("GET", "/api/me", nil); code != 200 || me["username"] != "alice" {
		t.Fatalf("me through the front: %d %v", code, me)
	}
	// A network connected through the front runs on the back.
	irc := newFakeIRC(t)
	code, n := ac.do("POST", "/api/networks", map[string]any{"name": "fake", "host": "127.0.0.1", "port": irc.port(), "tls": false})
	if code != 200 {
		t.Fatalf("create: %d %v", code, n)
	}
	id := int64(n["id"].(float64))
	t.Cleanup(func() { cl.back.b.Disconnect(id) })
	path := "/api/networks/" + strconv.FormatInt(id, 10)
	if code, out := ac.do("POST", path+"/connect", nil); code != 200 {
		t.Fatalf("connect: %d %v", code, out)
	}
	irc.expect("USER ")
	if _, live := cl.back.b.ConnFor(id); !live {
		t.Fatal("not running on the owner")
	}
	if _, live := e.b.ConnFor(id); live {
		t.Fatal("running on the front node")
	}
	eventually(t, "registration", func() bool { _, n := ac.do("GET", path, nil); return n["status"] == "connected" })

	// The chat socket is proxied too.
	ws, _, err := ac.stream(path, nil)
	if err != nil {
		t.Fatalf("socket through the front: %v", err)
	}
	defer ws.Close()
	next(t, ws) // ready
	irc.send(":carol!c@h PRIVMSG #x :via the back")
	if m := nextWith(t, ws, "text", "via the back"); m["sender"] != "carol" {
		t.Fatalf("pushed: %v", m)
	}

	// Admin pages about one user go to that user's node; pages about everyone
	// gather each node's live numbers.
	if code, u := admin.do("GET", userPath(alice), nil); code != 200 || u["networks"].([]any)[0].(map[string]any)["status"] != "connected" {
		t.Fatalf("admin get through the front: %d %v", code, u)
	}
	_, list := admin.doAny("GET", "/api/admin/users", nil)
	for _, u := range list.([]any) {
		if um := u.(map[string]any); um["username"] == "alice" && (um["online"] != 1.0 || um["status"] != "online") {
			t.Fatalf("alice's live state from the back: %v", um)
		}
	}
	code, ov := admin.do("GET", "/api/admin/overview", nil)
	if code != 200 || ov["clustered"] != true || ov["linksUp"] != 1.0 || ov["online"] != 1.0 {
		t.Fatalf("admin overview: %d %v", code, ov)
	}
	nodes := ov["nodes"].([]any)
	if len(nodes) != 2 || nodes[0].(map[string]any)["host"] != "front.invalid" || nodes[1].(map[string]any)["linksUp"] != 1.0 {
		t.Fatalf("nodes: %v", nodes)
	}
}

func TestForwardingFailures(t *testing.T) {
	cl := newTwoNodes(t)
	e := cl.front
	alice := e.user("alice", store.RoleUser)
	ac := e.login("alice")

	// No owner (suspended, just deleted): the database is the truth, served here.
	if code, me := ac.do("GET", "/api/me", nil); code != 200 || me["username"] != "alice" {
		t.Fatalf("no owner: %d %v", code, me)
	}
	// An owner with no address, and one that doesn't answer.
	cl.p.place(alice.ID, store.Node{ID: "nowhere"})
	if code, out := ac.do("GET", "/api/me", nil); code != http.StatusServiceUnavailable || out["error"] != middleware.Moving {
		t.Fatalf("owner without an address: %d %v", code, out)
	}
	dead := httptest.NewServer(nil)
	dead.Close()
	cl.p.place(alice.ID, store.Node{ID: "dead", HTTPURL: dead.URL})
	if code, out := ac.do("GET", "/api/me", nil); code != http.StatusServiceUnavailable || out["error"] != middleware.Moving {
		t.Fatalf("owner down: %d %v", code, out)
	}
	cl.p.fail(errors.New("lease table on fire"))
	if code, _ := ac.do("GET", "/api/me", nil); code != 500 {
		t.Fatalf("lookup error: %d", code)
	}
}

// Nodes that don't answer (or answer nonsense) are left out of the usage
// numbers; the page still loads.
func TestUsageSkipsBadNodes(t *testing.T) {
	e := newEnv(t)
	e.user("root", store.RoleAdmin)
	admin := e.login("root")
	dead := httptest.NewServer(nil)
	dead.Close()
	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "no", http.StatusInternalServerError)
	}))
	defer broken.Close()
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(auth.HeaderSecret) != clusterSecret {
			http.Error(w, "no secret", http.StatusUnauthorized)
			return
		}
		writeJSON(w, 200, map[string]any{"clients": map[int64]int{1: 2}, "online": map[int64]int{1: 3}, "connected": 4, "attached": 2,
			"links": []map[string]any{{"node": "good", "user": 1, "network": 1, "status": "connected"}}})
	}))
	defer good.Close()
	self := store.Node{ID: "me"}
	nodes := []store.Node{self, {ID: "nourl"}, {ID: "dead", HTTPURL: dead.URL}, {ID: "broken", HTTPURL: broken.URL}, {ID: "good", HTTPURL: good.URL}}
	e.api.SetCluster(&fakeCluster{self: self, p: &placement{owner: map[int64]store.Node{}}, nodes: nodes}, clusterSecret)

	code, ov := admin.do("GET", "/api/admin/overview", nil)
	if code != 200 {
		t.Fatalf("overview: %d %v", code, ov)
	}
	if ov["connectedNetworks"] != 4.0 || ov["attachedClients"] != 2.0 || ov["accountsAttached"] != 1.0 || ov["webAttached"] != 1.0 || ov["online"] != 1.0 {
		t.Fatalf("totals from the good node only: %v", ov)
	}
	if len(ov["nodes"].([]any)) != 5 {
		t.Fatalf("nodes: %v", ov["nodes"])
	}
}

// While a lease is handed over, the lookup can name this node although
// Owns says no: the request is served here.
func TestOwnerLookupNamesThisNode(t *testing.T) {
	e := newEnv(t)
	alice := e.user("alice", store.RoleUser)
	ac := e.login("alice")
	self := store.Node{ID: "me"}
	p := &placement{owner: map[int64]store.Node{alice.ID: self}}
	e.api.SetCluster(&fakeCluster{self: self, p: p, disown: true}, clusterSecret)
	if code, me := ac.do("GET", "/api/me", nil); code != 200 || me["username"] != "alice" {
		t.Fatalf("me: %d %v", code, me)
	}
}

func TestForwardedRequestHeaders(t *testing.T) {
	e := newEnv(t)
	alice := e.user("alice", store.RoleUser)
	ac := e.login("alice")
	got := make(chan *http.Request, 1)
	owner := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got <- r
		writeJSON(w, 200, map[string]string{"from": "owner"})
	}))
	defer owner.Close()
	p := &placement{owner: map[int64]store.Node{alice.ID: {ID: "o", HTTPURL: owner.URL}}}
	e.api.SetCluster(&fakeCluster{self: store.Node{ID: "front"}, p: p}, clusterSecret)

	if code, out := ac.do("GET", "/api/me?x=1", nil); code != 200 || out["from"] != "owner" {
		t.Fatalf("forwarded: %d %v", code, out)
	}
	r := <-got
	sessions, _ := e.st.ListSessions(t.Context(), alice.ID)
	switch {
	case r.URL.Path != "/api/me" || r.URL.RawQuery != "x=1":
		t.Errorf("path: %s", r.URL)
	case r.Header.Get("Cookie") != "":
		t.Errorf("the session cookie was forwarded")
	case r.Header.Get(auth.HeaderSecret) != clusterSecret:
		t.Errorf("secret: %q", r.Header.Get(auth.HeaderSecret))
	case r.Header.Get(auth.HeaderCaller) != strconv.FormatInt(alice.ID, 10):
		t.Errorf("caller: %q", r.Header.Get(auth.HeaderCaller))
	case len(sessions) != 1 || r.Header.Get(auth.HeaderSession) != sessions[0].ID:
		t.Errorf("session: %q", r.Header.Get(auth.HeaderSession))
	case r.Header.Get("X-Forwarded-For") != "127.0.0.1":
		t.Errorf("X-Forwarded-For: %q", r.Header.Get("X-Forwarded-For"))
	case r.Host != e.srv.Listener.Addr().String():
		t.Errorf("Host %q: the owner checks same-origin against the browser's host", r.Host)
	}
}

func TestInternalEndpoint(t *testing.T) {
	e := newEnv(t)
	alice := e.user("alice", store.RoleUser)
	self := store.Node{ID: "me"}
	p := &placement{owner: map[int64]store.Node{}}
	e.api.SetCluster(&fakeCluster{self: self, p: p}, clusterSecret)
	internal := httptest.NewServer(e.api.InternalHandler())
	defer internal.Close()
	get := func(path string, hdr map[string]string) (int, map[string]any) {
		req, _ := http.NewRequest("GET", internal.URL+path, nil)
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(res.Body).Decode(&out)
		return res.StatusCode, out
	}
	caller := strconv.FormatInt(alice.ID, 10)
	if code, _ := get("/api/me", map[string]string{auth.HeaderCaller: caller}); code != 401 {
		t.Fatalf("without the secret: %d", code)
	}
	if code, _ := get("/internal/usage", map[string]string{auth.HeaderSecret: "wrong"}); code != 401 {
		t.Fatalf("wrong secret: %d", code)
	}
	// Forwarded here, but this node has let the user go since: no loop.
	if code, out := get("/api/me", map[string]string{auth.HeaderSecret: clusterSecret, auth.HeaderCaller: caller}); code != http.StatusMisdirectedRequest {
		t.Fatalf("not the owner: %d %v", code, out)
	}
	p.place(alice.ID, self)
	if code, me := get("/api/me", map[string]string{auth.HeaderSecret: clusterSecret, auth.HeaderCaller: caller}); code != 200 || me["username"] != "alice" {
		t.Fatalf("as the owner: %d %v", code, me)
	}
	code, u := get("/internal/usage", map[string]string{auth.HeaderSecret: clusterSecret})
	if code != 200 || u["links"] == nil || u["connected"] != 0.0 {
		t.Fatalf("usage: %d %v", code, u)
	}
}
