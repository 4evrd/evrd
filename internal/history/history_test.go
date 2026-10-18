package history

// One suite, run against every backend: Pebble always, ScyllaDB when
// EVRD_TEST_SCYLLA names one (e.g. EVRD_TEST_SCYLLA=localhost:9042).

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/gocql/gocql"
)

// backend opens a fresh, empty store; reopen opens the same one again.
type backend func(t *testing.T) (s Store, reopen func() Store)

func backends(t *testing.T) map[string]backend {
	b := map[string]backend{
		"pebble": func(t *testing.T) (Store, func() Store) {
			dir := filepath.Join(t.TempDir(), "history")
			return mustOpen(t, "pebble:"+dir), func() Store { return mustOpen(t, "pebble:"+dir) }
		},
	}
	if hosts := os.Getenv("EVRD_TEST_SCYLLA"); hosts != "" {
		b["scylla"] = func(t *testing.T) (Store, func() Store) {
			ks := fmt.Sprintf("evrd_test_%d", rand.Uint32())
			dsn := fmt.Sprintf("scylla://%s/%s?consistency=one", hosts, ks)
			t.Cleanup(func() {
				c := gocql.NewCluster(hosts)
				if s, err := c.CreateSession(); err == nil {
					s.Query("DROP KEYSPACE IF EXISTS " + ks).Exec()
					s.Close()
				}
			})
			return mustOpen(t, dsn), func() Store { return mustOpen(t, dsn) }
		}
	}
	return b
}

