package client

import (
	"strings"
	"testing"

	"github.com/4evrd/evrd/internal/irc"
)

// known attaches a first client (which connects the network) and waits
// until evrd knows its channel whole (its join's names have ended).
func (r *rig) known(network, ch string) {
	r.t.Helper()
	r.attached()
	n := r.network(network)
	eventually(r.t, ch+" known", func() bool {
		conn, ok := r.b.ConnFor(n.ID)
		if !ok {
			return false
		}
		st, ok := conn.ChannelState(ch)
		return ok && st.NamesKnown && st.TopicKnown
	})
}

// asked counts the lines the network got that start with prefix.
func (r *rig) asked(prefix string) int {
	n := 0
	for _, l := range r.irc.Lines() {
		if strings.HasPrefix(l, prefix) {
			n++
		}
	}
	return n
}

func TestAttachAndQueriesComeFromEvrdsState(t *testing.T) {
	r := newRig(t)
	r.known("one", "#one")
	names, topics := r.asked("NAMES #one"), r.asked("TOPIC #one")

	ic := r.attach("alice/one:alice-password")
	seen := ic.until(":evrd 366 alice_one #one ")
	if !anyHas(seen, ":evrd 332 alice_one #one :the topic") || !anyHas(seen, ":evrd 353 alice_one = #one :@op alice_one") {
		t.Fatalf("names on attach: %q", seen)
	}
	if anyHas(seen, " 331 ") {
		t.Fatalf("No topic is set on attach: %q", seen)
	}

	// asked, evrd answers what it knows
	ic.send("TOPIC #one")
	ic.expect(":evrd 332 alice_one #one :the topic")
	ic.send("NAMES #one")
	ic.expect(":evrd 353 alice_one = #one :@op alice_one")
	ic.expect(":evrd 366 alice_one #one :End of /NAMES list")
	if r.asked("NAMES #one") != names || r.asked("TOPIC #one") != topics {
		t.Fatalf("the network was asked: %q", r.irc.Lines())
	}

	// modes evrd doesn't know yet are the network's to answer…
	ic.send("MODE #one")
	eventually(t, "the MODE passed on", func() bool { return r.asked("MODE #one") >= 2 }) // evrd's own, then the client's
	// …and a client that asked gets the answer, even with evrd's own
	// question still out: a network that doesn't answer that (263, too
	// many channels at once) would otherwise leave the client without
	// modes, its answer taken for evrd's (#141)
	ic.send("SIMULATE :fake 324 alice_one #one +ntk sesame")
	ic.send("SIMULATE :fake 329 alice_one #one 1600000000")
	ic.expect(":fake 324 alice_one #one +ntk sesame")
	ic.expect(":fake 329 alice_one #one 1600000000")
	conn, _ := r.b.ConnFor(r.network("one").ID)
	eventually(t, "the modes known", func() bool {
		st, _ := conn.ChannelState("#one")
		return st.ModesKnown && !st.Created.IsZero()
	})
	ic.send("MODE #one")
	ic.expect(":evrd 324 alice_one #one +ntk sesame")
	ic.expect(":evrd 329 alice_one #one 1600000000")

	// a topic someone sets is answered with who and when
	ic.send("SIMULATE :bob!b@h TOPIC #one :hello there")
	ic.expect(":bob!b@h TOPIC #one :hello there")
	ic.send("TOPIC #one")
	ic.expect(":evrd 332 alice_one #one :hello there")
	ic.expect(":evrd 333 alice_one #one bob!b@h ")

	// changes, lists and channels evrd isn't in go to the network
	ic.send("TOPIC #one :a new one")
	ic.send("MODE #one b")
	ic.send("NAMES #elsewhere")
	eventually(t, "passed on", func() bool {
		return r.irc.Has("TOPIC #one :a new one") && r.irc.Has("MODE #one b") && r.irc.Has("NAMES #elsewhere")
	})
}

