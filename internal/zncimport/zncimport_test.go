package zncimport

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/4evrd/evrd/internal/history"
)

func TestParseLine(t *testing.T) {
	day := time.Date(2024, 3, 9, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		line                      string
		kind, sender, text, extra string
	}{
		{"[12:34:56] <bob> hello there", "message", "bob", "hello there", ""},
		{"[12:34:56] <bob> <not> a nick", "message", "bob", "<not> a nick", ""},
		{"[01:02:03] * bob waves", "action", "bob", "waves", ""},
		{"[01:02:03] -NickServ- You are now identified", "notice", "NickServ", "You are now identified", ""},
		{"[01:02:03] *** Joins: bob (b@host.example)", "join", "bob", "", ""},
		{"[01:02:03] *** Parts: bob (b@host.example) (see you)", "part", "bob", "see you", ""},
		{"[01:02:03] *** Parts: bob (b@host.example)", "part", "bob", "", ""},
		{"[01:02:03] *** Quits: bob (b@host.example) (Ping timeout: 240 seconds)", "quit", "bob", "Ping timeout: 240 seconds", ""},
		{"[01:02:03] *** carol was kicked by op (spam)", "kick", "op", "spam", "carol"},
		{"[01:02:03] *** bob is now known as robert", "nick", "bob", "", "robert"},
		{"[01:02:03] *** op changes topic to 'Welcome — it's 'quoted''", "topic", "op", "Welcome — it's 'quoted'", ""},
		{"[01:02:03] *** op sets mode: +o bob", "mode", "op", "+o bob", ""},
	}
	for _, c := range cases {
		m, ok := ParseLine(day, "#chan", c.line)
		if !ok {
			t.Errorf("%q: not parsed", c.line)
			continue
		}
		if m.Kind != c.kind || m.Sender != c.sender || m.Text != c.text || m.Extra != c.extra || m.Target != "#chan" {
			t.Errorf("%q: got %+v", c.line, m)
		}
	}
	m, _ := ParseLine(day, "#chan", "[12:34:56] <bob> hi")
	if want := time.Date(2024, 3, 9, 12, 34, 56, 0, time.UTC); !m.Time.Equal(want) {
		t.Errorf("time %v, want %v", m.Time, want)
	}
	for _, skip := range []string{"[00:03:24] Connected to IRC (irc.example.org +6697)", "[00:03:24] Disconnected from IRC (irc.example.org +6697)", "no timestamp", "[00:00:01] *** something unknown"} {
		if _, ok := ParseLine(day, "#chan", skip); ok {
			t.Errorf("%q was imported", skip)
		}
	}
}

func TestFindBothLayoutsOnce(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, body string) {
		p := filepath.Join(dir, rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(body), 0o644)
	}
	// the same day in both layouts: only the user's copy counts
	write("moddata/log/libera/#go/2024-03-09.log", "[10:00:00] <bob> from the user log\n")
	write("networks/libera/moddata/log/#go/2024-03-09.log", "[10:00:00] <bob> from the network log\n")
	write("networks/libera/moddata/log/#go/2024-03-08.log", "[09:00:00] <bob> only in the network log\n")
	write("moddata/log/libera/*status/2024-03-09.log", "[10:00:00] <*status> skipped\n")
	write("moddata/log/rizon/carol/2024-03-10.log", "[11:00:00] <carol> a query\n")

	files, err := Find(dir, time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, f := range files {
		var msgs []history.Message
		if _, err := Read(f, func(m history.Message) error { msgs = append(msgs, m); return nil }); err != nil {
			t.Fatal(err)
		}
		for _, m := range msgs {
			got = append(got, f.Network+" "+m.Target+" "+m.Text)
		}
	}
	want := []string{
		"libera #go only in the network log",
		"libera #go from the user log",
		"rizon carol a query",
	}
	if len(got) != len(want) {
		t.Fatalf("got %q", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %q, want %q", got, want)
		}
	}
}
