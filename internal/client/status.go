package client

// *status is the bouncer's own user: /msg *status <command> controls evrd
// from any IRC client, like ZNC's *status.

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/4evrd/evrd/internal/modules"

	"github.com/4evrd/evrd/internal/accounts"
	"github.com/4evrd/evrd/internal/directory"
	"github.com/4evrd/evrd/internal/messages"
	"github.com/4evrd/evrd/internal/store"
	"github.com/4evrd/evrd/internal/upstream"
	"github.com/4evrd/evrd/internal/version"
)

const statusNick = "*status"

// maxPlayback caps how many lines one playback sends.
const maxPlayback = 500

// statusCmd is a *status command; its description is the irc.help.<name>
// text (internal/messages).
type statusCmd struct {
	name, args string
	run        func(se *session, args []string)
}

var statusCmds []statusCmd

func init() {
	statusCmds = []statusCmd{
		{"help", "", (*session).cmdHelp},
		{"networks", "[filter]", (*session).cmdNetworks},
		{"jumpnetwork", "<network>", (*session).cmdJumpNetwork},
		{"jump", "[server|network]", (*session).cmdJump},
		{"connect", "[network]", (*session).cmdConnect},
		{"disconnect", "[network]", (*session).cmdDisconnect},
		{"addnetwork", "<name> <host> [[+]port] [nick] or <id>", (*session).cmdAddNetwork},
		{"directory", "[search]", (*session).cmdDirectory},
		{"delnetwork", "<network>", (*session).cmdDelNetwork},
		{"show", "[network]", (*session).cmdShow},
		{"set", "[network] <setting> <value>", (*session).cmdSet},
		{"listservers", "", (*session).cmdListServers},
		{"addserver", "<host> [[+]port] [password]", (*session).cmdAddServer},
		{"delserver", "<host> [port]", (*session).cmdDelServer},
		{"listfingerprints", "", (*session).cmdListFingerprints},
		{"addfingerprint", "<sha-256>", (*session).cmdAddFingerprint},
		{"delfingerprint", "<sha-256>", (*session).cmdDelFingerprint},
		{"detach", "<#channel>[,…]", (*session).cmdDetach},
		{"attach", "<#channel>[,…]", (*session).cmdAttach},
		{"disablechan", "<#channel>[,…]", (*session).cmdDisableChan},
		{"enablechan", "<#channel>[,…]", (*session).cmdEnableChan},
		{"channels", "", (*session).cmdChannels},
		{"showchan", "<#channel>", (*session).cmdShowChan},
		{"listnicks", "<#channel>", (*session).cmdListNicks},
		{"topics", "", (*session).cmdTopics},
		{"buffers", "", (*session).cmdBuffers},
		{"playbuffer", "<channel|nick>", (*session).cmdPlayBuffer},
		{"playback", "<channel|nick> [lines]", (*session).cmdPlayback},
		{"clearbuffer", "<channel|nick>[,…]", (*session).cmdClearBuffer},
		{"clearallbuffers", "", (*session).cmdClearAllBuffers},
		{"clearallchannelbuffers", "", (*session).cmdClearAllChannelBuffers},
		{"clearallquerybuffers", "", (*session).cmdClearAllQueryBuffers},
		{"setbuffer", "<channel|nick|*> [lines|-]", (*session).cmdSetBuffer},
		{"autoclear", "<channel|nick>[,…] on|off|-", (*session).cmdAutoClear},
		{"modules", "", (*session).cmdModules},
		{"loadmod", "<module>", (*session).cmdLoadMod},
		{"unloadmod", "<module>", (*session).cmdUnloadMod},
		{"modset", "<module> <setting> <value>", (*session).cmdModSet},
		{"setbindhost", "<host>", (*session).cmdSetBindHost},
		{"clearbindhost", "", (*session).cmdClearBindHost},
		{"showbindhost", "", (*session).cmdShowBindHost},
		{"setuserbindhost", "<host>", (*session).cmdSetUserBindHost},
		{"clearuserbindhost", "", (*session).cmdClearUserBindHost},
		{"info", "", (*session).cmdInfo},
		{"listclients", "", (*session).cmdListClients},
		{"uptime", "", (*session).cmdUptime},
		{"traffic", "", (*session).cmdTraffic},
		{"listusers", "", (*session).cmdListUsers},
		{"listallusernetworks", "[filter]", (*session).cmdListAllUserNetworks},
		{"version", "", (*session).cmdVersion},
	}

}

