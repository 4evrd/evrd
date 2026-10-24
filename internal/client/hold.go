package client

// Held clients (issue #70): with a connection holder that holds clients
// (internal/links, protocol 2), the holder owns the IRC listeners and each
// client's connection, and evrd serves the clients over streams from it.
// evrd saves each signed-in client's state with the holder as it changes;
// when evrd restarts, the next one picks every client up from that state
// and carries on — no disconnect, no second welcome — while the holder
// answers the clients' PINGs and keeps what they send.

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"slices"
	"sort"
	"time"

	"github.com/4evrd/evrd/internal/bouncer"
	"github.com/4evrd/evrd/internal/links"
	"github.com/4evrd/evrd/internal/listen"
	"github.com/4evrd/evrd/internal/store"
)

// SetHolder serves IRC clients through the connection holder, if it holds
// clients: it keeps them connected while evrd restarts. Call it before
// ListenAndServe.
func (s *Server) SetHolder(h *links.Client) {
	s.holder = h
}

// Ready is closed once the clients the holder kept are back (at once
// without a holder): the network links can be picked up then, and what the
// networks said meanwhile reaches the clients as it's replayed.
func (s *Server) Ready() <-chan struct{} { return s.readyCh() }

func (s *Server) readyCh() chan struct{} {
	s.readyOnce.Do(func() { s.readyC = make(chan struct{}) })
	return s.readyC
}

func (s *Server) ready() {
	ch := s.readyCh()
	s.readyClose.Do(func() { close(ch) })
}

// serveHeld hands the IRC listeners to the holder and serves the clients
// it passes on, until ctx ends. False if the holder doesn't hold clients:
// evrd listens itself then.
func (s *Server) serveHeld(ctx context.Context, urls []string) bool {
	have, err := s.holder.Listeners(ctx)
	switch {
	case errors.Is(err, links.ErrNoClients):
		s.log.Warn("the connection holder is too old to hold IRC clients — restart evrd-links to update it; listening here meanwhile")
		return false
	case err != nil:
		s.log.Warn("connection holder unreachable — listening here", "err", err)
		return false
	}
	s.handOver(ctx, urls, have)
	for ctx.Err() == nil {
		s.watchHeld(ctx)
		s.ready() // whatever happened, the networks needn't wait
		select {
		case <-ctx.Done():
			return true
		case <-time.After(time.Second):
		}
		// the holder restarted? it needs the listeners again
		if have, err := s.holder.Listeners(ctx); err == nil {
			s.handOver(ctx, urls, have)
		}
	}
	return true
}

// handOver gives the holder the listeners it doesn't have yet, and the
// certificate for TLS ones.
func (s *Server) handOver(ctx context.Context, urls []string, have []links.Listener) {
	tlsUsed := false
	for _, u := range urls {
		scheme, addr := parseListen(u)
		var tlsOn bool
		switch scheme {
		case "irc", "irc+insecure", "tcp":
		case "ircs", "tls":
			tlsOn, tlsUsed = true, true
			s.tlsPort(addr)
		default:
			continue
		}
		if slices.ContainsFunc(have, func(l links.Listener) bool { return l.Addr == addr }) {
			s.log.Info("irc listener (in the connection holder)", "addr", addr, "scheme", scheme)
			continue
		}
		ln, err := listen.TCP(addr)
		if err == nil {
			err = s.holder.Listen(ctx, ln, addr, tlsOn)
		}
		if err != nil {
			s.log.Error("irc listen", "addr", addr, "scheme", scheme, "err", err)
			continue
		}
		s.log.Info("irc listener (handed to the connection holder)", "addr", addr, "scheme", scheme)
	}
	if tlsUsed && !s.certsPushed {
		s.certsPushed = true
		s.pushCert(ctx)
		go func() {
			t := time.NewTicker(time.Minute) // renewals, SIGHUP
			defer t.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-t.C:
					s.pushCert(ctx)
				}
			}
		}()
	} else if tlsUsed {
		s.certMu.Lock()
		s.lastCert = nil // a new holder: send it again
		s.certMu.Unlock()
		s.pushCert(ctx)
	}
}

