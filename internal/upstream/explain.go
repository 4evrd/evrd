package upstream

// Why a connection failed, in words a user can act on, and the connection
// log that shows what happened.

import (
	"regexp"
	"strings"
	"time"

	"github.com/4evrd/evrd/internal/messages"
)

// LogEntry is one line of a network's connection log: a step evrd took, or
// a line the server sent while connecting or when something went wrong.
// What evrd sends is never logged (it can carry passwords).
type LogEntry struct {
	At   time.Time `json:"at"`
	From string    `json:"from"`          // "evrd" or "server"
	Text string    `json:"text"`          // in English, or as the server sent it
	Bad  bool      `json:"bad,omitempty"` // a failure
	// Msg is evrd's own entries in words, to show in the reader's language
	// (zero for the server's lines).
	Msg messages.Text `json:"msg,omitzero"`
}

// hints match a failure (evrd's description of it, or what the server said)
// to what it means and what to do about it. First match wins.
var (
	// bannedRE and throttledRE also set how long the server rests before
	// evrd tries it again (rest.go): a ban or a throttle only gets longer when
	// retried soon. "Not authorised" is a server with no I-line for this
	// address: a refusal too.
	bannedRE    = regexp.MustCompile(`(?i)\b(banned|k-?lined?|g-?lined?|z-?lined?|d-?lined?|akill|autokill|you are not welcome|access denied|not authori[sz]ed|no authori[sz]ation|unauthori[sz]ed connection)\b`)
	throttledRE = regexp.MustCompile(`(?i)(throttl|too fast|too many (connections|clients|users)|reconnect(ing)? too|wait .* (before|between)|connection limit|session limit|clones)`)
)

// hints match a failure to what it means and what to do about it: the
// text is irc.hint.<key> (internal/messages).
var hints = []struct {
	re  *regexp.Regexp
	key string
}{
	{bannedRE, "banned"},
	{throttledRE, "throttled"},
	// services and channels that need a login (see services.go)
	{regexp.MustCompile(`(?i)(must be (registered|identified|logged in)|need (to be )?(identified|logged in|a registered nick)|registered nick|\(\+r\)|registered-only)`), "needsLogin"},
	{regexp.MustCompile(`(?i)(isn't registered|is not (a )?registered|not a registered|no such account|account does not exist|unknown account)`), "notRegistered"},
	{regexp.MustCompile(`(?i)(suspended|frozen)`), "suspended"},
	{regexp.MustCompile(`(?i)(services didn't answer|no services nick|services are (down|unavailable))`), "servicesNoAnswer"},
	{regexp.MustCompile(`(?i)(sasl (authentication |login )?(is )?required|requires sasl|only .* sasl)`), "saslRequired"},
	{regexp.MustCompile(`(?i)hung up during the TLS handshake`), "hungUp"},
	{regexp.MustCompile(`(?i)(doesn't offer SASL|refused the SASL capability)`), "noSASL"},
	{regexp.MustCompile(`(?i)(password (is )?(incorrect|mismatch|required|wrong)|(bad|invalid|incorrect|wrong) password|invalid (account )?credentials|sasl authentication failed|authentication failed|login (unsuccessful|failed))`), "password"},
	{regexp.MustCompile(`(?i)(didn't answer with TLS|plain-text port)`), "notTLS"},
	{regexp.MustCompile(`(?i)speaks TLS on this port`), "speaksTLS"},
	{regexp.MustCompile(`(?i)(certificate|x509|unknown authority|not trusted)`), "certificate"},
	{regexp.MustCompile(`(?i)(can't resolve|no such host)`), "noHost"},
	{regexp.MustCompile(`(?i)connection refused`), "refused"},
	{regexp.MustCompile(`(?i)(timed out|can't connect|no route|unreachable|i/o timeout)`), "noAnswer"},
	{regexp.MustCompile(`(?i)(nickname is already in use|erroneous nickname|nick .* in use|no free nick)`), "nick"},
	{regexp.MustCompile(`(?i)ping timeout`), "pingTimeout"},
	{regexp.MustCompile(`(?i)(closing link|connection closed by server|closed the connection)`), "closed"},
}

// Hint says what a failure means and what to do, in English, or "" when
// it's unclear.
func Hint(failure string) string { return HintText(failure).English() }

// HintText is Hint in words, to put in the reader's language (zero when
// it's unclear).
func HintText(failure string) messages.Text {
	for _, h := range hints {
		if h.re.MatchString(failure) {
			return messages.T("hint." + h.key)
		}
	}
	return messages.Text{}
}

// logged reports whether a server line belongs in the connection log:
// everything until registration, then only what explains trouble.
func logged(registered bool, command, prefix string, params []string) bool {
	switch command {
	case "PING", "PONG":
		return false
	case "ERROR", "KILL":
		return true
	}
	if !registered {
		return true
	}
	switch command {
	case "432", "433", "436", "437", "463", "464", "465", "466", "477", "484", "902", "904", "905", "906", "908":
		return true // nick, ban, password, SASL and +r channel trouble
	case "NOTICE":
		// from the server itself (no nick!user@host), not from a person
		return !strings.Contains(prefix, "!")
	}
	return false
}
