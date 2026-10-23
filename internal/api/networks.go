package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/4evrd/evrd/internal/accounts"
	"github.com/4evrd/evrd/internal/auth"
	"github.com/4evrd/evrd/internal/bouncer"
	"github.com/4evrd/evrd/internal/history"
	"github.com/4evrd/evrd/internal/irc"
	"github.com/4evrd/evrd/internal/modules"
	"github.com/4evrd/evrd/internal/store"
	"github.com/4evrd/evrd/internal/upstream"
)

func (s *Server) handleListNetworks(w http.ResponseWriter, r *http.Request) {
	nets, err := s.st().ListNetworks(r.Context(), auth.User(r).ID)
	if err != nil {
		s.fail(w, err)
		return
	}
	out := make([]map[string]any, 0, len(nets))
	for _, n := range nets {
		out = append(out, s.networkDTO(n))
	}
	writeJSON(w, http.StatusOK, out)
}

// networkInput is the editable part of a network, shared by create and
// update. Fields left out (nil) are unchanged; for passwords, "" clears.
type networkInput struct {
	Name          *string                           `json:"name"`
	Host          *string                           `json:"host"` // single-server shorthand
	Port          *int                              `json:"port"`
	TLS           *bool                             `json:"tls"`
	Servers       *[]serverInput                    `json:"servers"`
	Nick          *string                           `json:"nick"`
	AltNick       *string                           `json:"altNick"`
	Username      *string                           `json:"username"` // ident
	Realname      *string                           `json:"realname"`
	QuitMsg       *string                           `json:"quitMsg"`
	BindHost      *string                           `json:"bindHost"`
	Password      *string                           `json:"password"` // SASL
	SASLUser      *string                           `json:"saslUser"`
	AuthMethod    *string                           `json:"authMethod"`
	ServicesNick  *string                           `json:"servicesNick"`
	IdentifyArgs  *string                           `json:"identifyArgs"`
	IdentifyOn    *string                           `json:"identifyOn"`
	AuthCommand   *string                           `json:"authCommand"`
	GhostCmd      *string                           `json:"ghost"`
	JoinAfterAuth *bool                             `json:"joinAfterAuth"`
	AuthTimeout   *int                              `json:"authTimeout"`
	TrustAllCerts *bool                             `json:"trustAllCerts"`
	TrustPKI      *bool                             `json:"trustPKI"`
	Fingerprints  *[]string                         `json:"fingerprints"`
	Encoding      *string                           `json:"encoding"`
	FloodRate     *float64                          `json:"floodRate"`
	FloodBurst    *int                              `json:"floodBurst"`
	JoinDelay     *int                              `json:"joinDelay"`
	Channels      *[]string                         `json:"channels"`
	ChannelOpts   *map[string]store.ChannelSettings `json:"channelSettings"`
	Modules       *map[string]store.ModuleConfig    `json:"modules"`
}

// serverInput is a server in an edit; a nil password keeps the one saved
// for that host:port.
type serverInput struct {
	Host     string  `json:"host"`
	Port     int     `json:"port"`
	TLS      bool    `json:"tls"`
	Password *string `json:"password"`
}

