package client

import (
	"bufio"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/4evrd/evrd/internal/history"
	"github.com/4evrd/evrd/internal/irc"
	"github.com/4evrd/evrd/internal/store"
)

// ircv3 are the caps a modern client takes.
var ircv3 = []string{"draft/chathistory", "batch", "server-time", "message-tags"}

// seedHistory fills #hist with m0…m9 a second apart from base, and a JOIN
// between m4 and m5. It returns the messages' IDs.
func (r *rig) seedHistory(base time.Time) []history.ID {
	r.t.Helper()
	n := r.network("one")
	for i := range 10 {
		r.hist.Add(history.Message{NetworkID: n.ID, Target: "#hist", Sender: "bob", Kind: "message",
			Text: "m" + string(rune('0'+i)), Time: base.Add(time.Duration(i) * time.Second)})
	}
	r.hist.Add(history.Message{NetworkID: n.ID, Target: "#hist", Sender: "ann", Kind: "join", Time: base.Add(4500 * time.Millisecond)})
	var ids []history.ID
	for _, m := range chat(r.hist, n.ID, "#hist") {
		ids = append(ids, m.ID())
	}
	if len(ids) != 10 {
		r.t.Fatalf("seeded %d", len(ids))
	}
	return ids
}

// history sends a CHATHISTORY command and returns its batch's lines.
func (ic *ircClient) history(cmd string) []string {
	ic.t.Helper()
	ic.send("CHATHISTORY " + cmd)
	start := irc.Parse(ic.expect(" BATCH +"))
	ref := strings.TrimPrefix(start.Params[0], "+")
	var out []string
	for _, l := range ic.until("BATCH -" + ref) {
		if irc.Parse(l).Tags["batch"] == ref {
			out = append(out, l)
		}
	}
	return out
}

// said is what each line says (its last parameter), space-separated.
func said(lines []string) string {
	var out []string
	for _, l := range lines {
		m := irc.Parse(l)
		out = append(out, m.Params[len(m.Params)-1])
	}
	return strings.Join(out, " ")
}

func stampOf(t time.Time) string { return "timestamp=" + t.UTC().Format(timeFormat) }

