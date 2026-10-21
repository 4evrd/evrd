package client

// The rest of ZNC's *status commands: channels (attach, enable/disable,
// nicks, topics), clients, uptime, bind hosts, jumping servers, and the
// admin listings. The few that don't fit evrd say where that lives instead.

import (
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/4evrd/evrd/internal/store"
	"github.com/4evrd/evrd/internal/upstream"
)

// started is when this evrd started, for uptime.
var started = time.Now()

// online reports whether this session's network is connected, saying so
// when it isn't.
func (se *session) online() bool {
	if se.up == nil || se.srv.b.Status(se.net.ID) != upstream.StatusConnected {
		se.statusf("%s isn't connected right now.", se.net.Name)
		return false
	}
	return true
}

func splitChans(args []string) []string {
	var out []string
	for _, a := range args {
		for _, c := range strings.Split(a, ",") {
			if c != "" {
				out = append(out, c)
			}
		}
	}
	return out
}

func (se *session) cmdAttach(args []string) {
	if se.web != nil {
		se.statusf("Attaching shows channels to IRC clients; the web chat shows everything.")
		return
	}
	chans := se.matchBuffers(splitChans(args))
	if len(chans) == 0 {
		se.statusf("Usage: attach <#channel>[,…] (wildcards work)")
		return
	}
	for _, ch := range chans {
		switch {
		case !se.up.Joined(ch):
			se.statusf("You're not on %s.", ch)
		case !se.up.ChannelSettings(ch).Detached:
			se.statusf("%s isn't detached.", ch)
		default:
			se.attachChannel(ch)
		}
	}
}

// setDisabled switches channels on or off: off parts them but keeps them
// (and their key) saved; on joins them again.
func (se *session) setDisabled(args []string, off bool) {
	chans := splitChans(args)
	if len(chans) == 0 {
		if off {
			se.statusf("Usage: disablechan <#channel>[,…] — leave a channel but keep it saved (enablechan joins it again)")
		} else {
			se.statusf("Usage: enablechan <#channel>[,…]")
		}
		return
	}
	saved := se.net.Channels
	if se.up != nil {
		saved = se.up.Channels()
	}
	for _, ch := range se.matchBuffers(chans) {
		known := false
		for _, s := range saved {
			if strings.EqualFold(s, ch) {
				ch, known = s, true
			}
		}
		if !known {
			se.statusf("%s isn't one of %s's channels.", ch, se.net.Name)
			continue
		}
		if err := se.srv.b.SetChannelSettings(se.net.ID, ch, func(cs *store.ChannelSettings) { cs.Disabled = off }); err != nil {
			se.statusf("Couldn't save %s: %v", ch, err)
			continue
		}
		online := se.up != nil && se.srv.b.Status(se.net.ID) == upstream.StatusConnected
		switch {
		case off && online && se.up.Joined(ch):
			se.up.SendRaw("PART " + ch)
			se.statusf("Disabled %s: left it, kept it saved. enablechan %s joins it again.", ch, ch)
		case off:
			se.statusf("Disabled %s: it won't be joined.", ch)
		case online && !se.up.Joined(ch):
			if key := se.up.ChannelSettings(ch).Key; key != "" {
				se.up.SendRaw("JOIN " + ch + " " + key)
			} else {
				se.up.SendRaw("JOIN " + ch)
			}
			se.statusf("Enabled %s: joining it.", ch)
		default:
			se.statusf("Enabled %s.", ch)
		}
	}
}

func (se *session) cmdEnableChan(args []string)  { se.setDisabled(args, false) }
func (se *session) cmdDisableChan(args []string) { se.setDisabled(args, true) }

func (se *session) cmdListNicks(args []string) {
	if len(args) != 1 {
		se.statusf("Usage: listnicks <#channel>")
		return
	}
	if !se.online() {
		return
	}
	if !se.up.Joined(args[0]) {
		se.statusf("You're not on %s.", args[0])
		return
	}
	var nicks []string
	for _, m := range se.up.Members(args[0]) {
		nicks = append(nicks, m.Prefix+m.Nick)
	}
	for len(nicks) > 0 {
		n := min(len(nicks), 20)
		se.statusf("%s", strings.Join(nicks[:n], " "))
		nicks = nicks[n:]
	}
	se.statusf("%s: %s.", args[0], plural(se.up.MemberCount(args[0]), "nick"))
}

func (se *session) cmdTopics([]string) {
	if !se.online() {
		return
	}
	any := false
	for _, ch := range se.up.Channels() {
		if !se.up.Joined(ch) {
			continue
		}
		any = true
		se.statusf("%-20s %s", ch, orElse(se.up.Topic(ch), "(no topic)"))
	}
	if !any {
		se.statusf("You're on no channels on %s.", se.net.Name)
	}
}

