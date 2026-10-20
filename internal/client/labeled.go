package client

// labeled-response: a client labels a command and gets its reply back with
// the label. What evrd answers itself (*status, CHATHISTORY, BOUNCER,
// MARKREAD, echoes, errors) evrd labels: one line with the label, several in
// a labeled-response batch, none as ACK. A command evrd passes to the
// network goes with the label (prefixed with the session, as several
// clients share the link), and the network's labeled reply comes back to
// this client alone with its own label; the others get those lines plain.
// That needs the network to have labeled-response, so evrd offers it only
// while the link has it.

import (
	"fmt"
	"strings"

	"github.com/4evrd/evrd/internal/irc"
)

// labeled runs a labeled command and labels evrd's own answer.
func (se *session) labeled(label string, m irc.Message) {
	var lines []string
	se.capture, se.label, se.labelSent = &lines, label, false
	se.dispatch(m)
	se.capture, se.label = nil, ""
	if se.labelSent {
		// the network answers with the label; anything of evrd's goes plain
		for _, l := range lines {
			se.send(l)
		}
		return
	}
	switch len(lines) {
	case 0:
		se.send(fmt.Sprintf("@label=%s :%s ACK", label, serverName))
	case 1:
		se.send(withTag(lines[0], "label="+label))
	default:
		se.batches++
		ref := fmt.Sprint(se.batches)
		se.send(fmt.Sprintf("@label=%s :%s BATCH +%s labeled-response", label, serverName, ref))
		for _, l := range lines {
			se.send(withTag(l, "batch="+ref))
		}
		se.send(fmt.Sprintf(":%s BATCH -%s", serverName, ref))
	}
}

// withTag adds a tag to a line.
func withTag(line, tag string) string {
	if rest, ok := strings.CutPrefix(line, "@"); ok {
		return "@" + tag + ";" + rest
	}
	return "@" + tag + " " + line
}

// toNetwork sends a client's command to the network, with its label when
// it has one.
func (se *session) toNetwork(line string) {
	if se.label != "" && se.up.HasCap("labeled-response") {
		line = "@label=" + se.id + "~" + se.label + " " + line
		se.labelSent = true
	}
	se.up.SendRaw(line)
}

// ownLabel is a label the network echoed, as this client sent it, if the
// command was this client's.
func (se *session) ownLabel(v string) (string, bool) {
	rest, ok := strings.CutPrefix(v, se.id+"~")
	return rest, ok && se.caps["labeled-response"]
}
