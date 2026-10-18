package history

// The Pebble backend: an embedded LSM store on local disk. Single process
// only — for single-node installs, development and tests.
//
// Keys sort by network, then buffer, then time, so the questions a bouncer
// asks are range scans:
//
//	m <network id> <buffer> 0x00 <unix nanos> <seq>  -> encoded message
//	b <network id> <buffer>                          -> buffer name as first seen
//
// "Last N lines of #go" is a reverse scan, trimming a buffer to its newest N
// lines is one range delete, and dropping a network is two. Writes don't wait
// for an fsync: Pebble syncs its write-ahead log in the background, so a crash
// can lose the last moments of history but writing never stalls the network.

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cockroachdb/pebble/v2"
)

type pebbleStore struct {
	db     *pebble.DB
	seq    atomic.Uint32
	closed atomic.Bool

	mu    sync.Mutex
	known map[string]bool // buffer index keys already written
}

const (
	prefixMsg    = 'm'
	prefixBuffer = 'b'
)

// openPebble opens (creating if needed) a Pebble history store in dir.
func openPebble(dir string) (*pebbleStore, error) {
	db, err := pebble.Open(dir, &pebble.Options{
		// Sync the WAL in the background every 512 KiB rather than per write.
		WALBytesPerSync: 512 << 10,
		Logger:          pebbleLogger{},
	})
	if err != nil {
		return nil, fmt.Errorf("open history %s: %w", dir, err)
	}
	return &pebbleStore{db: db, known: map[string]bool{}}, nil
}

func (s *pebbleStore) Close() error {
	if s.closed.Swap(true) {
		return nil
	}
	return s.db.Close()
}

// --- keys ------------------------------------------------------------------

func networkPrefix(kind byte, networkID int64) []byte {
	k := make([]byte, 9, 64)
	k[0] = kind
	binary.BigEndian.PutUint64(k[1:], uint64(networkID))
	return k
}

// bufferPrefix is every message key of one buffer.
func bufferPrefix(networkID int64, target string) []byte {
	k := networkPrefix(prefixMsg, networkID)
	k = append(k, strings.ToLower(target)...)
	return append(k, 0)
}

func messageKey(m Message, seq uint32) []byte {
	k := bufferPrefix(m.NetworkID, m.Target)
	k = binary.BigEndian.AppendUint64(k, uint64(m.Time.UnixNano()))
	return binary.BigEndian.AppendUint32(k, seq)
}

// upperBound is the first key after every key starting with prefix.
func upperBound(prefix []byte) []byte {
	end := bytes.Clone(prefix)
	for i := len(end) - 1; i >= 0; i-- {
		if end[i] < 0xff {
			end[i]++
			return end[:i+1]
		}
	}
	return nil // all 0xff: no upper bound
}

// --- values ----------------------------------------------------------------

const encodingV1 = 1

func encode(m Message) []byte {
	b := make([]byte, 0, 32+len(m.Target)+len(m.Sender)+len(m.Text)+len(m.Extra))
	b = append(b, encodingV1)
	b = binary.AppendVarint(b, m.Time.UnixNano())
	for _, f := range []string{m.Target, m.Sender, m.Kind, m.Text, m.Extra} {
		b = binary.AppendUvarint(b, uint64(len(f)))
		b = append(b, f...)
	}
	return b
}

var errCorrupt = errors.New("history: corrupt record")

func decode(networkID int64, b []byte) (Message, error) {
	if len(b) == 0 || b[0] != encodingV1 {
		return Message{}, errCorrupt
	}
	b = b[1:]
	nanos, n := binary.Varint(b)
	if n <= 0 {
		return Message{}, errCorrupt
	}
	b = b[n:]
	var fields [5]string
	for i := range fields {
		l, n := binary.Uvarint(b)
		if n <= 0 || uint64(len(b)-n) < l {
			return Message{}, errCorrupt
		}
		fields[i] = string(b[n : n+int(l)])
		b = b[n+int(l):]
	}
	return Message{
		NetworkID: networkID, Time: time.Unix(0, nanos),
		Target: fields[0], Sender: fields[1], Kind: fields[2], Text: fields[3], Extra: fields[4],
	}, nil
}

// --- operations --------------------------------------------------------------

func (s *pebbleStore) Add(m Message) error {
	if m.Time.IsZero() {
		m.Time = time.Now()
	}
	batch := s.db.NewBatch()
	defer batch.Close()
	if err := batch.Set(messageKey(m, s.seq.Add(1)), encode(m), nil); err != nil {
		return err
	}
	// Index the buffer the first time this process sees it.
	bk := string(append(networkPrefix(prefixBuffer, m.NetworkID), strings.ToLower(m.Target)...))
	s.mu.Lock()
	isNew := !s.known[bk]
	s.known[bk] = true
	s.mu.Unlock()
	if isNew {
		if err := batch.Set([]byte(bk), []byte(m.Target), nil); err != nil {
			return err
		}
	}
	return batch.Commit(pebble.NoSync)
}

