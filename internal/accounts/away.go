package accounts

import (
	"strings"
	"unicode/utf8"
)

// MaxAwayMessage is the longest away message a user may set, in characters.
const MaxAwayMessage = 300

// CleanAwayMessage trims an away message (store.User.AwayMessage), reporting
// whether it's one: a single line of up to MaxAwayMessage characters.
func CleanAwayMessage(msg string) (string, bool) {
	msg = strings.TrimSpace(msg)
	if strings.ContainsAny(msg, "\r\n\x00") || utf8.RuneCountInString(msg) > MaxAwayMessage {
		return "", false
	}
	return msg, true
}
