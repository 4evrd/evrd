package client

// The rest of ZNC's *status commands: channels (attach, enable/disable,
// nicks, topics), clients, uptime, bind hosts, jumping servers, and the
// admin listings. The few that don't fit evrd say where that lives instead.

import (
	"context"
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
		se.say("notConnectedNow", "network", se.net.Name)
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
		se.say("web.attach")
		return
	}
	chans := se.matchBuffers(splitChans(args))
	if len(chans) == 0 {
		se.say("usage.attach")
		return
	}
	for _, ch := range chans {
		switch {
		case !se.up.Joined(ch):
			se.say("notOn", "channel", ch)
		case !se.up.ChannelSettings(ch).Detached:
			se.say("attach.notDetached", "channel", ch)
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
			se.say("usage.disablechan")
		} else {
			se.say("usage.enablechan")
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
			se.say("chan.notSaved", "channel", ch, "network", se.net.Name)
			continue
		}
		if err := se.srv.b.SetChannelSettings(se.net.ID, ch, func(cs *store.ChannelSettings) { cs.Disabled = off }); err != nil {
			se.say("chan.saveFailed", "channel", ch, "error", err)
			continue
		}
		online := se.up != nil && se.srv.b.Status(se.net.ID) == upstream.StatusConnected
		switch {
		case off && online && se.up.Joined(ch):
			se.up.SendRaw("PART " + ch)
			se.say("chan.disabledLeft", "channel", ch)
		case off:
			se.say("chan.disabled", "channel", ch)
		case online && !se.up.Joined(ch):
			if key := se.up.ChannelSettings(ch).Key; key != "" {
				se.up.SendRaw("JOIN " + ch + " " + key)
			} else {
				se.up.SendRaw("JOIN " + ch)
			}
			se.say("chan.enabledJoining", "channel", ch)
		default:
			se.say("chan.enabled", "channel", ch)
		}
	}
}

func (se *session) cmdEnableChan(args []string)  { se.setDisabled(args, false) }
func (se *session) cmdDisableChan(args []string) { se.setDisabled(args, true) }

func (se *session) cmdListNicks(args []string) {
	if len(args) != 1 {
		se.say("usage.listnicks")
		return
	}
	if !se.online() {
		return
	}
	if !se.up.Joined(args[0]) {
		se.say("notOn", "channel", args[0])
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
	se.say("listnicks.count", "channel", args[0], "nicks", se.tr("count.nicks", "count", se.up.MemberCount(args[0])))
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
		se.statusf("%-20s %s", ch, orElse(se.up.Topic(ch), se.tr("topics.none")))
	}
	if !any {
		se.say("topics.noChannels", "network", se.net.Name)
	}
}

func (se *session) cmdShowChan(args []string) {
	if len(args) != 1 {
		se.say("usage.showchan")
		return
	}
	ch := args[0]
	var cs store.ChannelSettings
	state := se.tr("showchan.notSaved")
	if se.up != nil {
		cs = se.up.ChannelSettings(ch)
		for _, s := range se.up.Channels() {
			if strings.EqualFold(s, ch) {
				ch, state = s, se.tr("showchan.savedNotJoined")
			}
		}
		if se.up.Joined(ch) {
			state = se.tr("showchan.joined", "nicks", se.tr("count.nicks", "count", se.up.MemberCount(ch)))
		}
	} else {
		cs = se.net.Channel(ch)
	}
	var flags []string
	if cs.Detached {
		flags = append(flags, se.tr("showchan.detached"))
	}
	if cs.Disabled {
		flags = append(flags, se.tr("showchan.disabled"))
	}
	if cs.Key != "" {
		flags = append(flags, se.tr("showchan.keySet"))
	}
	se.say("showchan.header", "channel", ch, "network", se.net.Name, "state", state, "flags", prefixed(" · ", strings.Join(flags, " · ")))
	if se.up != nil && se.up.Joined(ch) {
		se.statusf("  %s", se.tr("showchan.topic", "topic", orElse(se.up.Topic(ch), se.tr("showchan.noTopic"))))
	}
	p := se.playRules()
	mode := se.tr("showchan.keeps")
	if p.AutoClears(ch) {
		mode = se.tr("showchan.autoClears")
	}
	cleared := ""
	if at := p.ClearedAt(ch); !at.IsZero() {
		cleared = " · " + se.tr("showchan.cleared", "time", at.Local().Format("2006-01-02 15:04"))
	}
	se.statusf("  %s", se.tr("showchan.buffer", "lines", p.Lines(ch), "mode", mode, "cleared", cleared))
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
		se.say("clients.none")
		return
	}
	for _, c := range clients {
		me := "  "
		if se.att != nil && c.ID == se.att.ID() {
			me = "* "
		}
		what := orElse(c.Software, se.tr("clients.ircClient"))
		if c.Kind == "web" {
			what = se.tr("clients.webApp", "software", orElse(c.Software, se.tr("clients.browser")))
		}
		if c.Device != "" {
			what += " (" + c.Device + ")"
		}
		se.statusf("%s%-36s %-16s %-15s %s", me, what, orElse(strings.Join(c.Networks, ","), "-"), c.IP, se.tr("clients.since", "time", c.Since.Local().Format("Jan 2 15:04")))
	}
	se.say("clients.footer", "clients", se.tr("count.clients", "count", len(clients)))
}

