package history

// Line IDs, and the queries IRCv3 chathistory pages through history with.

import (
	"fmt"
	"sort"
	"strconv"
	"sync/atomic"
	"time"
)

// ID is a line's place in its buffer: its time in unix nanoseconds, then its
// Seq. Both backends key lines on it, so it's also a position to page from.
// Its string form is the line's IRCv3 msgid.
type ID struct {
	Nanos int64
	Seq   uint32
}

// MaxSeq is above every line's Seq: ID{t, MaxSeq} is after every line at t
// and ID{t, 0} before them.
const MaxSeq = 1<<31 - 1

var seqs atomic.Uint32

// NextSeq numbers a line, from 1 to MaxSeq-1 and round again. Every store in
// the process shares it, so lines of one instant don't share a number.
func NextSeq() uint32 { return 1 + seqs.Add(1)%(MaxSeq-1) }

// ID is the line's ID.
func (m Message) ID() ID { return ID{Nanos: m.Time.UnixNano(), Seq: m.Seq} }

// IsZero reports whether id is unset (an open end in a Query).
func (id ID) IsZero() bool { return id == ID{} }

// Before reports whether id sorts before o.
func (id ID) Before(o ID) bool {
	return id.Nanos < o.Nanos || id.Nanos == o.Nanos && id.Seq < o.Seq
}

// Time is the instant id names.
func (id ID) Time() time.Time { return time.Unix(0, id.Nanos) }

// String is the msgid: 16 hex digits of time, then 8 of sequence.
func (id ID) String() string { return fmt.Sprintf("%016x%08x", uint64(id.Nanos), id.Seq) }

// ParseID reads a msgid back.
func ParseID(s string) (ID, bool) {
	if len(s) != 24 {
		return ID{}, false
	}
	n, err1 := strconv.ParseUint(s[:16], 16, 64)
	q, err2 := strconv.ParseUint(s[16:], 16, 32)
	if err1 != nil || err2 != nil {
		return ID{}, false
	}
	return ID{Nanos: int64(n), Seq: uint32(q)}, true
}

// Query selects lines of a buffer strictly between After and Before (a zero
// ID leaves that end open): at most Limit of them, the newest when Latest is
// set, else the oldest.
type Query struct {
	After, Before ID
	Limit         int
	Latest        bool
}

// Target is a buffer and the time of its latest line in a range.
type Target struct {
	Name   string
	Latest time.Time
}

// Targets lists a network's buffers with lines strictly between after and
// before, each with the time of its latest line there, ordered by that time
// (newest first when newest is set), at most limit of them.
func Targets(s Store, networkID int64, after, before ID, limit int, newest bool) ([]Target, error) {
	bufs, err := s.Buffers(networkID)
	if err != nil {
		return nil, err
	}
	var out []Target
	for _, b := range bufs {
		last, err := s.Range(networkID, b, Query{After: after, Before: before, Limit: 1, Latest: true})
		if err != nil {
			return nil, err
		}
		if len(last) == 1 {
			out = append(out, Target{Name: b, Latest: last[0].Time})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if newest {
			return out[i].Latest.After(out[j].Latest)
		}
		return out[i].Latest.Before(out[j].Latest)
	})
	if len(out) > limit {
		out = out[:max(limit, 0)]
	}
	return out, nil
}
