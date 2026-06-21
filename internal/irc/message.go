// Package irc provides minimal IRC protocol message parsing and encoding.
//
// This is a small, dependency-free starting point. Once a Go toolchain is wired
// into CI it's worth swapping to github.com/ergochat/irc-go for complete IRCv3
// tag/CAP handling.
package irc

import "strings"

// Message is a parsed IRC protocol message.
type Message struct {
	Tags    map[string]string
	Prefix  string
	Command string
	Params  []string
}

// Parse parses a single IRC line (trailing CR/LF is tolerated).
func Parse(line string) Message {
	var m Message
	line = strings.TrimRight(line, "\r\n")

	if strings.HasPrefix(line, "@") {
		var tags string
		tags, line, _ = strings.Cut(line[1:], " ")
		m.Tags = parseTags(tags)
		line = strings.TrimLeft(line, " ")
	}
	if strings.HasPrefix(line, ":") {
		m.Prefix, line, _ = strings.Cut(line[1:], " ")
		line = strings.TrimLeft(line, " ")
	}

	var trailing string
	hasTrailing := false
	if i := strings.Index(line, " :"); i >= 0 {
		trailing = line[i+2:]
		hasTrailing = true
		line = line[:i]
	}

	fields := strings.Fields(line)
	if len(fields) > 0 {
		m.Command = strings.ToUpper(fields[0])
		m.Params = fields[1:]
	}
	if hasTrailing {
		m.Params = append(m.Params, trailing)
	}
	return m
}

func parseTags(s string) map[string]string {
	tags := map[string]string{}
	for _, kv := range strings.Split(s, ";") {
		if kv == "" {
			continue
		}
		k, v, _ := strings.Cut(kv, "=")
		tags[k] = v
	}
	return tags
}

// String encodes the message back to an IRC line (without trailing CRLF).
func (m Message) String() string {
	var b strings.Builder
	if m.Prefix != "" {
		b.WriteString(":")
		b.WriteString(m.Prefix)
		b.WriteString(" ")
	}
	b.WriteString(m.Command)
	for i, p := range m.Params {
		last := i == len(m.Params)-1
		if last && (p == "" || strings.ContainsAny(p, " ") || strings.HasPrefix(p, ":")) {
			b.WriteString(" :")
			b.WriteString(p)
		} else {
			b.WriteString(" ")
			b.WriteString(p)
		}
	}
	return b.String()
}
