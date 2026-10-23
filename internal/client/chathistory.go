package client

// IRCv3 chathistory (draft/chathistory): clients page through a buffer's
// history themselves instead of taking evrd's playback on attach.
//
//	CHATHISTORY LATEST  <target> <* | ref> <limit>
//	CHATHISTORY BEFORE  <target> <ref> <limit>
//	CHATHISTORY AFTER   <target> <ref> <limit>
//	CHATHISTORY AROUND  <target> <ref> <limit>
//	CHATHISTORY BETWEEN <target> <ref> <ref> <limit>
//	CHATHISTORY TARGETS <timestamp> <timestamp> <limit>
//
// A ref is msgid=<id> (a line's history.ID) or timestamp=<RFC 3339>. A
// timestamp names its whole millisecond, the precision server-time carries:
// BEFORE the time a line was shown with leaves that line out, and so does
// AFTER it.

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/4evrd/evrd/internal/history"
)

// chathistoryMax is the most lines one request returns (ISUPPORT
// CHATHISTORY). Clients ask for 50–100 at a time; 1000 lets one fill a long
// scrollback in a request while keeping each read of the store bounded.
const chathistoryMax = 1000

// ref is a parsed msgid= or timestamp= reference.
type ref struct {
	id history.ID // for a timestamp, Seq is 0
	ts bool
}

func parseRef(s string) (ref, string) {
	kind, v, _ := strings.Cut(s, "=")
	switch kind {
	case "msgid":
		if id, ok := history.ParseID(v); ok {
			return ref{id: id}, ""
		}
		return ref{}, "INVALID_PARAMS"
	case "timestamp":
		if t, err := time.Parse(time.RFC3339Nano, v); err == nil {
			return ref{id: history.ID{Nanos: t.UnixNano()}, ts: true}, ""
		}
		return ref{}, "INVALID_PARAMS"
	}
	return ref{}, "INVALID_MSGREFTYPE"
}

// after bounds lines after r: past a timestamp's whole millisecond.
func (r ref) after() history.ID {
	if r.ts {
		return history.ID{Nanos: r.id.Nanos + int64(time.Millisecond) - 1, Seq: history.MaxSeq}
	}
	return r.id
}

// before bounds lines before r.
func (r ref) before() history.ID {
	if r.ts {
		return history.ID{Nanos: r.id.Nanos}
	}
	return r.id
}

// from bounds lines from r on, r's own line(s) included.
func (r ref) from() history.ID {
	if r.ts || r.id.Seq == 0 {
		return history.ID{Nanos: r.id.Nanos - 1, Seq: history.MaxSeq}
	}
	return history.ID{Nanos: r.id.Nanos, Seq: r.id.Seq - 1}
}

