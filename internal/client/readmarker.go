package client

// draft/read-marker: how far each buffer is read, shared by the user's
// clients and the web app (they're the same read markers). MARKREAD asks or
// moves a marker; a client hears when one moves elsewhere, and after it
// joins a channel.
//
// https://ircv3.net/specs/extensions/read-marker

import (
	"fmt"
	"strings"
	"time"
)

// markRead answers MARKREAD <target> [timestamp=…].
func (se *session) markRead(p []string) {
	fail := func(code string, args ...string) {
		se.send(fmt.Sprintf(":%s FAIL MARKREAD %s %s", serverName, code, strings.Join(args, " ")))
	}
	if len(p) == 0 {
		fail("NEED_MORE_PARAMS", ":Missing parameters")
		return
	}
	target := p[0]
	if len(p) == 1 {
		markers, err := se.srv.b.ReadMarkers(se.net.ID)
		if err != nil {
			fail("INTERNAL_ERROR", target, ":The read timestamp could not be returned")
			return
		}
		se.sendMark(target, markers[strings.ToLower(target)])
		return
	}
	v, ok := strings.CutPrefix(p[1], "timestamp=")
	at, err := time.Parse(time.RFC3339Nano, v)
	if !ok || err != nil {
		fail("INVALID_PARAMS", p[1], ":Invalid parameters")
		return
	}
	now, err := se.srv.b.MarkReadAt(se.net.ID, target, at)
	if err != nil {
		se.srv.log.Warn("markread", "network", se.net.ID, "target", target, "err", err)
		fail("INTERNAL_ERROR", target, ":The read timestamp could not be set")
		return
	}
	se.sendMark(target, now)
}

// sendMark tells the client how far target is read (* for not known).
func (se *session) sendMark(target string, at time.Time) {
	ts := "*"
	if !at.IsZero() {
		ts = "timestamp=" + at.UTC().Format(timeFormat)
	}
	se.send(fmt.Sprintf(":%s MARKREAD %s %s", serverName, target, ts))
	if se.marks != nil {
		se.marks[strings.ToLower(target)] = at
	}
}

// joinedMark sends a channel's marker after its JOIN, as the spec asks.
func (se *session) joinedMark(ch string) {
	if !se.caps["draft/read-marker"] {
		return
	}
	markers, _ := se.srv.b.ReadMarkers(se.net.ID)
	se.sendMark(ch, markers[strings.ToLower(ch)])
}

// startMarks is what the client knows as it attaches to a network: the
// markers as they are now. Taken before its channels' JOINs, so a marker
// moved while those and the backlog go out still reaches it (syncMarks).
func (se *session) startMarks() {
	se.marks = nil
	if !se.caps["draft/read-marker"] || se.up == nil {
		return
	}
	markers, err := se.srv.b.ReadMarkers(se.net.ID)
	if err != nil {
		return // syncMarks starts from what it finds
	}
	se.marks = markers
}

// syncMarks tells the client about markers moved elsewhere (another client,
// the web app, the user speaking) since it last heard.
func (se *session) syncMarks() {
	if !se.caps["draft/read-marker"] || se.up == nil {
		return
	}
	markers, err := se.srv.b.ReadMarkers(se.net.ID)
	if err != nil {
		return
	}
	if se.marks == nil {
		se.marks = markers // a start: what it knows from now on
		return
	}
	for _, k := range sortedKeys(markers) {
		if !markers[k].Equal(se.marks[k]) {
			se.sendMark(k, markers[k])
		}
	}
}