// apply copies the set fields onto n.
func (in networkInput) apply(n *store.Network) {
	if in.Modules != nil {
		n.Modules = *in.Modules
	}
	str := func(dst *string, v *string) {
		if v != nil {
			*dst = *v
		}
	}
	str(&n.Name, in.Name)
	str(&n.Nick, in.Nick)
	str(&n.AltNick, in.AltNick)
	str(&n.Username, in.Username)
	str(&n.Realname, in.Realname)
	str(&n.QuitMsg, in.QuitMsg)
	str(&n.BindHost, in.BindHost)
	str(&n.Password, in.Password)
	str(&n.SASLUser, in.SASLUser)
	str(&n.AuthMethod, in.AuthMethod)
	str(&n.ServicesNick, in.ServicesNick)
	str(&n.IdentifyArgs, in.IdentifyArgs)
	str(&n.IdentifyOn, in.IdentifyOn)
	str(&n.AuthCommand, in.AuthCommand)
	str(&n.GhostCmd, in.GhostCmd)
	if in.JoinAfterAuth != nil {
		n.JoinAfterAuth = *in.JoinAfterAuth
	}
	if in.AuthTimeout != nil {
		n.AuthTimeout = *in.AuthTimeout
	}
	str(&n.Encoding, in.Encoding)
	if in.Servers != nil {
		old := n.ServerList()
		var list []store.Server
		for _, sv := range *in.Servers {
			ns := store.Server{Host: sv.Host, Port: sv.Port, TLS: sv.TLS}
			if sv.Password != nil {
				ns.Password = *sv.Password
			} else {
				for _, o := range old {
					if strings.EqualFold(o.Host, sv.Host) && o.Port == sv.Port {
						ns.Password = o.Password
					}
				}
			}
			list = append(list, ns)
		}
		n.Servers = list
		if len(list) == 0 {
			n.Host = "" // an empty list is no servers, not "keep the old one"
		}
	} else if in.Host != nil || in.Port != nil || in.TLS != nil {
		// the single-server shorthand edits the first server
		list := append([]store.Server(nil), n.ServerList()...)
		if len(list) == 0 {
			list = []store.Server{{}}
		}
		str(&list[0].Host, in.Host)
		if in.Port != nil {
			list[0].Port = *in.Port
		}
		if in.TLS != nil {
			list[0].TLS = *in.TLS
		}
		n.Servers = list
	}
	if in.TrustAllCerts != nil {
		n.TrustAllCerts = *in.TrustAllCerts
	}
	if in.TrustPKI != nil {
		n.TrustPKI = *in.TrustPKI
	}
	if in.Fingerprints != nil {
		n.Fingerprints = *in.Fingerprints
	}
	if in.FloodRate != nil {
		n.FloodRate = *in.FloodRate
	}
	if in.FloodBurst != nil {
		n.FloodBurst = *in.FloodBurst
	}
	if in.JoinDelay != nil {
		n.JoinDelay = *in.JoinDelay
	}
	if in.Channels != nil {
		n.Channels = *in.Channels
	}
	if in.ChannelOpts != nil {
		m := map[string]store.ChannelSettings{}
		for k, v := range *in.ChannelOpts {
			m[strings.ToLower(k)] = v
		}
		n.ChannelSettings = m
	}
}

// checkNetwork validates n for u (including whether u may use its bind host).
func (s *Server) checkNetwork(u store.User, n *store.Network, before *store.Network) error {
	if err := accounts.NormalizeNetwork(n); err != nil {
		return err
	}
	if err := modules.Check(toModuleConfigs(n.Modules)); err != nil {
		return &accounts.ValidationError{Message: err.Error()}
	}
	if before == nil || n.BindHost != before.BindHost {
		if err := s.acc.CheckBindHost(u, n.BindHost); err != nil {
			return err
		}
	}
	id := int64(0)
	if before != nil {
		id = before.ID
	}
	return s.acc.CheckNetworkName(n.UserID, n.Name, id)
}

func (s *Server) handleCreateNetwork(w http.ResponseWriter, r *http.Request) {
	u := auth.User(r)
	var in networkInput
	if !decode(w, r, &in) {
		return
	}
	// New networks start from the user's defaults and ZNC's flood settings.
	n := store.Network{
		UserID: u.ID, Nick: u.DefaultNick, AltNick: "", Username: u.DefaultIdent,
		Realname: u.DefaultRealname, TrustPKI: true, FloodRate: 2, FloodBurst: 9,
	}
	in.apply(&n)
	if err := s.checkNetwork(u, &n, nil); err != nil {
		s.fail(w, err)
		return
	}
	if err := s.acc.CanCreateNetwork(u); err != nil {
		s.fail(w, err)
		return
	}
	n, err := s.st().CreateNetwork(r.Context(), n)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.networkDTO(n))
}

func (s *Server) handleGetNetwork(w http.ResponseWriter, r *http.Request) {
	n, ok := s.ownedNetwork(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, s.networkDTO(n))
}