func TestCapLS302(t *testing.T) {
	r := newRig(t)
	c, err := net.Dial("tcp", r.addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ic := &ircClient{t: t, c: c, sc: bufio.NewScanner(c)}
	ic.send("CAP LS 302")
	ic.expect("CAP * LS :batch cap-notify draft/chathistory draft/event-playback echo-message message-tags ")
	ic.send("CAP REQ :draft/chathistory draft/event-playback batch message-tags server-time echo-message cap-notify")
	ic.expect("ACK :draft/chathistory")
	ic.send("CAP END")
	ic.send("PASS alice/one:alice-password")
	ic.send("NICK alice")
	ic.send("USER alice 0 * :alice")
	ic.expect(" 001 ")
	ic.expect(" 005 alice_one CHATHISTORY=1000 MSGREFTYPES=msgid,timestamp :are supported")
	ic.send("CAP LIST")
	ic.expect("LIST :batch cap-notify draft/chathistory draft/event-playback echo-message message-tags server-time")

	// a client without chathistory gets no ISUPPORT line at all, as before
	plain := r.attach("alice/one:alice-password", "server-time")
	if seen := plain.until(" 376 "); anyHas(seen, " 005 ") {
		t.Fatalf("005 for a plain client: %q", seen)
	}
}

func TestPlaybackInBatches(t *testing.T) {
	r := newRig(t)
	r.attached() // connect the network
	n := r.network("one")
	eventually(t, "joined #one", func() bool {
		m, _ := r.hist.Recent(n.ID, "#one", 10)
		return len(m) > 0 && m[0].Kind == "join"
	})
	at := time.Now().Add(-time.Minute)
	for _, m := range []history.Message{
		{Sender: "bob", Kind: "message", Text: "hello"},
		{Sender: "ann", Kind: "kick", Extra: "bob", Text: "bye"},
		{Sender: "ann", Kind: "topic", Text: "new topic"},
		{Sender: "ann", Kind: "mode", Text: "+o carol"},
		{Sender: "carol", Kind: "nick", Extra: "caroline"},
		{Sender: "dave", Kind: "quit", Text: "gone"},
		{Sender: "erin", Kind: "part"},
	} {
		at = at.Add(time.Millisecond)
		m.NetworkID, m.Target, m.Time = n.ID, "#one", at
		r.hist.Add(m)
	}
	hello := chat(r.hist, n.ID, "#one")[0]

	// batch, server-time and message-tags: the backlog in a batch, events
	// from *buffextras
	ic := r.attach("alice/one:alice-password", "batch", "server-time", "message-tags")
	seen := ic.until("BATCH -")
	open := ""
	for _, l := range seen {
		if strings.Contains(l, "BATCH +") {
			open = l
		}
	}
	if !strings.HasSuffix(open, " chathistory #one") {
		t.Fatalf("no batch for #one: %q", seen)
	}
	ref := strings.TrimPrefix(irc.Parse(open).Params[0], "+")
	want := "@time=" + hello.Time.UTC().Format(timeFormat) + ";batch=" + ref + ";msgid=" + hello.ID().String() + " :bob!bob@one PRIVMSG #one :hello"
	if !anyHas(seen, want) {
		t.Fatalf("no %q in %q", want, seen)
	}
	if !anyHas(seen, ";batch="+ref+";msgid=") || !anyHas(seen, ":*buffextras!buffextras@evrd PRIVMSG #one :ann kicked bob (bye)") {
		t.Fatalf("events: %q", seen)
	}

	// with event-playback, the events as they happened
	ev := r.attach("alice/one:alice-password", "batch", "server-time", "draft/event-playback")
	seen = ev.until("BATCH -")
	for _, want := range []string{
		":bob!bob@one PRIVMSG #one :hello",
		":ann!ann@one KICK #one bob bye",
		":ann!ann@one TOPIC #one :new topic",
		":ann!ann@one MODE #one +o carol",
		":carol!carol@one NICK caroline",
		":dave!dave@one QUIT gone",
		":erin!erin@one PART #one",
	} {
		if !anyHas(seen, want) {
			t.Errorf("no %q in %q", want, seen)
		}
	}
	if anyHas(seen, "buffextras") || anyHas(seen, "msgid=") {
		t.Errorf("buffextras or msgid without message-tags: %q", seen)
	}

	// none of the caps: exactly as before
	plain := r.attach("alice/one:alice-password")
	seen = plain.until("ann kicked bob (bye)")
	if anyHas(seen, "BATCH") || anyHas(seen, "msgid=") || anyHas(seen, "@time=") {
		t.Fatalf("a plain client got IRCv3: %q", seen)
	}
	if !anyHas(seen, ":bob!bob@one PRIVMSG #one :") || !anyHas(seen, "] hello") {
		t.Fatalf("plain playback: %q", seen)
	}

	// draft/chathistory: no playback; the client asks
	ch := r.attach("alice/one:alice-password", ircv3...)
	ch.send("PRIVMSG *status :version")
	if seen := ch.until("PRIVMSG alice_one :evrd "); anyHas(seen, "hello") || anyHas(seen, "BATCH") || !anyHas(seen, "JOIN #one") {
		t.Fatalf("playback for a chathistory client: %q", seen)
	}
	// nor when a detached channel comes back
	ch.send("DETACH #one")
	ch.expect("PART #one :Detached")
	ch.send("JOIN #one")
	ch.send("PRIVMSG *status :version")
	if seen := ch.until("PRIVMSG alice_one :evrd "); anyHas(seen, "hello") || !anyHas(seen, "JOIN #one") {
		t.Fatalf("reattached: %q", seen)
	}
	// *status playback, asked for, comes batched
	ch.send("PRIVMSG *status :playback #one 1")
	seen = ch.until("Replayed 1 line(s)")
	if !anyHas(seen, "chathistory #one") || !anyHas(seen, "BATCH -") {
		t.Fatalf("status playback: %q", seen)
	}
}

func TestLiveLinesCarryTheirMsgid(t *testing.T) {
	r := newRig(t)
	ic := r.attached("server-time", "message-tags", "echo-message")
	plain := r.attach("alice/one:alice-password")
	plain.expect(" 376 ")
	n := r.network("one")

	ic.send("SIMULATE :bob!u@h PRIVMSG #one :live")
	line := ic.expect("PRIVMSG #one :live")
	m := irc.Parse(line)
	var stored history.Message
	eventually(t, "saved", func() bool {
		for _, s := range chat(r.hist, n.ID, "#one") {
			if s.Text == "live" {
				stored = s
				return true
			}
		}
		return false
	})
	if m.Tags["msgid"] != stored.ID().String() || m.Tags["time"] != stored.Time.UTC().Format(timeFormat) || !strings.HasSuffix(line, " :bob!u@h PRIVMSG #one :live") {
		t.Fatalf("live line %q, stored %v", line, stored.ID())
	}
	// a client without the caps gets the server's line untouched
	if got := plain.expect("PRIVMSG #one :live"); got != ":bob!u@h PRIVMSG #one :live" {
		t.Fatalf("plain: %q", got)
	}

	// echo-message: the echo carries the msgid of what was saved
	ic.send("PRIVMSG #one :mine")
	echo := irc.Parse(ic.expect("PRIVMSG #one :mine"))
	eventually(t, "own line saved", func() bool {
		for _, s := range chat(r.hist, n.ID, "#one") {
			if s.Text == "mine" {
				stored = s
				return true
			}
		}
		return false
	})
	if echo.Tags["msgid"] != stored.ID().String() || echo.Prefix != "alice_one!alice_one@evrd" {
		t.Fatalf("echo %+v, stored %v", echo, stored.ID())
	}
	// other sessions see it with the same msgid
	other := r.attach("alice/one:alice-password", "message-tags")
	other.expect(" 376 ")
	ic.send("PRIVMSG #one :again")
	got := irc.Parse(other.expect("PRIVMSG #one :again"))
	if got.Tags["msgid"] == "" {
		t.Fatalf("no msgid for the other session: %+v", got)
	}
	// the server's own tags don't pass (the client didn't ask for them)
	ic.send("SIMULATE @account=bob;msgid=theirs :bob!u@h PRIVMSG #one :tagged")
	if l := ic.expect("PRIVMSG #one :tagged"); strings.Contains(l, "account=") || strings.Contains(l, "theirs") || !strings.Contains(l, "msgid=") {
		t.Fatalf("server tags: %q", l)
	}
}

func TestChathistorySubcommands(t *testing.T) {
	r := newRig(t)
	base := time.Now().Add(-time.Hour).Truncate(time.Second)
	id := r.seedHistory(base)
	ic := r.attached(ircv3...)
	at := func(i int) string { return stampOf(base.Add(time.Duration(i) * time.Second)) }
	msgid := func(i int) string { return "msgid=" + id[i].String() }

	for _, c := range []struct{ cmd, want string }{
		{"LATEST #hist * 3", "m7 m8 m9"},
		{"LATEST #hist " + msgid(6) + " 10", "m7 m8 m9"},
		{"LATEST #hist " + at(6) + " 10", "m7 m8 m9"},
		{"LATEST #HIST * 1", "m9"},
		{"latest #hist * 6", "m4 m5 m6 m7 m8 m9"}, // the JOIN doesn't count
		{"BEFORE #hist " + msgid(3) + " 2", "m1 m2"},
		{"BEFORE #hist " + at(3) + " 2", "m1 m2"},
		{"BEFORE #hist " + msgid(6) + " 2", "m4 m5"},
		{"AFTER #hist " + msgid(7) + " 5", "m8 m9"},
		{"AFTER #hist " + at(7) + " 5", "m8 m9"},
		{"AFTER #hist " + at(3) + " 2", "m4 m5"},
		{"AROUND #hist " + msgid(5) + " 4", "m3 m4 m5 m6"},
		{"AROUND #hist " + at(5) + " 3", "m4 m5 m6"},
		{"AROUND #hist " + msgid(0) + " 1", "m0"},
		{"BETWEEN #hist " + msgid(2) + " " + msgid(6) + " 10", "m3 m4 m5"},
		{"BETWEEN #hist " + msgid(6) + " " + msgid(2) + " 2", "m4 m5"},
		{"BETWEEN #hist " + at(2) + " " + msgid(6) + " 2", "m3 m4"},
		{"BETWEEN #hist " + at(8) + " " + at(1) + " 10", "m2 m3 m4 m5 m6 m7"},
		{"LATEST #hist * 5000", "m0 m1 m2 m3 m4 m5 m6 m7 m8 m9"}, // capped, not refused
		{"LATEST #nothing * 5", ""},
		{"AFTER #hist " + msgid(9) + " 5", ""},
	} {
		if got := said(ic.history(c.cmd)); got != c.want {
			t.Errorf("%s: got %q, want %q", c.cmd, got, c.want)
		}
	}

	// lines are tagged like playback
	lines := ic.history("LATEST #hist * 1")
	m := irc.Parse(lines[0])
	if m.Tags["msgid"] != id[9].String() || m.Tags["time"] != base.Add(9*time.Second).UTC().Format(timeFormat) || m.Prefix != "bob!bob@one" {
		t.Fatalf("line %q", lines[0])
	}

	// private buffers too
	n := r.network("one")
	r.hist.Add(history.Message{NetworkID: n.ID, Target: "carol", Sender: "carol", Kind: "message", Text: "psst", Time: base})
	if got := said(ic.history("LATEST Carol * 10")); got != "psst" {
		t.Fatalf("query: %q", got)
	}

	// with event-playback, events count and come as they were
	ev := r.attached("draft/chathistory", "batch", "server-time", "draft/event-playback")
	lines = ev.history("BETWEEN #hist " + msgid(4) + " " + msgid(5) + " 10")
	if len(lines) != 1 || !strings.HasSuffix(lines[0], ":ann!ann@one JOIN #hist") {
		t.Fatalf("event: %q", lines)
	}
	if got := said(ev.history("LATEST #hist * 3")); got != "m7 m8 m9" {
		t.Fatalf("latest: %q", got)
	}
	if got := ev.history("LATEST #hist * 6"); len(got) != 6 || !anyHas(got, "JOIN #hist") {
		t.Fatalf("with the event: %q", got)
	}
}

func TestChathistoryTargets(t *testing.T) {
	r := newRig(t)
	base := time.Now().Add(-time.Hour).Truncate(time.Second)
	r.seedHistory(base) // #hist: latest at +9s
	n := r.network("one")
	r.hist.Add(history.Message{NetworkID: n.ID, Target: "carol", Sender: "carol", Kind: "message", Text: "hi", Time: base.Add(20 * time.Second)})
	r.hist.Add(history.Message{NetworkID: n.ID, Target: "#old", Sender: "bob", Kind: "message", Text: "old", Time: base.Add(-time.Hour)})
	ic := r.attached(ircv3...)
	from, to := stampOf(base.Add(-time.Second)), stampOf(base.Add(time.Minute))

	ic.send("CHATHISTORY TARGETS " + from + " " + to + " 10")
	open := ic.expect(" BATCH +")
	if !strings.HasSuffix(open, " draft/chathistory-targets") {
		t.Fatalf("batch: %q", open)
	}
	seen := ic.until("BATCH -")
	want := []string{
		"CHATHISTORY TARGETS #hist " + base.Add(9*time.Second).UTC().Format(timeFormat),
		"CHATHISTORY TARGETS carol " + base.Add(20*time.Second).UTC().Format(timeFormat),
	}
	var got []string
	for _, l := range seen {
		if strings.Contains(l, "CHATHISTORY TARGETS") {
			if !strings.HasPrefix(l, "@batch=") {
				t.Errorf("not in the batch: %q", l)
			}
			got = append(got, l[strings.Index(l, "CHATHISTORY"):])
		}
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("targets: %q", got)
	}

	// newest first when the range runs backwards, and the limit
	ic.send("CHATHISTORY TARGETS " + to + " " + from + " 1")
	seen = ic.until("BATCH -")
	if !anyHas(seen, "CHATHISTORY TARGETS carol ") || anyHas(seen, "TARGETS #hist") {
		t.Fatalf("backwards: %q", seen)
	}
}

func TestChathistoryFailures(t *testing.T) {
	r := newRig(t)
	base := time.Now().Add(-time.Hour).Truncate(time.Second)
	id := r.seedHistory(base)
	ic := r.attached(ircv3...)
	ref := "msgid=" + id[3].String()
	for _, c := range []struct{ cmd, want string }{
		{"CHATHISTORY", "FAIL CHATHISTORY INVALID_PARAMS * :Insufficient parameters"},
		{"CHATHISTORY FOO #hist * 5", "FAIL CHATHISTORY INVALID_PARAMS FOO :Unknown command"},
		{"CHATHISTORY LATEST #hist *", "FAIL CHATHISTORY INVALID_PARAMS LATEST :Insufficient parameters"},
		{"CHATHISTORY BEFORE #hist " + ref + " 5 more", "FAIL CHATHISTORY INVALID_PARAMS BEFORE :Too many parameters"},
		{"CHATHISTORY BEFORE #hist " + ref + " 0", "FAIL CHATHISTORY INVALID_PARAMS BEFORE 0 :Invalid limit"},
		{"CHATHISTORY BEFORE #hist " + ref + " lots", "FAIL CHATHISTORY INVALID_PARAMS BEFORE lots :Invalid limit"},
		{"CHATHISTORY BEFORE #hist timestamp=yesterday 5", "FAIL CHATHISTORY INVALID_PARAMS BEFORE timestamp=yesterday :Invalid timestamp or msgid"},
		{"CHATHISTORY AFTER #hist msgid=nope 5", "FAIL CHATHISTORY INVALID_PARAMS AFTER msgid=nope :Invalid timestamp or msgid"},
		{"CHATHISTORY AFTER #hist * 5", "FAIL CHATHISTORY INVALID_MSGREFTYPE AFTER #hist :Unknown message reference type"},
		{"CHATHISTORY BEFORE #hist id=1 5", "FAIL CHATHISTORY INVALID_MSGREFTYPE BEFORE #hist"},
		{"CHATHISTORY TARGETS " + ref + " " + ref + " 5", "FAIL CHATHISTORY INVALID_PARAMS TARGETS " + ref},
		{"CHATHISTORY TARGETS x=1 " + ref + " 5", "FAIL CHATHISTORY INVALID_MSGREFTYPE TARGETS *"},
		{"CHATHISTORY LATEST *status * 5", "FAIL CHATHISTORY INVALID_TARGET LATEST *status :Messages could not be retrieved"},
		{"CHATHISTORY LATEST #a,#b * 5", "FAIL CHATHISTORY INVALID_TARGET LATEST #a,#b"},
	} {
		ic.send(c.cmd)
		ic.expect(":evrd " + c.want)
	}

	// the store failing
	r.hist.Close()
	ic.send("CHATHISTORY LATEST #hist * 5")
	ic.expect("FAIL CHATHISTORY MESSAGE_ERROR LATEST #hist :Messages could not be retrieved")
	ic.send("CHATHISTORY TARGETS " + stampOf(base) + " " + stampOf(time.Now()) + " 5")
	ic.expect("FAIL CHATHISTORY MESSAGE_ERROR TARGETS * :Messages could not be retrieved")
}

// Without the cap, CHATHISTORY is the server's business, as before; and a
// client without batch gets the lines bare.
func TestChathistoryWithoutCaps(t *testing.T) {
	r := newRig(t)
	base := time.Now().Add(-time.Hour).Truncate(time.Second)
	r.seedHistory(base)
	plain := r.attached()
	plain.send("CHATHISTORY LATEST #hist * 5")
	plain.send("PRIVMSG *status :version")
	if seen := plain.until("PRIVMSG alice_one :evrd "); anyHas(seen, "m9") || anyHas(seen, "FAIL") {
		t.Fatalf("answered without the cap: %q", seen)
	}

	bare := r.attached("draft/chathistory")
	bare.send("CHATHISTORY LATEST #hist * 2")
	seen := bare.until(":bob!bob@one PRIVMSG #hist :")
	seen = append(seen, bare.expect("PRIVMSG #hist"))
	if anyHas(seen, "BATCH") || !strings.HasSuffix(seen[len(seen)-1], "m9") {
		t.Fatalf("without batch: %q", seen)
	}
}

// A client proxied from another cluster node takes its caps along, and
// CHATHISTORY works there.
func TestChathistoryThroughAForwardingNode(t *testing.T) {
	r := newRig(t)
	base := time.Now().Add(-time.Hour).Truncate(time.Second)
	id := r.seedHistory(base)
	const secret = "cluster-secret"
	r.srv.SetCluster(&fakeCluster{self: "owner"}, secret)
	internal := freeAddr(t)
	go r.srv.ListenInternal(r.ctx, internal)
	dialUntil(t, func() (net.Conn, error) { return net.Dial("tcp", internal) })
	front := r.frontNode(&fakeCluster{self: "front", owner: store.Node{ID: "owner", IRCAddr: internal}}, secret)
	c := dialUntil(t, func() (net.Conn, error) { return net.Dial("tcp", front) })
	ic := &ircClient{t: t, c: c, sc: bufio.NewScanner(c)}
	ic.send("CAP LS 302")
	ic.send("CAP REQ :" + strings.Join(append(ircv3, "draft/event-playback"), " "))
	ic.send("PASS alice/one:alice-password")
	ic.send("NICK alice")
	ic.send("USER alice 0 * :alice")
	ic.send("CAP END")
	ic.expect(" 005 alice_one CHATHISTORY=1000")
	lines := ic.history("BEFORE #hist msgid=" + id[5].String() + " 2")
	if said(lines) != "m4 #hist" {
		t.Fatalf("through the front node: %q", lines)
	}
	if !anyHas(lines, "JOIN #hist") || !anyHas(lines, "msgid="+id[4].String()) {
		t.Fatalf("caps lost on the way: %q", lines)
	}
	io.WriteString(c, "QUIT\r\n")
}
