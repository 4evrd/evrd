package modules

import (
	"slices"
	"testing"
)

func TestAutoAttach(t *testing.T) {
	f := newFake()
	for _, c := range []string{"#home", "#work", "#news", "#quiet"} {
		f.joined[c] = true
	}
	f.detached["#home"], f.detached["#work"], f.detached["#news"] = true, true, true
	m := start(t, f, "autoattach", map[string]string{"rules": "#home\n#w?rk *deploy* *!*@ops.example.org\n!#news *spam*\n#n*\n#quiet"})

	m.Line(line(":bob!b@h PRIVMSG #home :anything at all"))
	if !slices.Equal(f.attachedChans, []string{"#home"}) {
		t.Fatalf("attached %q", f.attachedChans)
	}
	if len(f.logs) != 1 || f.logs[0] != "autoattach: bob said something in #home that matches; attaching it" {
		t.Fatalf("logged %q", f.logs)
	}

	// the search and the host must both match
	f.attachedChans = nil
	m.Line(line(":bob!b@elsewhere PRIVMSG #work :deploy now"))
	m.Line(line(":bob!b@ops.example.org PRIVMSG #work :lunch?"))
	if len(f.attachedChans) != 0 {
		t.Fatalf("attached %q", f.attachedChans)
	}
	m.Line(line(":bob!b@OPS.example.org NOTICE #work :\x01ACTION starts the DEPLOY\x01"))
	if !slices.Equal(f.attachedChans, []string{"#work"}) {
		t.Fatalf("attached %q", f.attachedChans)
	}

	// a ! rule wins over a match
	f.attachedChans = nil
	m.Line(line(":eve!e@h PRIVMSG #news :buy spam"))
	if len(f.attachedChans) != 0 {
		t.Fatalf("attached %q", f.attachedChans)
	}
	m.Line(line(":carol!c@h PRIVMSG #news :headline"))
	if !slices.Equal(f.attachedChans, []string{"#news"}) {
		t.Fatalf("attached %q", f.attachedChans)
	}

	// not a channel that isn't detached, nor our own lines, nor PMs
	f.attachedChans = nil
	f.detached["#home"] = true
	for _, l := range []string{
		":bob!b@h PRIVMSG #quiet :hi",
		":alice!a@h PRIVMSG #home :me, from another client",
		":bob!b@h PRIVMSG alice :#home",
		":bob!b@h JOIN #home",
	} {
		m.Line(line(l))
	}
	if len(f.attachedChans) != 0 {
		t.Fatalf("attached %q", f.attachedChans)
	}
}

func TestAutoAttachNoRules(t *testing.T) {
	f := newFake()
	f.joined["#home"], f.detached["#home"] = true, true
	m := start(t, f, "autoattach", nil)
	m.Line(line(":bob!b@h PRIVMSG #home :hi"))
	if len(f.attachedChans) != 0 {
		t.Fatal("attached with no rules")
	}
}