// aliases map ZNC's names for the same commands.
var statusAliases = map[string]string{
	"listnetworks": "networks", "listnets": "networks",
	"listmods": "modules", "listavailmods": "modules", "listnetmods": "modules", "listavailablemodules": "modules",
	"listbuffers": "buffers",
	"addnet":      "addnetwork", "delnet": "delnetwork", "listchans": "channels",
	"shownetwork": "show", "setnetwork": "set",
	"addtrustedserverfingerprint": "addfingerprint", "deltrustedserverfingerprint": "delfingerprint",
	"listtrustedserverfingerprints": "listfingerprints",
	"remserver":                     "delserver",
	"listmodules":                   "modules", "loadmodule": "loadmod", "unloadmodule": "unloadmod",
	"reloadmodule": "reloadmod", "updatemodule": "updatemod",
	"setvhost": "setbindhost", "clearvhost": "clearbindhost",
	"motd": "showmotd",
}

func (se *session) statusCommand(text string) {
	se.pr = nil // the account's language may have changed since
	fields := strings.Fields(text)
	if len(fields) == 0 {
		se.cmdHelp(nil)
		return
	}
	name := strings.ToLower(fields[0])
	if a, ok := statusAliases[name]; ok {
		name = a
	}
	for _, c := range statusCmds {
		if c.name == name {
			c.run(se, fields[1:])
			return
		}
	}
	if notHere[name] {
		se.say("notHere." + name)
		return
	}
	se.say("unknownCommand", "command", strconv.Quote(fields[0]))
}

// WebStatus runs a *status command for the web chat, on the given network,
// passing each reply line to out.
// lang is the language to answer in ("": the account's).
func (s *Server) WebStatus(userID int64, lang string, n store.Network, text string, out func(string)) {
	se := &session{srv: s, uid: userID, net: n, web: out, lang: lang}
	if conn, ok := s.b.ConnFor(n.ID); ok {
		se.up = conn
		se.unick = conn.Nick()
	}
	se.statusCommand(text)
}

// statusf sends a line from *status to this client.
func (se *session) statusf(format string, a ...any) {
	if se.web != nil {
		se.web(fmt.Sprintf(format, a...))
		return
	}
	se.send(fmt.Sprintf(":%s!status@%s PRIVMSG %s :%s", statusNick, serverName, se.unick, ircText(fmt.Sprintf(format, a...))))
}

// say sends key's text (irc.<key> in internal/messages) from *status, in
// the user's language, its {name}s filled from args' name/value pairs.
func (se *session) say(key string, args ...any) {
	se.statusf("%s", se.tr(key, args...))
}

// tr is key's text (irc.<key>) in the user's language.
func (se *session) tr(key string, args ...any) string {
	return se.printer().T(messages.Area+key, args...)
}

// printer is the user's language for *status, read again for each command:
// English without a messages service or a user.
func (se *session) printer() *messages.Printer {
	if se.pr == nil {
		se.pr = messages.English
		if se.srv.msgs != nil && se.uid != 0 {
			u, err := se.srv.acc.UserByID(context.Background(), se.uid)
			if err != nil {
				u = se.account()
			}
			if se.lang != "" {
				u.Language = se.lang
			}
			se.pr = se.srv.msgs.For(context.Background(), u)
		}
	}
	return se.pr
}

// sayErr sends err from *status as a sentence of its own: in the user's
// language when it says what it is in words (a validation error, a
// messages.Error), else as it is.
func (se *session) sayErr(err error) {
	se.statusf("%s", messages.Sentence(se.printer().Say(err)))
}

// state is a network's connection state, in words.
func (se *session) state(st upstream.Status) string {
	return se.tr("state." + string(st))
}

func (se *session) cmdHelp([]string) {
	se.say("help.intro", "status", statusNick)
	rows := [][]string{{se.tr("help.command"), se.tr("help.arguments"), se.tr("help.description")}}
	for _, c := range statusCmds {
		row := []string{strings.ToUpper(c.name), c.args, se.tr("help."+c.name, "max", maxPlayback)}
		if se.web == nil {
			for i := range row {
				row[i] = ircText(row[i]) // before padding, so the columns line up
			}
		}
		rows = append(rows, row)
	}
	for _, l := range markdownTable(rows) {
		se.statusf("%s", l)
	}
	se.say("help.settings", "settings", strings.Join(settingNames, ", "))
	se.say("help.caseInsensitive")
}

