package upstream

import (
	"testing"

	"github.com/4evrd/evrd/internal/irc"
)

func TestResyncRepliesStayWithEvrd(t *testing.T) {
	c := &Conn{resync: map[string]bool{"names #a": true, "topic #a": true}}
	for _, tc := range []struct {
		line string
		kept bool
	}{
		{":s 353 me = #A :x y", true},
		{":s 353 me = #b :x y", false}, // a channel nobody asked about
		{":s 366 me #a :End", true},
		{":s 353 me = #a :x y", false}, // answered: the client's own NAMES now
		{":s 332 me #a :topic", true},
		{":s 333 me #a bob 1", true},
		{":s 332 me #a :topic", false},
		{":s 331 me #a :No topic", false},
		{":s PRIVMSG #a :hi", false},
	} {
		if got := c.resyncReply(irc.Parse(tc.line)); got != tc.kept {
			t.Errorf("%s: kept %v", tc.line, got)
		}
	}
	c.resync["topic #c"] = true
	if !c.resyncReply(irc.Parse(":s 331 me #c :No topic")) || c.resync["topic #c"] {
		t.Error("331 doesn't end a topic answer")
	}
}
