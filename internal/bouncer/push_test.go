package bouncer

import (
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/4evrd/evrd/internal/history"
	"github.com/4evrd/evrd/internal/store"
)

type fakePusher struct{ got []history.Message }

func (f *fakePusher) Notify(_ store.Network, me string, m history.Message) {
	if me != "alice" {
		panic(me)
	}
	f.got = append(f.got, m)
}

// What would notify the user (a DM, their nick in a channel) goes to the
// pusher, while none of their clients is attached.
func TestPushedWhileNoClientIsAttached(t *testing.T) {
	st, err := store.Open("sqlite:" + filepath.Join(t.TempDir(), "evrd.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	hist, err := history.Open(filepath.Join(t.TempDir(), "history"))
	if err != nil {
		t.Fatal(err)
	}
	defer hist.Close()
	b := New(st, hist, slog.New(slog.NewTextHandler(io.Discard, nil)))
	p := &fakePusher{}
	b.SetPusher(p)
	u, _ := st.CreateUser(t.Context(), store.User{Username: "alice"})
	n, _ := st.CreateNetwork(t.Context(), store.Network{UserID: u.ID, Name: "net"})
	b.initReads(n)
	say := func(kind, target, from, text string) {
		b.observe(n, "alice", history.Message{NetworkID: n.ID, Target: target, Sender: from, Kind: kind, Text: text, Time: time.Now()})
	}
	say("message", "#go", "bob", "morning")           // not for her
	say("message", "#go", "bob", "alice: you there?") // a highlight
	say("message", "carol", "carol", "psst")          // a DM
	say("action", "carol", "carol", "waves")          // a DM too
	say("notice", "NickServ", "NickServ", "identify") // a notice: no
	say("message", "#go", "alice", "alice here")      // her own: no
	say("message", "irc.example.org", "irc.example.org", "server says")
	say("join", "#go", "dave", "")
	if len(p.got) != 3 || p.got[0].Text != "alice: you there?" || p.got[1].Text != "psst" || p.got[2].Text != "waves" {
		t.Fatalf("pushed %+v", p.got)
	}
	att, err := b.AttachClient(u.ID, 0, ClientInfo{}, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	say("message", "carol", "carol", "while attached")
	att.Release()
	say("message", "carol", "carol", "gone again")
	if len(p.got) != 4 || p.got[3].Text != "gone again" {
		t.Fatalf("pushed %+v", p.got)
	}
}
