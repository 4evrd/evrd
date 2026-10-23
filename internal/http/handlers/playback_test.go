package handlers_test

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/4evrd/evrd/internal/history"
	"github.com/4evrd/evrd/internal/store"
)

func TestPlaybackSettings(t *testing.T) {
	e := newEnv(t)
	e.user("alice", store.RoleUser)
	e.user("bob", store.RoleUser)
	c := e.login("alice")
	_, n := c.do("POST", "/api/networks", map[string]any{"name": "x", "host": "irc.example.org", "channels": []string{"#saved"}})
	id := int64(n["id"].(float64))
	path := "/api/networks/" + strconv.FormatInt(id, 10) + "/playback"
	now := time.Now().Add(-time.Minute)
	for i := range 3 {
		e.b.History().Add(history.Message{NetworkID: id, Target: "#a", Sender: "zed", Kind: "message", Text: "hi", Time: now.Add(time.Duration(i) * time.Second)})
	}
	e.b.History().Add(history.Message{NetworkID: id, Target: "zed", Sender: "zed", Kind: "message", Text: "psst", Time: now})

	buf := func(out map[string]any, name string) map[string]any {
		for _, b := range out["buffers"].([]any) {
			if bm := b.(map[string]any); bm["name"] == name {
				return bm
			}
		}
		t.Fatalf("no buffer %s in %v", name, out)
		return nil
	}

	code, out := c.do("GET", path, nil)
	if code != 200 || out["lines"] != nil || out["playsLines"] != float64(50) {
		t.Fatalf("get: %d %v", code, out)
	}
	a := buf(out, "#a")
	if a["lines"] != nil || a["playsLines"] != float64(50) || a["autoClears"] != false || a["waiting"] != float64(3) || a["channel"] != true {
		t.Fatalf("#a: %v", a)
	}
	if z := buf(out, "zed"); z["autoClears"] != true || z["autoClearDefault"] != true || z["channel"] != false {
		t.Fatalf("zed: %v", z)
	}
	buf(out, "#saved") // a channel with nothing said yet

	// the network's default, then a buffer's own lines and auto-clear
	if code, out = c.do("PATCH", path, map[string]any{"buffers": []string{""}, "lines": 20}); code != 200 || out["lines"] != float64(20) || buf(out, "#saved")["playsLines"] != float64(20) {
		t.Fatalf("network lines: %d %v", code, out)
	}
	code, out = c.do("PATCH", path, map[string]any{"buffers": []string{"#a"}, "lines": 2, "autoClear": true})
	if a = buf(out, "#a"); code != 200 || a["lines"] != float64(2) || a["autoClear"] != true || a["waiting"] != float64(2) {
		t.Fatalf("#a own: %d %v", code, a)
	}
	// leaving a field out keeps it; null goes back to the default
	code, out = c.do("PATCH", path, map[string]any{"buffers": []string{"#a"}, "autoClear": nil})
	if a = buf(out, "#a"); code != 200 || a["lines"] != float64(2) || a["autoClear"] != nil || a["autoClears"] != false {
		t.Fatalf("#a back to default: %d %v", code, a)
	}

	for _, bad := range []map[string]any{
		{"buffers": []string{"#a"}, "lines": 501},
		{"buffers": []string{"#a"}, "lines": "many"},
		{"buffers": []string{"#a"}, "autoClear": "yes"},
		{"buffers": []string{""}, "autoClear": true},
		{"buffers": []string{"two words"}, "lines": 1},
		{"lines": 1},
	} {
		if code, out := c.do("PATCH", path, bad); code != 400 {
			t.Errorf("%v: %d %v", bad, code, out)
		}
	}

	// clearing one, then all
	code, out = c.do("POST", path+"/clear", map[string]any{"buffers": []string{"#a"}})
	if a = buf(out, "#a"); code != 200 || a["clearedAt"] == nil || a["waiting"] != float64(0) || buf(out, "zed")["clearedAt"] != nil {
		t.Fatalf("clear #a: %d %v", code, out)
	}
	if code, out = c.do("POST", path+"/clear", nil); code != 200 || buf(out, "zed")["clearedAt"] == nil || buf(out, "#saved")["clearedAt"] == nil {
		t.Fatalf("clear all: %d %v", code, out)
	}
	// history keeps the lines
	if m, _ := e.b.History().Recent(id, "#a", 10); len(m) != 3 {
		t.Fatalf("history after clearing: %v", m)
	}

	// someone else's network
	bob := e.login("bob")
	for _, rt := range []string{"GET ", "PATCH ", "POST /clear"} {
		method, suffix, _ := strings.Cut(rt, " ")
		if code, _ := bob.do(method, path+suffix, map[string]any{"buffers": []string{"#a"}, "lines": 1}); code != 404 {
			t.Errorf("bob %s: %d", rt, code)
		}
	}
}
