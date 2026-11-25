package client

import (
	"strings"
	"testing"
	"time"
)

// namesFor returns the nicks in the 353s for ch among lines.
func namesFor(lines []string, ch string) []string {
	var nicks []string
	for _, l := range lines {
		f := strings.Fields(l)
		if len(f) >= 6 && f[1] == "353" && strings.EqualFold(f[4], ch) {
			_, list, _ := strings.Cut(l, " :")
			if i := strings.Index(list, " :"); i >= 0 {
				list = list[i+2:]
			}
			nicks = append(nicks, strings.Fields(list)...)
		}
	}
	return nicks
}

// Kicked, a client (mIRC's rejoin-on-kick) joins straight back: it gets
// its JOIN and the channel's names, itself among them, as for any join
// (#175).
var mircCaps = []string{"account-notify", "away-notify", "batch", "chghost", "echo-message", "extended-join", "invite-notify", "message-tags", "multi-prefix", "server-time", "setname", "userhost-in-names", "labeled-response"}

func TestRejoinAfterKick(t *testing.T) {
	type tc struct {
		name    string
		quiet   bool
		netCaps string
		caps    []string
		module  bool
	}
	netCaps := "batch labeled-response echo-message multi-prefix userhost-in-names extended-join message-tags server-time account-notify away-notify chghost"
	for _, c := range []tc{
		{name: "quiet rejoin", quiet: true},
		{name: "plain", quiet: false},
		{name: "mirc caps", quiet: true, caps: mircCaps},
		{name: "mirc caps, ircv3 network", quiet: true, netCaps: netCaps, caps: mircCaps},
		{name: "kickrejoin module", quiet: true, netCaps: netCaps, caps: mircCaps, module: true},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := newRigCaps(t, c.netCaps)
			u := r.user
			u.QuietRejoin = c.quiet
			r.st.UpdateUser(r.ctx, u)
			ic := r.attached(c.caps...)
			if c.module {
				ic.send("PRIVMSG *status :loadmod kickrejoin")
				ic.expect("kickrejoin is on")
				ic.send("PRIVMSG *status :modset kickrejoin delay 0")
				ic.expect("delay")
			}
			ic.quiet(time.Second)

			ic.send("SIMULATE :op!o@h KICK #one alice_one :out")
			ic.expect(" KICK #one alice_one ")
			ic.send("JOIN #one") // auto-rejoin
			seen := ic.quiet(2 * time.Second)
			if n := ownJoins(seen, "#one"); n != 1 {
				t.Fatalf("rejoin after a kick: %d JOINs of #one, want 1:\n%s", n, strings.Join(seen, "\n"))
			}
			names := namesFor(seen, "#one")
			self := false
			for _, n := range names {
				if strings.EqualFold(strings.TrimLeft(n, "@+%~&"), "alice_one") {
					self = true
				}
			}
			if !self {
				t.Fatalf("rejoin after a kick: the names (%v) leave out the client's own nick:\n%s", names, strings.Join(seen, "\n"))
			}
		})
	}
}

// nicklist plays lines as mIRC keeps a channel's nicklist: our JOIN
// starts it over and only the names after it, up to their end, fill it
// (our own nick included); others' JOIN, PART, KICK, QUIT and NICK change
// it; our KICK or PART empties it.
func nicklist(lines []string, ch, me string) (map[string]bool, bool) {
	list, open, naming := map[string]bool{}, false, false
	for _, l := range lines {
		m := strings.Fields(l)
		if len(m) > 0 && strings.HasPrefix(m[0], "@") {
			m = m[1:]
		}
		if len(m) < 2 {
			continue
		}
		who, _, _ := strings.Cut(strings.TrimPrefix(m[0], ":"), "!")
		self := strings.EqualFold(who, me)
		arg := func(i int) string {
			if len(m) > i {
				return strings.TrimPrefix(m[i], ":")
			}
			return ""
		}
		switch {
		case m[1] == "JOIN" && strings.EqualFold(arg(2), ch):
			if self {
				list, open, naming = map[string]bool{}, true, true
			} else {
				list[strings.ToLower(who)] = true
			}
		case (m[1] == "PART" && strings.EqualFold(arg(2), ch) && self) || (m[1] == "KICK" && strings.EqualFold(arg(2), ch) && strings.EqualFold(arg(3), me)):
			list, open, naming = map[string]bool{}, false, false
		case m[1] == "PART" && strings.EqualFold(arg(2), ch):
			delete(list, strings.ToLower(who))
		case m[1] == "KICK" && strings.EqualFold(arg(2), ch):
			delete(list, strings.ToLower(arg(3)))
		case m[1] == "QUIT":
			delete(list, strings.ToLower(who))
		case m[1] == "NICK":
			if list[strings.ToLower(who)] {
				delete(list, strings.ToLower(who))
				list[strings.ToLower(arg(2))] = true
			}
			if self {
				me = arg(2)
			}
		case m[1] == "353" && naming && len(m) >= 5 && strings.EqualFold(m[4], ch):
			for _, n := range namesFor([]string{strings.Join(m, " ")}, ch) {
				n = strings.TrimLeft(n, "@+%~&")
				n, _, _ = strings.Cut(n, "!")
				list[strings.ToLower(n)] = true
			}
		case m[1] == "366" && len(m) >= 4 && strings.EqualFold(m[3], ch):
			naming = false
		}
	}
	return list, open
}

// The network's link drops and comes back (not a kick): the client's
// nicklist still has the client in it once it's back in (#175).
func TestNicklistAfterReconnect(t *testing.T) {
	type tc struct {
		name     string
		quiet    bool
		caps     []string
		rejoin   bool // the client joins back as evrd's KICK arrives (mIRC)
		reattach bool // the client reconnects to evrd while the link's down
	}
	for _, c := range []tc{
		{name: "quiet rejoin", quiet: true},
		{name: "quiet rejoin, mirc caps", quiet: true, caps: mircCaps},
		{name: "kick on drop", quiet: false},
		{name: "kick on drop, mirc rejoins", quiet: false, caps: mircCaps, rejoin: true},
		{name: "quiet rejoin, client reattaches", quiet: true, caps: mircCaps, reattach: true},
		{name: "kick on drop, client reattaches", quiet: false, caps: mircCaps, reattach: true},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := newRig(t)
			u := r.user
			u.QuietRejoin = c.quiet
			r.st.UpdateUser(r.ctx, u)
			ic := r.attached(c.caps...)
			seen := ic.quiet(time.Second)

			r.irc.Drop()
			if c.reattach {
				ic.c.Close()
				ic = r.attach("alice/one:alice-password", c.caps...)
				ic.send("JOIN #one") // mIRC's rejoin on connect
				seen = nil
			} else if c.rejoin {
				seen = append(seen, ic.until(" KICK #one alice_one ")...)
				ic.send("JOIN #one")
			}
			seen = append(seen, ic.quiet(3*time.Second)...)
			list, open := nicklist(seen, "#one", "alice_one")
			if !open || !list["alice_one"] {
				t.Fatalf("after the reconnect: open %v, nicklist %v:\n%s", open, list, strings.Join(seen, "\n"))
			}
			ic.send("SIMULATE :op!o@h MODE #one +v alice_one")
			ic.expect("MODE #one +v alice_one")
		})
	}
}
