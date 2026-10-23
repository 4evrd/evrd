package api

import (
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/4evrd/evrd/internal/history"
	"github.com/4evrd/evrd/internal/store"
)

// liveNetwork creates a network for c pointing at a fake IRC server,
// connects it and waits until it's registered. It returns the network's
// path (/api/networks/{id}) and the server.
func (e *env) liveNetwork(c *client, name string) (string, *fakeIRC, int64) {
	e.t.Helper()
	irc := newFakeIRC(e.t)
	code, n := c.do("POST", "/api/networks", map[string]any{"name": name, "host": "127.0.0.1", "port": irc.port(), "tls": false})
	if code != 200 {
		e.t.Fatalf("create %s: %d %v", name, code, n)
	}
	id := int64(n["id"].(float64))
	e.t.Cleanup(func() { e.api.b.Disconnect(id) })
	path := "/api/networks/" + strconv.FormatInt(id, 10)
	if code, out := c.do("POST", path+"/connect", nil); code != 200 {
		e.t.Fatalf("connect: %d %v", code, out)
	}
	irc.expect("USER ")
	eventually(e.t, "registration", func() bool {
		_, n := c.do("GET", path, nil)
		return n["status"] == "connected"
	})
	return path, irc, id
}

func TestListAndGetNetworks(t *testing.T) {
	e := newEnv(t)
	e.user("alice", store.RoleUser)
	e.user("bob", store.RoleUser)
	alice, bob := e.login("alice"), e.login("bob")

	if code, out := alice.doAny("GET", "/api/networks", nil); code != 200 || len(out.([]any)) != 0 {
		t.Fatalf("empty list: %d %v", code, out)
	}
	_, n := alice.do("POST", "/api/networks", map[string]any{
		"name": "libera", "servers": []map[string]any{{"host": "irc.libera.chat", "port": 6697, "tls": true, "password": "pw"}},
	})
	code, list := alice.doAny("GET", "/api/networks", nil)
	if code != 200 || len(list.([]any)) != 1 {
		t.Fatalf("list: %d %v", code, list)
	}
	got := list.([]any)[0].(map[string]any)
	if got["name"] != "libera" || got["status"] != "disconnected" || got["currentNick"] != "alice" {
		t.Fatalf("listed network: %v", got)
	}
	sv := got["servers"].([]any)[0].(map[string]any)
	if sv["hasPassword"] != true || sv["password"] != nil {
		t.Fatalf("server passwords must be hidden: %v", sv)
	}
	if code, out := bob.doAny("GET", "/api/networks", nil); code != 200 || len(out.([]any)) != 0 {
		t.Fatalf("bob sees alice's networks: %v", out)
	}
	path := "/api/networks/" + strconv.Itoa(int(n["id"].(float64)))
	if code, out := alice.do("GET", path, nil); code != 200 || out["name"] != "libera" {
		t.Fatalf("get: %d %v", code, out)
	}
	for _, p := range []string{path, "/api/networks/999", "/api/networks/nope"} {
		if code, _ := bob.do("GET", p, nil); code != 404 {
			t.Errorf("bob GET %s: %d, want 404", p, code)
		}
	}
}

func TestNetworkInputValidation(t *testing.T) {
	e := newEnv(t)
	e.user("alice", store.RoleUser)
	c := e.login("alice")
	req, _ := http.NewRequest("POST", e.srv.URL+"/api/networks", strings.NewReader("{not json"))
	res, err := c.c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 400 {
		t.Fatalf("malformed body: %d, want 400", res.StatusCode)
	}
	if code, out := c.do("POST", "/api/networks", map[string]any{"name": "x"}); code != 422 {
		t.Fatalf("no server: %d %v", code, out)
	}
	_, n := c.do("POST", "/api/networks", map[string]any{"name": "x", "host": "irc.example.org"})
	path := "/api/networks/" + strconv.Itoa(int(n["id"].(float64)))
	req, _ = http.NewRequest("PATCH", e.srv.URL+path, strings.NewReader("["))
	res, err = c.c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 400 {
		t.Fatalf("malformed patch: %d, want 400", res.StatusCode)
	}
}

