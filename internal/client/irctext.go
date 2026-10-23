package client

import "strings"

// ircText is evrd's own text as sent over IRC: in plain ASCII, as some
// clients (mIRC on Windows) show "…", "—" and curly quotes as garbage. The
// web chat keeps them. Lines from the network pass through untouched.
func ircText(s string) string { return asciiPunct.Replace(s) }

var asciiPunct = strings.NewReplacer(
	"…", "...",
	"—", "-", "–", "-",
	"‘", "'", "’", "'",
	"“", `"`, "”", `"`,
	"·", "-",
	"→", "->",
)
