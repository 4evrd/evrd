package client

// WHO from evrd's own state (upstream.WhoState): a client's WHO about a
// channel evrd is in, or about a nick in one, is answered here instead of
// asking the network (#96) — the plain form and WHOX. A mask, a filter
// (WHO #chan o), or a channel evrd doesn't know whole and current yet goes
// to the network as before.

import (
	"fmt"
	"strings"

	"github.com/4evrd/evrd/internal/upstream"
)

// answerWho answers WHO #chan, WHO #chan %fields[,token] or WHO nick from
// evrd's state, reporting whether it did.
func (se *session) answerWho(params []string) bool {
	if len(params) == 0 || len(params) > 2 || strings.ContainsAny(params[0], ",*?") {
		return false
	}
	fields, token := "", ""
	if len(params) == 2 {
		spec, ok := strings.CutPrefix(params[1], "%")
		if !ok {
			return false // a filter: the network's
		}
		fields, token, _ = strings.Cut(spec, ",")
		if fields == "" || strings.Trim(fields, whoxFields) != "" {
			return false
		}
	}
	mask := params[0]
	var who []upstream.Who
	var channel string
	var ok, accounts bool
	if strings.ContainsAny(mask[:1], "#&+!") {
		who, ok, accounts = se.up.WhoState(mask)
		if st, in := se.up.ChannelState(mask); in {
			channel = st.Name
		}
	} else {
		var w upstream.Who
		w, channel, ok, accounts = se.up.WhoNick(mask)
		who = []upstream.Who{w}
	}
	if !ok || strings.Contains(fields, "a") && !accounts {
		return false
	}
	for _, w := range who {
		if fields == "" {
			se.reply("352", channel, w.User, w.Host, w.Server, w.Nick, se.memberFlags(w), fmt.Sprintf(":%d %s", w.Hops, w.Realname))
		} else {
			se.reply("354", whoxLine(fields, token, channel, w, se.memberFlags(w))...)
		}
	}
	se.reply("315", mask, ":End of /WHO list")
	return true
}

// whoxFields are the WHOX fields evrd can answer, in the order a reply
// gives them.
const whoxFields = "tcuihsnfdlaor"

// whoxLine is one RPL_WHOSPCRPL's fields in WHOX order, the realname last
// as a trailing parameter. What evrd can't know is given as servers give
// what they hide: the IP as 255.255.255.255, idle as 0, op level as n/a.
func whoxLine(fields, token, channel string, w upstream.Who, flags string) []string {
	var out []string
	for _, f := range whoxFields {
		if !strings.ContainsRune(fields, f) {
			continue
		}
		switch f {
		case 't':
			if token != "" {
				out = append(out, token)
			}
		case 'c':
			out = append(out, channel)
		case 'u':
			out = append(out, w.User)
		case 'i':
			out = append(out, "255.255.255.255")
		case 'h':
			out = append(out, w.Host)
		case 's':
			out = append(out, w.Server)
		case 'n':
			out = append(out, w.Nick)
		case 'f':
			out = append(out, flags)
		case 'd':
			out = append(out, fmt.Sprint(w.Hops))
		case 'l':
			out = append(out, "0")
		case 'a':
			out = append(out, orElse(w.Account, "0"))
		case 'o':
			out = append(out, "n/a")
		case 'r':
			out = append(out, ":"+w.Realname)
		}
	}
	return out
}

// memberFlags is WHO's flags for a member, fitted to the client: H or G, * for
// an operator, then its status prefixes (every one with multi-prefix).
func (se *session) memberFlags(w upstream.Who) string {
	f := "H"
	if w.Away {
		f = "G"
	}
	if w.Oper {
		f += "*"
	}
	if se.caps["multi-prefix"] && w.Prefixes != "" {
		return f + w.Prefixes
	}
	return f + w.Prefix
}
