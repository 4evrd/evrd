package history

// The ScyllaDB backend (Cassandra-compatible): history shared by every evrd
// node.
//
//	lines   ((network_id, buffer, bucket), ts, seq) — newest first
//	buckets ((network_id, buffer), bucket)          — the months a buffer has
//	buffers (network_id, buffer) -> target           — the buffer's spelling
//
// A buffer's lines are split into one partition per month (bucket = yyyymm)
// so no partition grows without bound. "Last N lines" reads the buffer's
// buckets newest first until it has N; trimming deletes a slice of the cutoff
// bucket and whole older ones. ts is unix nanoseconds (CQL timestamps are
// only milliseconds) and seq breaks ties within one writer. Every write is a
// plain upsert — no lightweight transactions, which ScyllaDB's tablets don't
// support.
//
// Add queues the write and returns: writer goroutines insert in the
// background, so a slow or distant database never stalls an IRC connection.
// Lines keep their order because ts and seq are fixed when they're queued.

import (
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gocql/gocql"
)

const (
	scyllaQueue   = 16384
	scyllaWriters = 8
)

type scyllaStore struct {
	s   *gocql.Session
	seq atomic.Uint32

	queue   chan scyllaWrite
	pending atomic.Int64 // queued or in flight
	writers sync.WaitGroup
	qmu     sync.RWMutex // Add holds it to send, Close to close the queue
	closed  bool

	mu    sync.Mutex
	known map[string]bool           // buffer and bucket index rows written by this process
	dirty map[int64]map[string]bool // buffers written since their last prune
}

type scyllaWrite struct {
	m   Message
	seq uint32
}

var keyspaceRe = regexp.MustCompile(`^[a-z_][a-z0-9_]{0,47}$`)

// openScylla connects to scylla://host1,host2:9042/keyspace?replication=1
// &consistency=local_quorum, creating the keyspace and tables if missing.
// replication only applies when the keyspace is created here; in production
// create it yourself with the replication you want.
func openScylla(dsn string) (*scyllaStore, error) {
	rest := dsn[strings.Index(dsn, "://")+3:]
	hostPart, path, _ := strings.Cut(rest, "/")
	keyspace, rawQuery, _ := strings.Cut(path, "?")
	if keyspace == "" {
		keyspace = "evrd"
	}
	if !keyspaceRe.MatchString(keyspace) {
		return nil, fmt.Errorf("history: bad keyspace %q", keyspace)
	}
	q, err := url.ParseQuery(rawQuery)
	if err != nil {
		return nil, fmt.Errorf("history: %w", err)
	}
	rf := 1
	if v := q.Get("replication"); v != "" {
		if rf, err = strconv.Atoi(v); err != nil || rf < 1 {
			return nil, fmt.Errorf("history: bad replication %q", v)
		}
	}
	consistency := gocql.LocalQuorum
	if v := q.Get("consistency"); v != "" {
		if err := consistency.UnmarshalText([]byte(strings.ToUpper(v))); err != nil {
			return nil, fmt.Errorf("history: bad consistency %q", v)
		}
	}
	hosts := strings.Split(hostPart, ",")

	cluster := gocql.NewCluster(hosts...)
	cluster.Consistency = consistency
	cluster.Timeout = 5 * time.Second
	cluster.ConnectTimeout = 10 * time.Second

	// The keyspace may not exist yet: connect without one to create it. A
	// database that's still starting gets a minute to come up.
	var boot *gocql.Session
	for wait := time.Now().Add(time.Minute); ; {
		if boot, err = cluster.CreateSession(); err == nil {
			break
		}
		if time.Now().After(wait) {
			return nil, fmt.Errorf("history: connect to %s: %w", hostPart, err)
		}
		slog.Warn("history store not reachable yet, retrying", "hosts", hostPart, "err", err)
		time.Sleep(3 * time.Second)
	}
	err = boot.Query(fmt.Sprintf(`CREATE KEYSPACE IF NOT EXISTS %s WITH replication =
		{'class': 'NetworkTopologyStrategy', 'replication_factor': %d}`, keyspace, rf)).Exec()
	boot.Close()
	if err != nil {
		return nil, fmt.Errorf("history: create keyspace: %w", err)
	}

	cluster.Keyspace = keyspace
	sess, err := cluster.CreateSession()
	if err != nil {
		return nil, fmt.Errorf("history: %w", err)
	}
	for _, ddl := range []string{
		`CREATE TABLE IF NOT EXISTS lines (
			network_id bigint, buffer text, bucket int, ts bigint, seq int,
			target text, sender text, kind text, text text, extra text,
			PRIMARY KEY ((network_id, buffer, bucket), ts, seq)
		) WITH CLUSTERING ORDER BY (ts DESC, seq DESC)`,
		`CREATE TABLE IF NOT EXISTS buckets (
			network_id bigint, buffer text, bucket int,
			PRIMARY KEY ((network_id, buffer), bucket)
		) WITH CLUSTERING ORDER BY (bucket DESC)`,
		`CREATE TABLE IF NOT EXISTS buffers (
			network_id bigint, buffer text, target text,
			PRIMARY KEY (network_id, buffer)
		)`,
	} {
		if err := sess.Query(ddl).Exec(); err != nil {
			sess.Close()
			return nil, fmt.Errorf("history: create tables: %w", err)
		}
	}

	return newScyllaStore(sess), nil
}

