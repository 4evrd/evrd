package history

// ScyllaDB tests that need no database: DSN parsing, partitioning, the write
// queue, and how the store behaves when the database can't be reached. The
// conformance suite in history_test.go runs against a real ScyllaDB when
// EVRD_TEST_SCYLLA is set (dev/ci-scylla.sh).

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gocql/gocql"
)

// Bad DSNs fail before anything tries to connect.
func TestOpenScyllaRejectsBadDSN(t *testing.T) {
	for dsn, want := range map[string]string{
		"scylla://localhost/Bad-Keyspace":               "bad keyspace",
		"cassandra://localhost/1abc":                    "bad keyspace",
		"scylla://localhost/" + strings.Repeat("k", 49): "bad keyspace",
		"scylla://localhost/evrd?replication=0":         "bad replication",
		"scylla://localhost/evrd?replication=two":       "bad replication",
		"scylla://localhost/evrd?consistency=most":      "bad consistency",
		"scylla://localhost/evrd?a=%zz":                 "invalid URL escape",
	} {
		start := time.Now()
		s, err := Open(dsn)
		if err == nil {
			s.Close()
			t.Errorf("%s: opened", dsn)
			continue
		}
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v, want %q", dsn, err, want)
		}
		if time.Since(start) > time.Second {
			t.Errorf("%s: took %v (tried to connect?)", dsn, time.Since(start))
		}
	}
}

func TestBucketOf(t *testing.T) {
	for _, c := range []struct {
		t    time.Time
		want int
	}{
		{time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC), 202610},
		{time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), 202601},
		{time.Date(2025, 12, 31, 23, 59, 59, 0, time.UTC), 202512},
		// Months are UTC: just after midnight in Berlin is still last year.
		{time.Date(2026, 1, 1, 0, 30, 0, 0, time.FixedZone("CET", 3600)), 202512},
		{time.Date(2026, 2, 28, 23, 0, 0, 0, time.FixedZone("X", -2*3600)), 202603},
	} {
		if got := bucketOf(c.t); got != c.want {
			t.Errorf("bucketOf(%v) = %d, want %d", c.t, got, c.want)
		}
	}
}

// unreachable is a Scylla store whose session is closed: every query fails,
// as when the database is down.
func unreachable(t *testing.T) *scyllaStore {
	t.Helper()
	sess := &gocql.Session{}
	sess.Close()
	s := newScyllaStore(sess)
	t.Cleanup(func() { s.Close() })
	return s
}

func captureLog(t *testing.T) *syncBuffer {
	buf := &syncBuffer{}
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return buf
}

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

// Add never waits for the database: writes queue, fail in the background
// (logged), and the index rows aren't marked written so they're retried.
func TestScyllaWritesQueueAndFailInBackground(t *testing.T) {
	log := captureLog(t)
	s := unreachable(t)
	for i := 0; i < 3; i++ {
		if err := s.Add(Message{NetworkID: 4, Target: "#Go", Text: "x"}); err != nil {
			t.Fatal(err)
		}
	}
	s.Flush()
	if p := s.Pending(); p != 0 {
		t.Fatalf("pending after flush: %d", p)
	}
	if out := log.String(); strings.Count(out, "history write failed") != 3 || !strings.Contains(out, "buffer=#Go") {
		t.Fatalf("failures not logged: %s", out)
	}
	s.mu.Lock()
	known, dirty := len(s.known), s.dirty[4]["#go"]
	s.mu.Unlock()
	if known != 0 {
		t.Fatalf("index rows marked written though they failed: %d", known)
	}
	if !dirty {
		t.Fatal("buffer not marked for pruning")
	}
}

// A full queue makes Add wait for room rather than drop the line.
func TestScyllaFullQueueWaits(t *testing.T) {
	log := captureLog(t)
	sess := &gocql.Session{}
	sess.Close()
	s := &scyllaStore{s: sess, queue: make(chan scyllaWrite, 1), known: map[string]bool{}, dirty: map[int64]map[string]bool{}}
	s.Add(Message{NetworkID: 1, Target: "#a", Text: "first"})
	added := make(chan error)
	go func() { added <- s.Add(Message{NetworkID: 1, Target: "#a", Text: "second"}) }()
	select {
	case <-added:
		t.Fatal("Add didn't wait for a full queue")
	case <-time.After(50 * time.Millisecond):
	}
	if p := s.Pending(); p != 2 {
		t.Fatalf("pending: %d", p)
	}
	// A writer drains the queue, and the waiting Add goes through.
	s.writers.Add(1)
	go s.writer()
	if err := <-added; err != nil {
		t.Fatal(err)
	}
	s.Flush()
	if !strings.Contains(log.String(), "history write queue full") {
		t.Fatalf("full queue not logged: %s", log.String())
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestScyllaUnreachableReturnsErrors(t *testing.T) {
	captureLog(t)
	s := unreachable(t)
	errs := map[string]error{"Ping": s.Ping(), "DeleteNetwork": s.DeleteNetwork(1)}
	_, errs["Recent"] = s.Recent(1, "#a", 10)
	_, errs["Buffers"] = s.Buffers(1)
	_, errs["Networks"] = s.Networks()
	_, errs["Count"] = s.Count(1, "#a", 10)
	_, errs["Range"] = s.Range(1, "#a", Query{Limit: 10})
	_, errs["Targets"] = Targets(s, 1, ID{}, ID{}, 10, false)
	for name, err := range errs {
		if !errors.Is(err, gocql.ErrSessionClosed) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// no limit: nothing asked, nothing to fail
	if got, err := s.Range(1, "#a", Query{}); got != nil || err != nil {
		t.Errorf("no limit: %v %v", got, err)
	}
}

// Prune only trims buffers written since the last prune; one that fails is
// tried again next time.
func TestScyllaPruneRetriesFailedBuffers(t *testing.T) {
	captureLog(t)
	s := unreachable(t)
	if err := s.Prune(1, 10); err != nil {
		t.Fatalf("nothing written, nothing to do: %v", err)
	}
	s.Add(Message{NetworkID: 1, Target: "#a"})
	s.Add(Message{NetworkID: 2, Target: "#b"})
	s.Flush()
	if err := s.Prune(1, 0); err != nil {
		t.Fatalf("keep 0 keeps everything: %v", err)
	}
	if err := s.Prune(1, 10); !errors.Is(err, gocql.ErrSessionClosed) {
		t.Fatalf("prune: %v", err)
	}
	s.mu.Lock()
	again, other := s.dirty[1]["#a"], s.dirty[2]["#b"]
	s.mu.Unlock()
	if !again || !other {
		t.Fatalf("buffers to prune lost: %v %v", again, other)
	}
}

func TestScyllaClose(t *testing.T) {
	captureLog(t)
	s := unreachable(t)
	for i := 0; i < 100; i++ {
		s.Add(Message{NetworkID: 1, Target: "#a"})
	}
	// Close finishes the queue before returning.
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if p := s.Pending(); p != 0 {
		t.Fatalf("Close returned with %d writes pending", p)
	}
	if err := s.Add(Message{NetworkID: 1, Target: "#a"}); err == nil {
		t.Fatal("Add after Close succeeded")
	}
	if err := s.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}