// markdownTable lays rows out as a Markdown table, the first row its header,
// the columns padded so they line up. Each line is in IRC's monospace
// formatting (\x11), so clients with a proportional font (IRCCloud, The
// Lounge…) line the columns up too. A "|" in a cell stays as it is: no IRC
// client reads Markdown, and the web chat, which draws it as a table, only
// takes a "|" with spaces around it as a column break.
func markdownTable(rows [][]string) []string {
	var width []int
	for _, r := range rows {
		for i, c := range r {
			if i == len(width) {
				width = append(width, 0)
			}
			width[i] = max(width[i], utf8.RuneCountInString(c))
		}
	}
	// (the last column isn't padded and rows don't end in "|": both are
	// optional, and its cells vary the most)
	line := func(cells []string) string {
		var b strings.Builder
		for i, w := range width {
			c := ""
			if i < len(cells) {
				c = cells[i]
			}
			b.WriteString("| " + c)
			if i < len(width)-1 {
				b.WriteString(strings.Repeat(" ", max(w-utf8.RuneCountInString(c), 0)) + " ")
			}
		}
		return "\x11" + b.String() + "\x11"
	}
	out := []string{line(rows[0])}
	sep := make([]string, len(width))
	for i, w := range width {
		sep[i] = strings.Repeat("-", max(w, 3))
	}
	sep[len(sep)-1] = "---"
	out = append(out, line(sep))
	for _, r := range rows[1:] {
		out = append(out, line(r))
	}
	return out
}

// networks loads the user's networks, reporting a failure itself.
func (se *session) networks() ([]store.Network, bool) {
	nets, err := se.srv.acc.ListNetworks(context.Background(), se.uid)
	if err != nil {
		se.say("networksFailed", "error", err)
		return nil, false
	}
	return nets, true
}

// network resolves a name argument, defaulting to the current network.
func (se *session) network(args []string) (store.Network, bool) {
	if len(args) == 0 {
		n, err := se.srv.acc.NetworkByID(context.Background(), se.net.ID)
		if err != nil {
			se.say("networkGone")
			return store.Network{}, false
		}
		return n, true
	}
	nets, ok := se.networks()
	if !ok {
		return store.Network{}, false
	}
	n, ok := findNetwork(nets, args[0])
	if !ok {
		se.say("noSuchNetwork", "network", strconv.Quote(args[0]))
	}
	return n, ok
}

// cmdNetworks lists the user's networks: connected first, then by name.
// A filter keeps those whose name matches it (* and ? wildcards; without
// any, a name containing it).
func (se *session) cmdNetworks(args []string) {
	all, ok := se.networks()
	if !ok {
		return
	}
	filter := strings.Join(args, " ")
	nets := se.sortNetworks(filterNetworks(all, filter, func(n store.Network) []string { return []string{n.Name} }))
	if filter != "" && len(nets) == 0 {
		se.say("networks.noMatch", "filter", filter)
		return
	}
	for _, n := range nets {
		here := "  "
		if n.ID == se.net.ID {
			here = "* "
		}
		nick := n.Nick
		if c, ok := se.srv.b.ConnFor(n.ID); ok {
			nick = c.Nick()
		}
		tls := ""
		if n.TLS {
			tls = "+"
		}
		se.statusf("%s%-16s %-12s %s %-14s %s:%s%d · %s", here, n.Name, se.state(se.srv.b.Status(n.ID)), se.tr("networks.as"), nick, n.Host, tls, n.Port, se.tr("count.channels", "count", len(n.Channels)))
	}
	if filter != "" {
		se.say("networks.footerFiltered", "shown", len(nets), "networks", se.tr("count.networks", "count", len(all)))
		return
	}
	se.say("networks.footer", "networks", se.tr("count.networks", "count", len(nets)))
}

// filterNetworks keeps the networks one of whose names (fields) matches
// filter, ignoring case: a pattern with * or ?, else a part of the name.
// An empty filter keeps them all.
func filterNetworks(nets []store.Network, filter string, fields func(store.Network) []string) []store.Network {
	if filter == "" {
		return nets
	}
	if !strings.ContainsAny(filter, "*?") {
		filter = "*" + filter + "*"
	}
	var out []store.Network
	for _, n := range nets {
		if slices.ContainsFunc(fields(n), func(f string) bool { return modules.Match(filter, f) }) {
			out = append(out, n)
		}
	}
	return out
}

// sortNetworks orders networks for a listing: connected, then on their way
// (connecting, reconnecting), then off; by name within each.
func (se *session) sortNetworks(nets []store.Network) []store.Network {
	rank := func(n store.Network) int {
		switch se.srv.b.Status(n.ID) {
		case upstream.StatusConnected:
			return 0
		case upstream.StatusConnecting, upstream.StatusReconnecting:
			return 1
		}
		return 2
	}
	out := slices.Clone(nets)
	slices.SortStableFunc(out, func(a, b store.Network) int {
		if d := rank(a) - rank(b); d != 0 {
			return d
		}
		return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})
	return out
}

