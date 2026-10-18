package client

// *status is the bouncer's own user: /msg *status <command> controls evrd
// from any IRC client, like ZNC's *status.

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/4evrd/evrd/internal/accounts"
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
		{"jump", "<network>", "Switch this client to another network.", (*session).cmdJump},
		{"connect", "[network]", "Connect a network (default: this one).", (*session).cmdConnect},
		{"disconnect", "[network]", "Disconnect a network (default: this one). evrd stops reconnecting it.", (*session).cmdDisconnect},
		{"addnetwork", "<name> <host> [[+]port] [nick]", "Add a network. +port means TLS (default +6697).", (*session).cmdAddNetwork},
		{"delnetwork", "<network>", "Remove a network and its saved history.", (*session).cmdDelNetwork},
		{"show", "[network]", "A network's settings (default: this one).", (*session).cmdShow},
		{"set", "[network] <setting> <value>", "Change a setting: name, host, port ([+]port), tls on|off, nick, ident, realname, password (- clears).", (*session).cmdSet},
		{"channels", "", "The channels saved for this network (JOIN and PART update it).", (*session).cmdChannels},
		{"playback", "<channel|nick> [lines]", fmt.Sprintf("Replay a buffer's last lines (default 50, up to %d).", maxPlayback), (*session).cmdPlayback},
		{"info", "", "Your account, limits and attached clients.", (*session).cmdInfo},
		{"version", "", "The evrd version.", (*session).cmdVersion},
	}
}

// aliases map ZNC's names for the same commands.
var statusAliases = map[string]string{
	"listnetworks": "networks", "listnets": "networks", "jumpnetwork": "jump",
	"addnet": "addnetwork", "delnet": "delnetwork", "listchans": "channels",
	"shownetwork": "show", "setnetwork": "set",
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
	se.statusf("evrd commands — /msg %s <command>:", statusNick)
	for _, c := range statusCmds {
		se.statusf("  %-42s %s", strings.TrimSpace(c.name+" "+c.args), c.help)
	}
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
	se.statusf("%s. * = this client. jump <network> to switch.", plural(len(nets), "network"))
}

func (se *session) cmdJump(args []string) {
	if se.web != nil {
		se.statusf("In the web app, pick a network from the Networks page instead.")
		return
	}
	if len(args) != 1 {
		se.statusf("Usage: jump <network>")
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
	if len(args) < 2 || len(args) > 4 {
		se.statusf("Usage: addnetwork <name> <host> [[+]port] [nick] — e.g. addnetwork libera irc.libera.chat +6697")
		return
	}
	u, err := se.srv.b.Store().UserByID(se.uid)
	if err != nil {
		se.statusf("Couldn't load your account: %v", err)
		return
	}
	n := store.Network{UserID: u.ID, Name: args[0], Host: args[1], Port: 6697, TLS: true, Nick: u.DefaultNick, Realname: u.DefaultRealname, Channels: []string{}}
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
	tls, pw := "off", "not set"
	if n.TLS {
		tls = "on"
	}
	if n.Password != "" {
		pw = "set"
	}
	se.statusf("%s — %s", n.Name, se.srv.b.Status(n.ID))
	se.statusf("  host %s · port %d · tls %s", n.Host, n.Port, tls)
	se.statusf("  nick %s · ident %s · realname %s", n.Nick, orElse(n.Username, "(nick)"), orElse(n.Realname, "(nick)"))
	se.statusf("  password %s · channels %s", pw, orElse(strings.Join(n.Channels, " "), "none"))
}

// settings are the fields set can change.
var settings = map[string]bool{
	"name": true, "host": true, "port": true, "tls": true,
	"nick": true, "ident": true, "realname": true, "password": true,
}

func (se *session) cmdSet(args []string) {
	usage := func() {
		se.statusf("Usage: set [network] <setting> <value> — settings: name host port tls nick ident realname password")
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
	switch field {
	case "name":
		n.Name = value
	case "host":
		n.Host = value
	case "port":
		port, err := strconv.Atoi(strings.TrimPrefix(value, "+"))
		if err != nil || port < 1 || port > 65535 {
			se.statusf("%q isn't a port — use e.g. 6667, or +6697 for TLS.", value)
			return
		}
		n.Port = port
		if strings.HasPrefix(value, "+") {
			n.TLS = true
		}
	case "tls":
		switch strings.ToLower(value) {
		case "on", "yes", "true":
			n.TLS = true
		case "off", "no", "false":
			n.TLS = false
		default:
			se.statusf("tls is on or off.")
			return
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
	shown := value
	if field == "password" {
		shown = "(hidden)"
		if clear {
			shown = "(cleared)"
		}
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