// handleUpdateNetwork edits a network. Fields left out are unchanged; a
// password of "" clears it. Settings that need a new connection reconnect a
// running network; the rest apply live.
func (s *Server) handleUpdateNetwork(w http.ResponseWriter, r *http.Request) {
	n, ok := s.ownedNetwork(w, r)
	if !ok {
		return
	}
	var in networkInput
	if !decode(w, r, &in) {
		return
	}
	before := n
	in.apply(&n)
	if err := s.checkNetwork(auth.User(r), &n, &before); err != nil {
		s.fail(w, err)
		return
	}
	if err := s.st().UpdateNetwork(r.Context(), n); err != nil {
		s.fail(w, err)
		return
	}
	s.b.Reconfigure(n)
	reconnected := false
	if accounts.NeedsReconnect(before, n) && s.b.Status(n.ID) != "disconnected" {
		if err := s.b.Reconnect(n.ID); err != nil {
			s.fail(w, err)
			return
		}
		reconnected = true
	}
	d := s.networkDTO(n)
	d["reconnected"] = reconnected
	writeJSON(w, http.StatusOK, d)
}

// handleBindHosts lists the bind hosts the caller may pick.
func (s *Server) handleBindHosts(w http.ResponseWriter, r *http.Request) {
	u := auth.User(r)
	writeJSON(w, http.StatusOK, map[string]any{
		"hosts": orEmpty(s.acc.BindHosts()), "locked": u.DenySetBindHost && !u.IsAdmin(), "any": u.IsAdmin(),
		"current": u.BindHost,
	})
}

func (s *Server) handleDeleteNetwork(w http.ResponseWriter, r *http.Request) {
	n, ok := s.ownedNetwork(w, r)
	if !ok {
		return
	}
	if err := s.b.DeleteNetwork(n.UserID, n.ID); err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleConnect(w http.ResponseWriter, r *http.Request) {
	n, ok := s.ownedNetwork(w, r)
	if !ok {
		return
	}
	if err := s.b.Connect(n.ID); err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": string(s.b.Status(n.ID))})
}

func (s *Server) handleDisconnect(w http.ResponseWriter, r *http.Request) {
	n, ok := s.ownedNetwork(w, r)
	if !ok {
		return
	}
	s.b.Disconnect(n.ID)
	writeJSON(w, http.StatusOK, map[string]any{"status": string(s.b.Status(n.ID))})
}

func (s *Server) handleMessages(w http.ResponseWriter, r *http.Request) {
	n, ok := s.ownedNetwork(w, r)
	if !ok {
		return
	}
	msgs, err := s.b.History().Recent(n.ID, r.URL.Query().Get("target"), 200)
	if err != nil {
		s.fail(w, err)
		return
	}
	out := make([]map[string]any, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, messageDTO(m))
	}
	writeJSON(w, http.StatusOK, out)
}

// handleNetworkLog is a network's connection log: what evrd did and what
// the server said while connecting, newest last.
func (s *Server) handleNetworkLog(w http.ResponseWriter, r *http.Request) {
	n, ok := s.ownedNetwork(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, s.b.NetworkLog(n.ID))
}

// handleAdminNetworkLog is a user's network's connection log, for admins
// helping them (GET /api/admin/users/{id}/networks/{nid}/log).
func (s *Server) handleAdminNetworkLog(w http.ResponseWriter, r *http.Request) {
	n, ok := s.adminNetwork(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, s.b.NetworkLog(n.ID))
}

func (s *Server) handleBuffers(w http.ResponseWriter, r *http.Request) {
	n, ok := s.ownedNetwork(w, r)
	if !ok {
		return
	}
	conn, live := s.b.ConnFor(n.ID)
	out := []map[string]any{}
	seen := map[string]bool{}
	add := func(b bouncer.BufferState) {
		key := strings.ToLower(b.Name)
		if seen[key] {
			return
		}
		seen[key] = true
		d := map[string]any{
			"name": b.Name, "unread": b.Unread, "mentions": b.Mentions,
			"lastAt": unixT(b.LastAt), "readAt": unixT(b.ReadAt), "joined": false,
		}
		if live && isChannelName(b.Name) {
			d["joined"] = conn.Joined(b.Name)
			d["members"] = conn.MemberCount(b.Name)
			d["topic"] = conn.Topic(b.Name)
		}
		out = append(out, d)
	}
	for _, b := range s.b.Buffers(n.ID) {
		add(b)
	}
	// Channels joined (or saved) with nothing said yet still show.
	chans := n.Channels
	if live {
		chans = conn.Channels()
	}
	for _, ch := range chans {
		add(bouncer.BufferState{Name: ch})
	}
	writeJSON(w, http.StatusOK, out)
}

