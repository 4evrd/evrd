package upstream

import (
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/4evrd/evrd/internal/store"
)

func TestLinkStateForClients(t *testing.T) {
	var mu sync.Mutex
	var saved []string
	s, c := connected(t, store.Network{Nick: "me", Channels: []string{"#a"}}, Options{OnChannels: func(ch []string) {
		mu.Lock()
		saved = ch
		mu.Unlock()
	}})
	s.send(":srv 005 me PREFIX=(qov)~@+ MONITOR=100 BOT=B :are supported")
	s.send(":srv 005 me -MONITOR NETWORK=Test :are supported")
	s.send(":srv 900 me me!u@h acct :You are now logged in")
	s.send(":me!u@h JOIN #a")
	s.send(":srv 353 me = #a :~me @bob")
	s.send(":srv CAP me NEW :draft/multiline=max-bytes=4096")
	settle(t, s)
	if got := strings.Join(c.ISupport(), " "); got != "PREFIX=(qov)~@+ BOT=B NETWORK=Test" {
		t.Fatalf("isupport %q", got)
	}
	if c.Account() != "acct" || c.CapValue("draft/multiline") != "max-bytes=4096" {
		t.Fatalf("account %q, value %q", c.Account(), c.CapValue("draft/multiline"))
	}
	if p := c.Presence("bob"); len(p) != 1 || p[0] != (Presence{"#a", "@"}) || c.PrefixMode('~') != 'q' || c.PrefixMode('x') != 0 {
		t.Fatalf("presence %+v", p)
	}
	s.send(":srv 901 me me!u@h :You are now logged out")
	s.send(":srv RENAME #a #b :moved")
	settle(t, s)
	mu.Lock()
	defer mu.Unlock()
	if c.Account() != "" || !c.Joined("#b") || c.Joined("#a") || len(c.Members("#b")) != 2 || strings.Join(saved, " ") != "#b" {
		t.Fatalf("after rename: joined %v, saved %v", c.Channels(), saved)
	}
}

func TestMsgidsAreRememberedAndForgotten(t *testing.T) {
	s, c := connected(t, store.Network{Nick: "me"}, Options{})
	s.send("@msgid=n1 :bob!u@h PRIVMSG #a :hi")
	s.send("@msgid=n2 :srv 001 me :not recorded")
	settle(t, s)
	ours, ok := c.LocalMsgID("n1")
	if !ok {
		t.Fatal("not remembered")
	}
	if back, _ := c.NetworkMsgID(ours); back != "n1" {
		t.Fatalf("back %q", back)
	}
	if _, ok := c.LocalMsgID("n2"); ok {
		t.Fatal("a line that recorded nothing has a pair")
	}
	var x msgids
	for i := range maxMsgids + 1 {
		x.add(strconv.Itoa(i), "o"+strconv.Itoa(i))
	}
	if _, ok := x.theirs["0"]; ok || x.ours["o0"] != "" || x.theirs["1"] != "o1" || len(x.order) != maxMsgids {
		t.Fatal("the oldest wasn't forgotten")
	}
	if got := c.networkRefs("+draft/reply=" + ours + ";+x=1;+reply=zz"); got != "+draft/reply=n1;+x=1;+reply=zz" {
		t.Fatalf("networkRefs %q", got)
	}
}