func (se *session) cmdUptime([]string) {
	se.say("uptime", "duration", roundUp(time.Since(started)), "since", started.Local().Format("2006-01-02 15:04"))
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
		se.say("traffic.web")
		return
	}
	se.say("traffic.url", "network", se.net.Name, "url", fmt.Sprintf("%s/networks/%d/health", se.srv.publicURL, se.net.ID))
}

// cmdJump is ZNC's Jump: with no argument, reconnect to the next server; with
// a server, to that one. A network name jumps this client to that network
// instead (JumpNetwork, as before).
func (se *session) cmdJump(args []string) {
	if len(args) > 1 {
		se.say("usage.jump")
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
		se.say("jump.notConnected", "network", n.Name)
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
			se.say("jump.unknown", "name", strconv.Quote(args[0]), "network", n.Name)
			return
		}
	}
	sv := servers[next]
	if err := se.srv.b.JumpServer(n.ID, next); err != nil {
		se.say("jump.failed", "error", err)
		return
	}
	se.say("jump.reconnecting", "network", n.Name, "host", sv.Host, "port", sv.Port)
}

func (se *session) cmdJumpNetwork(args []string) {
	if se.web != nil {
		se.say("web.jumpnetwork")
		return
	}
	if len(args) != 1 {
		se.say("usage.jumpnetwork")
		return
	}
	n, ok := se.network(args)
	if !ok {
		return
	}
	if n.ID == se.net.ID {
		se.say("jumpnetwork.already", "network", n.Name)
		return
	}
	if err := se.jump(n); err != nil {
		se.say("jumpnetwork.failed", "network", n.Name, "error", err)
	}
}

// --- bind hosts --------------------------------------------------------------

func (se *session) cmdSetBindHost(args []string) {
	if len(args) != 1 {
		se.say("usage.setbindhost")
		return
	}
	se.cmdSet([]string{"bindhost", args[0]})
}

func (se *session) cmdClearBindHost([]string) { se.cmdSet([]string{"bindhost", "-"}) }

func (se *session) cmdShowBindHost([]string) {
	u := se.account()
	se.say("bindhost.show", "network", orElse(se.net.BindHost, se.tr("bindhost.notSet")), "account", orElse(u.BindHost, se.tr("bindhost.notSet")))
}

func (se *session) setUserBindHost(host string) {
	u := se.account()
	if u.ID == 0 {
		se.say("accountFailedPlain")
		return
	}
	if err := se.srv.acc.CheckBindHost(u, host); err != nil {
		se.statusf("%s.", capitalize(err.Error()))
		return
	}
	u.BindHost = host
	if err := se.srv.acc.UpdateUser(context.Background(), u); err != nil {
		se.say("saveFailed", "error", err)
		return
	}
	se.acct = u
	if host == "" {
		se.say("bindhost.userCleared")
	} else {
		se.say("bindhost.userSet", "host", host)
	}
}

func (se *session) cmdSetUserBindHost(args []string) {
	if len(args) != 1 {
		se.say("usage.setuserbindhost")
		return
	}
	se.setUserBindHost(args[0])
}

func (se *session) cmdClearUserBindHost([]string) { se.setUserBindHost("") }

// --- admins ------------------------------------------------------------------

func (se *session) admin() bool {
	if !se.account().IsAdmin() {
		se.say("adminsOnly")
		return false
	}
	return true
}

func (se *session) cmdListUsers([]string) {
	if !se.admin() {
		return
	}
	users, err := se.srv.acc.ListUsers(context.Background())
	if err != nil {
		se.say("listUsersFailed", "error", err)
		return
	}
	nets, _ := se.srv.acc.AllNetworks(context.Background())
	count := map[int64]int{}
	for _, n := range nets {
		count[n.UserID]++
	}
	clients := se.srv.b.ClientsByUser()
	for _, u := range users {
		flag := ""
		if u.Suspended {
			flag = " · " + se.tr("users.suspended")
		}
		se.statusf("  %-20s %-6s %s · %s%s", u.Username, se.tr("role."+u.Role), se.tr("count.networks", "count", count[u.ID]), se.tr("users.attached", "count", clients[u.ID]), flag)
	}
	se.say("users.footer", "users", se.tr("count.users", "count", len(users)))
}

func (se *session) cmdListAllUserNetworks([]string) {
	if !se.admin() {
		return
	}
	users, err := se.srv.acc.ListUsers(context.Background())
	if err != nil {
		se.say("listUsersFailed", "error", err)
		return
	}
	names := map[int64]string{}
	for _, u := range users {
		names[u.ID] = u.Username
	}
	nets, err := se.srv.acc.AllNetworks(context.Background())
	if err != nil {
		se.say("listNetworksFailed", "error", err)
		return
	}
	for _, n := range nets {
		se.statusf("  %-20s %-16s %-12s %s:%d", names[n.UserID], n.Name, se.state(se.srv.b.Status(n.ID)), n.Host, n.Port)
	}
	se.say("networks.total", "networks", se.tr("count.networks", "count", len(nets)))
}

// notHere are ZNC commands that evrd does another way: the answer is
// irc.notHere.<command>, saying where.
var notHere = map[string]bool{
	"reloadmod": true, "updatemod": true, "rehash": true, "saveconfig": true, "restart": true,
	"shutdown": true, "listports": true, "addport": true, "delport": true, "showmotd": true,
	"setmotd": true, "addmotd": true, "clearmotd": true, "broadcast": true, "movenetwork": true,
}
