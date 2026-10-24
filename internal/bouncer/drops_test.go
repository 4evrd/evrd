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

func TestClientDrops(t *testing.T) {
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
	alice, _ := st.CreateUser(t.Context(), store.User{Username: "alice"})
	bob, _ := st.CreateUser(t.Context(), store.User{Username: "bob"})

	now := time.Now()
	phone := ClientInfo{Device: "Pixel", Software: "Goguma", IP: "192.0.2.1"}
	at := func(min float64) time.Time { return now.Add(time.Duration(min * float64(time.Minute))) }
	// the phone drops three times, back after 20s, 40s, and it's on again now
	for _, d := range []struct {
		on, off float64
		reason  string
	}{{-60, -50, DropPingTimeout}, {-50 + 20.0/60, -30, DropPingTimeout}, {-30 + 40.0/60, -10, DropReset}} {
		p := phone
		p.Since = at(d.on)
		b.RecordDisconnect(alice.ID, p, "libera", d.reason, at(d.off))
	}
	att, err := b.AttachClient(alice.ID, 0, ClientInfo{Device: "Pixel", Software: "Goguma", Networks: []string{"libera"}}, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	defer att.Release()
	// a laptop without a client password: known by its software and network
	b.RecordDisconnect(alice.ID, ClientInfo{Software: "HexChat", Since: at(-20)}, "libera", DropQuit, at(-5))
	b.RecordDisconnect(bob.ID, ClientInfo{Software: "irssi", Since: at(-3)}, "oftc", DropClosed, at(-2))
	// refused at the client limit
	b.RecordDisconnect(bob.ID, ClientInfo{Software: "", Since: at(-1)}, "oftc", DropClientLimit, at(-1))

	devs, err := b.ClientDrops(t.Context(), alice.ID, now.Add(-time.Hour-time.Minute))
	if err != nil || len(devs) != 2 {
		t.Fatalf("devices: %+v %v", devs, err)
	}
	p := devs[0]
	if p.Device != "Pixel" || p.Drops != 3 || p.MainReason != DropPingTimeout || p.Reasons[DropReset] != 1 || !p.Connected {
		t.Fatalf("the phone: %+v", p)
	}
	// gaps 20s, 40s and ~10min (back now): the median is 40s
	if p.MedianGap < 39 || p.MedianGap > 41 {
		t.Fatalf("median gap: %v", p.MedianGap)
	}
	if l := devs[1]; l.Software != "HexChat" || l.Drops != 1 || l.MainReason != DropQuit || l.MedianGap != 0 || l.Connected {
		t.Fatalf("the laptop: %+v", l)
	}

	sum, err := b.ClientDropSummary(t.Context(), now.Add(-2*time.Hour))
	if err != nil || sum.Drops != 6 || sum.Accounts != 2 || sum.Reasons[DropPingTimeout] != 2 || sum.Reasons[DropClientLimit] != 1 {
		t.Fatalf("summary: %+v %v", sum, err)
	}
	if k := DeviceKey(ClientInfo{Software: "x"}, "n"); k != "software:x/n" {
		t.Fatal(k)
	}
	if median([]float64{1, 3}) != 2 || median(nil) != 0 || mainReason(map[string]int{DropQuit: 1, DropReset: 1}) != DropReset {
		t.Fatal("median or main reason")
	}
}
