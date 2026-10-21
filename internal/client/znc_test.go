package client

import (
	"strings"
	"testing"
	"time"

	"github.com/4evrd/evrd/internal/history"
	"github.com/4evrd/evrd/internal/store"
)

// until reads until a line contains want and returns every line read.
func (ic *ircClient) until(want string) []string {
	ic.t.Helper()
	ic.c.SetReadDeadline(time.Now().Add(3 * time.Second))
	var seen []string
	for ic.sc.Scan() {
		seen = append(seen, ic.sc.Text())
		if strings.Contains(ic.sc.Text(), want) {
			return seen
		}
	}
	ic.t.Fatalf("never got %q; saw:\n%s", want, strings.Join(seen, "\n"))
	return nil
}

func anyHas(lines []string, s string) bool {
	for _, l := range lines {
		if strings.Contains(l, s) {
			return true
		}
	}
	return false
}

func TestDetachHidesAChannelUntilJoin(t *testing.T) {
	r := newRig(t)
	ic := r.attach("alice/one:alice-password")
	ic.expect(" 376 ")
	// the upstream has joined (it may have before this client subscribed)
	eventually(t, "joined #one", func() bool {
		m, _ := r.hist.Recent(r.network("one").ID, "#one", 10)
		return len(m) > 0 && m[0].Kind == "join"
	})
	ic.send("DETACH #one")
	ic.expect("PART #one :Detached")
	eventually(t, "detached saved", func() bool { return r.network("one").Channel("#one").Detached })

	// Lines for the channel are kept but not shown.
	ic.send("SIMULATE :bob!u@h PRIVMSG #one :while detached")
	ic.send("PRIVMSG *status :version")
	if seen := ic.until("evrd "); anyHas(seen, "while detached") {
		t.Fatal("a detached channel's lines reached the client")
	}
	eventually(t, "still logged", func() bool {
		m, _ := r.hist.Recent(r.network("one").ID, "#one", 10)
		return len(m) > 0 && m[len(m)-1].Text == "while detached"
	})

	// A new client doesn't get it either.
	other := r.attach("alice/one:alice-password")
	other.expect(" 376 ")
	other.send("PRIVMSG *status :version")
	if seen := other.until("evrd "); anyHas(seen, "JOIN #one") {
		t.Fatal("a detached channel was joined for a new client")
	}

	// /join brings it back, with playback.
	ic.send("JOIN #one")
	seen := ic.until("while detached")
	if !anyHas(seen, "alice_one@evrd JOIN #one") {
		t.Fatalf("no JOIN on reattach: %q", seen)
	}
	eventually(t, "attached saved", func() bool { return !r.network("one").Channel("#one").Detached })
}

func TestServerAndFingerprintCommands(t *testing.T) {
	r := newRig(t)
	ic := r.attach("alice/one:alice-password")
	ic.expect(" 376 ")
	ic.send("PRIVMSG *status :addserver irc.example.org +6697 hunter2")
	ic.expect("Added irc.example.org:6697 to one")
	ic.send("PRIVMSG *status :listservers")
	ic.expect("2. irc.example.org 6697 TLS (password set)")
	n := r.network("one")
	if len(n.Servers) != 2 || n.Servers[1].Password != "hunter2" {
		t.Fatalf("servers: %+v", n.Servers)
	}
	ic.send("PRIVMSG *status :delserver irc.example.org")
	ic.expect("Removed irc.example.org from one")
	ic.send("PRIVMSG *status :delserver 127.0.0.1")
	ic.expect("only server")

	fp := strings.Repeat("ab", 32)
	ic.send("PRIVMSG *status :addtrustedserverfingerprint " + strings.ToUpper(fp))
	ic.expect("Trusting that certificate on one")
	ic.send("PRIVMSG *status :listfingerprints")
	ic.expect("AB:AB:AB")
	ic.send("PRIVMSG *status :delfingerprint " + fp)
	ic.expect("No longer trusting")

	ic.send("PRIVMSG *status :set quitmsg gone fishing")
	ic.expect("Saved quitmsg = gone fishing")
	ic.send("PRIVMSG *status :set floodrate -1")
	ic.expect("flood rate")
	ic.send("PRIVMSG *status :set bindhost 192.0.2.1")
	ic.expect("doesn't offer bind hosts")
}

func TestPlaybackOnlyNewWithAutoClear(t *testing.T) {
	r := newRig(t)
	u, _ := r.st.UserByUsername("alice")
	u.AutoClearChanBuffer = true
	u.PrependTimestamp = false
	r.st.UpdateUser(u)
	now := time.Now()
	n := r.network("one")
	// an IRC client was last on one 10 minutes ago
	last := now.Add(-10 * time.Minute)
	r.st.UpdateBufferPlayback(n.ID, []string{""}, func(bp *store.BufferPlayback) { bp.ClearedAt = &last })
	r.hist.Add(history.Message{NetworkID: n.ID, Target: "#one", Sender: "bob", Kind: "message", Text: "old news", Time: now.Add(-20 * time.Minute)})
	r.hist.Add(history.Message{NetworkID: n.ID, Target: "#one", Sender: "bob", Kind: "message", Text: "fresh news", Time: now.Add(-5 * time.Minute)})

	ic := r.attach("alice/one:alice-password")
	if seen := ic.until("fresh news"); anyHas(seen, "old news") {
		t.Fatal("auto-clear played back lines from before the last visit")
	}
}

func TestPlaybackTimestampsWithoutServerTime(t *testing.T) {
	r := newRig(t)
	u, _ := r.st.UserByUsername("alice")
	u.TimestampFormat, u.AppendTimestamp, u.Timezone = "<%H>", true, "UTC"
	r.st.UpdateUser(u)
	n := r.network("one")
	at := time.Date(2026, 1, 2, 7, 0, 0, 0, time.UTC)
	r.hist.Add(history.Message{NetworkID: n.ID, Target: "#one", Sender: "bob", Kind: "message", Text: "hello", Time: at})

	ic := r.attach("alice/one:alice-password")
	ic.expect("PRIVMSG #one :<07> hello <07>")

	// with server-time the text is left alone
	tagged := r.attach("alice/one:alice-password", "server-time")
	tagged.expect("PRIVMSG #one :hello")
}