func (se *session) cmdShowChan(args []string) {
	if len(args) != 1 {
		se.statusf("Usage: showchan <#channel>")
		return
	}
	ch := args[0]
	var cs store.ChannelSettings
	state := "not saved"
	if se.up != nil {
		cs = se.up.ChannelSettings(ch)
		for _, s := range se.up.Channels() {
			if strings.EqualFold(s, ch) {
				ch, state = s, "saved, not joined"
			}
		}
		if se.up.Joined(ch) {
			state = fmt.Sprintf("joined, %s", plural(se.up.MemberCount(ch), "nick"))
		}
	} else {
		cs = se.net.Channel(ch)
	}
	var flags []string
	if cs.Detached {
		flags = append(flags, "detached")
	}
	if cs.Disabled {
		flags = append(flags, "disabled")
	}
	if cs.Key != "" {
		flags = append(flags, "key set")
	}
	se.statusf("%s on %s — %s%s", ch, se.net.Name, state, prefixed(" · ", strings.Join(flags, " · ")))
	if se.up != nil && se.up.Joined(ch) {
		se.statusf("  topic: %s", orElse(se.up.Topic(ch), "(none)"))
	}
	bps := se.playbacks()
	mode := "keeps its buffer"
	if se.autoClears(bps, ch) {
		mode = "auto-clears"
	}
	cleared := ""
	if at := bps[strings.ToLower(ch)].ClearedAt; at != nil {
		cleared = " · cleared " + at.Local().Format("2006-01-02 15:04")
	}
	se.statusf("  buffer: plays back %d lines · %s%s", se.bufferLines(bps, ch), mode, cleared)
}

func prefixed(p, s string) string {
	if s == "" {
		return ""
	}
	return p + s
}

func (se *session) cmdListClients([]string) {
	clients := se.srv.b.ClientsOf(se.uid)
	if len(clients) == 0 {
		se.statusf("No clients attached.")
		return
	}
	for _, c := range clients {
		me := "  "
		if se.att != nil && c.ID == se.att.ID() {
			me = "* "
		}
		what := orElse(c.Software, "IRC client")
		if c.Kind == "web" {
			what = "web app · " + orElse(c.Software, "browser")
		}
		if c.Device != "" {
			what += " (" + c.Device + ")"
		}
		se.statusf("%s%-36s %-16s %-15s since %s", me, what, orElse(strings.Join(c.Networks, ","), "-"), c.IP, c.Since.Local().Format("Jan 2 15:04"))
	}
	se.statusf("%s. * = this one.", plural(len(clients), "client"))
}

func (se *session) cmdUptime([]string) {
	se.statusf("evrd has been running for %s (since %s).", roundUp(time.Since(started)), started.Local().Format("2006-01-02 15:04"))
}

func roundUp(d time.Duration) string {
	d = d.Round(time.Second)
	days := int(d / (24 * time.Hour))
	d -= time.Duration(days) * 24 * time.Hour
	if days > 0 {
		return fmt.Sprintf("%dd %s", days, d)
	}
	return d.String()
}

func (se *session) cmdTraffic([]string) {
	if se.srv.publicURL == "" {
		se.statusf("Traffic per network is on each network's health page in the web app.")
		return
	}
	se.statusf("Traffic for %s: %s/networks/%d/health", se.net.Name, se.srv.publicURL, se.net.ID)
}

// cmdJump is ZNC's Jump: with no argument, reconnect to the next server; with
// a server, to that one. A network name jumps this client to that network
// instead (JumpNetwork, as before).
func (se *session) cmdJump(args []string) {
	if len(args) > 1 {
		se.statusf("Usage: jump [server|network]")
		return
	}
	if len(args) == 1 {
		if nets, ok := se.networks(); ok {
			if _, ok := findNetwork(nets, args[0]); ok {
				se.cmdJumpNetwork(args)
				return
			}
		}
	}
	n, ok := se.network(nil)
	if !ok {
		return
	}
	if se.srv.b.Status(n.ID) == upstream.StatusDisconnected {
		se.statusf("%s isn't connected — connect brings it up.", n.Name)
		return
	}
	servers := n.ServerList()
	cur := -1
	if c, ok := se.srv.b.ConnFor(n.ID); ok {
		h := c.Health()
		for i, sv := range servers {
			if h.Server == net.JoinHostPort(sv.Host, strconv.Itoa(sv.Port)) {
				cur = i
			}
		}
	}
	next := (cur + 1) % len(servers)
	if len(args) == 1 {
		next = -1
		for i, sv := range servers {
			if strings.EqualFold(sv.Host, args[0]) || strings.EqualFold(net.JoinHostPort(sv.Host, strconv.Itoa(sv.Port)), args[0]) {
				next = i
				break
			}
		}
		if next < 0 {
			se.statusf("%q is neither one of your networks nor a server of %s — see networks or listservers.", args[0], n.Name)
			return
		}
	}
	sv := servers[next]
	if err := se.srv.b.JumpServer(n.ID, next); err != nil {
		se.statusf("Couldn't jump: %v", err)
		return
	}
	se.statusf("Reconnecting %s to %s:%d…", n.Name, sv.Host, sv.Port)
}

func (se *session) cmdJumpNetwork(args []string) {
	if se.web != nil {
		se.statusf("In the web app, pick a network from the Networks page instead.")
		return
	}
	if len(args) != 1 {
		se.statusf("Usage: jumpnetwork <network>")
		return
	}
	n, ok := se.network(args)
	if !ok {
		return
	}
	if n.ID == se.net.ID {
		se.statusf("You're already on %s.", n.Name)
		return
	}
	if err := se.jump(n); err != nil {
		se.statusf("Couldn't jump to %s: %v", n.Name, err)
	}
}