func mustOpen(t *testing.T, dsn string) Store {
	t.Helper()
	s, err := Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// settle waits for asynchronous writes (the Scylla backend queues them).
func settle(s Store) {
	if f, ok := s.(interface{ Flush() }); ok {
		f.Flush()
	}
}

func each(t *testing.T, test func(t *testing.T, s Store, reopen func() Store)) {
	for name, open := range backends(t) {
		t.Run(name, func(t *testing.T) {
			s, reopen := open(t)
			test(t, s, reopen)
		})
	}
}

func add(t *testing.T, s Store, net int64, target string, n int, start time.Time) {
	t.Helper()
	for i := 0; i < n; i++ {
		m := Message{NetworkID: net, Target: target, Sender: "bob", Kind: "message", Text: fmt.Sprint(i), Time: start.Add(time.Duration(i) * time.Second)}
		if err := s.Add(m); err != nil {
			t.Fatal(err)
		}
	}
	settle(s)
}

func texts(ms []Message) string {
	out := ""
	for _, m := range ms {
		out += m.Text + " "
	}
	return out
}

func TestRecentIsNewestOldestFirstAndCaseInsensitive(t *testing.T) {
	each(t, func(t *testing.T, s Store, _ func() Store) {
		now := time.Now()
		add(t, s, 1, "#Go", 10, now)
		add(t, s, 1, "#rust", 3, now)
		add(t, s, 2, "#go", 3, now) // another network
		got, err := s.Recent(1, "#go", 4)
		if err != nil {
			t.Fatal(err)
		}
		if texts(got) != "6 7 8 9 " || !strings.EqualFold(got[0].Target, "#go") || got[0].Sender != "bob" || got[0].NetworkID != 1 {
			t.Fatalf("got %q %+v", texts(got), got)
		}
		if got, _ := s.Recent(1, "#nothing", 5); len(got) != 0 {
			t.Fatalf("empty buffer: %v", got)
		}
	})
}

func TestRecentSpansMonths(t *testing.T) {
	each(t, func(t *testing.T, s Store, _ func() Store) {
		old := time.Now().AddDate(0, -3, 0)
		add(t, s, 1, "#a", 3, old)
		add(t, s, 1, "#a", 2, time.Now().Add(-time.Hour))
		if got, _ := s.Recent(1, "#a", 4); texts(got) != "1 2 0 1 " {
			t.Fatalf("got %q", texts(got))
		}
	})
}

func TestRoundTripsEveryField(t *testing.T) {
	each(t, func(t *testing.T, s Store, _ func() Store) {
		m := Message{NetworkID: 7, Target: "#x", Sender: "ann", Kind: "kick", Text: "bye ünïcode", Extra: "bob", Time: time.Unix(0, 1792284500123456789)}
		s.Add(m)
		settle(s)
		got, _ := s.Recent(7, "#x", 1)
		if len(got) != 1 || !got[0].Time.Equal(m.Time) {
			t.Fatalf("got %+v", got)
		}
		got[0].Time = m.Time
		if got[0] != m {
			t.Fatalf("got %+v, want %+v", got[0], m)
		}
	})
}

func TestSameInstantKeepsBothLines(t *testing.T) {
	each(t, func(t *testing.T, s Store, _ func() Store) {
		at := time.Now()
		s.Add(Message{NetworkID: 1, Target: "#a", Text: "one", Time: at})
		s.Add(Message{NetworkID: 1, Target: "#a", Text: "two", Time: at})
		settle(s)
		if got, _ := s.Recent(1, "#a", 5); texts(got) != "one two " {
			t.Fatalf("got %q", texts(got))
		}
	})
}

func TestBuffers(t *testing.T) {
	each(t, func(t *testing.T, s Store, _ func() Store) {
		add(t, s, 1, "#Go", 1, time.Now())
		add(t, s, 1, "#go", 1, time.Now())
		add(t, s, 1, "alice", 1, time.Now())
		add(t, s, 2, "#other", 1, time.Now())
		// One #go buffer (spelled #Go or #go), and alice.
		got, _ := s.Buffers(1)
		sort.Strings(got)
		if len(got) != 2 || !strings.EqualFold(got[0], "#go") || got[1] != "alice" {
			t.Fatalf("got %v", got)
		}
	})
}

func TestPruneKeepsNewestPerBuffer(t *testing.T) {
	each(t, func(t *testing.T, s Store, _ func() Store) {
		now := time.Now().Add(-time.Hour)
		add(t, s, 1, "#a", 10, now)
		add(t, s, 1, "#b", 2, now)
		add(t, s, 2, "#a", 10, now)
		if err := s.Prune(1, 3); err != nil {
			t.Fatal(err)
		}
		if got, _ := s.Recent(1, "#a", 100); texts(got) != "7 8 9 " {
			t.Fatalf("#a: %q", texts(got))
		}
		if got, _ := s.Recent(1, "#b", 100); texts(got) != "0 1 " {
			t.Fatalf("#b (under the limit): %q", texts(got))
		}
		if got, _ := s.Recent(2, "#a", 100); len(got) != 10 {
			t.Fatalf("another network was pruned: %d", len(got))
		}
		add(t, s, 1, "#a", 1, time.Now()) // written again, so pruned again
		if err := s.Prune(1, 1); err != nil {
			t.Fatal(err)
		}
		if got, _ := s.Recent(1, "#a", 100); texts(got) != "0 " || len(got) != 1 {
			t.Fatalf("keep 1: %q", texts(got))
		}
	})
}

func TestPruneAcrossMonths(t *testing.T) {
	each(t, func(t *testing.T, s Store, _ func() Store) {
		add(t, s, 1, "#a", 4, time.Now().AddDate(0, -2, 0))
		add(t, s, 1, "#a", 2, time.Now().Add(-time.Hour))
		if err := s.Prune(1, 3); err != nil {
			t.Fatal(err)
		}
		if got, _ := s.Recent(1, "#a", 100); texts(got) != "3 0 1 " {
			t.Fatalf("got %q", texts(got))
		}
	})
}

func TestDeleteNetworkAndNetworks(t *testing.T) {
	each(t, func(t *testing.T, s Store, _ func() Store) {
		for _, id := range []int64{1, 2, 300} {
			add(t, s, id, "#a", 2, time.Now())
		}
		if ids, _ := s.Networks(); fmt.Sprint(sorted(ids)) != "[1 2 300]" {
			t.Fatalf("networks: %v", ids)
		}
		if err := s.DeleteNetwork(2); err != nil {
			t.Fatal(err)
		}
		if got, _ := s.Recent(2, "#a", 10); len(got) != 0 {
			t.Fatal("history survived")
		}
		if b, _ := s.Buffers(2); len(b) != 0 {
			t.Fatal("buffer index survived")
		}
		if ids, _ := s.Networks(); fmt.Sprint(sorted(ids)) != "[1 300]" {
			t.Fatalf("networks after delete: %v", ids)
		}
		// Writing again after a delete re-indexes the buffer.
		add(t, s, 2, "#a", 1, time.Now())
		if b, _ := s.Buffers(2); fmt.Sprint(b) != "[#a]" {
			t.Fatalf("re-index: %v", b)
		}
	})
}

func TestSurvivesReopen(t *testing.T) {
	each(t, func(t *testing.T, s Store, reopen func() Store) {
		add(t, s, 1, "#a", 3, time.Now())
		s.Close()
		s = reopen()
		if got, _ := s.Recent(1, "#a", 10); texts(got) != "0 1 2 " {
			t.Fatalf("after reopen: %q", texts(got))
		}
	})
}

func sorted(ids []int64) []int64 {
	out := append([]int64(nil), ids...)
	for i := range out {
		for j := i + 1; j < len(out); j++ {
			if out[j] < out[i] {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

func TestCount(t *testing.T) {
	each(t, func(t *testing.T, s Store, _ func() Store) {
		add(t, s, 1, "#a", 7, time.Now())
		if n, _ := s.Count(1, "#a", 100); n != 7 {
			t.Fatalf("count: %d", n)
		}
		if n, _ := s.Count(1, "#A", 5); n != 5 {
			t.Fatalf("capped count: %d", n)
		}
		if err := s.Ping(); err != nil {
			t.Fatal(err)
		}
	})
}

// Goroutines can still be recording when the daemon shuts the store down:
// after Close, calls fail instead of panicking.
func TestPebbleAfterClose(t *testing.T) {
	s := mustOpen(t, "pebble:"+t.TempDir())
	s.Close()
	if err := s.Add(Message{NetworkID: 1, Target: "#x", Text: "late", Time: time.Now()}); !errors.Is(err, ErrClosed) {
		t.Fatalf("Add after Close: %v", err)
	}
	if _, err := s.Recent(1, "#x", 10); !errors.Is(err, ErrClosed) {
		t.Fatalf("Recent after Close: %v", err)
	}
	if err := s.Ping(); !errors.Is(err, ErrClosed) {
		t.Fatalf("Ping after Close: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}