func TestDeleteNetwork(t *testing.T) {
	e := newEnv(t)
	e.user("alice", store.RoleUser)
	c := e.login("alice")
	_, n := c.do("POST", "/api/networks", map[string]any{"name": "x", "host": "irc.example.org"})
	id := int64(n["id"].(float64))
	e.api.b.History().Add(history.Message{NetworkID: id, Target: "#a", Sender: "z", Kind: "message", Text: "hi", Time: time.Now()})
	path := "/api/networks/" + strconv.FormatInt(id, 10)
	if code, _ := c.do("DELETE", path, nil); code != 204 {
		t.Fatalf("delete: %d", code)
	}
	if code, _ := c.do("GET", path, nil); code != 404 {
		t.Fatalf("after delete: %d", code)
	}
	if m, _ := e.api.b.History().Recent(id, "#a", 10); len(m) != 0 {
		t.Fatalf("history kept after delete: %v", m)
	}
	if code, _ := c.do("DELETE", path, nil); code != 404 {
		t.Fatalf("delete twice: %d", code)
	}
}

func TestMessagesAndBuffersOffline(t *testing.T) {
	e := newEnv(t)
	e.user("alice", store.RoleUser)
	c := e.login("alice")
	_, n := c.do("POST", "/api/networks", map[string]any{"name": "x", "host": "irc.example.org", "channels": []string{"#saved"}})
	id := int64(n["id"].(float64))
	path := "/api/networks/" + strconv.FormatInt(id, 10)
	now := time.Now()
	for i, text := range []string{"one", "two", "three"} {
		e.api.b.History().Add(history.Message{NetworkID: id, Target: "#a", Sender: "zed", Kind: "message", Text: text, Extra: "x", Time: now.Add(time.Duration(i) * time.Second)})
	}
	e.api.b.History().Add(history.Message{NetworkID: id, Target: "zed", Sender: "zed", Kind: "message", Text: "psst", Time: now})

	code, out := c.doAny("GET", path+"/messages?target=%23a", nil)
	msgs, _ := out.([]any)
	if code != 200 || len(msgs) != 3 {
		t.Fatalf("messages: %d %v", code, out)
	}
	first, last := msgs[0].(map[string]any), msgs[2].(map[string]any)
	if first["text"] != "one" || last["text"] != "three" || first["sender"] != "zed" || first["time"] != float64(now.Unix()) {
		t.Fatalf("messages should be oldest first with fields: %v", msgs)
	}
	if _, out := c.doAny("GET", path+"/messages?target=nobody", nil); len(out.([]any)) != 0 {
		t.Fatalf("empty buffer: %v", out)
	}

	// Not running: buffers come from history, plus saved channels.
	code, out = c.doAny("GET", path+"/buffers", nil)
	if code != 200 {
		t.Fatalf("buffers: %d %v", code, out)
	}
	names := []string{}
	for _, b := range out.([]any) {
		bm := b.(map[string]any)
		names = append(names, bm["name"].(string))
		if bm["joined"] != false {
			t.Errorf("offline buffer %v marked joined", bm)
		}
	}
	if strings.Join(names, ",") != "#a,zed,#saved" {
		t.Fatalf("buffer names: %v", names)
	}

	// Read markers persist without a live connection.
	if code, _ := c.do("POST", path+"/read", map[string]string{"target": "#a"}); code != 204 {
		t.Fatalf("mark read: %d", code)
	}
	_, ch := c.do("GET", path+"/channel?target=%23a", nil)
	if ch["readAt"] == nil || ch["joined"] != false || ch["topic"] != "" {
		t.Fatalf("channel offline: %v", ch)
	}
	if code, _ := c.do("POST", path+"/read", nil); code != 204 {
		t.Fatalf("mark all read: %d", code)
	}
	marks, _ := e.st.ReadMarkers(t.Context(), id)
	if marks["zed"].IsZero() || marks["#a"].IsZero() {
		t.Fatalf("read markers: %v", marks)
	}
}

func TestNetworkEndpointsAreTheOwnersOnly(t *testing.T) {
	e := newEnv(t)
	e.user("alice", store.RoleUser)
	e.user("bob", store.RoleUser)
	alice, bob := e.login("alice"), e.login("bob")
	_, n := alice.do("POST", "/api/networks", map[string]any{"name": "x", "host": "irc.example.org"})
	path := "/api/networks/" + strconv.Itoa(int(n["id"].(float64)))
	for _, rt := range []string{
		"GET /messages", "GET /buffers", "GET /channel?target=%23a", "POST /read",
		"POST /connect", "POST /disconnect", "GET /stream", "PATCH ", "DELETE ",
	} {
		method, suffix, _ := strings.Cut(rt, " ")
		if code, _ := bob.do(method, path+suffix, map[string]any{}); code != 404 {
			t.Errorf("bob %s: %d, want 404", rt, code)
		}
	}
	// alice's network survived bob's attempts
	if code, out := alice.do("GET", path, nil); code != 200 || out["enabled"] != false {
		t.Fatalf("alice's network: %d %v", code, out)
	}
}

