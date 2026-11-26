package client

import (
	"strings"
	"testing"

	"github.com/4evrd/evrd/internal/history"
	"github.com/4evrd/evrd/internal/irc"
)

// storedLines is everything history kept for a buffer, events included.
func (r *rig) storedLines(target string) []history.Message {
	all, _ := r.hist.Recent(r.network("one").ID, target, 100)
	return all
}

func tagMsgs(ms []history.Message) []history.Message {
	var out []history.Message
	for _, m := range ms {
		if m.Kind == history.KindTagMsg {
			out = append(out, m)
		}
	}
	return out
}

// Replies and reactions survive into history, naming lines by evrd's
// msgids, so a client that wasn't there sees the threads a live one did.
func TestHistoryKeepsRepliesAndReactions(t *testing.T) {
	r := newRigCaps(t, "message-tags")
	live := r.attached("message-tags", "batch")
	r.linked()

	live.send("SIMULATE @msgid=up1 :bob!u@h PRIVMSG #one :oops")
	ours := irc.Parse(live.expect("PRIVMSG #one :oops")).Tags["msgid"]
	if _, ok := history.ParseID(ours); !ok {
		t.Fatalf("msgid %q", ours)
	}
	live.send("SIMULATE @msgid=up2;+draft/reply=up1;+typing=active;account=carol :carol!u@h PRIVMSG #one :lol")
	live.expect("PRIVMSG #one :lol")
	live.send("SIMULATE @msgid=up3;+draft/react=up;+draft/reply=up1 :dave!u@h TAGMSG #one")
	if l := irc.Parse(live.expect(":dave!u@h TAGMSG #one")); l.Tags["+draft/reply"] != ours || l.Tags["msgid"] == "" {
		t.Fatalf("live reaction: %+v", l.Tags)
	}
	live.send("SIMULATE @+typing=active :bob!u@h TAGMSG #one")
	live.expect(":bob!u@h TAGMSG #one")
	// the user's own reaction, and a reply to bob in private
	live.send("@+draft/react=heart;+draft/reply=" + ours + " TAGMSG #one")
	live.send("@+draft/reply=" + ours + ";+typing=done PRIVMSG bob :seen it")
	eventually(t, "reaction sent upstream", func() bool { return r.irc.Has("@+draft/react=heart;+draft/reply=up1 TAGMSG #one") })
	eventually(t, "reply sent upstream", func() bool { return r.irc.Has("@+draft/reply=up1;+typing=done PRIVMSG bob :seen it") })
	eventually(t, "reactions saved", func() bool { return len(tagMsgs(r.storedLines("#one"))) == 2 })

	for _, m := range append(r.storedLines("#one"), r.storedLines("bob")...) {
		if strings.Contains(m.Tags, "typing") || strings.Contains(m.Tags, "account") {
			t.Fatalf("stored %+v", m)
		}
		switch m.Text {
		case "lol", "seen it":
			if m.Tags != "+draft/reply="+ours {
				t.Fatalf("stored reply %+v", m)
			}
		}
	}
	reacts := tagMsgs(r.storedLines("#one"))
	if reacts[0].Sender != "dave" || reacts[0].Tags != "+draft/react=up;+draft/reply="+ours ||
		reacts[1].Sender != "alice_one" || reacts[1].Tags != "+draft/react=heart;+draft/reply="+ours {
		t.Fatalf("stored reactions %+v", reacts)
	}

	// CHATHISTORY with event-playback: the reply and the reactions
	ev := r.attached(append(ircv3, "draft/event-playback")...)
	got := ev.history("LATEST #one * 50")
	reply, tagmsgs := "", 0
	for _, l := range got {
		m := irc.Parse(l)
		if strings.HasSuffix(l, "PRIVMSG #one :lol") {
			reply = m.Tags["+draft/reply"]
			if m.Tags["+typing"] != "" {
				t.Fatalf("typing replayed: %q", l)
			}
		}
		if m.Command == "TAGMSG" {
			tagmsgs++
			if m.Tags["+draft/reply"] != ours || m.Tags["+draft/react"] == "" || m.Tags["msgid"] == "" {
				t.Fatalf("replayed reaction %q", l)
			}
		}
	}
	if reply != ours || tagmsgs != 2 {
		t.Fatalf("chathistory: reply %q, %d reactions: %q", reply, tagmsgs, got)
	}
	if !anyHas(got, ":dave!dave@one TAGMSG #one") {
		t.Fatalf("no dave's reaction: %q", got)
	}
	pm := ev.history("LATEST bob * 10")
	if len(pm) != 1 || !strings.Contains(pm[0], "+draft/reply="+ours) || !strings.Contains(pm[0], "PRIVMSG bob :seen it") {
		t.Fatalf("private reply: %q", pm)
	}

	// without event-playback: the reply, no reactions
	plain := r.attached(ircv3...)
	got = plain.history("LATEST #one * 50")
	if anyHas(got, "TAGMSG") || !anyHas(got, "+draft/reply="+ours) {
		t.Fatalf("without event-playback: %q", got)
	}
	// without message-tags: neither
	untagged := r.attached("draft/chathistory", "batch", "server-time", "draft/event-playback")
	got = untagged.history("LATEST #one * 50")
	if anyHas(got, "TAGMSG") || anyHas(got, "draft/reply") || !anyHas(got, "PRIVMSG #one :lol") {
		t.Fatalf("without message-tags: %q", got)
	}
}

// Playback on attach carries the tags too, and reactions only to a client
// with event-playback and message-tags.
func TestPlaybackKeepsReplyTags(t *testing.T) {
	r := newRigCaps(t, "message-tags")
	live := r.attached("message-tags")
	r.linked()
	live.send("SIMULATE @msgid=up1 :bob!u@h PRIVMSG #one :oops")
	ours := irc.Parse(live.expect("PRIVMSG #one :oops")).Tags["msgid"]
	live.send("SIMULATE @+draft/reply=up1 :carol!u@h PRIVMSG #one :lol")
	live.send("SIMULATE @+draft/react=up;+draft/reply=up1 :dave!u@h TAGMSG #one")
	eventually(t, "reaction saved", func() bool { return len(tagMsgs(r.storedLines("#one"))) == 1 })
	// a reply stored with the network's msgid (from before evrd matched
	// it): matched on replay
	n := r.network("one")
	r.hist.Add(history.Message{NetworkID: n.ID, Target: "#one", Sender: "erin", Kind: "message", Text: "late", Tags: "+draft/reply=up1"})

	ev := r.attach("alice/one:alice-password", "batch", "server-time", "message-tags", "draft/event-playback")
	seen := ev.until("BATCH -")
	if !anyHas(seen, "+draft/reply="+ours) || !anyHas(seen, ":dave!dave@one TAGMSG #one") {
		t.Fatalf("event-playback: %q", seen)
	}
	for _, l := range seen {
		if strings.HasSuffix(l, "PRIVMSG #one :late") && irc.Parse(l).Tags["+draft/reply"] != ours {
			t.Fatalf("unmatched reply: %q", l)
		}
	}
	tagged := r.attach("alice/one:alice-password", "batch", "server-time", "message-tags")
	seen = tagged.until("BATCH -")
	if anyHas(seen, "TAGMSG") || !anyHas(seen, "+draft/reply="+ours) {
		t.Fatalf("no event-playback: %q", seen)
	}
}
