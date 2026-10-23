package client

// *status is the bouncer's own user: /msg *status <command> controls evrd
// from any IRC client, like ZNC's *status.

import (
	"cmp"
	"errors"
	"fmt"
	"github.com/4evrd/evrd/internal/modules"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/4evrd/evrd/internal/accounts"
	"github.com/4evrd/evrd/internal/directory"
	"github.com/4evrd/evrd/internal/store"
	"github.com/4evrd/evrd/internal/upstream"
	"github.com/4evrd/evrd/internal/version"
)

const statusNick = "*status"

// maxPlayback caps how many lines one playback sends.
const maxPlayback = 500

type statusCmd struct {
	name, args, help string
	run              func(se *session, args []string)
}

var statusCmds []statusCmd

func init() {
	statusCmds = []statusCmd{
		{"help", "", "This list.", (*session).cmdHelp},
		{"networks", "", "Your networks, their status, and which one you're on.", (*session).cmdNetworks},
		{"jumpnetwork", "<network>", "Switch this client to another network.", (*session).cmdJumpNetwork},
		{"jump", "[server|network]", "Reconnect to the next server (or the one named); a network name switches to that network.", (*session).cmdJump},
		{"connect", "[network]", "Connect a network (default: this one).", (*session).cmdConnect},
		{"disconnect", "[network]", "Disconnect a network (default: this one). evrd stops reconnecting it.", (*session).cmdDisconnect},
		{"addnetwork", "<name> <host> [[+]port] [nick] | <id>", "Add a network. +port means TLS (default +6697). With just an id, one from the directory.", (*session).cmdAddNetwork},
		{"directory", "[search]", "Public networks you can add by id (addnetwork <id>).", (*session).cmdDirectory},
		{"delnetwork", "<network>", "Remove a network and its saved history.", (*session).cmdDelNetwork},
		{"show", "[network]", "A network's settings (default: this one).", (*session).cmdShow},
		{"set", "[network] <setting> <value>", "Change a network setting (listed below the table; - clears).", (*session).cmdSet},
		{"listservers", "", "This network's servers, in failover order.", (*session).cmdListServers},
		{"addserver", "<host> [[+]port] [password]", "Add a server (tried in turn when the link drops). +port means TLS.", (*session).cmdAddServer},
		{"delserver", "<host> [port]", "Remove a server.", (*session).cmdDelServer},
		{"listfingerprints", "", "Certificate fingerprints trusted for this network.", (*session).cmdListFingerprints},
		{"addfingerprint", "<sha-256>", "Trust a server certificate by its SHA-256 fingerprint.", (*session).cmdAddFingerprint},
		{"delfingerprint", "<sha-256>", "Stop trusting a certificate fingerprint.", (*session).cmdDelFingerprint},
		{"detach", "<#channel>[,…]", "Hide channels from IRC clients; evrd stays in them and keeps logging. /join or attach brings them back.", (*session).cmdDetach},
		{"attach", "<#channel>[,…]", "Show detached channels again, with what you missed.", (*session).cmdAttach},
		{"disablechan", "<#channel>[,…]", "Leave channels but keep them saved (with their key).", (*session).cmdDisableChan},
		{"enablechan", "<#channel>[,…]", "Join disabled channels again.", (*session).cmdEnableChan},
		{"channels", "", "The channels saved for this network (JOIN and PART update it).", (*session).cmdChannels},
		{"showchan", "<#channel>", "A channel's state, settings and buffer.", (*session).cmdShowChan},
		{"listnicks", "<#channel>", "Who's in a channel.", (*session).cmdListNicks},
		{"topics", "", "The topics of your channels.", (*session).cmdTopics},
		{"buffers", "", "This network's buffers: lines played back, auto-clear, what's waiting.", (*session).cmdBuffers},
		{"playbuffer", "<channel|nick>", "Play a buffer back, as on attach (after its clear mark).", (*session).cmdPlayBuffer},
		{"playback", "<channel|nick> [lines]", fmt.Sprintf("Replay a buffer's last lines from history, cleared or not (default 50, up to %d).", maxPlayback), (*session).cmdPlayback},
		{"clearbuffer", "<channel|nick>[,…]", "Clear buffers: playback starts after now (history keeps the lines). Wildcards work.", (*session).cmdClearBuffer},
		{"clearallbuffers", "", "Clear every buffer on this network.", (*session).cmdClearAllBuffers},
		{"clearallchannelbuffers", "", "Clear every channel buffer on this network.", (*session).cmdClearAllChannelBuffers},
		{"clearallquerybuffers", "", "Clear every query buffer on this network.", (*session).cmdClearAllQueryBuffers},
		{"setbuffer", "<channel|nick|*> [lines|-]", fmt.Sprintf("How many lines a buffer plays back (0–%d; * for this network's default; - for the default).", maxPlayback), (*session).cmdSetBuffer},
		{"autoclear", "<channel|nick>[,…] on|off|-", "Play back only what's new (on) or the last lines every time (off); - for your account's setting.", (*session).cmdAutoClear},
		{"modules", "", "This network's modules: which are on, and what each does.", (*session).cmdModules},
		{"loadmod", "<module>", "Switch a module on for this network.", (*session).cmdLoadMod},
		{"unloadmod", "<module>", "Switch a module off for this network.", (*session).cmdUnloadMod},
		{"modset", "<module> <setting> <value>", "Change a module's setting (lines: separate them with \" | \").", (*session).cmdModSet},
		{"setbindhost", "<host>", "This network's local address.", (*session).cmdSetBindHost},
		{"clearbindhost", "", "Back to your account's bind host.", (*session).cmdClearBindHost},
		{"showbindhost", "", "This network's and your account's bind hosts.", (*session).cmdShowBindHost},
		{"setuserbindhost", "<host>", "Your account's bind host (for networks without their own).", (*session).cmdSetUserBindHost},
		{"clearuserbindhost", "", "Clear your account's bind host.", (*session).cmdClearUserBindHost},
		{"info", "", "Your account, limits and attached clients.", (*session).cmdInfo},
		{"listclients", "", "Your attached IRC clients and web chats.", (*session).cmdListClients},
		{"uptime", "", "How long evrd has been running.", (*session).cmdUptime},
		{"traffic", "", "Where to see this network's traffic.", (*session).cmdTraffic},
		{"listusers", "", "Admins: every user.", (*session).cmdListUsers},
		{"listallusernetworks", "", "Admins: every user's networks.", (*session).cmdListAllUserNetworks},
		{"version", "", "The evrd version.", (*session).cmdVersion},
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
}

func (se *session) statusCommand(text string) {
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
	if why, ok := notHere[name]; ok {
		se.statusf("%s", why)
		return
	}
	se.statusf("Unknown command %q — try help.", fields[0])
}

// WebStatus runs a *status command for the web chat, on the given network,
// passing each reply line to out.
func (s *Server) WebStatus(userID int64, n store.Network, text string, out func(string)) {
	se := &session{srv: s, uid: userID, net: n, web: out}
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
	se.send(fmt.Sprintf(":%s!status@%s PRIVMSG %s :%s", statusNick, serverName, se.unick, fmt.Sprintf(format, a...)))
}

func (se *session) cmdHelp([]string) {
	se.statusf("evrd commands — /msg %s <command> or /znc <command>:", statusNick)
	rows := [][]string{{"Command", "Arguments", "Description"}}
	for _, c := range statusCmds {
		rows = append(rows, []string{strings.ToUpper(c.name), c.args, c.help})
	}
	for _, l := range markdownTable(rows) {
		se.statusf("%s", l)
	}
	se.statusf("SET's settings: %s.", strings.Join(settingNames, ", "))
	se.statusf("Commands are case-insensitive.")
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
	nets, err := se.srv.b.Store().ListNetworks(se.uid)
	if err != nil {
		se.statusf("Couldn't load your networks: %v", err)
		return nil, false
	}
	return nets, true
}

// network resolves a name argument, defaulting to the current network.
func (se *session) network(args []string) (store.Network, bool) {
	if len(args) == 0 {
		n, err := se.srv.b.Store().NetworkByID(se.net.ID)
		if err != nil {
			se.statusf("This network no longer exists.")
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
		se.statusf("No network called %q — see networks.", args[0])
	}
	return n, ok
}

func (se *session) cmdNetworks([]string) {
	nets, ok := se.networks()
	if !ok {
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
		se.statusf("%s%-16s %-12s as %-14s %s:%s%d · %s", here, n.Name, se.srv.b.Status(n.ID), nick, n.Host, tls, n.Port, plural(len(n.Channels), "channel"))
	}
	se.statusf("%s. * = this client. jumpnetwork <network> to switch.", plural(len(nets), "network"))
}

func (se *session) cmdConnect(args []string) {
	n, ok := se.network(args)
	if !ok {
		return
	}
	if st := se.srv.b.Status(n.ID); st != upstream.StatusDisconnected {
		se.statusf("%s is already %s.", n.Name, st)
		return
	}
	if err := se.srv.b.Connect(n.ID); err != nil {
		se.statusf("Couldn't connect %s: %v", n.Name, err)
		return
	}
	se.statusf("Connecting to %s (%s:%d)…", n.Name, n.Host, n.Port)
}

func (se *session) cmdDisconnect(args []string) {
	n, ok := se.network(args)
	if !ok {
		return
	}
	if se.srv.b.Status(n.ID) == upstream.StatusDisconnected {
		se.statusf("%s isn't connected.", n.Name)
		return
	}
	se.srv.b.Disconnect(n.ID)
	if n.ID == se.net.ID {
		se.statusf("Disconnected from %s. You're still attached — connect to bring it back.", n.Name)
	} else {
		se.statusf("Disconnected from %s.", n.Name)
	}
}

func (se *session) cmdAddNetwork(args []string) {
	if len(args) < 1 || len(args) > 4 {
		se.statusf("Usage: addnetwork <name> <host> [[+]port] [nick] — e.g. addnetwork libera irc.libera.chat +6697 — or addnetwork <id> for a network in the directory")
		return
	}
	u, err := se.srv.b.Store().UserByID(se.uid)
	if err != nil {
		se.statusf("Couldn't load your account: %v", err)
		return
	}
	n := store.Network{UserID: u.ID, Name: args[0], Port: 6697, TLS: true, Nick: u.DefaultNick, Realname: u.DefaultRealname, Channels: []string{}}
	if len(args) == 1 {
		// one word: a network from the directory, with its servers and
		// suggested channels
		d, ok := se.srv.directory().Lookup(args[0])
		if !ok {
			se.statusf("No network %q in the directory — see directory, or addnetwork <name> <host> [[+]port] [nick].", args[0])
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
			se.statusf("%q isn't a port — use e.g. 6667, or +6697 for TLS.", p)
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
		se.statusf("You already have a network called %s.", n.Name)
		return
	}
	if err := se.srv.acc.CanCreateNetwork(u); err != nil {
		var ve *accounts.ValidationError
		if errors.As(err, &ve) {
			se.statusf("%s.", capitalize(ve.Message))
		} else {
			se.statusf("Couldn't add it: %v", err)
		}
		return
	}
	n, err = se.srv.b.Store().CreateNetwork(n)
	if err != nil {
		se.statusf("Couldn't add it: %v", err)
		return
	}
	tls := ""
	if n.TLS {
		tls = "+"
	}
	se.statusf("Added %s (%s:%s%d, nick %s). connect %s to bring it online, then jump %s.", n.Name, n.Host, tls, n.Port, n.Nick, n.Name, n.Name)
}

// cmdDirectory lists the network directory, or the networks matching a
// search.
func (se *session) cmdDirectory(args []string) {
	all := se.srv.directory().Networks()
	if len(all) == 0 {
		se.statusf("The network directory is empty here (an admin can set it up: network_directory).")
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
		se.statusf("No network in the directory matches %q.", strings.Join(args, " "))
		return
	}
	se.statusf("%s. addnetwork <id> adds one.", plural(len(nets), "network"))
}

func (se *session) cmdDelNetwork(args []string) {
	if len(args) != 1 {
		se.statusf("Usage: delnetwork <network>")
		return
	}
	n, ok := se.network(args)
	if !ok {
		return
	}
	if n.ID == se.net.ID {
		se.statusf("You're on %s — jump to another network first.", n.Name)
		return
	}
	if err := se.srv.b.DeleteNetwork(se.uid, n.ID); err != nil {
		se.statusf("Couldn't remove %s: %v", n.Name, err)
		return
	}
	se.statusf("Removed %s and its history.", n.Name)
}

func (se *session) cmdShow(args []string) {
	if len(args) > 1 {
		se.statusf("Usage: show [network]")
		return
	}
	n, ok := se.network(args)
	if !ok {
		return
	}
	pw := "not set"
	if n.Password != "" {
		pw = "set"
	}
	se.statusf("%s — %s", n.Name, se.srv.b.Status(n.ID))
	for i, sv := range n.ServerList() {
		tls := ""
		if sv.TLS {
			tls = " (TLS)"
		}
		se.statusf("  server %d: %s:%d%s", i+1, sv.Host, sv.Port, tls)
	}
	se.statusf("  nick %s · altnick %s · ident %s · realname %s", n.Nick, orElse(n.AltNick, "(default)"),
		orElse(n.Username, "(nick)"), orElse(n.Realname, "(nick)"))
	se.statusf("  sasl password %s · sasl user %s · bindhost %s", pw, orElse(n.SASLUser, "(ident)"), orElse(n.BindHost, "(default)"))
	se.statusf("  auth %s · servicesnick %s · identifyargs %s · identifyon %s · ghost %s · joinafterauth %s · authtimeout %ds",
		n.Auth(), orElse(n.ServicesNick, "NickServ"), orElse(n.IdentifyArgs, "auto"), orElse(n.IdentifyOn, "connect"),
		orElse(n.GhostCmd, "off"), onOff(n.JoinAfterAuth), cmp.Or(n.AuthTimeout, store.DefaultAuthTimeout))
	if n.AuthCommand != "" {
		se.statusf("  authcommand %s", n.AuthCommand)
	}
	se.statusf("  quitmsg %s · encoding %s", orElse(n.QuitMsg, "(default)"), orElse(n.Encoding, "UTF-8"))
	se.statusf("  trustallcerts %s · trustpki %s · %d trusted fingerprint(s)", onOff(n.TrustAllCerts), onOff(n.TrustPKI), len(n.Fingerprints))
	se.statusf("  floodrate %g · floodburst %d · joindelay %ds", n.FloodRate, n.FloodBurst, n.JoinDelay)
	se.statusf("  channels %s", orElse(strings.Join(n.Channels, " "), "none"))
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
		se.statusf("Usage: set [network] <setting> <value> — settings: name host port tls nick ident realname password auth …")
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
			se.statusf("%s is on or off.", field)
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
			se.statusf("%q isn't a port — use e.g. 6667, or +6697 for TLS.", value)
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
		u, err := se.srv.b.Store().UserByID(se.uid)
		if err == nil {
			err = se.srv.acc.CheckBindHost(u, n.BindHost)
		}
		if err != nil {
			se.statusf("%s.", capitalize(err.Error()))
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
			se.statusf("floodrate is lines per second, e.g. 2 (0 turns it off).")
			return
		}
		n.FloodRate = v
	case "floodburst", "joindelay", "authtimeout":
		v, err := strconv.Atoi(value)
		if err != nil {
			se.statusf("%s is a whole number.", field)
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
			se.statusf("%s.", capitalize(ve.Message))
		} else {
			se.statusf("Couldn't save: %v", err)
		}
		return
	}
	if err := se.srv.acc.CheckNetworkName(se.uid, n.Name, n.ID); err != nil {
		se.statusf("%s.", capitalize(err.Error()))
		return
	}
	if err := se.srv.b.Store().UpdateNetwork(n); err != nil {
		se.statusf("Couldn't save: %v", err)
		return
	}
	if n.ID == se.net.ID {
		se.net = n
	}
	se.srv.b.Reconfigure(n)
	shown := value
	switch {
	case clear:
		shown = "(cleared)"
	case field == "password" || field == "authcommand":
		shown = "(hidden)"
	}
	if accounts.NeedsReconnect(before, n) && se.srv.b.Status(n.ID) != upstream.StatusDisconnected {
		if err := se.srv.b.Reconnect(n.ID); err != nil {
			se.statusf("Saved %s = %s, but reconnecting failed: %v", field, shown, err)
			return
		}
		se.statusf("Saved %s = %s on %s. Reconnecting to apply it…", field, shown, n.Name)
		return
	}
	se.statusf("Saved %s = %s on %s.", field, shown, n.Name)
}

func (se *session) cmdChannels([]string) {
	chans := se.net.Channels
	if se.up != nil {
		chans = se.up.Channels()
	}
	if len(chans) == 0 {
		se.statusf("No channels on %s yet. /join one and it's saved.", se.net.Name)
		return
	}
	se.statusf("%s: %s", se.net.Name, strings.Join(chans, " "))
}

func (se *session) cmdPlayback(args []string) {
	if se.web != nil {
		se.statusf("The web chat already shows each buffer's history — scroll up.")
		return
	}
	if len(args) < 1 || len(args) > 2 {
		se.statusf("Usage: playback <channel|nick> [lines]")
		return
	}
	n := 50
	if len(args) == 2 {
		v, err := strconv.Atoi(args[1])
		if err != nil || v < 1 {
			se.statusf("%q isn't a number of lines.", args[1])
			return
		}
		n = min(v, maxPlayback)
	}
	if sent := se.playback(args[0], n); sent == 0 {
		se.statusf("Nothing saved for %s.", args[0])
	} else {
		se.statusf("Replayed %d line(s) of %s.", sent, args[0])
	}
}

func (se *session) cmdInfo([]string) {
	u, err := se.srv.b.Store().UserByID(se.uid)
	if err != nil {
		se.statusf("Couldn't load your account: %v", err)
		return
	}
	lim := se.srv.acc.LimitsFor(u)
	nets, _ := se.srv.b.Store().CountNetworks(u.ID)
	of := func(n int) string {
		if n == 0 {
			return "unlimited"
		}
		return strconv.Itoa(n)
	}
	se.statusf("Account %s (%s). On %s as %s.", u.Username, u.Role, se.net.Name, orElse(se.unick, se.net.Nick))
	se.statusf("Networks: %d of %s. Clients attached: %d of %s. Backlog: %s lines per buffer.",
		nets, of(lim.MaxNetworks), se.srv.b.Clients(u.ID), of(lim.MaxClients), of(lim.BacklogLines))
}

func (se *session) cmdVersion([]string) {
	se.statusf("evrd %s", version.Version)
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
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
		se.statusf("%s.", capitalize(err.Error()))
		return
	}
	if err := se.srv.b.Store().UpdateNetwork(n); err != nil {
		se.statusf("Couldn't save: %v", err)
		return
	}
	if n.ID == se.net.ID {
		se.net = n
	}
	se.srv.b.Reconfigure(n)
	if accounts.NeedsReconnect(before, n) && se.srv.b.Status(n.ID) != upstream.StatusDisconnected {
		if err := se.srv.b.Reconnect(n.ID); err != nil {
			se.statusf("%s But reconnecting failed: %v", done, err)
			return
		}
		se.statusf("%s Reconnecting to apply it…", done)
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
			extra += " (password set)"
		}
		se.statusf("%d. %s %d%s", i+1, sv.Host, sv.Port, extra)
	}
}

