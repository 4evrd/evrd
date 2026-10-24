package upstream

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/4evrd/evrd/internal/store"
)

func TestChannelStateFollowsTheServer(t *testing.T) {
	s, c := connected(t, store.Network{ID: 3, Nick: "me"}, Options{})
	raw, stop := c.SubscribeRaw()
	defer stop()

	s.send(":srv 005 me PREFIX=(ov)@+ CHANMODES=beI,k,l,imnpst CHANTYPES=# :are supported")
	s.send(":me!u@h JOIN #Chat")
	s.send(":srv 332 me #chat :hello world")
	s.send(":srv 333 me #chat op!o@h1 1700000000")
	s.send(":srv 353 me = #chat :@+op!o@h1 carol!c@h2 me!u@h")
	settle(t, s)
	if st, _ := c.ChannelState("#chat"); st.NamesKnown || st.TopicKnown && st.Topic != "hello world" {
		t.Fatalf("whole before the names ended: %+v", st)
	}
	s.send(":srv 366 me #chat :End of /NAMES list.")
	// evrd asks for the modes itself, and keeps the answer to itself
	eventually(t, "evrd asked for the modes", func() bool { return s.has("MODE #Chat") })
	s.send(":srv 324 me #chat +ntk secret")
	s.send(":srv 329 me #chat 1600000000")
	settle(t, s)

	st, ok := c.ChannelState("#CHAT")
	if !ok || st.Name != "#Chat" || !st.TopicKnown || st.Topic != "hello world" || st.TopicBy != "op!o@h1" ||
		!st.TopicAt.Equal(time.Unix(1700000000, 0)) || !st.NamesKnown || !st.ModesKnown ||
		st.Modes != "+ntk" || !slices.Equal(st.ModeArgs, []string{"secret"}) || !st.Created.Equal(time.Unix(1600000000, 0)) {
		t.Fatalf("state: %+v", st)
	}
	if op := st.Members[0]; op.Nick != "op" || op.Prefix != "@" || op.Prefixes != "@+" || op.User != "o" || op.Host != "h1" {
		t.Fatalf("op: %+v", op)
	}
	for _, l := range drain(raw) {
		if strings.Contains(l, " 324 ") || strings.Contains(l, " 329 ") {
			t.Fatalf("evrd's own MODE answer went to clients: %q", l)
		}
	}

	// changes keep it current
	s.send(":op!o@h1 MODE #chat +l-k+m-v 5 secret op")
	s.send(":carol!c@h2 TOPIC #chat :new topic")
	s.send(":carol!c@h2 CHGHOST cc new.host")
	s.send(":carol!cc@new.host NICK carol2")
	s.send(":op!o@h1 MODE #chat +v carol2")
	settle(t, s)
	st, _ = c.ChannelState("#chat")
	if st.Modes != "+ntlm" || !slices.Equal(st.ModeArgs, []string{"5"}) {
		t.Fatalf("modes after a change: %q %q", st.Modes, st.ModeArgs)
	}
	if st.Topic != "new topic" || st.TopicBy != "carol!c@h2" || st.TopicAt.IsZero() {
		t.Fatalf("topic after a change: %+v", st)
	}
	byNick := map[string]Member{}
	for _, m := range st.Members {
		byNick[m.Nick] = m
	}
	if op := byNick["op"]; op.Prefixes != "@" || op.Prefix != "@" {
		t.Fatalf("op after -v: %+v", op)
	}
	if c2 := byNick["carol2"]; c2.User != "cc" || c2.Host != "new.host" || c2.Prefixes != "+" {
		t.Fatalf("carol after chghost, nick and +v: %+v", c2)
	}

	// a client's own MODE query is answered to the clients
	drain(raw)
	s.send(":srv 324 me #chat +ntlm 5")
	settle(t, s)
	if !slices.ContainsFunc(drain(raw), func(l string) bool { return strings.Contains(l, " 324 ") }) {
		t.Fatal("a 324 a client asked for went nowhere")
	}

	// leaving forgets it
	s.send(":me!u@h PART #chat :bye")
	settle(t, s)
	if _, ok := c.ChannelState("#chat"); ok {
		t.Fatal("still there after parting")
	}
}

func TestChannelStateWithoutTopicOrMultiPrefix(t *testing.T) {
	s, c := connected(t, store.Network{ID: 3, Nick: "me"}, Options{})
	s.send(":me!u@h JOIN #quiet")
	s.send(":srv 353 me = #quiet :@op me")
	s.send(":srv 366 me #quiet :End of /NAMES list.")
	settle(t, s)
	st, _ := c.ChannelState("#quiet")
	if !st.TopicKnown || st.Topic != "" || !st.NamesKnown || st.ModesKnown {
		t.Fatalf("a join without a topic: %+v", st)
	}
	// without multi-prefix only the highest prefix is known: taking it
	// leaves none
	s.send(":op!o@h MODE #quiet +v op")
	s.send(":x!x@h MODE #quiet -o op")
	settle(t, s)
	if st, _ := c.ChannelState("#quiet"); st.Members[0].Prefix != "+" {
		t.Fatalf("members: %+v", st.Members)
	}
	// the server's answer to a TOPIC query is kept too
	s.send(":srv 331 me #quiet :No topic is set")
	s.send(":srv 332 me #quiet :now one")
	settle(t, s)
	if st, _ := c.ChannelState("#quiet"); st.Topic != "now one" || st.TopicBy != "" {
		t.Fatalf("topic: %+v", st)
	}
	if _, ok := c.ChannelState("#elsewhere"); ok {
		t.Fatal("a channel evrd isn't in")
	}
}

// drain is the raw lines waiting on a feed.
func drain(raw <-chan Line) []string {
	var out []string
	for {
		select {
		case l := <-raw:
			out = append(out, l.Text)
		default:
			return out
		}
	}
}