func (se *session) cmdConnect(args []string) {
	n, ok := se.network(args)
	if !ok {
		return
	}
	if st := se.srv.b.Status(n.ID); st != upstream.StatusDisconnected {
		se.say("connect.already", "network", n.Name, "state", se.state(st))
		return
	}
	if err := se.srv.b.Connect(n.ID); err != nil {
		se.say("connect.failed", "network", n.Name, "error", err)
		return
	}
	se.say("connect.connecting", "network", n.Name, "host", n.Host, "port", n.Port)
}

func (se *session) cmdDisconnect(args []string) {
	n, ok := se.network(args)
	if !ok {
		return
	}
	if se.srv.b.Status(n.ID) == upstream.StatusDisconnected {
		se.say("notConnected", "network", n.Name)
		return
	}
	se.srv.b.Disconnect(n.ID)
	if n.ID == se.net.ID {
		se.say("disconnect.here", "network", n.Name)
	} else {
		se.say("disconnect.done", "network", n.Name)
	}
}

func (se *session) cmdAddNetwork(args []string) {
	if len(args) < 1 || len(args) > 4 {
		se.say("usage.addnetwork")
		return
	}
	u, err := se.srv.acc.UserByID(context.Background(), se.uid)
	if err != nil {
		se.say("accountFailed", "error", err)
		return
	}
	n := store.Network{UserID: u.ID, Name: args[0], Port: 6697, TLS: true, Nick: u.DefaultNick, Realname: u.DefaultRealname, Channels: []string{}}
	if len(args) == 1 {
		// one word: a network from the directory, with its servers and
		// suggested channels
		d, ok := se.srv.directory().Lookup(args[0])
		if !ok {
			se.say("addnetwork.notInDirectory", "id", strconv.Quote(args[0]))
			return
		}
		n.Name, n.Channels = d.ID, append([]string{}, d.Channels...)
		for _, s := range d.Connect() {
			n.Servers = append(n.Servers, store.Server{Host: s.Host, Port: s.Port, TLS: s.TLS})
		}
		n.Host, n.Port, n.TLS = n.Servers[0].Host, n.Servers[0].Port, n.Servers[0].TLS
	} else {
		n.Host = args[1]
	}
	if len(args) >= 3 {
		p := args[2]
		n.TLS = strings.HasPrefix(p, "+")
		port, err := strconv.Atoi(strings.TrimPrefix(p, "+"))
		if err != nil || port < 1 || port > 65535 {
			se.say("badPort", "port", strconv.Quote(p))
			return
		}
		n.Port = port
	}
	if len(args) == 4 {
		n.Nick = args[3]
	}
	if n.Nick == "" {
		n.Nick = u.Username
	}
	if nets, ok := se.networks(); !ok {
		return
	} else if _, taken := findNetwork(nets, n.Name); taken {
		se.say("addnetwork.taken", "network", n.Name)
		return
	}
	if err := se.srv.acc.CanCreateNetwork(u); err != nil {
		var ve *accounts.ValidationError
		if errors.As(err, &ve) {
			se.sayErr(ve)
		} else {
			se.say("addnetwork.failed", "error", err)
		}
		return
	}
	n, err = se.srv.acc.CreateNetwork(context.Background(), n)
	if err != nil {
		se.say("addnetwork.failed", "error", err)
		return
	}
	tls := ""
	if n.TLS {
		tls = "+"
	}
	se.say("addnetwork.added", "network", n.Name, "host", n.Host, "tls", tls, "port", n.Port, "nick", n.Nick)
}

// cmdDirectory lists the network directory, or the networks matching a
// search.
func (se *session) cmdDirectory(args []string) {
	all := se.srv.directory().Networks()
	if len(all) == 0 {
		se.say("directory.empty")
		return
	}
	nets := directory.Search(all, strings.Join(args, " "))
	for _, n := range nets {
		s := n.Connect()[0]
		tls := ""
		if s.TLS {
			tls = "+"
		}
		se.statusf("  %-14s %-20s %s:%s%d", n.ID, n.Name, s.Host, tls, s.Port)
	}
	if len(nets) == 0 {
		se.say("directory.noMatch", "search", strconv.Quote(strings.Join(args, " ")))
		return
	}
	se.say("directory.footer", "networks", se.tr("count.networks", "count", len(nets)))
}

func (se *session) cmdDelNetwork(args []string) {
	if len(args) != 1 {
		se.say("usage.delnetwork")
		return
	}
	n, ok := se.network(args)
	if !ok {
		return
	}
	if n.ID == se.net.ID {
		se.say("delnetwork.current", "network", n.Name)
		return
	}
	if err := se.srv.b.DeleteNetwork(se.uid, n.ID); err != nil {
		se.say("delnetwork.failed", "network", n.Name, "error", err)
		return
	}
	se.say("delnetwork.done", "network", n.Name)
}