// newScyllaStore wraps a session whose keyspace and tables exist, and starts
// the writers.
func newScyllaStore(sess *gocql.Session) *scyllaStore {
	st := &scyllaStore{
		s:     sess,
		queue: make(chan scyllaWrite, scyllaQueue),
		known: map[string]bool{},
		dirty: map[int64]map[string]bool{},
	}
	for range scyllaWriters {
		st.writers.Add(1)
		go st.writer()
	}
	return st
}

// bucketOf is a time's partition: yyyymm, UTC.
func bucketOf(t time.Time) int {
	t = t.UTC()
	return t.Year()*100 + int(t.Month())
}

func (s *scyllaStore) Add(m Message) error {
	s.qmu.RLock()
	defer s.qmu.RUnlock()
	if s.closed {
		return errors.New("history: store closed")
	}
	if m.Time.IsZero() {
		m.Time = time.Now()
	}
	buf := strings.ToLower(m.Target)
	s.mu.Lock()
	if s.dirty[m.NetworkID] == nil {
		s.dirty[m.NetworkID] = map[string]bool{}
	}
	s.dirty[m.NetworkID][buf] = true
	s.mu.Unlock()

	s.pending.Add(1)
	w := scyllaWrite{m: m, seq: s.seq.Add(1)}
	select {
	case s.queue <- w:
	default:
		// The database is falling behind: wait rather than drop history.
		slog.Warn("history write queue full — the history store is slow", "queued", len(s.queue))
		s.queue <- w
	}
	return nil
}

func (s *scyllaStore) writer() {
	defer s.writers.Done()
	for w := range s.queue {
		if err := s.write(w); err != nil {
			slog.Error("history write failed", "network", w.m.NetworkID, "buffer", w.m.Target, "err", err)
		}
		s.pending.Add(-1)
	}
}

