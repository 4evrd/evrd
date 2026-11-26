package history

import (
	"encoding/binary"
	"fmt"
	"math/rand/v2"
	"os"
	"testing"
	"time"

	"github.com/gocql/gocql"
)

func TestStoredTags(t *testing.T) {
	for in, want := range map[string]string{
		"":                                   "",
		"+typing=active":                     "",
		"+draft/reply=abc":                   "+draft/reply=abc",
		"+typing=active;+draft/reply=abc":    "+draft/reply=abc",
		"+reply=x;+draft/react=👍;+unknown=1": "+draft/react=👍;+reply=x",
		"+draft/unreact=👍;+unreact=👍;+react=❤": "+draft/unreact=👍;+react=❤;+unreact=👍",
		"+draft/channel-context=#go;msgid=x":   "+draft/channel-context=#go",
		"+draft/reply":                         "+draft/reply",
	} {
		if got := StoredTags(in); got != want {
			t.Errorf("StoredTags(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestReaction(t *testing.T) {
	for in, want := range map[string]bool{
		"":                                false,
		"+typing=active":                  false,
		"+draft/reply=abc":                false,
		"+draft/react=👍;+draft/reply=abc": true,
		"+react=👍":                        true,
		"+draft/unreact=👍":                true,
		"+unreact=👍":                      true,
	} {
		if got := Reaction(in); got != want {
			t.Errorf("Reaction(%q) = %v", in, got)
		}
	}
	if !KeptTag("+draft/reply") || KeptTag("+typing") || !ReplyTag("+reply") || ReplyTag("+draft/react") {
		t.Fatal("KeptTag / ReplyTag")
	}
}

// Replies and reactions are stored with the line, in every backend; typing
// and unknown tags are not.
func TestTagsRoundTrip(t *testing.T) {
	each(t, func(t *testing.T, s Store, reopen func() Store) {
		at := time.Unix(0, 1792284500123456789)
		ref := ID{Nanos: at.UnixNano() - 5, Seq: 9}.String()
		lines := []Message{
			{NetworkID: 3, Target: "#t", Sender: "ann", Kind: "message", Text: "plain", Time: at, Seq: 1},
			{NetworkID: 3, Target: "#t", Sender: "bob", Kind: "message", Text: "re", Time: at, Seq: 2,
				Tags: "+typing=active;+draft/reply=" + ref + ";+x=y"},
			{NetworkID: 3, Target: "#t", Sender: "cat", Kind: KindTagMsg, Time: at, Seq: 3,
				Tags: "+draft/react=👍;+draft/reply=" + ref},
			{NetworkID: 3, Target: "#t", Sender: "dan", Kind: "message", Text: "typing only", Time: at, Seq: 4, Tags: "+typing=done"},
		}
		for _, m := range lines {
			if err := s.Add(m); err != nil {
				t.Fatal(err)
			}
		}
		settle(s)
		want := []string{"", "+draft/reply=" + ref, "+draft/react=👍;+draft/reply=" + ref, ""}
		check := func(s Store) {
			t.Helper()
			got, err := s.Recent(3, "#t", 10)
			if err != nil || len(got) != len(want) {
				t.Fatalf("got %+v, %v", got, err)
			}
			for i, m := range got {
				if m.Tags != want[i] || m.Kind != lines[i].Kind || m.Text != lines[i].Text {
					t.Errorf("line %d: %+v, want tags %q", i, m, want[i])
				}
			}
			r, err := s.Range(3, "#t", Query{After: lines[1].ID(), Limit: 1})
			if err != nil || len(r) != 1 || r[0].Tags != want[2] {
				t.Errorf("range: %+v, %v", r, err)
			}
		}
		check(s)
		s.Close()
		check(reopen())
	})
}

// A record written before tags were kept (no tags field) still reads, and
// one with tags reads in a reader that ignores fields it doesn't know.
func TestPebbleTagsCompatible(t *testing.T) {
	old := []byte{encodingV1}
	old = binary.AppendVarint(old, 1000)
	for _, f := range []string{"#a", "bob", "message", "hi", ""} {
		old = binary.AppendUvarint(old, uint64(len(f)))
		old = append(old, f...)
	}
	m, err := decode(1, old)
	if err != nil || m.Text != "hi" || m.Tags != "" {
		t.Fatalf("old record: %+v, %v", m, err)
	}
	// without tags the encoding is exactly the old one
	if got := encode(Message{Target: "#a", Sender: "bob", Kind: "message", Text: "hi", Time: time.Unix(0, 1000), Tags: "+typing=active"}); string(got) != string(old) {
		t.Fatalf("untagged record changed: %x vs %x", got, old)
	}
	tagged := encode(Message{Target: "#a", Sender: "bob", Kind: "message", Text: "hi", Time: time.Unix(0, 1000), Tags: "+draft/reply=x"})
	if len(tagged) <= len(old) || string(tagged[:len(old)]) != string(old) {
		t.Fatalf("tags aren't an appended field: %x", tagged)
	}
	if m, err := decode(1, tagged); err != nil || m.Tags != "+draft/reply=x" {
		t.Fatalf("tagged: %+v, %v", m, err)
	}
	// a damaged tags field is dropped, not the line
	if m, err := decode(1, append(old, 0x10, 'x')); err != nil || m.Text != "hi" || m.Tags != "" {
		t.Fatalf("damaged tags: %+v, %v", m, err)
	}
}

// A ScyllaDB lines table made before tags were kept gets the column, and
// its rows still read.
func TestScyllaAddsTagsColumn(t *testing.T) {
	hosts := os.Getenv("EVRD_TEST_SCYLLA")
	if hosts == "" {
		t.Skip("EVRD_TEST_SCYLLA not set")
	}
	ks := fmt.Sprintf("evrd_test_%d", rand.Uint32())
	c := gocql.NewCluster(hosts)
	boot, err := c.CreateSession()
	if err != nil {
		t.Fatal(err)
	}
	defer boot.Close()
	t.Cleanup(func() { boot.Query("DROP KEYSPACE IF EXISTS " + ks).Exec() })
	at := time.Now()
	for _, cql := range []string{
		`CREATE KEYSPACE ` + ks + ` WITH replication = {'class': 'NetworkTopologyStrategy', 'replication_factor': 1}`,
		`CREATE TABLE ` + ks + `.lines (
			network_id bigint, buffer text, bucket int, ts bigint, seq int,
			target text, sender text, kind text, text text, extra text,
			PRIMARY KEY ((network_id, buffer, bucket), ts, seq)
		) WITH CLUSTERING ORDER BY (ts DESC, seq DESC)`,
		`CREATE TABLE ` + ks + `.buckets (network_id bigint, buffer text, bucket int,
			PRIMARY KEY ((network_id, buffer), bucket)) WITH CLUSTERING ORDER BY (bucket DESC)`,
		`CREATE TABLE ` + ks + `.buffers (network_id bigint, buffer text, target text, PRIMARY KEY (network_id, buffer))`,
		fmt.Sprintf(`INSERT INTO %s.lines (network_id, buffer, bucket, ts, seq, target, sender, kind, text, extra)
			VALUES (1, '#a', %d, %d, 1, '#a', 'bob', 'message', 'old', '')`, ks, bucketOf(at), at.UnixNano()),
		fmt.Sprintf(`INSERT INTO %s.buckets (network_id, buffer, bucket) VALUES (1, '#a', %d)`, ks, bucketOf(at)),
	} {
		if err := boot.Query(cql).Exec(); err != nil {
			t.Fatal(err)
		}
	}
	s := mustOpen(t, fmt.Sprintf("scylla://%s/%s?consistency=one", hosts, ks))
	s.Add(Message{NetworkID: 1, Target: "#a", Sender: "ann", Kind: "message", Text: "new", Time: at.Add(time.Second), Tags: "+draft/reply=x"})
	settle(s)
	got, err := s.Recent(1, "#a", 10)
	if err != nil || len(got) != 2 || got[0].Text != "old" || got[0].Tags != "" || got[1].Tags != "+draft/reply=x" {
		t.Fatalf("got %+v, %v", got, err)
	}
	// opening again finds the column there
	mustOpen(t, fmt.Sprintf("scylla://%s/%s?consistency=one", hosts, ks))
}