func TestNamesFromEvrdsStateFitTheClient(t *testing.T) {
	r := newRigCaps(t, everyLinkCap)
	r.known("one", "#one")
	full := r.full()
	full.send("SIMULATE :bob!b@hostb JOIN #one")
	full.expect(":bob!b@hostb JOIN #one")
	full.send("SIMULATE :fake MODE #one +ov bob bob")
	full.expect("MODE #one +ov bob bob")

	// every prefix and the hosts evrd knows (its own from its JOIN, op's
	// from evrd's own WHO, once that's answered)
	eventually(t, "op's host known", func() bool {
		conn, _ := r.b.ConnFor(r.network("one").ID)
		for _, m := range conn.Members("#one") {
			if m.Nick == "op" {
				return m.Host != ""
			}
		}
		return false
	})
	full.send("NAMES #one")
	full.expect(":evrd 353 alice_one = #one :@+bob!b@hostb @op!o@oh alice_one!u@h")

	// the highest prefix and nicks only, for a client without the caps
	plain := r.attach("alice/one:alice-password")
	plain.expect(":evrd 353 alice_one = #one :@bob @op alice_one")

	// no names on attach for a client with no-implicit-names
	quiet := r.attach("alice/one:alice-password", "no-implicit-names")
	quiet.expect(" JOIN #one")
	quiet.send("PRIVMSG *status :version")
	if seen := quiet.until("PRIVMSG alice_one :evrd "); anyHas(seen, " 353 ") {
		t.Fatalf("implicit names: %q", seen)
	}
}

func TestLabeledQueryFromEvrdsState(t *testing.T) {
	r := newRigCaps(t, everyLinkCap)
	r.known("one", "#one")
	ic := r.labeledClient()
	ic.send("@label=n1 NAMES #one")
	open := irc.Parse(ic.expect(" BATCH +"))
	if open.Tags["label"] != "n1" || open.Params[1] != "labeled-response" {
		t.Fatalf("batch: %+v", open)
	}
	ref := strings.TrimPrefix(open.Params[0], "+")
	if l := ic.expect(" 353 alice_one = #one "); !strings.HasPrefix(l, "@batch="+ref+" ") {
		t.Fatalf("names outside the batch: %q", l)
	}
	ic.expect(" 366 alice_one #one ")
	ic.expect("BATCH -" + ref)
	ic.send("@label=t1 TOPIC #one")
	if l := ic.expect(" 332 alice_one #one :the topic"); !strings.HasPrefix(l, "@label=t1 ") {
		t.Fatalf("one line, labeled: %q", l)
	}
}

// count is how many of lines have s in them.
func count(lines []string, s string) int {
	n := 0
	for _, l := range lines {
		if strings.Contains(l, s) {
			n++
		}
	}
	return n
}

func TestADroppedLinkTakesClientsOutOfItsChannels(t *testing.T) {
	r := newRig(t)
	r.known("one", "#one")
	ic := r.attach("alice/one:alice-password")
	ic.until(":evrd 366 alice_one #one ")

	// out of its channels with the link, as ZNC does: the rejoin is then
	// a join, names and all, and not one into a channel it's in (#125)
	ic.send("PRIVMSG *status :disconnect")
	if l := ic.expect(" KICK #one alice_one "); !strings.HasPrefix(l, ":evrd KICK #one alice_one :Disconnected from one") {
		t.Fatalf("kick: %q", l)
	}
	// one attaching meanwhile (which connects the network again) gets
	// the channel once evrd is in it, and only once
	late := r.attach("alice/one:alice-password")
	for _, c := range []*ircClient{ic, late} {
		seen := c.until(" 366 alice_one #one ")
		if count(seen, " JOIN #one") != 1 || !anyHas(seen, "@op") {
			t.Fatalf("rejoin: %q", seen)
		}
		c.send("PRIVMSG *status :version")
		if seen := c.until("PRIVMSG alice_one :evrd "); anyHas(seen, " JOIN #one") || anyHas(seen, " 353 ") {
			t.Fatalf("joined twice: %q", seen)
		}
	}
}

func TestAJoinIntoAnOpenChannelDoesntReachTheClient(t *testing.T) {
	r := newRig(t)
	r.known("one", "#one")
	ic := r.attach("alice/one:alice-password")
	ic.until(":evrd 366 alice_one #one ")
	ic.send("SIMULATE :alice_one!u@h JOIN #one")
	ic.send("SIMULATE :fake 353 alice_one = #one :alice_one @op")
	ic.send("SIMULATE :fake 366 alice_one #one :End of /NAMES list.")
	ic.send("SIMULATE :bob!b@h PRIVMSG #one :after")
	if seen := ic.until("PRIVMSG #one :after"); anyHas(seen, " JOIN #one") || anyHas(seen, " 353 ") || anyHas(seen, " 366 ") {
		t.Fatalf("a JOIN into an open channel: %q", seen)
	}
	// asked, the names still come
	ic.send("NAMES #one")
	ic.expect(" 366 alice_one #one ")
}
