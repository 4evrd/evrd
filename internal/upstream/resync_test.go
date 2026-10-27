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

// A client that asks the network itself gets its answer, even with evrd's
// own question still out (#141).
func TestAClientsOwnQuestionGetsItsAnswer(t *testing.T) {
	c := &Conn{resync: map[string]bool{"mode #a": true, "topic #a": true}, whoAsked: map[string]byte{}, clientWho: map[string]int{}}
	c.ClientAsks("MODE", "#A")
	if c.resyncReply(irc.Parse(":s 324 me #a +nt")) || c.resyncReply(irc.Parse(":s 329 me #a 1")) {
		t.Fatal("the client's MODE answer was kept from it")
	}
	if !c.resyncReply(irc.Parse(":s 332 me #a :topic")) {
		t.Fatal("evrd's own TOPIC answer, nobody else asked, went to clients")
	}

	// evrd's WHO in WHOX's form: a client's plain WHO is told apart, and
	// each 315 goes to whoever's turn it is
	c.resync["who #a"], c.whoAsked["#a"] = true, 'x'
	c.ClientAsks("WHO", "#a")
	for _, tc := range []struct {
		line string
		kept bool
	}{
		{":s 352 me #a u h s bob H :0 Bob", false},                     // the client's
		{":s 354 me " + whoToken + " #a u h s bob H 0 bob :Bob", true}, // evrd's
		{":s 315 me #a :End", false},                                   // one is the client's
		{":s 315 me #a :End", true},                                    // the other evrd's
		{":s 354 me " + whoToken + " #a u h s bob H 0 bob :Bob", false},
	} {
		if got := c.resyncReply(irc.Parse(tc.line)); got != tc.kept {
			t.Errorf("%s: kept %v", tc.line, got)
		}
	}

	// a plain WHO of evrd's looks just like the client's: they're all the client's
	c.resync["who #b"], c.whoAsked["#b"] = true, 'w'
	c.ClientAsks("WHO", "#b")
	if c.resyncReply(irc.Parse(":s 352 me #b u h s bob H :0 Bob")) || c.resyncReply(irc.Parse(":s 315 me #b :End")) {
		t.Fatal("a plain WHO answer was kept from the client that asked")
	}
}
