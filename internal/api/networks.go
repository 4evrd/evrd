package api

import (
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/4evrd/evrd/internal/accounts"
	"github.com/4evrd/evrd/internal/auth"
	"github.com/4evrd/evrd/internal/bouncer"
	"github.com/4evrd/evrd/internal/history"
	"github.com/4evrd/evrd/internal/store"
	"github.com/4evrd/evrd/internal/upstream"
)

func (s *Server) handleListNetworks(w http.ResponseWriter, r *http.Request) {
	nets, err := s.st().ListNetworks(auth.User(r).ID)
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

func (s *Server) handleCreateNetwork(w http.ResponseWriter, r *http.Request) {
	u := auth.User(r)
	var req struct {
		Name, Host                         string
		Port                               int
		TLS                                bool
		Nick, Username, Realname, Password string
		Channels                           []string
	}
	if !decode(w, r, &req) {
		return
	}
	n := store.Network{
		UserID: u.ID, Name: req.Name, Host: req.Host, Port: req.Port, TLS: req.TLS,
		Nick: req.Nick, Username: req.Username, Realname: req.Realname,
		Password: req.Password, Channels: req.Channels,
	}
	if strings.TrimSpace(n.Nick) == "" {
		n.Nick = u.DefaultNick
	}
	if strings.TrimSpace(n.Realname) == "" {
		n.Realname = u.DefaultRealname
	}
	if err := accounts.NormalizeNetwork(&n); err != nil {
		s.fail(w, err)
		return
	}
	if err := s.acc.CanCreateNetwork(u); err != nil {
		s.fail(w, err)
		return
	}
	if err := s.acc.CheckNetworkName(u.ID, n.Name, 0); err != nil {
		s.fail(w, err)
		return
	}
	n, err := s.st().CreateNetwork(n)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.networkDTO(n))
}

// handleUpdateNetwork edits a network. Fields left out are unchanged; a
// password of "" clears it. A connected network reconnects to apply.
func (s *Server) handleUpdateNetwork(w http.ResponseWriter, r *http.Request) {
	n, ok := s.ownedNetwork(w, r)
	if !ok {
		return
	}
	var in struct {
		Name     *string   `json:"name"`
		Host     *string   `json:"host"`
		Port     *int      `json:"port"`
		TLS      *bool     `json:"tls"`
		Nick     *string   `json:"nick"`
		Username *string   `json:"username"`
		Realname *string   `json:"realname"`
		Password *string   `json:"password"`
		Channels *[]string `json:"channels"`
	}
	if !decode(w, r, &in) {
		return
	}
	before := n
	set := func(dst *string, v *string) {
		if v != nil {
			*dst = *v
		}
	}
	set(&n.Name, in.Name)
	set(&n.Host, in.Host)
	set(&n.Nick, in.Nick)
	set(&n.Username, in.Username)
	set(&n.Realname, in.Realname)
	set(&n.Password, in.Password)
	if in.Port != nil {
		n.Port = *in.Port
	}
	if in.TLS != nil {
		n.TLS = *in.TLS
	}
	if in.Channels != nil {
		n.Channels = *in.Channels
	}
	if err := accounts.NormalizeNetwork(&n); err != nil {
		s.fail(w, err)
		return
	}
	if err := s.acc.CheckNetworkName(n.UserID, n.Name, n.ID); err != nil {
		s.fail(w, err)
		return
	}
	if err := s.st().UpdateNetwork(n); err != nil {
		s.fail(w, err)
		return
	}
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
//
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
		Software: browserName(r.UserAgent()), Networks: []string{n.Name}, IP: clientAddr(r),
	}, func() { ws.Close() })
	defer att.Release()

	sub, unsub := conn.Subscribe()
	defer unsub()

	// *status replies, written by the select loop below (the only writer).
	replies := make(chan history.Message, 256)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			var in struct{ Target, Text, Kind, Raw, Status string }
			if err := ws.ReadJSON(&in); err != nil {
				return
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
		case m := <-replies:
			if err := ws.WriteJSON(messageDTO(m)); err != nil {
				return
			}
		case <-done:
			return
		}
	}
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
	if fresh, err := s.st().NetworkByID(n.ID); err == nil {
		n = fresh
	}
	s.status.WebStatus(auth.User(r).ID, n, text, reply)
}

// ownedNetwork loads the {id} network if it belongs to the signed-in user,
// answering 404 itself otherwise.
func (s *Server) ownedNetwork(w http.ResponseWriter, r *http.Request) (store.Network, bool) {
	n, err := s.st().NetworkByID(pathID(r))
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
			"status": string(h.Status), "lastError": h.LastError, "lastErrorAt": unixT(h.LastErrorAt),
			"attempts": h.Attempts, "retryIn": int(h.RetryIn.Seconds()), "connectedAt": unixT(h.ConnectedAt),
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
		"unread":      unread, "mentions": mentions,
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

// clientAddr is the browser's address (behind the load balancer and any
// forwarding node, the first X-Forwarded-For entry).
func clientAddr(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		first, _, _ := strings.Cut(xff, ",")
		return strings.TrimSpace(first)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
