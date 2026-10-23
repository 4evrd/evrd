package client

import (
	"strings"
	"testing"
	"unicode"
)

func TestIRCText(t *testing.T) {
	got := ircText("autoclear <channel|nick>[,…] on|off|- — it’s “new” · 0–500 → x, café")
	want := `autoclear <channel|nick>[,...] on|off|- - it's "new" - 0-500 -> x, café`
	if got != want {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
}

// What *status sends over IRC is plain ASCII (mIRC on Windows shows "…"
// and "—" as garbage), and the help table still lines up.
func TestStatusRepliesAreASCII(t *testing.T) {
	r := newRig(t)
	ic := r.attached()
	for _, cmd := range []string{"help", "autoclear", "clearbuffer", "setbuffer", "buffers", "attach", "disablechan"} {
		ic.send("PRIVMSG *status :" + cmd)
	}
	ic.send("PRIVMSG *status :uptime")
	var table []string
	for _, l := range ic.until("evrd has been running") {
		if !strings.Contains(l, ":*status!") {
			continue
		}
		for _, c := range l {
			if c > unicode.MaxASCII {
				t.Errorf("non-ASCII %q in %q", c, l)
				break
			}
		}
		if _, text, ok := strings.Cut(l, " :"); ok && strings.HasPrefix(strings.Trim(text, "\x11"), "| ") {
			table = append(table, strings.Trim(text, "\x11"))
		}
	}
	if len(table) < 10 {
		t.Fatalf("no help table: %q", table)
	}
	// the description column starts at the same place on every row
	col := func(l string) int { // where the third "| " is
		at := -1
		for range 3 {
			at += 1 + strings.Index(l[at+1:], "| ")
		}
		return at
	}
	for _, l := range table[1:] {
		if col(l) != col(table[0]) {
			t.Errorf("misaligned:\n%s\n%s", table[0], l)
		}
	}
}

// The web chat breaks columns at a "|" with spaces around it, so only the
// last column (the description) may have one.
func TestHelpColumnsHaveNoSpacedPipes(t *testing.T) {
	for _, c := range statusCmds {
		if strings.Contains(c.name+" "+c.args+" ", " | ") {
			t.Errorf("%s: %q would split the web chat's table", c.name, c.args)
		}
	}
}