// handleChannel is a channel's live topic and members.
func (s *Server) handleChannel(w http.ResponseWriter, r *http.Request) {
	n, ok := s.ownedNetwork(w, r)
	if !ok {
		return
	}
	target := r.URL.Query().Get("target")
	out := map[string]any{"name": target, "topic": "", "members": []any{}, "joined": false}
	if conn, live := s.b.ConnFor(n.ID); live {
		out["topic"] = conn.Topic(target)
		out["members"] = conn.Members(target)
		out["joined"] = conn.Joined(target)
	}
	for _, b := range s.b.Buffers(n.ID) {
		if strings.EqualFold(b.Name, target) {
			out["readAt"] = unixT(b.ReadAt)
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// handleMarkRead marks a buffer ({"target"}) or the whole network read.
func (s *Server) handleMarkRead(w http.ResponseWriter, r *http.Request) {
	n, ok := s.ownedNetwork(w, r)
	if !ok {
		return
	}
	var in struct{ Target string }
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in)
	if err := s.b.MarkRead(n.ID, in.Target); err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func isChannelName(s string) bool { return s != "" && strings.ContainsRune("#&+!", rune(s[0])) }

// handleStream upgrades to a WebSocket: it pushes live messages and events,
// and accepts frames from the web chat:
//
//	{target, text}               a message
//	{target, text, kind:"action"} /me
//	{target, text, kind:"notice"} /notice
//	{raw}                        a raw IRC line (/join, /nick, /topic, /quote …)
//	{status}                     a *status command; replies come back as
//	                             messages from *status (not saved)
//	{target, typing}             the user is typing: active, paused or done
//	                             (sent on when the network takes message-tags)
//
// Others' typing comes back as {type:"typing", target, nick, state}.
// The session cookie authenticates it; the upgrader refuses other sites'
// pages.
func (s *Server) handleStream(w http.ResponseWriter, r *http.Request) {
	n, ok := s.ownedNetwork(w, r)
	if !ok {
		return
	}
	conn, ok := s.b.ConnFor(n.ID)
	if !ok {
		writeErr(w, http.StatusConflict, "network not connected")
		return
	}

	ws, err := s.up.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer ws.Close()
	// Closed when the user is stopped here (suspended, deleted, or moved to
	// another node); the web chat reconnects and lands on the new owner.
	att := s.b.TrackStream(auth.User(r).ID, bouncer.ClientInfo{
		Software: browserName(r.UserAgent()), Networks: []string{n.Name}, IP: s.clientAddr(r),
	}, func() { ws.Close() })
	defer att.Release()

	sub, unsub := conn.Subscribe()
	defer unsub()
	raw, unsubRaw := conn.SubscribeRaw()
	defer unsubRaw()
	// Tell the page it's subscribed: nothing sent from now on is missed.
	if err := ws.WriteJSON(map[string]string{"type": "ready"}); err != nil {
		return
	}

	// *status replies, written by the select loop below (the only writer).
	replies := make(chan history.Message, 256)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			var in struct{ Target, Text, Kind, Raw, Status, Typing string }
			if err := ws.ReadJSON(&in); err != nil {
				return
			}
			if in.Typing != "" {
				if typingStates[in.Typing] && in.Target != "" && !strings.ContainsAny(in.Target, " \r\n") {
					conn.SendTagMsg("web", "+typing="+in.Typing, in.Target)
				}
				continue
			}
			if in.Status != "" {
				s.webStatus(r, n, in.Status, replies)
				continue
			}
			if raw := strings.TrimSpace(in.Raw); raw != "" {
				if !strings.ContainsAny(raw, "\r\n") {
					conn.SendRaw(raw)
				}
				continue
			}
			if in.Target == "" || in.Text == "" || strings.ContainsAny(in.Target+in.Text, "\r\n") {
				continue
			}
			switch in.Kind {
			case "action":
				conn.SendFrom("", "PRIVMSG", in.Target, "\x01ACTION "+in.Text+"\x01")
			case "notice":
				conn.SendFrom("", "NOTICE", in.Target, in.Text)
			default:
				conn.SendMessage(in.Target, in.Text)
			}
		}
	}()

	for {
		select {
		case m, ok := <-sub:
			if !ok {
				return
			}
			if err := ws.WriteJSON(messageDTO(m)); err != nil {
				return
			}
		case l, ok := <-raw:
			if !ok {
				return
			}
			if t, ok := typingOf(l.Text, conn.Nick()); ok {
				if err := ws.WriteJSON(t); err != nil {
					return
				}
			}
		case m := <-replies:
			if err := ws.WriteJSON(messageDTO(m)); err != nil {
				return
			}
		case <-done:
			return
		}
	}
}

// typingStates are the +typing values (IRCv3 typing client tag).
var typingStates = map[string]bool{"active": true, "paused": true, "done": true}

// typingOf turns someone else's typing TAGMSG into the web chat's frame.
// (Their message ends it too; the page clears it when the message comes.)
func typingOf(line, me string) (map[string]string, bool) {
	if !strings.HasPrefix(line, "@") {
		return nil, false
	}
	m := irc.Parse(line)
	state := m.Tags["+typing"]
	nick, _, _ := strings.Cut(m.Prefix, "!")
	if m.Command != "TAGMSG" || !typingStates[state] || len(m.Params) == 0 || nick == "" || strings.EqualFold(nick, me) {
		return nil, false
	}
	return map[string]string{"type": "typing", "target": m.Params[0], "nick": nick, "state": state}, true
}

// webStatus runs a *status command typed in the web chat. The command sees
// the network as it is now (it may have been edited since the socket opened).
func (s *Server) webStatus(r *http.Request, n store.Network, text string, replies chan<- history.Message) {
	reply := func(line string) {
		select {
		case replies <- history.Message{Target: "*status", Sender: "*status", Kind: "message", Text: line, Time: time.Now()}:
		default: // a flood of output; drop rather than block the reader
		}
	}
	if s.status == nil {
		reply("*status isn't available here.")
		return
	}
	if fresh, err := s.st().NetworkByID(r.Context(), n.ID); err == nil {
		n = fresh
	}
	s.status.WebStatus(auth.User(r).ID, n, text, reply)
}

// ownedNetwork loads the {id} network if it belongs to the signed-in user,
// answering 404 itself otherwise.
func (s *Server) ownedNetwork(w http.ResponseWriter, r *http.Request) (store.Network, bool) {
	n, err := s.st().NetworkByID(r.Context(), pathID(r))
	if err != nil || n.UserID != auth.User(r).ID {
		writeErr(w, http.StatusNotFound, "not found")
		return store.Network{}, false
	}
	return n, true
}

func (s *Server) networkDTO(n store.Network) map[string]any {
	ch := n.Channels
	if ch == nil {
		ch = []string{}
	}
	nick := n.Nick
	health := map[string]any{"status": string(upstream.StatusDisconnected)}
	if c, ok := s.b.ConnFor(n.ID); ok {
		nick = c.Nick() // the live nick, after any /nick
		h := c.Health()
		health = map[string]any{
			"status": string(h.Status), "lastError": h.LastError, "lastErrorAt": unixT(h.LastErrorAt), "hint": h.Hint,
			"attempts": h.Attempts, "retryIn": int(h.RetryIn.Seconds()), "connectedAt": unixT(h.ConnectedAt),
			"server": h.Server, "untrustedFingerprint": h.UntrustedFingerprint, "auth": h.Auth,
		}
		if h.Lag >= 0 {
			health["lag"] = h.Lag.Milliseconds()
		}
	}
	unread, mentions := 0, 0
	for _, b := range s.b.Buffers(n.ID) {
		unread += b.Unread
		mentions += b.Mentions
	}
	d := map[string]any{
		"currentNick": nick,
		"id":          n.ID, "name": n.Name, "host": n.Host, "port": n.Port,
		"tls": n.TLS, "nick": n.Nick, "username": n.Username,
		"realname": n.Realname, "channels": ch, "enabled": n.Enabled,
		"hasPassword": n.Password != "",
		"servers":     serversDTO(n.ServerList()),
		"altNick":     n.AltNick, "quitMsg": n.QuitMsg, "bindHost": n.BindHost, "saslUser": n.SASLUser,
		"trustAllCerts": n.TrustAllCerts, "trustPKI": n.TrustPKI, "fingerprints": orEmpty(n.Fingerprints),
		"authMethod": n.Auth(), "servicesNick": n.ServicesNick, "identifyArgs": n.IdentifyArgs, "identifyOn": n.IdentifyOn,
		"authCommand": n.AuthCommand, "ghost": n.GhostCmd, "joinAfterAuth": n.JoinAfterAuth, "authTimeout": n.AuthTimeout,
		"encoding": n.Encoding, "floodRate": n.FloodRate, "floodBurst": n.FloodBurst, "joinDelay": n.JoinDelay,
		"channelSettings": orEmptyMap(n.ChannelSettings),
		"modules":         orEmptyModules(n.Modules),
		"unread":          unread, "mentions": mentions,
	}
	for k, v := range health {
		d[k] = v
	}
	return d
}

func unixT(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.Unix()
}

func messageDTO(m history.Message) map[string]any {
	return map[string]any{
		"target": m.Target, "sender": m.Sender, "kind": m.Kind,
		"text": m.Text, "extra": m.Extra, "time": m.Time.Unix(),
	}
}

// browserName is a short name for a web chat client: "Chrome on macOS".
func browserName(ua string) string {
	b := "Web"
	switch {
	case strings.Contains(ua, "Edg/"):
		b = "Edge"
	case strings.Contains(ua, "Firefox/"):
		b = "Firefox"
	case strings.Contains(ua, "Chrome/"):
		b = "Chrome"
	case strings.Contains(ua, "Safari/"):
		b = "Safari"
	}
	switch {
	case strings.Contains(ua, "iPhone"), strings.Contains(ua, "iPad"):
		return b + " on iOS"
	case strings.Contains(ua, "Android"):
		return b + " on Android"
	case strings.Contains(ua, "Mac OS X"):
		return b + " on macOS"
	case strings.Contains(ua, "Windows"):
		return b + " on Windows"
	case strings.Contains(ua, "Linux"):
		return b + " on Linux"
	}
	return b
}

// clientAddr is the browser's address (see auth.Manager.ClientIP).
func (s *Server) clientAddr(r *http.Request) string { return s.auth.ClientIP(r) }

// serversDTO hides server passwords (it says whether one is set).
func serversDTO(list []store.Server) []map[string]any {
	out := make([]map[string]any, 0, len(list))
	for _, sv := range list {
		out = append(out, map[string]any{"host": sv.Host, "port": sv.Port, "tls": sv.TLS, "hasPassword": sv.Password != ""})
	}
	return out
}

func orEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func orEmptyMap(m map[string]store.ChannelSettings) map[string]store.ChannelSettings {
	if m == nil {
		return map[string]store.ChannelSettings{}
	}
	return m
}

func toModuleConfigs(in map[string]store.ModuleConfig) map[string]modules.Config {
	out := map[string]modules.Config{}
	for k, v := range in {
		out[k] = modules.Config{Enabled: v.Enabled, Settings: v.Settings}
	}
	return out
}

func orEmptyModules(m map[string]store.ModuleConfig) map[string]store.ModuleConfig {
	if m == nil {
		return map[string]store.ModuleConfig{}
	}
	return m
}

// handleModules lists the modules networks can use, and which an admin has
// switched off server-wide.
func (s *Server) handleModules(w http.ResponseWriter, r *http.Request) {
	off := map[string]bool{}
	for _, n := range s.cfg().ModulesOff {
		off[strings.ToLower(n)] = true
	}
	out := []map[string]any{}
	for _, m := range modules.All() {
		out = append(out, map[string]any{
			"name": m.Name, "title": m.Title, "description": m.Description, "znc": m.ZNC,
			"settings": m.Settings, "off": off[m.Name],
		})
	}
	writeJSON(w, http.StatusOK, out)
}