func (se *session) cmdAddServer(args []string) {
	if len(args) < 1 || len(args) > 3 {
		se.statusf("Usage: addserver <host> [[+]port] [password]")
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
			se.statusf("%q isn't a port — use e.g. 6667, or +6697 for TLS.", args[1])
			return
		}
		sv.Port, sv.TLS = port, strings.HasPrefix(args[1], "+")
	}
	if len(args) == 3 {
		sv.Password = args[2]
	}
	before := n
	n.Servers = append(append([]store.Server(nil), n.ServerList()...), sv)
	se.saveNetwork(before, n, fmt.Sprintf("Added %s:%d to %s. It's used when the link drops.", sv.Host, sv.Port, n.Name))
}

func (se *session) cmdDelServer(args []string) {
	if len(args) < 1 || len(args) > 2 {
		se.statusf("Usage: delserver <host> [port]")
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
		se.statusf("%s has no server %s.", n.Name, args[0])
		return
	case len(kept) == 0:
		se.statusf("That's %s's only server — add another first.", n.Name)
		return
	}
	n.Servers = kept
	se.saveNetwork(before, n, fmt.Sprintf("Removed %s from %s.", args[0], n.Name))
}

func (se *session) cmdListFingerprints([]string) {
	n, ok := se.network(nil)
	if !ok {
		return
	}
	if len(n.Fingerprints) == 0 {
		se.statusf("No trusted fingerprints on %s.", n.Name)
		return
	}
	for _, f := range n.Fingerprints {
		se.statusf("%s", upstream.ColonHex(f))
	}
}

