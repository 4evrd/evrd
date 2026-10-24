package upstream

import (
	"strings"
	"testing"

	"github.com/4evrd/evrd/internal/store"
)

// withCaps acknowledges caps on c, as a CAP ACK would.
func withCaps(c *Conn, caps ...string) {
	c.mu.Lock()
	for _, cp := range caps {
		c.caps[cp] = true
	}
	c.mu.Unlock()
}

// whoByNick is WhoState's members by nick.
func whoByNick(who []Who) map[string]Who {
	out := map[string]Who{}
	for _, w := range who {
		out[w.Nick] = w
	}
	return out
}

func TestWhoStateFollowsTheServer(t *testing.T) {
	s, c := connected(t, store.Network{ID: 3, Nick: "me"}, Options{})
	withCaps(c, "extended-join", "away-notify", "account-notify", "setname")
	raw, stop := c.SubscribeRaw()
	defer stop()

	s.send(":srv 005 me PREFIX=(ov)@+ WHOX :are supported")
	s.send(":me!u@h JOIN #Chat acct :Me")
	s.send(":srv 353 me = #chat :@op carol me")
	s.send(":srv 366 me #chat :End of /NAMES list.")
	// evrd asks who's there itself (WHOX, with its token), and keeps the
	// answer to itself
	eventually(t, "evrd asked WHOX", func() bool { return s.has("WHO #Chat %" + whoFields + "," + whoToken) })
	if _, ok, _ := c.WhoState("#chat"); ok {
		t.Fatal("answerable before the WHO ended")
	}
	s.send(":srv 354 me " + whoToken + " #chat o h1 srv.example op H*@ 0 opacct :The Op")
	s.send(":srv 354 me " + whoToken + " #chat c h2 leaf.example carol G 1 0 :Carol C")
	s.send(":srv 354 me " + whoToken + " #chat u h srv.example me H 0 acct :Me")
	s.send(":srv 315 me #Chat :End of /WHO list.")
	settle(t, s)
	for _, l := range drain(raw) {
		if strings.Contains(l, " 354 ") || strings.Contains(l, " 315 ") {
			t.Fatalf("evrd's own WHO answer went to clients: %q", l)
		}
	}

	who, ok, accounts := c.WhoState("#CHAT")
	if !ok || !accounts || len(who) != 3 {
		t.Fatalf("who: %v %v %+v", ok, accounts, who)
	}
	by := whoByNick(who)
	if op := by["op"]; op.User != "o" || op.Host != "h1" || op.Server != "srv.example" || !op.Oper || op.Away || op.Account != "opacct" || op.Realname != "The Op" || op.Prefix != "@" {
		t.Fatalf("op: %+v", op)
	}
	if carol := by["carol"]; !carol.Away || carol.Account != "" || carol.Hops != 1 || carol.Realname != "Carol C" {
		t.Fatalf("carol: %+v", carol)
	}

	// kept current from what the network says anyway
	s.send(":carol!c@h2 AWAY")
	s.send(":carol!c@h2 ACCOUNT carolacct")
	s.send(":carol!c@h2 SETNAME :Carol Two")
	s.send(":carol!c@h2 NICK carol2")
	s.send(":dave!d@h3 JOIN #chat daveacct :Dave D")
	settle(t, s)
	by = whoByNick(func() []Who { w, _, _ := c.WhoState("#chat"); return w }())
	if c2 := by["carol2"]; c2.Away || c2.Account != "carolacct" || c2.Realname != "Carol Two" {
		t.Fatalf("carol after AWAY, ACCOUNT, SETNAME, NICK: %+v", c2)
	}
	if d := by["dave"]; d.Account != "daveacct" || d.Realname != "Dave D" || d.Away || d.User != "d" || d.Host != "h3" || d.Server != "srv" {
		t.Fatalf("dave from extended-join: %+v", d)
	}
	s.send(":dave!d@h3 AWAY :lunch")
	settle(t, s)
	if w, _, ok, _ := c.WhoNick("DAVE"); !ok || !w.Away {
		t.Fatalf("WHO dave: %v %+v", ok, w)
	}

	// someone who leaves every channel is forgotten
	s.send(":dave!d@h3 PART #chat")
	s.send(":carol2!c@h2 QUIT :bye")
	settle(t, s)
	c.mu.Lock()
	_, dave := c.people["dave"]
	_, carol := c.people["carol2"]
	c.mu.Unlock()
	if dave || carol {
		t.Fatalf("people kept after they left: dave %v carol %v", dave, carol)
	}
}

func TestWhoStateNeedsWhatItCanKeepCurrent(t *testing.T) {
	// no away-notify: away can't be kept current, so WHO is the network's
	s, c := connected(t, store.Network{ID: 3, Nick: "me"}, Options{})
	s.send(":me!u@h JOIN #chat")
	s.send(":srv 353 me = #chat :me")
	s.send(":srv 366 me #chat :End of /NAMES list.")
	// no WHOX: a plain WHO, whose replies are hidden too
	eventually(t, "evrd asked WHO", func() bool { return s.has("WHO #chat") })
	s.send(":srv 352 me #chat u h srv me H :0 Me")
	s.send(":srv 315 me #chat :End of /WHO list.")
	settle(t, s)
	if _, ok, _ := c.WhoState("#chat"); ok {
		t.Fatal("answerable without away-notify")
	}

	// away-notify, but no account-notify: WHO yes, accounts no
	withCaps(c, "away-notify")
	who, ok, accounts := c.WhoState("#chat")
	if !ok || accounts || len(who) != 1 || who[0].Realname != "Me" {
		t.Fatalf("who: %v %v %+v", ok, accounts, who)
	}

	// a joiner without extended-join: their realname isn't known
	s.send(":eve!e@h4 JOIN #chat")
	settle(t, s)
	if _, ok, _ := c.WhoState("#chat"); ok {
		t.Fatal("answerable with a member whose realname isn't known")
	}
	// a client's WHO (which isn't hidden) fills it in
	s.send(":srv 352 me #chat e h4 srv eve H :0 Eve")
	s.send(":srv 315 me #chat :End of /WHO list.")
	settle(t, s)
	if _, ok, _ := c.WhoState("#chat"); !ok {
		t.Fatal("not answerable after a WHO filled the realname in")
	}

	// leaving the channel forgets it
	s.send(":me!u@h PART #chat")
	settle(t, s)
	if _, ok, _ := c.WhoState("#chat"); ok {
		t.Fatal("answerable for a channel evrd left")
	}
	if _, _, ok, _ := c.WhoNick("eve"); ok {
		t.Fatal("WHO eve answerable after leaving the only shared channel")
	}
}