func (se *session) cmdShow(args []string) {
	if len(args) > 1 {
		se.say("usage.show")
		return
	}
	n, ok := se.network(args)
	if !ok {
		return
	}
	pw := se.tr("show.notSet")
	if n.Password != "" {
		pw = se.tr("show.set")
	}
	byDefault, byNick := se.tr("show.default"), se.tr("show.byNick")
	se.statusf("%s — %s", n.Name, se.state(se.srv.b.Status(n.ID)))
	for i, sv := range n.ServerList() {
		tls := ""
		if sv.TLS {
			tls = " (TLS)"
		}
		se.statusf("  %s", se.tr("show.server", "n", i+1, "host", sv.Host, "port", sv.Port, "tls", tls))
	}
	se.statusf("  nick %s · altnick %s · ident %s · realname %s", n.Nick, orElse(n.AltNick, byDefault),
		orElse(n.Username, byNick), orElse(n.Realname, byNick))
	se.statusf("  sasl password %s · sasl user %s · bindhost %s", pw, orElse(n.SASLUser, se.tr("show.byIdent")), orElse(n.BindHost, byDefault))
	se.statusf("  auth %s · servicesnick %s · identifyargs %s · identifyon %s · ghost %s · joinafterauth %s · authtimeout %ds",
		n.Auth(), orElse(n.ServicesNick, "NickServ"), orElse(n.IdentifyArgs, "auto"), orElse(n.IdentifyOn, "connect"),
		orElse(n.GhostCmd, "off"), onOff(n.JoinAfterAuth), cmp.Or(n.AuthTimeout, store.DefaultAuthTimeout))
	if n.AuthCommand != "" {
		se.statusf("  authcommand %s", n.AuthCommand)
	}
	se.statusf("  quitmsg %s · encoding %s", orElse(n.QuitMsg, byDefault), orElse(n.Encoding, "UTF-8"))
	se.statusf("  trustallcerts %s · trustpki %s · %s", onOff(n.TrustAllCerts), onOff(n.TrustPKI), se.tr("count.fingerprints", "count", len(n.Fingerprints)))
	se.statusf("  floodrate %g · floodburst %d · joindelay %ds", n.FloodRate, n.FloodBurst, n.JoinDelay)
	se.statusf("  channels %s", orElse(strings.Join(n.Channels, " "), se.tr("show.none")))
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

// settingNames are the fields set can change, in help order.
var settingNames = []string{"name", "host", "port", "tls", "nick", "altnick", "ident", "realname",
	"password", "sasluser", "auth", "servicesnick", "identifyargs", "identifyon", "authcommand", "ghost",
	"joinafterauth", "authtimeout", "bindhost", "quitmsg", "encoding", "trustallcerts", "trustpki",
	"floodrate", "floodburst", "joindelay"}

var settings = func() map[string]bool {
	m := map[string]bool{}
	for _, n := range settingNames {
		m[n] = true
	}
	return m
}()

func (se *session) cmdSet(args []string) {
	usage := func() {
		se.say("usage.set")
	}
	if len(args) < 2 {
		usage()
		return
	}
	// "set libera nick bob" names a network; "set nick bob" means this one.
	var target []string
	if !settings[strings.ToLower(args[0])] && len(args) >= 3 {
		target, args = args[:1], args[1:]
	}
	field := strings.ToLower(args[0])
	if !settings[field] {
		usage()
		return
	}
	n, ok := se.network(target)
	if !ok {
		return
	}
	before := n
	value := strings.Join(args[1:], " ")
	clear := value == "-"
	n.Servers = append([]store.Server(nil), n.ServerList()...) // host/port/tls edit the first
	bool_ := func(dst *bool) bool {
		switch strings.ToLower(value) {
		case "on", "yes", "true":
			*dst = true
		case "off", "no", "false":
			*dst = false
		default:
			se.say("set.onOff", "setting", field)
			return false
		}
		return true
	}
	text := func(dst *string) {
		*dst = value
		if clear {
			*dst = ""
		}
	}
	switch field {
	case "name":
		n.Name = value
	case "host":
		n.Servers[0].Host = value
	case "port":
		port, err := strconv.Atoi(strings.TrimPrefix(value, "+"))
		if err != nil || port < 1 || port > 65535 {
			se.say("badPort", "port", strconv.Quote(value))
			return
		}
		n.Servers[0].Port = port
		if strings.HasPrefix(value, "+") {
			n.Servers[0].TLS = true
		}
	case "tls":
		if !bool_(&n.Servers[0].TLS) {
			return
		}
	case "altnick":
		text(&n.AltNick)
	case "sasluser":
		text(&n.SASLUser)
	case "auth":
		text(&n.AuthMethod)
	case "servicesnick":
		text(&n.ServicesNick)
	case "identifyargs":
		text(&n.IdentifyArgs)
		if strings.EqualFold(n.IdentifyArgs, "auto") {
			n.IdentifyArgs = ""
		}
	case "identifyon":
		text(&n.IdentifyOn)
	case "authcommand":
		text(&n.AuthCommand)
	case "ghost":
		text(&n.GhostCmd)
		if strings.EqualFold(n.GhostCmd, "off") {
			n.GhostCmd = ""
		}
	case "joinafterauth":
		if !bool_(&n.JoinAfterAuth) {
			return
		}
	case "quitmsg":
		text(&n.QuitMsg)
	case "encoding":
		text(&n.Encoding)
	case "bindhost":
		text(&n.BindHost)
		u, err := se.srv.acc.UserByID(context.Background(), se.uid)
		if err == nil {
			err = se.srv.acc.CheckBindHost(u, n.BindHost)
		}
		if err != nil {
			se.sayErr(err)
			return
		}
	case "trustallcerts":
		if !bool_(&n.TrustAllCerts) {
			return
		}
	case "trustpki":
		if !bool_(&n.TrustPKI) {
			return
		}
	case "floodrate":
		v, err := strconv.ParseFloat(value, 64)
		if err != nil {
			se.say("set.floodrate")
			return
		}
		n.FloodRate = v
	case "floodburst", "joindelay", "authtimeout":
		v, err := strconv.Atoi(value)
		if err != nil {
			se.say("set.wholeNumber", "setting", field)
			return
		}
		switch field {
		case "floodburst":
			n.FloodBurst = v
		case "joindelay":
			n.JoinDelay = v
		default:
			n.AuthTimeout = v
		}
	case "nick":
		n.Nick = value
	case "ident":
		n.Username = value
		if clear {
			n.Username = ""
		}
	case "realname":
		n.Realname = value
		if clear {
			n.Realname = ""
		}
	case "password":
		n.Password = value
		if clear {
			n.Password = ""
		}
	}
	if err := accounts.NormalizeNetwork(&n); err != nil {
		var ve *accounts.ValidationError
		if errors.As(err, &ve) {
			se.sayErr(ve)
		} else {
			se.say("saveFailed", "error", err)
		}
		return
	}
	if err := se.srv.acc.CheckNetworkName(se.uid, n.Name, n.ID); err != nil {
		se.sayErr(err)
		return
	}
	if err := se.srv.acc.UpdateNetwork(context.Background(), n); err != nil {
		se.say("saveFailed", "error", err)
		return
	}
	if n.ID == se.net.ID {
		se.net = n
	}
	se.srv.b.Reconfigure(n)
	shown := value
	switch {
	case clear:
		shown = se.tr("set.cleared")
	case field == "password" || field == "authcommand":
		shown = se.tr("set.hidden")
	}
	if accounts.NeedsReconnect(before, n) && se.srv.b.Status(n.ID) != upstream.StatusDisconnected {
		if err := se.srv.b.Reconnect(n.ID); err != nil {
			se.say("set.savedReconnectFailed", "setting", field, "value", shown, "error", err)
			return
		}
		se.say("set.savedReconnecting", "setting", field, "value", shown, "network", n.Name)
		return
	}
	se.say("set.saved", "setting", field, "value", shown, "network", n.Name)
}

func (se *session) cmdChannels([]string) {
	chans := se.net.Channels
	if se.up != nil {
		chans = se.up.Channels()
	}
	if len(chans) == 0 {
		se.say("channels.none", "network", se.net.Name)
		return
	}
	se.statusf("%s: %s", se.net.Name, strings.Join(chans, " "))
}

func (se *session) cmdPlayback(args []string) {
	if se.web != nil {
		se.say("web.history")
		return
	}
	if len(args) < 1 || len(args) > 2 {
		se.say("usage.playback")
		return
	}
	n := 50
	if len(args) == 2 {
		v, err := strconv.Atoi(args[1])
		if err != nil || v < 1 {
			se.say("playback.badLines", "lines", strconv.Quote(args[1]))
			return
		}
		n = min(v, maxPlayback)
	}
	if sent := se.playback(args[0], n); sent == 0 {
		se.say("playback.nothing", "buffer", args[0])
	} else {
		se.say("replayed", "count", sent, "buffer", args[0])
	}
}

func (se *session) cmdInfo([]string) {
	u, err := se.srv.acc.UserByID(context.Background(), se.uid)
	if err != nil {
		se.say("accountFailed", "error", err)
		return
	}
	lim := se.srv.acc.LimitsFor(u)
	nets, _ := se.srv.acc.CountNetworks(context.Background(), u.ID)
	of := func(n int) string {
		if n == 0 {
			return se.tr("info.unlimited")
		}
		return strconv.Itoa(n)
	}
	se.say("info.account", "user", u.Username, "role", se.tr("role."+u.Role), "network", se.net.Name, "nick", orElse(se.unick, se.net.Nick))
	se.say("info.limits", "networks", nets, "maxNetworks", of(lim.MaxNetworks), "clients", se.srv.b.Clients(u.ID),
		"maxClients", of(lim.MaxClients), "backlog", of(lim.BacklogLines))
}

func (se *session) cmdVersion([]string) {
	se.statusf("evrd %s", version.Version)
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// --- servers, fingerprints, detach ----------------------------------------------

// saveNetwork validates and saves n, reconnecting when needed.
func (se *session) saveNetwork(before, n store.Network, done string) {
	if err := accounts.NormalizeNetwork(&n); err != nil {
		se.sayErr(err)
		return
	}
	if err := se.srv.acc.UpdateNetwork(context.Background(), n); err != nil {
		se.say("saveFailed", "error", err)
		return
	}
	if n.ID == se.net.ID {
		se.net = n
	}
	se.srv.b.Reconfigure(n)
	if accounts.NeedsReconnect(before, n) && se.srv.b.Status(n.ID) != upstream.StatusDisconnected {
		if err := se.srv.b.Reconnect(n.ID); err != nil {
			se.say("saveNetwork.reconnectFailed", "done", done, "error", err)
			return
		}
		se.say("saveNetwork.reconnecting", "done", done)
		return
	}
	se.statusf("%s", done)
}

func (se *session) cmdListServers([]string) {
	n, ok := se.network(nil)
	if !ok {
		return
	}
	for i, sv := range n.ServerList() {
		extra := ""
		if sv.TLS {
			extra += " TLS"
		}
		if sv.Password != "" {
			extra += " " + se.tr("servers.password")
		}
		se.statusf("%d. %s %d%s", i+1, sv.Host, sv.Port, extra)
	}
}

func (se *session) cmdAddServer(args []string) {
	if len(args) < 1 || len(args) > 3 {
		se.say("usage.addserver")
		return
	}
	n, ok := se.network(nil)
	if !ok {
		return
	}
	sv := store.Server{Host: args[0], Port: 6697, TLS: true}
	if len(args) >= 2 {
		port, err := strconv.Atoi(strings.TrimPrefix(args[1], "+"))
		if err != nil || port < 1 || port > 65535 {
			se.say("badPort", "port", strconv.Quote(args[1]))
			return
		}
		sv.Port, sv.TLS = port, strings.HasPrefix(args[1], "+")
	}
	if len(args) == 3 {
		sv.Password = args[2]
	}
	before := n
	n.Servers = append(append([]store.Server(nil), n.ServerList()...), sv)
	se.saveNetwork(before, n, se.tr("addserver.done", "host", sv.Host, "port", sv.Port, "network", n.Name))
}

func (se *session) cmdDelServer(args []string) {
	if len(args) < 1 || len(args) > 2 {
		se.say("usage.delserver")
		return
	}
	n, ok := se.network(nil)
	if !ok {
		return
	}
	before := n
	var kept []store.Server
	removed := 0
	for _, sv := range n.ServerList() {
		if strings.EqualFold(sv.Host, args[0]) && (len(args) == 1 || args[1] == strconv.Itoa(sv.Port) || args[1] == "+"+strconv.Itoa(sv.Port)) {
			removed++
			continue
		}
		kept = append(kept, sv)
	}
	switch {
	case removed == 0:
		se.say("delserver.none", "network", n.Name, "host", args[0])
		return
	case len(kept) == 0:
		se.say("delserver.last", "network", n.Name)
		return
	}
	n.Servers = kept
	se.saveNetwork(before, n, se.tr("delserver.done", "host", args[0], "network", n.Name))
}

func (se *session) cmdListFingerprints([]string) {
	n, ok := se.network(nil)
	if !ok {
		return
	}
	if len(n.Fingerprints) == 0 {
		se.say("fingerprints.none", "network", n.Name)
		return
	}
	for _, f := range n.Fingerprints {
		se.statusf("%s", upstream.ColonHex(f))
	}
}

func (se *session) cmdAddFingerprint(args []string) {
	if len(args) != 1 {
		se.say("usage.addfingerprint")
		return
	}
	n, ok := se.network(nil)
	if !ok {
		return
	}
	before := n
	n.Fingerprints = append(append([]string(nil), n.Fingerprints...), args[0])
	se.saveNetwork(before, n, se.tr("fingerprints.added", "network", n.Name))
}

func (se *session) cmdDelFingerprint(args []string) {
	if len(args) != 1 {
		se.say("usage.delfingerprint")
		return
	}
	n, ok := se.network(nil)
	if !ok {
		return
	}
	before := n
	want := accounts.NormalizeFingerprint(args[0])
	n.Fingerprints = slices.DeleteFunc(append([]string(nil), n.Fingerprints...), func(f string) bool { return f == want })
	if len(n.Fingerprints) == len(before.Fingerprints) {
		se.say("fingerprints.notTrusted", "network", n.Name)
		return
	}
	se.saveNetwork(before, n, se.tr("fingerprints.removed", "network", n.Name))
}

func (se *session) cmdDetach(args []string) {
	if se.web != nil {
		se.say("web.detach")
		return
	}
	if len(args) != 1 {
		se.say("usage.detach")
		return
	}
	se.detach(strings.Split(args[0], ","))
}

func (se *session) cmdModules([]string) {
	n, ok := se.network(nil)
	if !ok {
		return
	}
	off := se.srv.b.ModulesOff()
	for _, m := range modules.All() {
		state := se.tr("modules.off")
		switch {
		case off[m.Name]:
			state = se.tr("modules.unavailable")
		case n.Modules[m.Name].Enabled:
			state = se.tr("modules.on")
		}
		se.statusf("%-10s %s — %s", m.Name, state, se.tr("module."+m.Name))
	}
	se.say("modules.footer")
}

// setModule changes a module's settings; done is what to say, given the
// network's name.
func (se *session) setModule(name string, f func(*store.ModuleConfig) error, done func(network string) string) {
	n, ok := se.network(nil)
	if !ok {
		return
	}
	info, ok := modules.Get(name)
	if !ok {
		se.say("modules.none", "module", strconv.Quote(name))
		return
	}
	before := n
	mods := map[string]store.ModuleConfig{}
	for k, v := range n.Modules {
		mods[k] = v
	}
	mc := mods[info.Name]
	settings := map[string]string{}
	for k, v := range mc.Settings {
		settings[k] = v
	}
	mc.Settings = settings
	if err := f(&mc); err != nil {
		se.statusf("%s", err)
		return
	}
	mods[info.Name] = mc
	cfgs := map[string]modules.Config{}
	for k, v := range mods {
		cfgs[k] = modules.Config{Enabled: v.Enabled, Settings: v.Settings}
	}
	if err := modules.Check(cfgs); err != nil {
		se.sayErr(err)
		return
	}
	n.Modules = mods
	se.saveNetwork(before, n, done(n.Name))
}

func (se *session) cmdLoadMod(args []string) {
	if len(args) != 1 {
		se.say("usage.loadmod")
		return
	}
	if se.srv.b.ModulesOff()[strings.ToLower(args[0])] {
		se.say("modules.switchedOff", "module", args[0])
		return
	}
	se.setModule(args[0], func(m *store.ModuleConfig) error { m.Enabled = true; return nil }, func(network string) string {
		return se.tr("modules.loaded", "module", strings.ToLower(args[0]), "network", network)
	})
}

func (se *session) cmdUnloadMod(args []string) {
	if len(args) != 1 {
		se.say("usage.unloadmod")
		return
	}
	se.setModule(args[0], func(m *store.ModuleConfig) error { m.Enabled = false; return nil }, func(network string) string {
		return se.tr("modules.unloaded", "module", strings.ToLower(args[0]), "network", network)
	})
}

func (se *session) cmdModSet(args []string) {
	if len(args) < 2 {
		se.say("usage.modset")
		return
	}
	value := strings.Join(args[2:], " ")
	se.setModule(args[0], func(m *store.ModuleConfig) error {
		info, _ := modules.Get(args[0])
		for _, st := range info.Settings {
			if st.Key == args[1] && st.Kind == "lines" {
				value = strings.Join(strings.Split(value, " | "), "\n")
			}
		}
		if value == "" {
			delete(m.Settings, args[1])
		} else {
			m.Settings[args[1]] = value
		}
		return nil
	}, func(network string) string {
		return se.tr("modules.set", "module", strings.ToLower(args[0]), "setting", args[1], "network", network)
	})
}