func (se *session) cmdAddFingerprint(args []string) {
	if len(args) != 1 {
		se.statusf("Usage: addfingerprint <sha-256>")
		return
	}
	n, ok := se.network(nil)
	if !ok {
		return
	}
	before := n
	n.Fingerprints = append(append([]string(nil), n.Fingerprints...), args[0])
	se.saveNetwork(before, n, fmt.Sprintf("Trusting that certificate on %s.", n.Name))
}

func (se *session) cmdDelFingerprint(args []string) {
	if len(args) != 1 {
		se.statusf("Usage: delfingerprint <sha-256>")
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
		se.statusf("That fingerprint isn't trusted on %s.", n.Name)
		return
	}
	se.saveNetwork(before, n, fmt.Sprintf("No longer trusting that certificate on %s.", n.Name))
}

func (se *session) cmdDetach(args []string) {
	if se.web != nil {
		se.statusf("Detaching hides channels from IRC clients; the web chat shows everything.")
		return
	}
	if len(args) != 1 {
		se.statusf("Usage: detach <#channel>[,…]")
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
		state := "off"
		switch {
		case off[m.Name]:
			state = "unavailable (switched off by an admin)"
		case n.Modules[m.Name].Enabled:
			state = "ON"
		}
		se.statusf("%-10s %s — %s", m.Name, state, m.Description)
	}
	se.statusf("loadmod <module> / unloadmod <module> switch one on or off; modset <module> <setting> <value> changes a setting (or use the web app).")
}

