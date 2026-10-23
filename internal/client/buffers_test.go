package client

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/4evrd/evrd/internal/upstream"
)

// said waits until a line is saved in a buffer of network name.
func (r *rig) said(name, buffer, text string) {
	r.t.Helper()
	eventually(r.t, "saved: "+text, func() bool {
		for _, m := range chat(r.hist, r.network(name).ID, buffer) {
			if m.Text == text {
				return true
			}
		}
		return false
	})
}

// left closes a client and waits for evrd to note it left network name.
func (r *rig) left(ic *ircClient, name string) {
	r.t.Helper()
	before := time.Now()
	ic.c.Close()
	eventually(r.t, "left "+name, func() bool {
		bps, _ := r.st.BufferPlaybacks(context.Background(), r.network(name).ID)
		at := bps[""].ClearedAt
		return at != nil && !at.Before(before)
	})
}

// replayed attaches to network name and returns what came before *status
// answered uptime.
func (r *rig) replayed(name string) []string {
	r.t.Helper()
	ic := r.attach("alice/" + name + ":alice-password")
	ic.expect(" 376 ")
	ic.send("PRIVMSG *status :uptime")
	seen := ic.until("evrd has been running")
	r.left(ic, name)
	return seen
}

func TestClearBufferStartsPlaybackOver(t *testing.T) {
	r := newRig(t)
	ic := r.attached()
	ic.send("SIMULATE :bob!u@h PRIVMSG #one :before the clear")
	r.said("one", "#one", "before the clear")
	ic.status("clearbuffer #one", "Cleared 1 buffer on one")
	ic.send("SIMULATE :bob!u@h PRIVMSG #one :after the clear")
	r.said("one", "#one", "after the clear")
	r.left(ic, "one")

	seen := r.replayed("one")
	if anyHas(seen, "before the clear") || !anyHas(seen, "after the clear") {
		t.Fatalf("playback after clearbuffer:\n%s", strings.Join(seen, "\n"))
	}
	// history still has it
	ic = r.attached()
	ic.status("playback #one", "before the clear")
}

func TestAutoClearIsPerNetwork(t *testing.T) {
	r := newRig(t)
	two := r.network("two")
	if err := r.b.Connect(two.ID); err != nil {
		t.Fatal(err)
	}
	eventually(t, "two connected", func() bool { return r.b.Status(two.ID) == upstream.StatusConnected })

	// on one: auto-clear #one; seen live, so not played back again
	ic := r.attached()
	ic.status("autoclear #one on", "#one auto-clears")
	ic.send("SIMULATE :bob!u@h PRIVMSG #one :seen live")
	ic.expect("seen live")
	r.left(ic, "one")

	// meanwhile on two, where no client was: kept for the first client there
	c2, _ := r.b.ConnFor(two.ID)
	c2.SendRaw("SIMULATE :carol!u@h PRIVMSG alice_two :while you were on one")
	r.said("two", "carol", "while you were on one")

	if seen := r.replayed("one"); anyHas(seen, "seen live") {
		t.Fatalf("an auto-clearing buffer played back what was seen:\n%s", strings.Join(seen, "\n"))
	}
	if seen := r.replayed("two"); !anyHas(seen, "while you were on one") {
		t.Fatalf("two's query wasn't played back:\n%s", strings.Join(seen, "\n"))
	}
	// and it's cleared now
	if seen := r.replayed("two"); anyHas(seen, "while you were on one") {
		t.Fatalf("two's query played back twice:\n%s", strings.Join(seen, "\n"))
	}
}

func TestAutoClearWhileAnotherClientIsAttached(t *testing.T) {
	r := newRig(t)
	stay := r.attached()
	stay.status("autoclear #one on", "#one auto-clears")
	stay.send("SIMULATE :bob!u@h PRIVMSG #one :the other client saw it")
	stay.expect("the other client saw it")
	if seen := r.replayed("one"); anyHas(seen, "the other client saw it") {
		t.Fatalf("played back what an attached client saw:\n%s", strings.Join(seen, "\n"))
	}
}

