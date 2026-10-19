package history

// Pebble-specific tests: keys, the record encoding, damaged records and the
// store's lifecycle. The behaviour every backend shares is in history_test.go.

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func openPebbleT(t *testing.T) *pebbleStore {
	t.Helper()
	return mustOpen(t, "pebble:"+filepath.Join(t.TempDir(), "history")).(*pebbleStore)
}

func TestOpen(t *testing.T) {
	dir := t.TempDir()
	// pebble: and a bare path name the same store.
	s := mustOpen(t, "pebble:"+filepath.Join(dir, "h"))
	add(t, s, 1, "#a", 2, time.Now())
	s.Close()
	s = mustOpen(t, filepath.Join(dir, "h"))
	if got, _ := s.Recent(1, "#a", 10); texts(got) != "0 1 " {
		t.Fatalf("bare path: %q", texts(got))
	}

	// One process at a time: Pebble locks its directory.
	if _, err := Open(filepath.Join(dir, "h")); err == nil {
		t.Fatal("opened a store that's already open")
	}

	// A path under a file can't be created.
	file := filepath.Join(dir, "file")
	os.WriteFile(file, []byte("x"), 0o600)
	if _, err := Open("pebble:" + filepath.Join(file, "h")); err == nil || !strings.Contains(err.Error(), "open history") {
		t.Fatalf("under a file: %v", err)
	}

	for _, dsn := range []string{"mysql://localhost/evrd", "redis://x"} {
		if _, err := Open(dsn); err == nil || !strings.Contains(err.Error(), "unknown store") {
			t.Errorf("%s: %v", dsn, err)
		}
	}
}

func TestEncodeDecodeRoundTrip(t *testing.T) {
	for _, m := range []Message{
		{},
		{Target: "#go", Sender: "bob", Kind: "message", Text: "hi", Time: time.Unix(0, 1)},
		{Target: strings.Repeat("x", 300), Sender: "ünï", Kind: "nick", Text: "", Extra: "new", Time: time.Unix(-5, 0)},
		{Target: "a\x00b", Text: strings.Repeat("long ", 1000), Time: time.Unix(0, math.MaxInt64)},
	} {
		got, err := decode(42, encode(m))
		if err != nil {
			t.Fatalf("%+v: %v", m, err)
		}
		if !got.Time.Equal(time.Unix(0, m.Time.UnixNano())) {
			t.Fatalf("time %v -> %v", m.Time, got.Time)
		}
		m.NetworkID, m.Time, got.Time = 42, time.Time{}, time.Time{}
		if got != m {
			t.Fatalf("got %+v, want %+v", got, m)
		}
	}
}