func (se *session) setModule(name string, f func(*store.ModuleConfig) error, done string) {
	n, ok := se.network(nil)
	if !ok {
		return
	}
	info, ok := modules.Get(name)
	if !ok {
		se.statusf("No module %q. modules lists them.", name)
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
		se.statusf("%s.", capitalize(err.Error()))
		return
	}
	n.Modules = mods
	se.saveNetwork(before, n, strings.ReplaceAll(done, "%n", n.Name))
}

func (se *session) cmdLoadMod(args []string) {
	if len(args) != 1 {
		se.statusf("Usage: loadmod <module>")
		return
	}
	if se.srv.b.ModulesOff()[strings.ToLower(args[0])] {
		se.statusf("An admin has switched %s off on this server.", args[0])
		return
	}
	se.setModule(args[0], func(m *store.ModuleConfig) error { m.Enabled = true; return nil }, strings.ToLower(args[0])+" is on for %n.")
}

func (se *session) cmdUnloadMod(args []string) {
	if len(args) != 1 {
		se.statusf("Usage: unloadmod <module>")
		return
	}
	se.setModule(args[0], func(m *store.ModuleConfig) error { m.Enabled = false; return nil }, strings.ToLower(args[0])+" is off for %n.")
}

func (se *session) cmdModSet(args []string) {
	if len(args) < 2 {
		se.statusf("Usage: modset <module> <setting> <value> (an empty value goes back to the default)")
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
	}, fmt.Sprintf("Set %s's %s on %%n.", strings.ToLower(args[0]), args[1]))
}
