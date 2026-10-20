package history

// Line IDs and the chathistory queries, against every backend.

import (
	"fmt"
	"testing"
	"time"
)

// ids are a buffer's line IDs, oldest first.
func ids(t *testing.T, s Store, net int64, target string) []ID {
	t.Helper()
	ms, err := s.Recent(net, target, 1000)
	if err != nil {
		t.Fatal(err)
	}
	var out []ID
	for _, m := range ms {
		if m.Seq == 0 {
			t.Fatalf("line without a seq: %+v", m)
		}
		out = append(out, m.ID())
	}
	return out
}

func TestRange(t *testing.T) {
	each(t, func(t *testing.T, s Store, _ func() Store) {
		start := time.Now().Add(-time.Hour).Truncate(time.Second)
		add(t, s, 1, "#a", 10, start)
		add(t, s, 1, "#b", 3, start)
		id := ids(t, s, 1, "#a")
		at := func(i int) int64 { return start.Add(time.Duration(i) * time.Second).UnixNano() }
		for _, c := range []struct {
			name string
			q    Query
			want string
		}{
			{"oldest", Query{Limit: 3}, "0 1 2 "},
			{"latest", Query{Limit: 3, Latest: true}, "7 8 9 "},
			{"everything", Query{Limit: 100}, "0 1 2 3 4 5 6 7 8 9 "},
			{"after", Query{After: id[2], Limit: 3}, "3 4 5 "},
			{"after, latest", Query{After: id[2], Limit: 3, Latest: true}, "7 8 9 "},
			{"before", Query{Before: id[5], Limit: 2, Latest: true}, "3 4 "},
			{"before, oldest", Query{Before: id[5], Limit: 2}, "0 1 "},
			{"between", Query{After: id[2], Before: id[6], Limit: 10}, "3 4 5 "},
			{"between, latest", Query{After: id[2], Before: id[6], Limit: 2, Latest: true}, "4 5 "},
			{"after a time", Query{After: ID{at(3), MaxSeq}, Limit: 2}, "4 5 "},
			{"before a time", Query{Before: ID{at(3), 0}, Limit: 2, Latest: true}, "1 2 "},
			{"nothing between", Query{After: id[5], Before: id[6], Limit: 10}, ""},
			{"crossed", Query{After: id[6], Before: id[5], Limit: 10}, ""},
			{"no limit", Query{Limit: 0}, ""},
			{"past the end", Query{After: id[9], Limit: 5}, ""},
		} {
			got, err := s.Range(1, "#A", c.q)
			if err != nil {
				t.Fatalf("%s: %v", c.name, err)
			}
			if texts(got) != c.want {
				t.Errorf("%s: got %q, want %q", c.name, texts(got), c.want)
			}
		}
		// The lines come back with the IDs Recent gave them.
		got, _ := s.Range(1, "#a", Query{After: id[0], Limit: 1})
		if len(got) != 1 || got[0].ID() != id[1] || got[0].NetworkID != 1 || got[0].Sender != "bob" {
			t.Fatalf("got %+v, want ID %v", got, id[1])
		}
		if got, _ := s.Range(2, "#a", Query{Limit: 5}); len(got) != 0 {
			t.Fatalf("another network: %v", got)
		}
	})
}

func TestRangeSpansMonths(t *testing.T) {
	each(t, func(t *testing.T, s Store, _ func() Store) {
		add(t, s, 1, "#a", 3, time.Now().AddDate(0, -3, 0))
		add(t, s, 1, "#a", 2, time.Now().Add(-time.Hour))
		id := ids(t, s, 1, "#a")
		for _, c := range []struct {
			q    Query
			want string
		}{
			{Query{Limit: 4}, "0 1 2 0 "},
			{Query{Limit: 3, Latest: true}, "2 0 1 "},
			{Query{After: id[2], Limit: 5}, "0 1 "},
			{Query{After: id[1], Limit: 2}, "2 0 "},
			{Query{Before: id[3], Limit: 5, Latest: true}, "0 1 2 "},
			{Query{Before: id[4], Limit: 2, Latest: true}, "2 0 "},
			{Query{After: id[0], Before: id[4], Limit: 5}, "1 2 0 "},
		} {
			if got, err := s.Range(1, "#a", c.q); err != nil || texts(got) != c.want {
				t.Errorf("%+v: got %q %v, want %q", c.q, texts(got), err, c.want)
			}
		}
	})
}