func (s *scyllaStore) write(w scyllaWrite) error {
	m := w.m
	buf, bucket := strings.ToLower(m.Target), bucketOf(m.Time)
	// Index rows are upserts, written once per process.
	for _, ix := range []struct {
		key  string
		cql  string
		args []any
	}{
		{fmt.Sprintf("%d/%s", m.NetworkID, buf), `INSERT INTO buffers (network_id, buffer, target) VALUES (?, ?, ?)`, []any{m.NetworkID, buf, m.Target}},
		{fmt.Sprintf("%d/%s/%d", m.NetworkID, buf, bucket), `INSERT INTO buckets (network_id, buffer, bucket) VALUES (?, ?, ?)`, []any{m.NetworkID, buf, bucket}},
	} {
		s.mu.Lock()
		done := s.known[ix.key]
		s.mu.Unlock()
		if done {
			continue
		}
		if err := s.s.Query(ix.cql, ix.args...).Exec(); err != nil {
			return err
		}
		s.mu.Lock()
		s.known[ix.key] = true
		s.mu.Unlock()
	}
	return s.s.Query(`INSERT INTO lines (network_id, buffer, bucket, ts, seq, target, sender, kind, text, extra)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		m.NetworkID, buf, bucket, m.Time.UnixNano(), int32(w.seq), m.Target, m.Sender, m.Kind, m.Text, m.Extra).Exec()
}

// Flush waits until every queued write has been attempted.
func (s *scyllaStore) Flush() {
	for s.pending.Load() > 0 {
		time.Sleep(2 * time.Millisecond)
	}
}

// buckets lists the months a buffer has lines in, newest first.
func (s *scyllaStore) buckets(networkID int64, buf string) ([]int, error) {
	it := s.s.Query(`SELECT bucket FROM buckets WHERE network_id = ? AND buffer = ?`, networkID, buf).Iter()
	var out []int
	var b int
	for it.Scan(&b) {
		out = append(out, b)
	}
	return out, it.Close()
}

func (s *scyllaStore) Recent(networkID int64, target string, limit int) ([]Message, error) {
	buf := strings.ToLower(target)
	buckets, err := s.buckets(networkID, buf)
	if err != nil || limit <= 0 {
		return nil, err
	}
	var out []Message // newest first while collecting
	for _, b := range buckets {
		if len(out) >= limit {
			break
		}
		it := s.s.Query(`SELECT ts, target, sender, kind, text, extra FROM lines
			WHERE network_id = ? AND buffer = ? AND bucket = ? LIMIT ?`,
			networkID, buf, b, limit-len(out)).Iter()
		var ts int64
		var m Message
		for it.Scan(&ts, &m.Target, &m.Sender, &m.Kind, &m.Text, &m.Extra) {
			m.NetworkID, m.Time = networkID, time.Unix(0, ts)
			out = append(out, m)
			m = Message{}
		}
		if err := it.Close(); err != nil {
			return nil, err
		}
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}

func (s *scyllaStore) Buffers(networkID int64) ([]string, error) {
	it := s.s.Query(`SELECT target FROM buffers WHERE network_id = ?`, networkID).Iter()
	out := []string{}
	var t string
	for it.Scan(&t) {
		out = append(out, t)
	}
	return out, it.Close()
}

// Prune trims the buffers written since their last prune; the others can't
// have grown. Reading a buffer's newest keep rows to find the cutoff is the
// cost, so skipping idle buffers matters.
func (s *scyllaStore) Prune(networkID int64, keep int) error {
	if keep <= 0 {
		return nil
	}
	s.mu.Lock()
	bufs := s.dirty[networkID]
	delete(s.dirty, networkID)
	s.mu.Unlock()
	for buf := range bufs {
		if err := s.pruneBuffer(networkID, buf, keep); err != nil {
			// Try again next time.
			s.mu.Lock()
			if s.dirty[networkID] == nil {
				s.dirty[networkID] = map[string]bool{}
			}
			s.dirty[networkID][buf] = true
			s.mu.Unlock()
			return err
		}
	}
	return nil
}

func (s *scyllaStore) pruneBuffer(networkID int64, buf string, keep int) error {
	buckets, err := s.buckets(networkID, buf)
	if err != nil {
		return err
	}
	// Walk newest-first to the keep-th line; it's the oldest one kept.
	seen, cut := 0, -1 // cut indexes buckets
	var cutTS int64
	var cutSeq int32
	for i, b := range buckets {
		if seen >= keep {
			break
		}
		it := s.s.Query(`SELECT ts, seq FROM lines WHERE network_id = ? AND buffer = ? AND bucket = ? LIMIT ?`,
			networkID, buf, b, keep-seen).PageSize(1000).Iter()
		var ts int64
		var seq int32
		for it.Scan(&ts, &seq) {
			seen++
			cut, cutTS, cutSeq = i, ts, seq
		}
		if err := it.Close(); err != nil {
			return err
		}
	}
	if seen < keep {
		return nil // under the limit
	}
	// Older lines in the cutoff bucket: earlier timestamps, then earlier
	// seqs at the same timestamp.
	for _, q := range []struct {
		cql  string
		args []any
	}{
		{`DELETE FROM lines WHERE network_id = ? AND buffer = ? AND bucket = ? AND ts < ?`, []any{networkID, buf, buckets[cut], cutTS}},
		{`DELETE FROM lines WHERE network_id = ? AND buffer = ? AND bucket = ? AND ts = ? AND seq < ?`, []any{networkID, buf, buckets[cut], cutTS, cutSeq}},
	} {
		if err := s.s.Query(q.cql, q.args...).Exec(); err != nil {
			return err
		}
	}
	// Whole older buckets, and their index rows.
	for _, b := range buckets[cut+1:] {
		if err := s.s.Query(`DELETE FROM lines WHERE network_id = ? AND buffer = ? AND bucket = ?`, networkID, buf, b).Exec(); err != nil {
			return err
		}
		if err := s.s.Query(`DELETE FROM buckets WHERE network_id = ? AND buffer = ? AND bucket = ?`, networkID, buf, b).Exec(); err != nil {
			return err
		}
		s.mu.Lock()
		delete(s.known, fmt.Sprintf("%d/%s/%d", networkID, buf, b))
		s.mu.Unlock()
	}
	return nil
}

func (s *scyllaStore) DeleteNetwork(networkID int64) error {
	s.Flush() // a queued line must not land after the delete
	it := s.s.Query(`SELECT buffer FROM buffers WHERE network_id = ?`, networkID).Iter()
	var bufs []string
	var name string
	for it.Scan(&name) {
		bufs = append(bufs, name)
	}
	if err := it.Close(); err != nil {
		return err
	}
	for _, buf := range bufs {
		buckets, err := s.buckets(networkID, buf)
		if err != nil {
			return err
		}
		for _, b := range buckets {
			if err := s.s.Query(`DELETE FROM lines WHERE network_id = ? AND buffer = ? AND bucket = ?`, networkID, buf, b).Exec(); err != nil {
				return err
			}
		}
		if err := s.s.Query(`DELETE FROM buckets WHERE network_id = ? AND buffer = ?`, networkID, buf).Exec(); err != nil {
			return err
		}
	}
	if err := s.s.Query(`DELETE FROM buffers WHERE network_id = ?`, networkID).Exec(); err != nil {
		return err
	}
	s.mu.Lock()
	prefix := fmt.Sprintf("%d/", networkID)
	for k := range s.known {
		if strings.HasPrefix(k, prefix) {
			delete(s.known, k)
		}
	}
	delete(s.dirty, networkID)
	s.mu.Unlock()
	return nil
}

func (s *scyllaStore) Networks() ([]int64, error) {
	it := s.s.Query(`SELECT DISTINCT network_id FROM buffers`).Iter()
	var out []int64
	var id int64
	for it.Scan(&id) {
		out = append(out, id)
	}
	return out, it.Close()
}

func (s *scyllaStore) Close() error {
	s.qmu.Lock()
	if s.closed {
		s.qmu.Unlock()
		return nil
	}
	s.closed = true
	close(s.queue)
	s.qmu.Unlock()
	s.writers.Wait()
	s.s.Close()
	return nil
}

func (s *scyllaStore) Count(networkID int64, target string, max int) (int, error) {
	buf := strings.ToLower(target)
	buckets, err := s.buckets(networkID, buf)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, b := range buckets {
		if n >= max {
			break
		}
		it := s.s.Query(`SELECT ts FROM lines WHERE network_id = ? AND buffer = ? AND bucket = ? LIMIT ?`,
			networkID, buf, b, max-n).PageSize(1000).Iter()
		var ts int64
		for it.Scan(&ts) {
			n++
		}
		if err := it.Close(); err != nil {
			return n, err
		}
	}
	return n, nil
}

func (s *scyllaStore) Ping() error {
	var now string
	return s.s.Query(`SELECT release_version FROM system.local`).Scan(&now)
}

// Pending is the number of writes waiting in the queue.
func (s *scyllaStore) Pending() int64 { return s.pending.Load() }