// pushCert gives the holder the IRC certificate, if it changed.
func (s *Server) pushCert(ctx context.Context) {
	conf, err := s.tlsConfig()
	if err != nil {
		s.log.Error("irc tls", "err", err)
		return
	}
	var cert *tls.Certificate
	if conf.GetCertificate != nil {
		cert, err = conf.GetCertificate(&tls.ClientHelloInfo{ServerName: s.sts.host})
	} else if len(conf.Certificates) > 0 {
		cert = &conf.Certificates[0]
	}
	if err != nil || cert == nil || len(cert.Certificate) == 0 {
		s.log.Error("irc tls: no certificate for the connection holder", "err", err)
		return
	}
	s.certMu.Lock()
	defer s.certMu.Unlock()
	if bytes.Equal(s.lastCert, cert.Certificate[0]) {
		return
	}
	var chain bytes.Buffer
	for _, der := range cert.Certificate {
		pem.Encode(&chain, &pem.Block{Type: "CERTIFICATE", Bytes: der})
	}
	der, err := x509.MarshalPKCS8PrivateKey(cert.PrivateKey)
	if err != nil {
		s.log.Error("irc tls: certificate key", "err", err)
		return
	}
	key := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	if err := s.holder.SetCert(ctx, chain.Bytes(), key); err != nil {
		s.log.Error("irc tls: give the connection holder the certificate", "err", err)
		return
	}
	s.lastCert = cert.Certificate[0]
}

// watchHeld takes the clients the holder passes on until its stream ends.
// Those it kept from the last evrd come back before Ready, in order.
func (s *Server) watchHeld(ctx context.Context) {
	ch, err := s.holder.Clients(ctx)
	if err != nil {
		if ctx.Err() == nil {
			s.log.Warn("connection holder: clients", "err", err)
		}
		return
	}
	resumed := 0
	for hc := range ch {
		if hc.Client == "" {
			if resumed > 0 {
				s.log.Info("resumed held IRC clients", "clients", resumed)
			}
			s.ready()
			continue
		}
		if s.taking(hc.Client) {
			continue // ours already (told twice)
		}
		if s.takeHeld(ctx, hc) {
			resumed++
		}
	}
}

// taking claims a client id for this evrd; false if it has it already.
func (s *Server) taking(id string) bool {
	s.heldMu.Lock()
	defer s.heldMu.Unlock()
	if s.held == nil {
		s.held = map[string]bool{}
	}
	if s.held[id] {
		return true
	}
	s.held[id] = true
	return false
}

func (s *Server) taken(id string) {
	s.heldMu.Lock()
	delete(s.held, id)
	s.heldMu.Unlock()
}

// takeHeld attaches to a client in the holder and serves it: a new one
// from registration, a kept one from the state it was saved with. Reports
// whether it resumed a kept one.
func (s *Server) takeHeld(ctx context.Context, hc links.HeldClient) bool {
	cc, err := s.holder.AttachClient(ctx, hc)
	if err != nil {
		s.taken(hc.Client)
		if !errors.Is(err, links.ErrNoClient) {
			s.log.Warn("connection holder: attach client", "client", hc.Client, "err", err)
		}
		return false
	}
	se := newSession(s, cc)
	se.held = cc
	resumed := false
	if cc.Info.State != nil {
		if err := se.restore(cc.Info.State); err != nil {
			s.log.Warn("couldn't resume a held IRC client", "client", hc.Client, "ip", remoteHost(cc), "err", err)
			se.send("ERROR :Closing link: evrd restarted, reconnect")
			cc.Close()
			s.taken(hc.Client)
			return false
		}
		resumed = true
	}
	// evrd stopping: whatever the session is doing, the client is the
	// holder's again (a signed-in one saved its state already)
	stop := context.AfterFunc(ctx, func() { cc.Detach() })
	go func() {
		defer s.taken(hc.Client)
		defer stop()
		se.serve(ctx)
	}()
	return resumed
}

// heldStateVersion is the state's format; a state of another version
// isn't resumed (the client is told to reconnect).
const heldStateVersion = 1

// heldState is what the next evrd needs to carry on with a client: what
// outlives a single command.
type heldState struct {
	V          int                          `json:"v"`
	User       int64                        `json:"user"`
	Network    int64                        `json:"network,omitempty"` // 0: a bouncer connection
	Nick       string                       `json:"nick"`              // as the client knows it
	RegNick    string                       `json:"regNick,omitempty"`
	CapVersion int                          `json:"capVersion,omitempty"`
	Caps       []string                     `json:"caps,omitempty"`
	Advertised map[string]string            `json:"advertised,omitempty"`
	Batches    int                          `json:"batches,omitempty"`
	NetBatches map[string]bool              `json:"netBatches,omitempty"` // the network's, open
	NetsSent   map[string]map[string]string `json:"netsSent,omitempty"`
	Marks      map[string]time.Time         `json:"marks,omitempty"`
	FromIP     string                       `json:"fromIP,omitempty"`
	Device     string                       `json:"device,omitempty"`
	Software   string                       `json:"software,omitempty"`
	Since      time.Time                    `json:"since"`
}

