package client

// soju.im/bouncer-networks: clients (Goguma, gamja…) list, add, change and
// remove the user's networks, and open one connection per network with
// BOUNCER BIND. A client that asks for the capability and names no network
// gets a bouncer connection, bound to none: it manages networks, and opens
// others for chatting. With soju.im/bouncer-networks-notify it hears when
// networks come, change (state too) and go.
//
// https://codeberg.org/emersion/soju/src/branch/master/doc/ext/bouncer-networks.md

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/4evrd/evrd/internal/accounts"
	"github.com/4evrd/evrd/internal/store"
	"github.com/4evrd/evrd/internal/upstream"
)

// bouncerFail sends FAIL BOUNCER code sub [context…] :text.
func (se *session) bouncerFail(code, sub string, args ...string) {
	text := args[len(args)-1]
	args = append(args[:len(args)-1:len(args)-1], ":"+ircText(text))
	se.send(fmt.Sprintf(":%s FAIL BOUNCER %s %s %s", serverName, code, sub, strings.Join(args, " ")))
}

// bouncerCommand answers BOUNCER after registration.
func (se *session) bouncerCommand(p []string) {
	if len(p) == 0 || !se.caps["soju.im/bouncer-networks"] {
		sub := "*"
		if len(p) > 0 {
			sub = p[0]
		}
		se.bouncerFail("UNKNOWN_COMMAND", sub, "Unknown subcommand")
		return
	}
	sub := strings.ToUpper(p[0])
	switch sub {
	case "BIND":
		se.bouncerFail("REGISTRATION_IS_COMPLETED", sub, "BIND must come before registration ends")
	case "LISTNETWORKS":
		se.listNetworks()
	case "ADDNETWORK":
		if len(p) < 2 {
			se.bouncerFail("NEED_ATTRIBUTE", sub, "*", "host", "Missing attributes")
			return
		}
		se.addNetwork(parseAttrs(p[1]))
	case "CHANGENETWORK":
		if len(p) < 3 {
			se.bouncerFail("NEED_ATTRIBUTE", sub, "*", "Missing parameters")
			return
		}
		se.changeNetwork(p[1], parseAttrs(p[2]))
	case "DELNETWORK":
		if len(p) < 2 {
			se.bouncerFail("INVALID_NETID", sub, "*", "Missing network ID")
			return
		}
		n, ok := se.netByID(sub, p[1])
		if !ok {
			return
		}
		if err := se.srv.b.DeleteNetwork(se.uid, n.ID); err != nil {
			se.bouncerFail("UNKNOWN_ERROR", sub, p[1], err.Error())
			return
		}
		se.send(fmt.Sprintf(":%s BOUNCER DELNETWORK %d", serverName, n.ID))
		se.notifyNetworks()
		if se.up != nil && n.ID == se.net.ID {
			se.conn.Close() // its network is gone
		}
	default:
		se.bouncerFail("UNKNOWN_COMMAND", p[0], "Unknown subcommand")
	}
}

// netByID finds one of the user's networks by its netid, failing for sub.
func (se *session) netByID(sub, id string) (store.Network, bool) {
	nets, _ := se.srv.b.Store().ListNetworks(context.Background(), se.uid)
	for _, n := range nets {
		if strconv.FormatInt(n.ID, 10) == id {
			return n, true
		}
	}
	se.bouncerFail("INVALID_NETID", sub, id, "Unknown network ID")
	return store.Network{}, false
}

// networkAttrs are a network's attributes as bouncer-networks has them.
// pass is write-only.
func (se *session) networkAttrs(n store.Network) map[string]string {
	a := map[string]string{"name": n.Name, "nickname": n.Nick, "state": "disconnected"}
	if s := n.ServerList(); len(s) > 0 {
		a["host"], a["port"], a["tls"] = s[0].Host, strconv.Itoa(s[0].Port), "0"
		if s[0].TLS {
			a["tls"] = "1"
		}
	}
	if n.Username != "" {
		a["username"] = n.Username
	}
	if n.Realname != "" {
		a["realname"] = n.Realname
	}
	switch se.srv.b.Status(n.ID) {
	case upstream.StatusConnected:
		a["state"] = "connected"
	case upstream.StatusConnecting, upstream.StatusReconnecting:
		a["state"] = "connecting"
	}
	if c, ok := se.srv.b.ConnFor(n.ID); ok && a["state"] != "connected" {
		if e := c.Health().LastError; e != "" {
			a["error"] = e
		}
	}
	return a
}