// chathistory answers a CHATHISTORY command.
func (se *session) chathistory(p []string) {
	fail := func(code string, args ...string) {
		se.send(fmt.Sprintf(":%s FAIL CHATHISTORY %s %s", serverName, code, strings.Join(args, " ")))
	}
	if len(p) == 0 {
		fail("INVALID_PARAMS", "*", ":Insufficient parameters")
		return
	}
	sub := strings.ToUpper(p[0])
	want := map[string]int{"LATEST": 4, "BEFORE": 4, "AFTER": 4, "AROUND": 4, "BETWEEN": 5, "TARGETS": 4}[sub]
	switch {
	case want == 0:
		fail("INVALID_PARAMS", p[0], ":Unknown command")
		return
	case len(p) < want:
		fail("INVALID_PARAMS", sub, ":Insufficient parameters")
		return
	case len(p) > want:
		fail("INVALID_PARAMS", sub, ":Too many parameters")
		return
	}
	limit, err := strconv.Atoi(p[want-1])
	if err != nil || limit < 1 {
		fail("INVALID_PARAMS", sub, p[want-1], ":Invalid limit")
		return
	}
	limit = min(limit, chathistoryMax)
	// the refs: every parameter between the subcommand (and target) and the limit
	first, target := 2, p[1]
	if sub == "TARGETS" {
		first, target = 1, "*"
	}
	var refs []ref
	for _, s := range p[first : want-1] {
		if sub == "LATEST" && s == "*" {
			continue
		}
		r, bad := parseRef(s)
		if bad == "" && sub == "TARGETS" && !r.ts {
			bad = "INVALID_PARAMS"
		}
		switch bad {
		case "INVALID_PARAMS":
			fail(bad, sub, s, ":Invalid timestamp or msgid")
			return
		case "INVALID_MSGREFTYPE":
			fail(bad, sub, target, ":Unknown message reference type")
			return
		}
		refs = append(refs, r)
	}
	if sub == "TARGETS" {
		se.chathistoryTargets(refs[0], refs[1], limit)
		return
	}

	if target == "" || strings.ContainsAny(target, ",*?") {
		fail("INVALID_TARGET", sub, target, ":Messages could not be retrieved")
		return
	}
	var msgs []history.Message
	switch sub {
	case "LATEST":
		q := history.Query{Limit: limit, Latest: true}
		if len(refs) == 1 {
			q.After = refs[0].after()
		}
		msgs, err = se.lines(target, q)
	case "BEFORE":
		msgs, err = se.lines(target, history.Query{Before: refs[0].before(), Limit: limit, Latest: true})
	case "AFTER":
		msgs, err = se.lines(target, history.Query{After: refs[0].after(), Limit: limit})
	case "AROUND":
		// half before, the rest from the ref on (its own line first)
		msgs, err = se.lines(target, history.Query{Before: refs[0].before(), Limit: limit / 2, Latest: true})
		if err == nil {
			var later []history.Message
			later, err = se.lines(target, history.Query{After: refs[0].from(), Limit: limit - len(msgs)})
			msgs = append(msgs, later...)
		}
	case "BETWEEN":
		// counted from the first ref, which may be the later one
		a, b := refs[0], refs[1]
		if a.id.Before(b.id) {
			msgs, err = se.lines(target, history.Query{After: a.after(), Before: b.before(), Limit: limit})
		} else {
			msgs, err = se.lines(target, history.Query{After: b.after(), Before: a.before(), Limit: limit, Latest: true})
		}
	}
	if err != nil {
		se.srv.log.Warn("chathistory", "network", se.net.ID, "target", target, "err", err)
		fail("MESSAGE_ERROR", sub, target, ":Messages could not be retrieved")
		return
	}
	se.sendHistory(target, msgs)
}

// lines reads a slice of a buffer for CHATHISTORY. Channel events only go to
// a client with draft/event-playback.
func (se *session) lines(target string, q history.Query) ([]history.Message, error) {
	return se.readLines(target, q, se.caps["draft/event-playback"])
}

// readLines reads a slice of a buffer: chat lines, and channel events if
// events is set. Skipped events don't use up the limit; reading goes on.
func (se *session) readLines(target string, q history.Query, events bool) ([]history.Message, error) {
	h := se.srv.b.History()
	need := q.Limit
	var out []history.Message
	for need > 0 {
		q.Limit = max(need, 50)
		got, err := h.Range(se.net.ID, target, q)
		if err != nil {
			return nil, err
		}
		var kept []history.Message
		for _, m := range got {
			if events || isChat(m.Kind) {
				kept = append(kept, m)
			}
		}
		if len(kept) > need {
			if q.Latest {
				kept = kept[len(kept)-need:]
			} else {
				kept = kept[:need]
			}
		}
		if q.Latest {
			out = append(kept, out...)
		} else {
			out = append(out, kept...)
		}
		need -= len(kept)
		if len(got) < q.Limit {
			break // read it all
		}
		if q.Latest {
			q.Before = got[0].ID()
		} else {
			q.After = got[len(got)-1].ID()
		}
	}
	return out, nil
}

// chathistoryTargets answers TARGETS: the buffers with lines between two
// times, by their latest line's time, counted from the first.
func (se *session) chathistoryTargets(a, b ref, limit int) {
	var targets []history.Target
	var err error
	if a.id.Before(b.id) {
		targets, err = history.Targets(se.srv.b.History(), se.net.ID, a.after(), b.before(), limit, false)
	} else {
		targets, err = history.Targets(se.srv.b.History(), se.net.ID, b.after(), a.before(), limit, true)
	}
	if err != nil {
		se.srv.log.Warn("chathistory targets", "network", se.net.ID, "err", err)
		se.send(fmt.Sprintf(":%s FAIL CHATHISTORY MESSAGE_ERROR TARGETS * :Messages could not be retrieved", serverName))
		return
	}
	batch := se.openBatch("draft/chathistory-targets")
	for _, t := range targets {
		tags := ""
		if batch != "" {
			tags = "@batch=" + batch + " "
		}
		se.send(fmt.Sprintf("%s:%s CHATHISTORY TARGETS %s %s", tags, serverName, t.Name, t.Latest.UTC().Format(timeFormat)))
	}
	se.closeBatch(batch)
}
