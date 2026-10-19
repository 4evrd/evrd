package upstream

// Why a connection failed, in words a user can act on, and the connection
// log that shows what happened.

import (
	"regexp"
	"strings"
	"time"
)

// LogEntry is one line of a network's connection log: a step evrd took, or
// a line the server sent while connecting or when something went wrong.
// What evrd sends is never logged (it can carry passwords).
type LogEntry struct {
	At   time.Time `json:"at"`
	From string    `json:"from"` // "evrd" or "server"
	Text string    `json:"text"`
	Bad  bool      `json:"bad,omitempty"` // a failure
}

// hints match a failure (evrd's description of it, or what the server said)
// to what it means and what to do about it. First match wins.
var hints = []struct {
	re   *regexp.Regexp
	hint string
}{
	{regexp.MustCompile(`(?i)\b(banned|k-?lined?|g-?lined?|z-?lined?|d-?lined?|akill|autokill|you are not welcome|access denied)\b`),
		"The server refuses connections from this bouncer's address. Try another server of the network — some only ban per server — or ask the network's staff why; the server's own words are in the connection log."},
	{regexp.MustCompile(`(?i)(throttl|too fast|too many (connections|clients|users)|reconnect(ing)? too|wait .* (before|between)|connection limit|session limit|clones)`),
		"The server limits how often, or how many, connections come from one address — other users of this bouncer on the same network count too. evrd keeps retrying, more slowly each time."},
	{regexp.MustCompile(`(?i)(password (incorrect|mismatch|required)|bad password|invalid password|sasl authentication failed|authentication failed|login unsuccessful)`),
		"The password was refused. Check the account and password under Authentication (and a server password, if the server needs one)."},
	{regexp.MustCompile(`(?i)(didn't answer with TLS|plain-text port)`),
		"This port doesn't speak TLS. Use the server's TLS port (often 6697, sometimes 9999 or 7000), or switch TLS off for this server."},
	{regexp.MustCompile(`(?i)speaks TLS on this port`),
		"The server expects TLS on this port. Switch TLS on for this server."},
	{regexp.MustCompile(`(?i)(certificate|x509|unknown authority|not trusted)`),
		"The server's certificate couldn't be verified. If it's self-signed and you know it's the right server, trust its fingerprint; otherwise check the host name."},
	{regexp.MustCompile(`(?i)(can't resolve|no such host)`),
		"That host name doesn't exist. Check its spelling, or the network's list of servers."},
	{regexp.MustCompile(`(?i)connection refused`),
		"The server's address answered, but nothing listens on that port. Check the port (and that the server is up)."},
	{regexp.MustCompile(`(?i)(timed out|can't connect|no route|unreachable|i/o timeout)`),
		"The server didn't answer. It may be down, or a firewall is in the way — of the server, or of this bouncer's outgoing connections."},
	{regexp.MustCompile(`(?i)(nickname is already in use|erroneous nickname|nick .* in use|no free nick)`),
		"The nick is taken or not allowed. Set an alternate nick, or another nick, for this network."},
	{regexp.MustCompile(`(?i)ping timeout`),
		"The connection went quiet and was dropped. Usually a network hiccup; evrd reconnects by itself."},
	{regexp.MustCompile(`(?i)(closing link|connection closed by server|closed the connection)`),
		"The server closed the connection. Its last words, if any, are in the connection log."},
}

// Hint says what a failure means and what to do, or "" when it's unclear.
func Hint(failure string) string {
	for _, h := range hints {
		if h.re.MatchString(failure) {
			return h.hint
		}
	}
	return ""
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
	case "432", "433", "436", "437", "463", "464", "465", "466", "484", "902", "904", "905", "906", "908":
		return true // nick, ban, password and SASL trouble
	case "NOTICE":
		// from the server itself (no nick!user@host), not from a person
		return !strings.Contains(prefix, "!")
	}
	return false
}