// networks is every network's attributes, by netid.
func (se *session) bouncerNetworks() map[string]map[string]string {
	out := map[string]map[string]string{}
	nets, _ := se.srv.b.Store().ListNetworks(context.Background(), se.uid)
	for _, n := range nets {
		out[strconv.FormatInt(n.ID, 10)] = se.networkAttrs(n)
	}
	return out
}

// listNetworks answers LISTNETWORKS: every network, in a batch.
func (se *session) listNetworks() {
	nets := se.bouncerNetworks()
	batch := se.openBatch("soju.im/bouncer-networks")
	for _, id := range sortedKeys(nets) {
		se.send(se.batchTag(batch) + fmt.Sprintf(":%s BOUNCER NETWORK %s %s", serverName, id, encodeAttrs(nets[id])))
	}
	se.closeBatch(batch)
	se.netsSent = nets
}

// batchTag is the tag that puts a line in batch ("" for none).
func (se *session) batchTag(batch string) string {
	if batch == "" {
		return ""
	}
	return "@batch=" + batch + " "
}

// notifyNetworks tells a client with bouncer-networks-notify what changed
// since it last heard: new networks in full, changed attributes only (one
// without a value is gone), and * for a network removed.
func (se *session) notifyNetworks() {
	if !se.caps["soju.im/bouncer-networks-notify"] || se.netsSent == nil {
		return
	}
	now := se.bouncerNetworks()
	for _, id := range sortedKeys(now) {
		old, had := se.netsSent[id]
		if !had {
			se.send(fmt.Sprintf(":%s BOUNCER NETWORK %s %s", serverName, id, encodeAttrs(now[id])))
			continue
		}
		diff := map[string]string{}
		var gone []string
		for k, v := range now[id] {
			if old[k] != v {
				diff[k] = v
			}
		}
		for k := range old {
			if _, ok := now[id][k]; !ok {
				gone = append(gone, k)
			}
		}
		if len(diff) > 0 || len(gone) > 0 {
			attrs := encodeAttrs(diff)
			sort.Strings(gone)
			for _, k := range gone {
				attrs = strings.TrimPrefix(attrs+";"+k, ";")
			}
			se.send(fmt.Sprintf(":%s BOUNCER NETWORK %s %s", serverName, id, attrs))
		}
	}
	for _, id := range sortedKeys(se.netsSent) {
		if _, ok := now[id]; !ok {
			se.send(fmt.Sprintf(":%s BOUNCER NETWORK %s *", serverName, id))
		}
	}
	se.netsSent = now
}

// settable are the attributes a client may set; state and error are the
// bouncer's.
var settable = map[string]bool{"name": true, "host": true, "port": true, "tls": true, "nickname": true, "username": true, "realname": true, "pass": true}

// applyAttrs sets attributes on n, failing (for sub, network id) on the
// first bad one.
func (se *session) applyAttrs(n *store.Network, attrs map[string]string, sub, id string) bool {
	servers := append([]store.Server(nil), n.ServerList()...)
	if len(servers) == 0 {
		servers = []store.Server{{Port: 6697, TLS: true}}
	}
	sv := &servers[0]
	portSet := false
	for _, k := range sortedKeys(attrs) {
		v := attrs[k]
		if k == "state" || k == "error" {
			se.bouncerFail("READ_ONLY_ATTRIBUTE", sub, id, k, "Read-only attribute")
			return false
		}
		if !settable[k] {
			se.bouncerFail("UNKNOWN_ATTRIBUTE", sub, id, k, "Unknown attribute")
			return false
		}
		bad := false
		switch k {
		case "name":
			n.Name = v
		case "host":
			sv.Host = v
		case "port":
			p, err := strconv.Atoi(v)
			bad = err != nil || p < 1 || p > 65535
			sv.Port, portSet = p, true
		case "tls":
			bad = v != "0" && v != "1"
			sv.TLS = v == "1"
		case "nickname":
			n.Nick = v
		case "username":
			n.Username = v
		case "realname":
			n.Realname = v
		case "pass":
			sv.Password = v
		}
		if bad {
			se.bouncerFail("INVALID_ATTRIBUTE", sub, id, k, "Invalid "+k)
			return false
		}
	}
	if _, ok := attrs["tls"]; ok && !portSet && sub == "ADDNETWORK" {
		sv.Port = map[bool]int{true: 6697, false: 6667}[sv.TLS]
	}
	n.Servers = servers
	n.Host, n.Port, n.TLS = sv.Host, sv.Port, sv.TLS
	if err := accounts.NormalizeNetwork(n); err != nil {
		se.bouncerFail("INVALID_ATTRIBUTE", sub, id, "*", err.Error())
		return false
	}
	nets, _ := se.srv.b.Store().ListNetworks(context.Background(), se.uid)
	for _, o := range nets {
		if o.ID != n.ID && strings.EqualFold(o.Name, n.Name) {
			se.bouncerFail("INVALID_ATTRIBUTE", sub, id, "name", "You already have a network called "+n.Name)
			return false
		}
	}
	return true
}

