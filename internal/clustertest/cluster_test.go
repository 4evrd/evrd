// Package clustertest runs two real evrd nodes in one process — each with its
// public API, IRC listener, internal endpoints and cluster member — against
// one shared database and history store, and drives them the way browsers,
// IRC clients and admins would: always through the node that does NOT own
// the user, so every request must be forwarded.
package clustertest

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"code.ircforever.org/ircforever/evrd/internal/accounts"
	"code.ircforever.org/ircforever/evrd/internal/api"
	"code.ircforever.org/ircforever/evrd/internal/auth"
	"code.ircforever.org/ircforever/evrd/internal/bouncer"
	"code.ircforever.org/ircforever/evrd/internal/client"
	"code.ircforever.org/ircforever/evrd/internal/cluster"
	"code.ircforever.org/ircforever/evrd/internal/config"
	"code.ircforever.org/ircforever/evrd/internal/history"
	"code.ircforever.org/ircforever/evrd/internal/store"
	"code.ircforever.org/ircforever/evrd/internal/testirc"
)

const secret = "test-cluster-secret"

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

type node struct {
	id     string
	b      *bouncer.Bouncer
	cl     *cluster.Cluster
	public *httptest.Server
	irc    string // public IRC address
}

type env struct {
	t     *testing.T
	st    store.Store
	acc   *accounts.Service
	clock *clock
	nodes []*node
}

func freeAddr(t *testing.T) string {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().String()
}