func TestDecodeRejectsDamage(t *testing.T) {
	good := encode(Message{Target: "#a", Sender: "b", Kind: "message", Text: "hello", Extra: "x", Time: time.Now()})
	cases := map[string][]byte{
		"empty":         nil,
		"wrong version": append([]byte{2}, good[1:]...),
		"no time":       {encodingV1},
		"bad time":      {encodingV1, 0x80, 0x80},
		"no fields":     binary.AppendVarint([]byte{encodingV1}, 1),
		"truncated":     good[:len(good)-1],
		"field too long": func() []byte {
			b := []byte{encodingV1}
			b = binary.AppendVarint(b, 1)
			return binary.AppendUvarint(b, 1<<40)
		}(),
		"bad length": {encodingV1, 2, 0x80},
	}
	for name, b := range cases {
		if _, err := decode(1, b); !errors.Is(err, errCorrupt) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// Bytes past the last field are ignored (room for a later field).
	if _, err := decode(1, append(bytes.Clone(good), 0, 0)); err != nil {
		t.Fatalf("trailing bytes: %v", err)
	}
}

func TestUpperBound(t *testing.T) {
	for _, c := range []struct{ in, want []byte }{
		{[]byte{1, 2, 3}, []byte{1, 2, 4}},
		{[]byte{1, 0xff}, []byte{2}},
		{[]byte{1, 0xff, 0xff}, []byte{2}},
		{[]byte{0xff, 0xff}, nil},
		{[]byte{}, nil},
	} {
		if got := upperBound(c.in); !bytes.Equal(got, c.want) {
			t.Errorf("upperBound(%x) = %x, want %x", c.in, got, c.want)
		}
	}
	in := []byte{1, 2}
	upperBound(in)
	if !bytes.Equal(in, []byte{1, 2}) {
		t.Fatal("upperBound changed its argument")
	}
}

func TestKeysSortByNetworkBufferTime(t *testing.T) {
	at := time.Unix(1000, 0)
	m := func(net int64, target string, d time.Duration) Message {
		return Message{NetworkID: net, Target: target, Time: at.Add(d)}
	}
	ordered := [][]byte{
		messageKey(m(1, "#a", 0), 5),
		messageKey(m(1, "#a", 0), 6), // same instant: seq breaks the tie
		messageKey(m(1, "#A", time.Nanosecond), 1),
		messageKey(m(1, "#a", time.Hour), 0),
		messageKey(m(1, "#a-b", 0), 0), // not part of #a's range
		messageKey(m(1, "#b", 0), 0),
		messageKey(m(2, "#a", 0), 0),
		messageKey(m(256, "#a", 0), 0),
	}
	for i := 1; i < len(ordered); i++ {
		if bytes.Compare(ordered[i-1], ordered[i]) >= 0 {
			t.Errorf("key %d sorts before key %d", i, i-1)
		}
	}
	// A buffer's range holds its keys and no other buffer's, even one whose
	// name starts with the same letters.
	p := bufferPrefix(1, "#A")
	lo, hi := p, upperBound(p)
	in := func(k []byte) bool { return bytes.Compare(k, lo) >= 0 && bytes.Compare(k, hi) < 0 }
	for i, k := range ordered {
		if want := i < 4; in(k) != want {
			t.Errorf("key %d in #a's range: %v", i, in(k))
		}
	}
}

// A damaged record is skipped, not fatal: the rest of the buffer still reads.
func TestRecentSkipsCorruptRecords(t *testing.T) {
	s := openPebbleT(t)
	at := time.Now()
	add(t, s, 1, "#a", 3, at)
	bad := Message{NetworkID: 1, Target: "#a", Time: at.Add(1500 * time.Millisecond)}
	if err := s.db.Set(messageKey(bad, 0), []byte{9, 9, 9}, nil); err != nil {
		t.Fatal(err)
	}
	got, err := s.Recent(1, "#a", 10)
	if err != nil || texts(got) != "0 1 2 " {
		t.Fatalf("got %q %v", texts(got), err)
	}
	// Count still counts it, but it doesn't take one of Recent's slots.
	if n, _ := s.Count(1, "#a", 10); n != 4 {
		t.Fatalf("count: %d", n)
	}
	if got, _ := s.Recent(1, "#a", 2); texts(got) != "1 2 " {
		t.Fatalf("limited: %q", texts(got))
	}
}

func TestAddFillsTimeAndKeepsFirstSpelling(t *testing.T) {
	s := openPebbleT(t)
	before := time.Now()
	if err := s.Add(Message{NetworkID: 1, Target: "#Go", Text: "now"}); err != nil {
		t.Fatal(err)
	}
	s.Add(Message{NetworkID: 1, Target: "#GO", Text: "later"})
	got, _ := s.Recent(1, "#go", 10)
	if len(got) != 2 || got[0].Time.Before(before) || got[0].Time.After(time.Now()) {
		t.Fatalf("zero time not filled in: %+v", got)
	}
	// Each line keeps how it was spelled; the buffer, how it was first seen.
	if got[0].Target != "#Go" || got[1].Target != "#GO" {
		t.Fatalf("targets: %q %q", got[0].Target, got[1].Target)
	}
	if b, _ := s.Buffers(1); fmt.Sprint(b) != "[#Go]" {
		t.Fatalf("buffers: %v", b)
	}
}

func TestLimitsAndEmpty(t *testing.T) {
	s := openPebbleT(t)
	add(t, s, 1, "#a", 5, time.Now())
	for _, limit := range []int{0, -1} {
		if got, err := s.Recent(1, "#a", limit); err != nil || len(got) != 0 {
			t.Fatalf("limit %d: %v %v", limit, got, err)
		}
		if n, err := s.Count(1, "#a", limit); err != nil || n != 0 {
			t.Fatalf("count max %d: %d %v", limit, n, err)
		}
	}
	if got, _ := s.Recent(1, "#a", 100); len(got) != 5 {
		t.Fatalf("limit over the size: %d", len(got))
	}
	if b, err := s.Buffers(9); err != nil || b == nil || len(b) != 0 {
		t.Fatalf("no buffers is an empty list: %#v %v", b, err)
	}
	if n, err := s.Count(9, "#a", 10); err != nil || n != 0 {
		t.Fatalf("count of nothing: %d %v", n, err)
	}
	// Prune keeping everything, or with nothing to prune, changes nothing.
	for _, keep := range []int{0, -3, 5, 100} {
		if err := s.Prune(1, keep); err != nil {
			t.Fatal(err)
		}
		if err := s.Prune(9, keep); err != nil {
			t.Fatal(err)
		}
	}
	if n, _ := s.Count(1, "#a", 100); n != 5 {
		t.Fatalf("after no-op prunes: %d", n)
	}
	if err := s.DeleteNetwork(9); err != nil {
		t.Fatalf("deleting a network without history: %v", err)
	}
	if ids, err := s.Networks(); err != nil || fmt.Sprint(ids) != "[1]" {
		t.Fatalf("networks: %v %v", ids, err)
	}
}

func TestEmptyStoreHasNoNetworks(t *testing.T) {
	s := openPebbleT(t)
	if ids, err := s.Networks(); err != nil || len(ids) != 0 {
		t.Fatalf("got %v %v", ids, err)
	}
}

// Networks are listed in key order; IDs at the edges of the range work.
func TestNetworksEdgeIDs(t *testing.T) {
	s := openPebbleT(t)
	for _, id := range []int64{math.MaxInt64, 5, 0, -1, math.MinInt64} {
		add(t, s, id, "#a", 1, time.Now())
	}
	done := make(chan []int64)
	go func() {
		ids, _ := s.Networks()
		done <- ids
	}()
	select {
	case ids := <-done:
		// Sorted as unsigned: negative IDs come last.
		if fmt.Sprint(ids) != fmt.Sprint([]int64{0, 5, math.MaxInt64, math.MinInt64, -1}) {
			t.Fatalf("got %v", ids)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Networks didn't finish")
	}
	if err := s.DeleteNetwork(-1); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Recent(math.MinInt64, "#a", 5); len(got) != 1 {
		t.Fatal("deleting -1 touched another network")
	}
}

func TestDeleteNetworkForgetsIndexedBuffers(t *testing.T) {
	s := openPebbleT(t)
	add(t, s, 1, "#a", 1, time.Now())
	add(t, s, 12, "#a", 1, time.Now())
	if err := s.DeleteNetwork(1); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	n := len(s.known)
	s.mu.Unlock()
	if n != 1 {
		t.Fatalf("index cache after delete: %d entries", n)
	}
	if b, _ := s.Buffers(12); fmt.Sprint(b) != "[#a]" {
		t.Fatalf("another network's buffers: %v", b)
	}
}

func TestPebbleEveryCallAfterClose(t *testing.T) {
	s := openPebbleT(t)
	add(t, s, 1, "#a", 1, time.Now())
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	checks := map[string]error{"Add": s.Add(Message{NetworkID: 1, Target: "#a"})}
	_, checks["Recent"] = s.Recent(1, "#a", 1)
	_, checks["Buffers"] = s.Buffers(1)
	checks["Prune"] = s.Prune(1, 1)
	checks["DeleteNetwork"] = s.DeleteNetwork(1)
	_, checks["Networks"] = s.Networks()
	_, checks["Count"] = s.Count(1, "#a", 1)
	checks["Ping"] = s.Ping()
	for name, err := range checks {
		if !errors.Is(err, ErrClosed) {
			t.Errorf("%s after Close: %v", name, err)
		}
	}
}

// Close waits for calls in flight and the ones after it fail cleanly, with
// writers, readers and pruning all running at once.
func TestCloseWhileBusy(t *testing.T) {
	for round := 0; round < 50; round++ {
		// Not mustOpen: its cleanup would hang on a deadlocked store
		// instead of letting the test fail.
		s, err := openPebble(filepath.Join(t.TempDir(), "history"))
		if err != nil {
			t.Fatal(err)
		}
		for b := 0; b < 20; b++ {
			add(t, s, 1, fmt.Sprintf("#%d", b), 5, time.Now())
		}
		var wg sync.WaitGroup
		stop := make(chan struct{})
		bad := make(chan error, 100)
		work := func(f func() error) {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for {
					select {
					case <-stop:
						return
					default:
					}
					if err := f(); err != nil && !errors.Is(err, ErrClosed) {
						bad <- err
						return
					}
				}
			}()
		}
		work(func() error { return s.Add(Message{NetworkID: 1, Target: "#0", Text: "x"}) })
		work(func() error { _, err := s.Recent(1, "#1", 10); return err })
		for range 4 {
			work(func() error { return s.Prune(1, 3) })
			work(func() error { return s.Prune(2, 3) })
		}
		work(func() error { _, err := s.Networks(); return err })
		time.Sleep(time.Millisecond)
		closed := make(chan error)
		go func() { closed <- s.Close() }()
		select {
		case err := <-closed:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("Close deadlocked with calls in flight")
		}
		close(stop)
		wg.Wait()
		close(bad)
		for err := range bad {
			t.Fatalf("call during Close: %v", err)
		}
	}
}

func TestPebbleLogger(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	var l pebbleLogger
	l.Infof("replaying WAL %d", 7)
	if buf.Len() != 0 {
		t.Fatalf("routine info logged: %s", buf.String())
	}
	l.Errorf("disk %s", "full")
	if out := buf.String(); !strings.Contains(out, "level=ERROR") || !strings.Contains(out, "history store: disk full") {
		t.Fatalf("error not logged: %s", out)
	}
}