// addNetwork answers ADDNETWORK: host is needed; the name defaults to it,
// TLS to on, the port to 6697 (6667 without TLS), the nick to the user's.
func (se *session) addNetwork(attrs map[string]string) {
	const sub = "ADDNETWORK"
	if attrs["host"] == "" {
		se.bouncerFail("NEED_ATTRIBUTE", sub, "*", "host", "A host is needed")
		return
	}
	u, err := se.srv.b.Store().UserByID(context.Background(), se.uid)
	if err != nil {
		se.bouncerFail("UNKNOWN_ERROR", sub, "*", err.Error())
		return
	}
	n := store.Network{UserID: u.ID, Name: attrs["host"], Nick: orElse(u.DefaultNick, u.Username), Realname: u.DefaultRealname, Channels: []string{}}
	if !se.applyAttrs(&n, attrs, sub, "*") {
		return
	}
	if err := se.srv.acc.CanCreateNetwork(u); err != nil {
		var ve *accounts.ValidationError
		if errors.As(err, &ve) {
			err = errors.New(capitalize(ve.Message))
		}
		se.bouncerFail("UNKNOWN_ERROR", sub, "*", err.Error())
		return
	}
	n, err = se.srv.b.Store().CreateNetwork(context.Background(), n)
	if err != nil {
		se.bouncerFail("UNKNOWN_ERROR", sub, "*", err.Error())
		return
	}
	se.srv.b.Connect(n.ID)
	se.send(fmt.Sprintf(":%s BOUNCER ADDNETWORK %d", serverName, n.ID))
	se.notifyNetworks()
}

// changeNetwork answers CHANGENETWORK, reconnecting when the change needs it.
func (se *session) changeNetwork(id string, attrs map[string]string) {
	const sub = "CHANGENETWORK"
	n, ok := se.netByID(sub, id)
	if !ok {
		return
	}
	before := n
	if !se.applyAttrs(&n, attrs, sub, id) {
		return
	}
	if err := se.srv.b.Store().UpdateNetwork(context.Background(), n); err != nil {
		se.bouncerFail("UNKNOWN_ERROR", sub, id, err.Error())
		return
	}
	if se.up != nil && n.ID == se.net.ID {
		se.net = n
	}
	se.srv.b.Reconfigure(n)
	if accounts.NeedsReconnect(before, n) && se.srv.b.Status(n.ID) != upstream.StatusDisconnected {
		se.srv.b.Reconnect(n.ID)
	}
	se.send(fmt.Sprintf(":%s BOUNCER CHANGENETWORK %s", serverName, id))
	se.notifyNetworks()
}

// attrEscaper escapes an attribute value like a message tag's.
var attrEscaper = strings.NewReplacer(`\`, `\\`, ";", `\:`, " ", `\s`, "\r", `\r`, "\n", `\n`)

func encodeAttrs(attrs map[string]string) string {
	var out []string
	for _, k := range sortedKeys(attrs) {
		out = append(out, k+"="+attrEscaper.Replace(attrs[k]))
	}
	return strings.Join(out, ";")
}

func parseAttrs(s string) map[string]string {
	out := map[string]string{}
	for _, kv := range strings.Split(s, ";") {
		if kv == "" {
			continue
		}
		k, v, _ := strings.Cut(kv, "=")
		out[k] = unescapeTag(v)
	}
	return out
}

// unescapeTag undoes a message tag value's escaping.
func unescapeTag(v string) string {
	var b strings.Builder
	for i := 0; i < len(v); i++ {
		if v[i] != '\\' || i+1 == len(v) {
			if v[i] != '\\' {
				b.WriteByte(v[i])
			}
			continue
		}
		i++
		switch v[i] {
		case ':':
			b.WriteByte(';')
		case 's':
			b.WriteByte(' ')
		case 'r':
			b.WriteByte('\r')
		case 'n':
			b.WriteByte('\n')
		default:
			b.WriteByte(v[i])
		}
	}
	return b.String()
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