func waitListening(t *testing.T, addr string) {
	for i := 0; i < 100; i++ {
		if c, err := net.Dial("tcp", addr); err == nil {
			c.Close()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("%s never listened", addr)
}

func newEnv(t *testing.T) *env {
	t.Helper()
	st, err := store.Open("sqlite:" + filepath.Join(t.TempDir(), "evrd.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	hist, err := history.Open(filepath.Join(t.TempDir(), "history"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { hist.Close() })
	cfg := config.Default()
	cfg.Auth.CookieSecure = false
	cfg.Limits = config.Limits{MaxNetworks: 5, MaxClients: 5, BacklogLines: 100}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	acc := accounts.New(st, cfg.Limits)
	am, err := auth.New(context.Background(), st, acc, cfg.Auth, "", log)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	e := &env{t: t, st: st, acc: acc, clock: &clock{t: time.Now()}}
	for _, id := range []string{"node-a", "node-b"} {
		n := &node{id: id}
		internalHTTP := httptest.NewUnstartedServer(nil)
		ircInternal, ircPublic := freeAddr(t), freeAddr(t)
		n.irc = ircPublic

		n.b = bouncer.New(st, hist, log)
		n.cl = cluster.New(st, store.Node{ID: id, HTTPURL: "http://" + internalHTTP.Listener.Addr().String(), IRCAddr: ircInternal},
			cluster.Hooks{Acquire: n.b.ResumeUser, Lose: n.b.StopUser}, log)
		n.cl.SetClock(e.clock.now)
		n.b.SetOwnership(n.cl.Owns)
		n.b.Start(ctx)

		ircSrv := client.NewServer(n.b, acc, log, "", "")
		ircSrv.SetCluster(n.cl, secret)
		go ircSrv.ListenInternal(ctx, ircInternal)
		go ircSrv.ListenAndServe(ctx, []string{"irc+insecure://" + ircPublic})

		apiSrv := api.NewServer(n.b, acc, am, cfg, ircSrv, log)
		apiSrv.SetCluster(n.cl, secret)
		internalHTTP.Config.Handler = apiSrv.InternalHandler()
		internalHTTP.Start()
		t.Cleanup(internalHTTP.Close)
		n.public = httptest.NewServer(apiSrv.Handler())
		t.Cleanup(n.public.Close)

		waitListening(t, ircPublic)
		waitListening(t, ircInternal)
		e.nodes = append(e.nodes, n)
	}
	return e
}

// settle ticks both nodes long enough for users to spread.
func (e *env) settle() {
	for i := 0; i < 6; i++ {
		e.tick()
	}
}

func (e *env) tick(nodes ...*node) {
	if nodes == nil {
		nodes = e.nodes
	}
	e.clock.add(cluster.Heartbeat)
	for _, n := range nodes {
		n.cl.Tick()
	}
}

// user creates an account with one network on the fake IRC server.
func (e *env) user(name, role, ircd string) store.User {
	e.t.Helper()
	u, err := e.acc.Create(accounts.CreateInput{Username: name, Password: name + "-password", Role: role})
	if err != nil {
		e.t.Fatal(err)
	}
	host, port, _ := net.SplitHostPort(ircd)
	p, _ := strconv.Atoi(port)
	e.st.CreateNetwork(store.Network{UserID: u.ID, Name: "net", Host: host, Port: p, Nick: name, Channels: []string{"#c"}, Enabled: true})
	return u
}

// ownerAndOther returns the node running the user and the other one.
func (e *env) ownerAndOther(uid int64) (owner, other *node) {
	e.t.Helper()
	for _, n := range e.nodes {
		if n.cl.Owns(uid) {
			owner = n
		} else {
			other = n
		}
	}
	if owner == nil || other == nil {
		e.t.Fatalf("user %d: owner=%v", uid, owner)
	}
	return owner, other
}

type browser struct {
	t    *testing.T
	base string
	c    *http.Client
}

func (e *env) login(n *node, name string) *browser {
	e.t.Helper()
	jar, _ := cookiejar.New(nil)
	b := &browser{t: e.t, base: n.public.URL, c: &http.Client{Jar: jar}}
	if code, out := b.do("POST", "/auth/password", map[string]string{"username": name, "password": name + "-password"}); code != 200 {
		e.t.Fatalf("login %s: %d %v", name, code, out)
	}
	return b
}

func (b *browser) do(method, path string, body any) (int, map[string]any) {
	b.t.Helper()
	var r io.Reader
	if body != nil {
		buf, _ := json.Marshal(body)
		r = bytes.NewReader(buf)
	}
	req, _ := http.NewRequest(method, b.base+path, r)
	req.Header.Set("Content-Type", "application/json")
	res, err := b.c.Do(req)
	if err != nil {
		b.t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	out := map[string]any{}
	if len(raw) > 0 && raw[0] == '[' {
		var list []map[string]any
		json.Unmarshal(raw, &list)
		out["list"] = list
	} else {
		json.Unmarshal(raw, &out)
	}
	return res.StatusCode, out
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for i := 0; i < 150; i++ {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("never: %s", what)
}

type ircConn struct {
	t  *testing.T
	c  net.Conn
	sc *bufio.Scanner
}

func dialIRC(t *testing.T, addr, pass string) *ircConn {
	t.Helper()
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	ic := &ircConn{t: t, c: c, sc: bufio.NewScanner(c)}
	ic.send("CAP LS 302")
	ic.send("CAP REQ :server-time")
	ic.send("CAP END")
	ic.send("PASS " + pass)
	ic.send("NICK x")
	ic.send("USER x 0 * :x")
	return ic
}

func (ic *ircConn) send(l string) { fmt.Fprintf(ic.c, "%s\r\n", l) }

func (ic *ircConn) expect(want string) string {
	ic.t.Helper()
	ic.c.SetReadDeadline(time.Now().Add(3 * time.Second))
	var seen []string
	for ic.sc.Scan() {
		if strings.Contains(ic.sc.Text(), want) {
			return ic.sc.Text()
		}
		seen = append(seen, ic.sc.Text())
	}
	ic.t.Fatalf("never got %q; saw:\n%s", want, strings.Join(seen, "\n"))
	return ""
}

func (ic *ircConn) closedByServer() bool {
	ic.c.SetReadDeadline(time.Now().Add(3 * time.Second))
	for ic.sc.Scan() {
	}
	err := ic.sc.Err()
	return err == nil // EOF, not a timeout
}

// pickUsers creates users until each node owns at least one.
func (e *env) pickUsers(ircd string) (onA, onB store.User) {
	e.t.Helper()
	for i := 0; i < 40 && (onA.ID == 0 || onB.ID == 0); i++ {
		u := e.user(fmt.Sprintf("user%d", i), store.RoleUser, ircd)
		e.settle()
		owner, _ := e.ownerAndOther(u.ID)
		if owner == e.nodes[0] && onA.ID == 0 {
			onA = u
		} else if owner == e.nodes[1] && onB.ID == 0 {
			onB = u
		}
	}
	if onA.ID == 0 || onB.ID == 0 {
		e.t.Fatal("couldn't place a user on each node")
	}
	return onA, onB
}

func TestWebRequestsAreServedByTheOwner(t *testing.T) {
	e := newEnv(t)
	ircd := testirc.Start(t)
	e.settle()
	u, _ := e.pickUsers(ircd)
	owner, other := e.ownerAndOther(u.ID)
	eventually(t, "owner connected the network", func() bool {
		nets, _ := e.st.ListNetworks(u.ID)
		return owner.b.Status(nets[0].ID) == "connected"
	})

	b := e.login(other, u.Username)
	code, out := b.do("GET", "/api/networks", nil)
	nets := out["list"].([]map[string]any)
	if code != 200 || len(nets) != 1 || nets[0]["status"] != "connected" {
		t.Fatalf("networks through the other node: %d %v", code, out)
	}
	id := int64(nets[0]["id"].(float64))
	if code, _ := b.do("POST", fmt.Sprintf("/api/networks/%d/disconnect", id), nil); code != 200 {
		t.Fatalf("disconnect: %d", code)
	}
	if owner.b.Status(id) != "disconnected" {
		t.Fatal("disconnect didn't reach the owner")
	}
	if code, out := b.do("POST", fmt.Sprintf("/api/networks/%d/connect", id), nil); code != 200 {
		t.Fatalf("connect: %d %v", code, out)
	}
	if _, ok := other.b.ConnFor(id); ok {
		t.Fatal("the non-owner connected the network")
	}
	eventually(t, "owner reconnected", func() bool { return owner.b.Status(id) == "connected" })

	// The chat WebSocket, opened on the other node, reaches the owner's
	// connection: a sent line is recorded and echoed back.
	u2, _ := url.Parse(other.public.URL)
	hdr := http.Header{}
	hdr.Set("Origin", other.public.URL)
	for _, c := range b.c.Jar.Cookies(u2) {
		hdr.Add("Cookie", c.Name+"="+c.Value)
	}
	ws, res, err := websocket.DefaultDialer.Dial("ws://"+u2.Host+fmt.Sprintf("/api/networks/%d/stream", id), hdr)
	if err != nil {
		t.Fatalf("websocket through the other node: %v (%v)", err, res)
	}
	defer ws.Close()
	ws.WriteJSON(map[string]string{"target": "#c", "text": "hello across nodes"})
	ws.SetReadDeadline(time.Now().Add(3 * time.Second))
	var m map[string]any
	if err := ws.ReadJSON(&m); err != nil || m["text"] != "hello across nodes" {
		t.Fatalf("echo: %v %v", m, err)
	}
	// *status from the web runs on the owner too.
	ws.WriteJSON(map[string]string{"status": "networks"})
	for {
		if err := ws.ReadJSON(&m); err != nil {
			t.Fatal(err)
		}
		if m["target"] == "*status" && strings.Contains(m["text"].(string), "connected") {
			break
		}
	}
}

func TestIRCClientsAreSplicedToTheOwner(t *testing.T) {
	e := newEnv(t)
	ircd := testirc.Start(t)
	e.settle()
	u, _ := e.pickUsers(ircd)
	owner, other := e.ownerAndOther(u.ID)

	ic := dialIRC(t, other.irc, u.Username+":"+u.Username+"-password")
	ic.expect("Welcome to evrd")
	ic.expect("JOIN #c")
	ic.send("PRIVMSG *status :networks")
	ic.expect("* net")
	eventually(t, "the client counted on the owner", func() bool { return owner.b.Clients(u.ID) == 1 })
	if other.b.Clients(u.ID) != 0 {
		t.Fatal("the client was attached on the non-owner")
	}
	// server-time, negotiated with the first node, still applies on the owner.
	ic.send("PRIVMSG *status :playback #c")
	ic.expect("Replayed")

	// Bad credentials are still refused (by the owner).
	bad := dialIRC(t, other.irc, u.Username+":wrong")
	bad.expect("464")
}

func TestAdminActionsReachTheOwner(t *testing.T) {
	e := newEnv(t)
	ircd := testirc.Start(t)
	e.settle()
	e.user("root", store.RoleAdmin, ircd)
	u, _ := e.pickUsers(ircd)
	owner, other := e.ownerAndOther(u.ID)
	ic := dialIRC(t, owner.irc, u.Username+":"+u.Username+"-password")
	ic.expect("Welcome to evrd")

	// The admin's request lands on the non-owner and must still cut the user
	// off at once, on the owner.
	b := e.login(other, "root")
	code, out := b.do("PATCH", fmt.Sprintf("/api/admin/users/%d", u.ID), map[string]any{"suspended": true})
	if code != 200 || out["suspended"] != true {
		t.Fatalf("suspend: %d %v", code, out)
	}
	if !ic.closedByServer() {
		t.Fatal("the suspended user's IRC client wasn't dropped")
	}
	e.tick()
	if owner.cl.Owns(u.ID) || other.cl.Owns(u.ID) {
		t.Fatal("a suspended user is still owned")
	}
	// A suspended user has no owner: their admin page is served locally.
	if code, out := b.do("GET", fmt.Sprintf("/api/admin/users/%d", u.ID), nil); code != 200 || out["suspended"] != true {
		t.Fatalf("admin page of a suspended user: %d %v", code, out)
	}

	b.do("PATCH", fmt.Sprintf("/api/admin/users/%d", u.ID), map[string]any{"suspended": false})
	e.tick()
	again, _ := e.ownerAndOther(u.ID)
	nets, _ := e.st.ListNetworks(u.ID)
	eventually(t, "unsuspended user reconnects on its owner", func() bool { return again.b.Status(nets[0].ID) == "connected" })

	// The overview adds up every node.
	dialIRC(t, other.irc, u.Username+":"+u.Username+"-password").expect("Welcome to evrd")
	eventually(t, "overview counts the client", func() bool {
		_, ov := b.do("GET", "/api/admin/overview", nil)
		return ov["attachedClients"] == 1.0 && ov["nodes"] == 2.0
	})
	_, list := b.do("GET", "/api/admin/users", nil)
	for _, row := range list["list"].([]map[string]any) {
		if row["username"] == u.Username && row["clients"] != 1.0 {
			t.Fatalf("user list clients: %v", row)
		}
	}
}

func TestDrainMovesUsersToTheOtherNode(t *testing.T) {
	e := newEnv(t)
	ircd := testirc.Start(t)
	e.settle()
	u, _ := e.pickUsers(ircd)
	owner, other := e.ownerAndOther(u.ID)
	ic := dialIRC(t, other.irc, u.Username+":"+u.Username+"-password")
	ic.expect("Welcome to evrd")

	owner.cl.Drain() // a graceful shutdown
	if !ic.closedByServer() {
		t.Fatal("clients of a drained node weren't dropped")
	}
	// The drained node stops beating; once it's no longer live the survivor
	// takes its users.
	for i := 0; i < 4; i++ {
		e.tick(other)
	}
	if !other.cl.Owns(u.ID) {
		t.Fatal("the user didn't move")
	}
	nets, _ := e.st.ListNetworks(u.ID)
	eventually(t, "the new owner connected the network", func() bool { return other.b.Status(nets[0].ID) == "connected" })
	// Clients reconnect and land on the new owner.
	dialIRC(t, owner.irc, u.Username+":"+u.Username+"-password").expect("Welcome to evrd")
}