// Lines of one instant are told apart by their seq.
func TestRangeSameInstant(t *testing.T) {
	each(t, func(t *testing.T, s Store, _ func() Store) {
		at := time.Now()
		for _, text := range []string{"one", "two", "three"} {
			s.Add(Message{NetworkID: 1, Target: "#a", Text: text, Time: at})
		}
		settle(s)
		id := ids(t, s, 1, "#a")
		if len(id) != 3 || id[0].Nanos != id[2].Nanos || id[0] == id[1] {
			t.Fatalf("ids: %v", id)
		}
		if got, _ := s.Range(1, "#a", Query{After: id[0], Limit: 5}); texts(got) != "two three " {
			t.Fatalf("after the first: %q", texts(got))
		}
		if got, _ := s.Range(1, "#a", Query{Before: id[2], Limit: 5, Latest: true}); texts(got) != "one two " {
			t.Fatalf("before the last: %q", texts(got))
		}
		// A line keeps the seq it was given.
		s.Add(Message{NetworkID: 1, Target: "#b", Text: "set", Time: at, Seq: 77})
		settle(s)
		if got, _ := s.Recent(1, "#b", 1); len(got) != 1 || got[0].Seq != 77 {
			t.Fatalf("seq: %+v", got)
		}
	})
}

func TestTargets(t *testing.T) {
	each(t, func(t *testing.T, s Store, _ func() Store) {
		start := time.Now().Add(-time.Hour).Truncate(time.Second)
		add(t, s, 1, "#a", 3, start)                     // latest at +2s
		add(t, s, 1, "bob", 1, start.Add(5*time.Second)) // +5s
		add(t, s, 1, "#old", 1, start.Add(-time.Hour))
		add(t, s, 2, "#other", 1, start)
		show := func(ts []Target) string {
			out := ""
			for _, t := range ts {
				out += fmt.Sprintf("%s@%d ", t.Name, t.Latest.Sub(start)/time.Second)
			}
			return out
		}
		from, to := ID{start.UnixNano(), 0}, ID{start.Add(time.Minute).UnixNano(), 0}
		for _, c := range []struct {
			after, before ID
			limit         int
			newest        bool
			want          string
		}{
			{from, to, 10, false, "#a@2 bob@5 "},
			{from, to, 10, true, "bob@5 #a@2 "},
			{from, to, 1, true, "bob@5 "},
			{from, to, 0, false, ""},
			// the latest line inside the range counts
			{from, ID{start.Add(time.Second).UnixNano(), MaxSeq}, 10, false, "#a@1 "},
			{ID{}, ID{}, 10, false, "#old@-3600 #a@2 bob@5 "},
		} {
			got, err := Targets(s, 1, c.after, c.before, c.limit, c.newest)
			if err != nil || show(got) != c.want {
				t.Errorf("%+v: got %q %v, want %q", c, show(got), err, c.want)
			}
		}
	})
}

func TestIDs(t *testing.T) {
	id := ID{Nanos: 1792284500123456789, Seq: 42}
	s := id.String()
	if len(s) != 24 {
		t.Fatalf("msgid %q", s)
	}
	if back, ok := ParseID(s); !ok || back != id {
		t.Fatalf("parsed %v %v", back, ok)
	}
	for _, bad := range []string{"", "x", s[:23], s + "0", "zz" + s[2:], s[:16] + "zzzzzzzz"} {
		if _, ok := ParseID(bad); ok {
			t.Errorf("parsed %q", bad)
		}
	}
	if !id.Time().Equal(time.Unix(0, id.Nanos)) || id.IsZero() || !(ID{}).IsZero() {
		t.Fatal("time / zero")
	}
	m := Message{Time: id.Time(), Seq: 42}
	if m.ID() != id {
		t.Fatalf("message id %v", m.ID())
	}
	for _, c := range []struct {
		a, b ID
		want bool
	}{
		{ID{1, 5}, ID{2, 0}, true},
		{ID{1, 5}, ID{1, 6}, true},
		{ID{1, 5}, ID{1, 5}, false},
		{ID{2, 0}, ID{1, 9}, false},
	} {
		if got := c.a.Before(c.b); got != c.want {
			t.Errorf("%v before %v: %v", c.a, c.b, got)
		}
	}
	// Seqs stay between 0 and MaxSeq, both exclusive, even wrapping round.
	seqs.Store(MaxSeq - 3)
	for range 5 {
		if n := NextSeq(); n == 0 || n >= MaxSeq {
			t.Fatalf("seq %d", n)
		}
	}
}