func TestSetBufferLines(t *testing.T) {
	r := newRig(t)
	ic := r.attached()
	for i := 1; i <= 5; i++ {
		ic.send(fmt.Sprintf("SIMULATE :bob!u@h PRIVMSG #one :line %d", i))
	}
	r.said("one", "#one", "line 5")
	ic.status("setbuffer #one 2", "#one plays back 2 lines.")
	ic.status("setbuffer #one", "#one plays back 2 lines.")
	ic.status("setbuffer * 7", "Buffers on one play back 7 lines")
	ic.status("setbuffer #one x", "Lines is a number")
	ic.status("buffers", "#one")
	r.left(ic, "one")

	seen := r.replayed("one")
	if anyHas(seen, "line 3") || !anyHas(seen, "line 4") || !anyHas(seen, "line 5") {
		t.Fatalf("setbuffer 2 played back:\n%s", strings.Join(seen, "\n"))
	}

	ic = r.attached()
	ic.status("setbuffer #one -", "#one plays back 7 lines.")
	ic.status("playbuffer #one", "Replayed")
}

func TestZNCChannelCommands(t *testing.T) {
	r := newRig(t)
	ic := r.attached()
	eventually(t, "joined #one", func() bool {
		c, ok := r.b.ConnFor(r.network("one").ID)
		return ok && c.Joined("#one")
	})
	ic.status("listnicks #one", "@op")
	ic.status("listnicks #nope", "You're not on #nope.")
	ic.status("topics", "the topic")
	ic.status("showchan #one", "#one on one — joined")
	ic.status("listclients", "1 client")
	ic.status("uptime", "evrd has been running")
	ic.status("rehash", "evrd applies settings as they're saved")
	ic.status("listmods", "loadmod <module>")

	// detach, then attach
	ic.status("detach #one", "PART #one :Detached")
	ic.status("attach #one", "JOIN #one")
	ic.status("attach #one", "#one isn't detached.")

	// disable leaves the channel but keeps it saved; enable joins again
	ic.status("disablechan #one", "Disabled #one: left it")
	eventually(t, "parted", func() bool {
		c, _ := r.b.ConnFor(r.network("one").ID)
		return !c.Joined("#one")
	})
	n := r.network("one")
	if !n.Channel("#one").Disabled || !strings.Contains(strings.Join(n.Channels, " "), "#one") {
		t.Fatalf("disabled #one wasn't kept: %v %+v", n.Channels, n.ChannelSettings)
	}
	ic.status("showchan #one", "disabled")
	ic.status("enablechan #one", "Enabled #one: joining it.")
	eventually(t, "rejoined", func() bool {
		c, _ := r.b.ConnFor(r.network("one").ID)
		return c.Joined("#one")
	})
	ic.status("enablechan #nope", "#nope isn't one of one's channels.")
}

func TestJumpServer(t *testing.T) {
	r := newRig(t)
	ic := r.attached()
	n := r.network("one")
	ic.status("jump", fmt.Sprintf("Reconnecting one to %s:%d", n.Host, n.Port))
	eventually(t, "back", func() bool { return r.b.Status(n.ID) == upstream.StatusConnected })
	ic.status("jump two", "Jumped from one to two.")
}

func TestClearAllBuffersAndPlayBuffer(t *testing.T) {
	r := newRig(t)
	ic := r.attached()
	ic.status("playbuffer", "Usage: playbuffer")
	ic.status("playbuffer #empty", "The buffer for #empty is empty.")
	ic.send("SIMULATE :bob!u@h PRIVMSG #one :in the channel")
	ic.send("SIMULATE :bob!u@h PRIVMSG alice_one :in private")
	r.said("one", "#one", "in the channel")
	r.said("one", "bob", "in private")
	ic.status("playbuffer #one", "in the channel")
	ic.status("clearallchannelbuffers", "Cleared 1 channel buffer on one")
	ic.status("playbuffer #one", "The buffer for #one is empty.")
	ic.status("playbuffer bob", "in private")
	ic.status("clearallquerybuffers", "Cleared 1 query buffer on one")
	ic.status("playbuffer bob", "The buffer for bob is empty.")
	ic.status("clearallbuffers", "Cleared 2 buffers on one")
	ic.status("clearbuffer", "Usage: clearbuffer")
	ic.status("autoclear #one", "Usage: autoclear")
	ic.status("autoclear #one maybe", "autoclear is on, off or -")
	ic.status("autoclear #one,bob off", "#one keeps its buffer")
	ic.status("setbuffer #one 501", "Lines is a number from 0 to 500")
}