// saveState gives the holder the client's state, if it changed.
func (se *session) saveState() {
	// a client waiting for its second factor signs in again instead
	if se.held == nil || !se.registered || se.att == nil || se.locked != nil {
		return
	}
	info := se.att.Info()
	st := heldState{
		V: heldStateVersion, User: se.uid, Network: se.net.ID, Nick: se.unick, RegNick: se.nick,
		CapVersion: se.capVersion, Advertised: se.advertised, Batches: se.batches,
		NetBatches: se.ubatch, NetsSent: se.netsSent, Marks: se.marks, FromIP: se.fromIP,
		Device: info.Device, Software: info.Software, Since: info.Since,
	}
	for c, on := range se.caps {
		if on {
			st.Caps = append(st.Caps, c)
		}
	}
	sort.Strings(st.Caps)
	b, err := json.Marshal(st)
	if err != nil || bytes.Equal(b, se.savedState) {
		return
	}
	se.savedState = b
	se.wmu.Lock()
	defer se.wmu.Unlock()
	se.w.WriteString(links.SaveState(b) + "\n")
	se.w.Flush()
}

// restore sets the session up from a saved state, as attach would have,
// without a word to the client: it's been attached all along.
func (se *session) restore(raw []byte) error {
	var st heldState
	if err := json.Unmarshal(raw, &st); err != nil {
		return err
	}
	if st.V != heldStateVersion {
		return fmt.Errorf("state version %d", st.V)
	}
	ctx := context.Background()
	u, err := se.srv.acc.UserByID(ctx, st.User)
	if err != nil {
		return fmt.Errorf("user: %w", err)
	}
	if u.Suspended {
		return errors.New("suspended")
	}
	if !se.srv.b.Owns(u.ID) {
		return errors.New("not this node's user")
	}
	var bound []string
	if st.Network != 0 {
		nets, err := se.srv.acc.ListNetworks(ctx, u.ID)
		if err != nil {
			return err
		}
		i := slices.IndexFunc(nets, func(n store.Network) bool { return n.ID == st.Network })
		if i < 0 {
			return errors.New("the network is gone")
		}
		bound = []string{nets[i].Name}
		se.net = nets[i]
	}
	se.uid, se.acct = u.ID, u
	se.nick, se.unick = st.RegNick, st.Nick
	se.capVersion, se.advertised, se.batches = st.CapVersion, st.Advertised, st.Batches
	for _, c := range st.Caps {
		se.caps[c] = true
	}
	if st.NetBatches != nil {
		se.ubatch = st.NetBatches
	}
	se.netsSent, se.marks, se.fromIP = st.NetsSent, st.Marks, st.FromIP
	att, err := se.srv.b.AttachClient(u.ID, 0,
		bouncer.ClientInfo{Device: st.Device, Networks: bound, IP: se.clientIP(), Software: st.Software},
		func() { se.conn.Close() })
	if err != nil {
		return err
	}
	att.Update(func(ci *bouncer.ClientInfo) {
		if !st.Since.IsZero() {
			ci.Since = st.Since
		}
	})
	se.att, se.release = att, att.Release
	if st.Network != 0 {
		if err := se.bind(se.net); err != nil {
			se.release()
			return err
		}
	}
	se.registered, se.restored, se.restoredAt = true, true, time.Now()
	se.savedState = raw
	return nil
}

// heldOver reports whether the client stays with the holder for the next
// evrd: evrd is stopping, and the client was signed in (not still waiting
// for its second factor).
func (se *session) heldOver(ctx context.Context) bool {
	return se.held != nil && ctx.Err() != nil && se.att != nil && se.locked == nil
}

// closeConn ends the session's connection — but a client held over is
// left to the holder, with its state saved.
func (se *session) closeConn(ctx context.Context) {
	if se.heldOver(ctx) {
		se.saveState()
		se.held.Detach()
		return
	}
	se.conn.Close()
}
