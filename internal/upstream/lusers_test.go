package upstream

import (
	"strings"
	"testing"
	"time"

	"github.com/4evrd/evrd/internal/store"
)

func TestLusersFromTheServer(t *testing.T) {
	s, c := connected(t, store.Network{ID: 4, Nick: "me"}, Options{})
	if l := c.Lusers(); l.Users != -1 || l.Channels != -1 || !l.At.IsZero() {
		t.Fatalf("before LUSERS: %+v", l)
	}
	raw, stop := c.SubscribeRaw()
	defer stop()

	// After registration, unasked: counted, and passed to clients.
	s.send(":srv 251 me :There are 7 users and 3 invisible on 2 servers")
	s.send(":srv 254 me 12 :channels formed")
	settle(t, s)
	if l := c.Lusers(); l.Users != 10 || l.Channels != 12 || l.At.IsZero() {
		t.Fatalf("from 251/254: %+v", l)
	}
	s.send(":srv 266 me 11 40 :Current global users 11, max 40")
	settle(t, s)
	if l := c.Lusers(); l.Users != 11 {
		t.Fatalf("266 wins: %+v", l)
	}
	if got := drain(raw); len(got) < 3 {
		t.Fatalf("unasked LUSERS kept from clients: %q", got)
	}

	// Asked: sent, counted, and kept from clients up to 266.
	c.AskLusers()
	eventually(t, "LUSERS sent", func() bool { return s.has("LUSERS") })
	for _, l := range []string{
		":srv 251 me :There are 1 users and 1 invisible on 1 servers",
		":srv 252 me 1 :operator(s) online",
		":srv 254 me 5 :channels formed",
		":srv 255 me :I have 2 clients and 0 servers",
		":srv 265 me 2 9 :Current local users 2, max 9",
		":srv 266 me :Current global users: 3, max: 9",
	} {
		s.send("%s", l)
	}
	settle(t, s)
	if l := c.Lusers(); l.Users != 3 || l.Channels != 5 {
		t.Fatalf("after asking: %+v", l)
	}
	for _, l := range drain(raw) {
		for _, n := range []string{" 251 ", " 252 ", " 254 ", " 255 ", " 265 ", " 266 "} {
			if strings.Contains(l, n) {
				t.Fatalf("evrd's own LUSERS went to clients: %q", l)
			}
		}
	}
	// 266 ended it: a client's own LUSERS reaches it again.
	s.send(":srv 254 me 6 :channels formed")
	settle(t, s)
	if got := drain(raw); len(got) == 0 || !strings.Contains(got[0], " 254 ") {
		t.Fatalf("a client's LUSERS after evrd's: %q", got)
	}
}

// A server without RPL_GLOBALUSERS: evrd's claim on the replies runs out.
func TestLusersClaimRunsOut(t *testing.T) {
	_, c := connected(t, store.Network{ID: 5, Nick: "me"}, Options{})
	c.AskLusers()
	if !c.lusersOwn("251") {
		t.Fatal("251 right after asking isn't evrd's")
	}
	c.mu.Lock()
	c.lu.asked = time.Now().Add(-lusersWait - time.Second)
	c.mu.Unlock()
	if c.lusersOwn("251") || c.lusersOwn("254") {
		t.Fatal("still evrd's after lusersWait")
	}
	if c.lusersOwn("001") {
		t.Fatal("not a LUSERS numeric")
	}
}

func TestLusersParsing(t *testing.T) {
	for in, want := range map[string]int{
		"There are 7 users and 3 invisible on 2 servers": 10,
		"There are 1 user and 0 invisible on 1 server":   1,
		"There are 4 users on 1 servers":                 4,
		"nothing here":                                   0,
	} {
		if got, _ := sumUsers(in); got != want {
			t.Errorf("sumUsers(%q) = %d, want %d", in, got, want)
		}
	}
	if _, ok := sumUsers("no numbers"); ok {
		t.Error("sumUsers found users in nothing")
	}
	for in, want := range map[string]int{
		"Current global users: 3, max: 9": 3,
		"Current global users 11, max 40": -1,
		"no colon":                        -1,
	} {
		if got := firstNumberAfter(in, ":"); got != want {
			t.Errorf("firstNumberAfter(%q) = %d, want %d", in, got, want)
		}
	}
}
