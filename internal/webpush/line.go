package webpush

import (
	"strings"
	"time"
	"unicode/utf8"

	"github.com/4evrd/evrd/internal/history"
)

// timeFormat is server-time's.
const timeFormat = "2006-01-02T15:04:05.000Z"

// pingMessage is what a test push carries, as Ergo's: a line clients
// ignore.
var pingMessage = []byte("PING webpush")

// MessageLine is the push payload for m, a line that arrived on network
// for the user (me, their nick there): the IRC line as an attached client
// would have had it, with its @time, @msgid and client-only tags (a reply's
// +draft/reply…), from nick!nick@<network> as evrd's replayed lines are.
// A private message is to me. It fits MaxPayload: the client-only tags go
// first, then the text is cut short.
func MessageLine(network, me string, m history.Message) []byte {
	target := m.Target
	if !isChannel(target) && me != "" {
		target = me
	}
	text := m.Text
	if m.Kind == "action" {
		text = "\x01ACTION " + text + "\x01"
	}
	cmd := "PRIVMSG"
	if m.Kind == "notice" {
		cmd = "NOTICE"
	}
	sender := orElse(m.Sender, "*")
	host := strings.ReplaceAll(orElse(network, "irc"), " ", "-")
	tags := "time=" + m.Time.UTC().Format(timeFormat) + ";msgid=" + m.ID().String()
	head := ":" + sender + "!" + sender + "@" + host + " " + cmd + " " + target + " :"
	line := "@" + tags
	if m.Tags != "" {
		line += ";" + m.Tags
	}
	line += " " + head + text
	if len(line) <= MaxPayload {
		return []byte(line)
	}
	line = "@" + tags + " " + head
	return []byte(line + cut(text, MaxPayload-len(line)))
}

// markReadLine is the push that tells the user's devices target is read up
// to at elsewhere (Ergo's and soju's), so they can take its notifications
// away.
func markReadLine(server, target string, at, now time.Time) []byte {
	return []byte("@time=" + now.UTC().Format(timeFormat) + " :" + server + " MARKREAD " + target +
		" timestamp=" + at.UTC().Format(timeFormat))
}

// cut is s in at most n bytes, not splitting a character.
func cut(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

func isChannel(s string) bool { return s != "" && strings.ContainsRune("#&+!", rune(s[0])) }

func orElse(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
