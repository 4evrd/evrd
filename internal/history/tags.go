package history

// Client-only tags kept with a line. Most client-only tags (+typing, a
// client's own experiments) only matter live, but replies and reactions
// shape the conversation: a client reading history later needs them to show
// threads. Those, and only those, are stored with the line.
//
// Msgids in them are evrd's: a reply names the line it answers by the ID
// evrd gave it (an ID, so it still matches after a restart), the same msgid
// clients saw live and get on replay. The upstream link translates the
// network's msgids before the line is stored; one it can't translate (a line
// from before evrd last started) is kept as the network gave it.

import (
	"sort"
	"strings"
)

// KindTagMsg is a stored TAGMSG: a reaction (or its undoing), which has
// tags but no text. Only TAGMSGs carrying a reaction are stored.
const KindTagMsg = "tagmsg"

// keptTags are the client-only tags history keeps.
var keptTags = map[string]bool{
	"+draft/reply":           true,
	"+reply":                 true,
	"+draft/react":           true,
	"+react":                 true,
	"+draft/unreact":         true,
	"+unreact":               true,
	"+draft/channel-context": true,
}

// KeptTag reports whether history stores the client-only tag k.
func KeptTag(k string) bool { return keptTags[k] }

// ReplyTag reports whether the tag k names another line by msgid.
func ReplyTag(k string) bool { return k == "+draft/reply" || k == "+reply" }

// StoredTags is the part of tags ("k=v;k2;…", wire form) history keeps, in
// a fixed order; "" when none is kept.
func StoredTags(tags string) string {
	if tags == "" {
		return ""
	}
	var out []string
	for _, kv := range strings.Split(tags, ";") {
		k, _, _ := strings.Cut(kv, "=")
		if keptTags[k] {
			out = append(out, kv)
		}
	}
	sort.Strings(out)
	return strings.Join(out, ";")
}

// Reaction reports whether tags carry a reaction or the undoing of one: a
// TAGMSG worth storing.
func Reaction(tags string) bool {
	for _, kv := range strings.Split(tags, ";") {
		switch k, _, _ := strings.Cut(kv, "="); k {
		case "+draft/react", "+react", "+draft/unreact", "+unreact":
			return true
		}
	}
	return false
}