func (s *pebbleStore) Recent(networkID int64, target string, limit int) ([]Message, error) {
	prefix := bufferPrefix(networkID, target)
	it, err := s.db.NewIter(&pebble.IterOptions{LowerBound: prefix, UpperBound: upperBound(prefix)})
	if err != nil {
		return nil, err
	}
	defer it.Close()
	var out []Message
	for ok := it.Last(); ok && len(out) < limit; ok = it.Prev() {
		m, err := decode(networkID, it.Value())
		if err != nil {
			continue // skip a damaged record rather than lose the buffer
		}
		out = append(out, m)
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, it.Error()
}

func (s *pebbleStore) Buffers(networkID int64) ([]string, error) {
	prefix := networkPrefix(prefixBuffer, networkID)
	it, err := s.db.NewIter(&pebble.IterOptions{LowerBound: prefix, UpperBound: upperBound(prefix)})
	if err != nil {
		return nil, err
	}
	defer it.Close()
	out := []string{}
	for ok := it.First(); ok; ok = it.Next() {
		out = append(out, string(it.Value()))
	}
	return out, it.Error()
}

func (s *pebbleStore) Prune(networkID int64, keep int) error {
	if keep <= 0 {
		return nil
	}
	buffers, err := s.Buffers(networkID)
	if err != nil {
		return err
	}
	for _, b := range buffers {
		prefix := bufferPrefix(networkID, b)
		it, err := s.db.NewIter(&pebble.IterOptions{LowerBound: prefix, UpperBound: upperBound(prefix)})
		if err != nil {
			return err
		}
		// Walk back to the oldest line to keep; everything before it goes.
		n, ok := 0, it.Last()
		for ok && n < keep-1 {
			ok, n = it.Prev(), n+1
		}
		var cutoff []byte
		if ok && it.Prev() {
			cutoff = bytes.Clone(it.Key())
		}
		if err := it.Close(); err != nil {
			return err
		}
		if cutoff != nil {
			// [prefix, cutoff] inclusive: the end bound is exclusive, so step past it.
			if err := s.db.DeleteRange(prefix, append(cutoff, 0), pebble.NoSync); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *pebbleStore) DeleteNetwork(networkID int64) error {
	for _, kind := range []byte{prefixMsg, prefixBuffer} {
		p := networkPrefix(kind, networkID)
		if err := s.db.DeleteRange(p, upperBound(p), pebble.NoSync); err != nil {
			return err
		}
	}
	s.mu.Lock()
	prefix := string(networkPrefix(prefixBuffer, networkID))
	for k := range s.known {
		if strings.HasPrefix(k, prefix) {
			delete(s.known, k)
		}
	}
	s.mu.Unlock()
	return nil
}

func (s *pebbleStore) Networks() ([]int64, error) {
	var out []int64
	it, err := s.db.NewIter(&pebble.IterOptions{LowerBound: []byte{prefixBuffer}, UpperBound: []byte{prefixBuffer + 1}})
	if err != nil {
		return nil, err
	}
	defer it.Close()
	for ok := it.First(); ok; {
		id := int64(binary.BigEndian.Uint64(it.Key()[1:9]))
		out = append(out, id)
		ok = it.SeekGE(networkPrefix(prefixBuffer, id+1))
	}
	return out, it.Error()
}

// pebbleLogger keeps Pebble's routine chatter (WAL replay, compactions) out of
// evrd's log, passing on only errors.
type pebbleLogger struct{}

func (pebbleLogger) Infof(string, ...any) {}
func (pebbleLogger) Errorf(format string, args ...any) {
	slog.Error("history store: " + fmt.Sprintf(format, args...))
}
func (pebbleLogger) Fatalf(format string, args ...any) {
	slog.Error("history store: " + fmt.Sprintf(format, args...))
	os.Exit(1)
}

func (s *pebbleStore) Count(networkID int64, target string, max int) (int, error) {
	prefix := bufferPrefix(networkID, target)
	it, err := s.db.NewIter(&pebble.IterOptions{LowerBound: prefix, UpperBound: upperBound(prefix)})
	if err != nil {
		return 0, err
	}
	defer it.Close()
	n := 0
	for ok := it.Last(); ok && n < max; ok = it.Prev() {
		n++
	}
	return n, it.Error()
}

func (s *pebbleStore) Ping() error { return nil }