func TestLiveNetwork(t *testing.T) {
	e := newEnv(t)
	e.user("alice", store.RoleUser)
	c := e.login("alice")
	path, irc, id := e.liveNetwork(c, "fake")

	_, n := c.do("GET", path, nil)
	if n["enabled"] != true || n["currentNick"] != "alice" || n["connectedAt"] == nil || !strings.HasPrefix(n["server"].(string), "127.0.0.1:") {
		t.Fatalf("live network: %v", n)
	}
	// Connecting again is a no-op.
	if code, out := c.do("POST", path+"/connect", nil); code != 200 || out["status"] != "connected" {
		t.Fatalf("connect again: %d %v", code, out)
	}

	conn, _ := e.api.b.ConnFor(id)
	conn.SendRaw("JOIN #evrd")
	irc.expect("JOIN #evrd")
	eventually(t, "the join", func() bool { return conn.Joined("#evrd") })
	irc.send(":fake 332 alice #evrd :the #evrd topic")
	irc.send(":fake 353 alice = #evrd :@alice carol")
	eventually(t, "the names", func() bool { return conn.MemberCount("#evrd") == 2 })
	irc.send(":carol!c@h PRIVMSG #evrd :hello alice")
	irc.send(":dave!d@h PRIVMSG alice :private")
	eventually(t, "the messages", func() bool {
		_, n := c.do("GET", path, nil)
		return n["unread"] == 2.0
	})

	code, out := c.doAny("GET", path+"/buffers", nil)
	if code != 200 {
		t.Fatalf("buffers: %d", code)
	}
	bufs := map[string]map[string]any{}
	for _, b := range out.([]any) {
		bm := b.(map[string]any)
		bufs[bm["name"].(string)] = bm
	}
	ev := bufs["#evrd"]
	if ev == nil || ev["joined"] != true || ev["members"] != 2.0 || ev["topic"] != "the #evrd topic" || ev["unread"] != 1.0 || ev["mentions"] != 1.0 {
		t.Fatalf("#evrd buffer: %v (all %v)", ev, bufs)
	}
	if d := bufs["dave"]; d == nil || d["unread"] != 1.0 || d["mentions"] != 1.0 || d["members"] != nil {
		t.Fatalf("query buffer: %v", d)
	}

	_, ch := c.do("GET", path+"/channel?target=%23EVRD", nil)
	if ch["joined"] != true || ch["topic"] != "the #evrd topic" || len(ch["members"].([]any)) != 2 {
		t.Fatalf("channel: %v", ch)
	}
	if code, _ := c.do("POST", path+"/read", map[string]string{"target": "#evrd"}); code != 204 {
		t.Fatalf("read: %d", code)
	}
	_, n = c.do("GET", path, nil)
	if n["unread"] != 1.0 || n["mentions"] != 1.0 {
		t.Fatalf("after reading #evrd: unread %v mentions %v", n["unread"], n["mentions"])
	}

	// The overview sees the joined channel and its rate.
	_, ov := c.do("GET", "/api/overview", nil)
	nets := ov["networks"].([]any)
	obufs := nets[0].(map[string]any)["buffers"].([]any)
	if len(obufs) != 1 || obufs[0].(map[string]any)["name"] != "#evrd" || obufs[0].(map[string]any)["rate"] != 1.0 {
		t.Fatalf("overview buffers: %v", obufs)
	}

	// Editing something that needs a new connection reconnects.
	if code, out := c.do("PATCH", path, map[string]any{"nick": "alice2"}); code != 200 || out["reconnected"] != true {
		t.Fatalf("nick change: %d %v", code, out)
	}
	irc.expect("NICK alice2")
	// A live-only change doesn't.
	if code, out := c.do("PATCH", path, map[string]any{"quitMsg": "bye"}); code != 200 || out["reconnected"] != false {
		t.Fatalf("quit message change: %d %v", code, out)
	}

	if code, out := c.do("POST", path+"/disconnect", nil); code != 200 || out["status"] != "disconnected" {
		t.Fatalf("disconnect: %d %v", code, out)
	}
	if _, n := c.do("GET", path, nil); n["enabled"] != false {
		t.Fatalf("disconnected network still enabled: %v", n)
	}
}