// --- bind hosts --------------------------------------------------------------

func (se *session) cmdSetBindHost(args []string) {
	if len(args) != 1 {
		se.statusf("Usage: setbindhost <host> — this network's local address (clearbindhost goes back to your account's)")
		return
	}
	se.cmdSet([]string{"bindhost", args[0]})
}

func (se *session) cmdClearBindHost([]string) { se.cmdSet([]string{"bindhost", "-"}) }

func (se *session) cmdShowBindHost([]string) {
	u := se.account()
	se.statusf("This network's bind host: %s · your account's: %s", orElse(se.net.BindHost, "(not set)"), orElse(u.BindHost, "(not set)"))
}

func (se *session) setUserBindHost(host string) {
	u := se.account()
	if u.ID == 0 {
		se.statusf("Couldn't load your account.")
		return
	}
	if err := se.srv.acc.CheckBindHost(u, host); err != nil {
		se.statusf("%s.", capitalize(err.Error()))
		return
	}
	u.BindHost = host
	if err := se.srv.b.Store().UpdateUser(u); err != nil {
		se.statusf("Couldn't save: %v", err)
		return
	}
	se.acct = u
	if host == "" {
		se.statusf("Cleared your account's bind host. Networks use it when they next connect.")
	} else {
		se.statusf("Your account's bind host is %s. Networks without their own use it when they next connect.", host)
	}
}

func (se *session) cmdSetUserBindHost(args []string) {
	if len(args) != 1 {
		se.statusf("Usage: setuserbindhost <host>")
		return
	}
	se.setUserBindHost(args[0])
}

func (se *session) cmdClearUserBindHost([]string) { se.setUserBindHost("") }

// --- admins ------------------------------------------------------------------

func (se *session) admin() bool {
	if !se.account().IsAdmin() {
		se.statusf("Admins only.")
		return false
	}
	return true
}

func (se *session) cmdListUsers([]string) {
	if !se.admin() {
		return
	}
	users, err := se.srv.b.Store().ListUsers()
	if err != nil {
		se.statusf("Couldn't list users: %v", err)
		return
	}
	nets, _ := se.srv.b.Store().AllNetworks()
	count := map[int64]int{}
	for _, n := range nets {
		count[n.UserID]++
	}
	clients := se.srv.b.ClientsByUser()
	for _, u := range users {
		flag := ""
		if u.Suspended {
			flag = " · suspended"
		}
		se.statusf("  %-20s %-6s %s · %s attached%s", u.Username, u.Role, plural(count[u.ID], "network"), strconv.Itoa(clients[u.ID]), flag)
	}
	se.statusf("%s.", plural(len(users), "user"))
}

func (se *session) cmdListAllUserNetworks([]string) {
	if !se.admin() {
		return
	}
	users, err := se.srv.b.Store().ListUsers()
	if err != nil {
		se.statusf("Couldn't list users: %v", err)
		return
	}
	names := map[int64]string{}
	for _, u := range users {
		names[u.ID] = u.Username
	}
	nets, err := se.srv.b.Store().AllNetworks()
	if err != nil {
		se.statusf("Couldn't list networks: %v", err)
		return
	}
	for _, n := range nets {
		se.statusf("  %-20s %-16s %-12s %s:%d", names[n.UserID], n.Name, se.srv.b.Status(n.ID), n.Host, n.Port)
	}
	se.statusf("%s.", plural(len(nets), "network"))
}

// notHere answers ZNC commands that evrd does another way.
var notHere = map[string]string{
	"reloadmod":   "Modules are built into evrd and always current — nothing to reload.",
	"updatemod":   "Modules are built into evrd and always current — nothing to update.",
	"rehash":      "evrd applies settings as they're saved; evrd.conf is read at start (restart evrd to re-read it).",
	"saveconfig":  "evrd saves every change as it's made.",
	"restart":     "Restart evrd from the server (rcctl restart evrd, systemctl restart evrd, or docker compose restart) — the holder keeps everyone connected.",
	"shutdown":    "Stop evrd from the server (rcctl stop evrd, systemctl stop evrd, or docker compose stop).",
	"listports":   "evrd's ports are in evrd.conf (listen, irc_listen) and on the admin Settings page.",
	"addport":     "evrd's ports are in evrd.conf (listen, irc_listen) and on the admin Settings page.",
	"delport":     "evrd's ports are in evrd.conf (listen, irc_listen) and on the admin Settings page.",
	"showmotd":    "evrd has no message of the day of its own; /motd shows the network's.",
	"setmotd":     "evrd has no message of the day of its own.",
	"addmotd":     "evrd has no message of the day of its own.",
	"clearmotd":   "evrd has no message of the day of its own.",
	"broadcast":   "evrd can't broadcast to every user yet.",
	"movenetwork": "Networks can't move between users; add it to the other account instead (the web app's import can copy settings).",
}
