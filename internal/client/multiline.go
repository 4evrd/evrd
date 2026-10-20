package client

// draft/multiline, offered while the network has it (with its limits): a
// client sends a message of several lines as a batch, which goes to the
// network as one and is saved a line at a time (a line marked
// draft/multiline-concat joins the one before). The network's multiline
// batches reach clients that take them; the others get the lines.
//
// https://ircv3.net/specs/extensions/multiline

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/4evrd/evrd/internal/irc"
)

// multiline is a client's open draft/multiline batch.
type multiline struct {
	target, command, tags string
	lines                 []string
	concat                []bool
	bytes                 int
}

// clientBatch takes a client's BATCH +ref draft/multiline <target> and
// BATCH -ref. It reports whether it was one of those.
func (se *session) clientBatch(m irc.Message) bool {
	if !se.caps["draft/multiline"] || len(m.Params) == 0 {
		return false
	}
	ref := m.Params[0]
	switch {
	case strings.HasPrefix(ref, "+") && len(m.Params) >= 3 && m.Params[1] == "draft/multiline":
		if se.mlines == nil {
			se.mlines = map[string]*multiline{}
		}
		se.mlines[ref[1:]] = &multiline{target: m.Params[2], tags: se.clientTags(m)}
		return true
	case strings.HasPrefix(ref, "-"):
		b := se.mlines[ref[1:]]
		if b == nil {
			return false
		}
		delete(se.mlines, ref[1:])
		se.sendMultiline(b)
		return true
	}
	return false
}

// inMultiline adds a PRIVMSG or NOTICE to its open batch, if it's in one.
func (se *session) inMultiline(m irc.Message) bool {
	b := se.mlines[m.Tags["batch"]]
	if b == nil {
		return false
	}
	target, text := m.Params[0], m.Params[len(m.Params)-1]
	switch {
	case !strings.EqualFold(target, b.target):
		b.command = "!" + target // fails when the batch ends
	case b.command == "" || b.command == m.Command:
		b.command = m.Command
	default:
		b.command = "!"
	}
	_, concat := m.Tags["draft/multiline-concat"]
	b.lines, b.concat = append(b.lines, text), append(b.concat, concat)
	b.bytes += len(text)
	if !concat && len(b.lines) > 1 {
		b.bytes++ // the line break
	}
	return true
}

// multilineLimits are the network's max-bytes and max-lines (0: none).
func (se *session) multilineLimits() (maxBytes, maxLines int) {
	for _, kv := range strings.Split(se.up.CapValue("draft/multiline"), ",") {
		k, v, _ := strings.Cut(kv, "=")
		n, _ := strconv.Atoi(v)
		switch k {
		case "max-bytes":
			maxBytes = n
		case "max-lines":
			maxLines = n
		}
	}
	return
}

// sendMultiline sends a finished batch to the network, echoing it to the
// client with echo-message.
func (se *session) sendMultiline(b *multiline) {
	fail := func(code string, args ...string) {
		se.send(fmt.Sprintf(":%s FAIL BATCH %s %s", serverName, code, strings.Join(args, " ")))
	}
	maxBytes, maxLines := se.multilineLimits()
	switch {
	case strings.HasPrefix(b.command, "!") && len(b.command) > 1:
		fail("MULTILINE_INVALID_TARGET", b.target, b.command[1:], ":Every line goes to the batch's target")
		return
	case b.command == "!" || len(b.lines) == 0 || b.concat[0]:
		fail("MULTILINE_INVALID", ":Invalid multiline batch")
		return
	case maxBytes > 0 && b.bytes > maxBytes:
		fail("MULTILINE_MAX_BYTES", strconv.Itoa(maxBytes), ":Multiline batch too long")
		return
	case maxLines > 0 && len(b.lines) > maxLines:
		fail("MULTILINE_MAX_LINES", strconv.Itoa(maxLines), ":Too many lines in the batch")
		return
	}
	ids := se.up.SendMultiline(se.id, b.tags, b.command, b.target, b.lines, b.concat)
	if !se.caps["echo-message"] {
		return
	}
	first := ""
	if len(ids) > 0 {
		first = se.tags(ids[0], "", b.tags)
	}
	se.batches++
	ref := strconv.Itoa(se.batches)
	se.send(fmt.Sprintf("%s:%s!%s@%s BATCH +%s draft/multiline %s", first, se.unick, se.unick, serverName, ref, b.target))
	for i, l := range b.lines {
		tag := "@batch=" + ref
		if b.concat[i] {
			tag += ";draft/multiline-concat"
		}
		se.send(fmt.Sprintf("%s :%s!%s@%s %s %s :%s", tag, se.unick, se.unick, serverName, b.command, b.target, l))
	}
	se.send(fmt.Sprintf(":%s BATCH -%s", serverName, ref))
}
