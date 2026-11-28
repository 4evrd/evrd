package zncimport

// A ZNC user's settings that go to its evrd account, from the modules it
// loads: simple_away (auto-away, its delay and reason) and clientnotify.
// clientnotify's options and the notes module's notes are in ZNC's moddata,
// not the config, so they don't come.

import (
	"strconv"
	"strings"
)

// Settings are a ZNC user's account settings.
type Settings struct {
	// AutoAway is simple_away loaded (for the user, or one of its
	// networks); AutoAwayDelay its timer in seconds; AwayMessage its
	// reason ("": evrd's).
	AutoAway      bool
	AutoAwayDelay *int
	AwayMessage   string
	// ClientNotify is clientnotify loaded.
	ClientNotify bool
}

// simpleAwayDelay is simple_away's timer when the config doesn't set one.
const simpleAwayDelay = 60

// userSettings reads a <User>'s settings: its own LoadModule lines, then
// its networks' (simple_away is a network module too).
func userSettings(u *section) Settings {
	var s Settings
	lines := u.all("LoadModule")
	for _, n := range u.kids("Network") {
		lines = append(lines, n.all("LoadModule")...)
	}
	for _, line := range lines {
		name, args, _ := strings.Cut(strings.TrimSpace(line), " ")
		switch strings.ToLower(name) {
		case "simple_away":
			if !s.AutoAway { // the first one wins
				s.AutoAway = true
				s.AutoAwayDelay, s.AwayMessage = simpleAway(args)
			}
		case "clientnotify":
			s.ClientNotify = true
		}
	}
	return s
}

// simpleAway reads simple_away's arguments: [-notimer | -timer N] [reason].
func simpleAway(args string) (*int, string) {
	delay := simpleAwayDelay
	f := strings.Fields(args)
	switch {
	case len(f) > 0 && strings.EqualFold(f[0], "-notimer"):
		delay, f = 0, f[1:]
	case len(f) > 1 && strings.EqualFold(f[0], "-timer"):
		if n, err := strconv.Atoi(f[1]); err == nil && n >= 0 {
			delay = n
		}
		f = f[2:]
	}
	return &delay, strings.Join(f, " ")
}
