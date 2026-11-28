package dcc

// Relay: one network connection's relayed DCCs.

import (
	"errors"
	"net"
	"net/netip"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/4evrd/evrd/internal/config"
	"github.com/4evrd/evrd/internal/messages"
)

// current is the server's DCC settings (Configure).
var current atomic.Pointer[config.DCC]

// Configure sets the DCC settings every relay uses from then on.
func Configure(c config.DCC) { current.Store(&c) }

// Current is the server's DCC settings.
func Current() config.DCC {
	if c := current.Load(); c != nil {
		return *c
	}
	return config.DCC{Timeout: config.DefaultDCCTimeout}
}

// MaxWaiting is how many DCCs one connection relays at once: listeners
// take ports, which a flood of offers mustn't use up.
const MaxWaiting = 16

// Relay relays the DCCs of one network connection. The hooks are how it
// reaches the rest of evrd.
type Relay struct {
	// Config is the DCC settings (nil: Current).
	Config func() config.DCC
	// Send sends a line to the server as the user.
	Send func(line string)
	// ClientIPs are where the user's IRC clients attached from: only they
	// may connect to a listener meant for a client (nil: anyone).
	ClientIPs func() []string
	// Log writes to the network's connection log; Reply tells the user's
	// clients (both may be nil).
	Log   func(messages.Text)
	Reply func(messages.Text)

	mu        sync.Mutex
	transfers []*transfer
	warned    bool
}

// transfer is one DCC being relayed, or waiting for a passive answer.
type transfer struct {
	peer string // the other side's nick
	name string // the file
	// the port and token each side was told (for RESUME and ACCEPT)
	client, remote side
	// answer: the user's client offered a passive DCC; the peer's answer
	// (its address) is still to come, and from clientIP's client
	answer   bool
	clientIP netip.Addr
	typ      string
	size     int64
}

type side struct {
	port  int
	token string
}

func (s side) matches(o Offer) bool {
	if o.Port != 0 {
		return s.port == o.Port
	}
	return s.port == 0 && o.Token != "" && s.token == o.Token
}

func (r *Relay) config() config.DCC {
	if r.Config != nil {
		return r.Config()
	}
	return Current()
}

func (r *Relay) log(t messages.Text) {
	if r.Log != nil {
		r.Log(t)
	}
}

// refuse tells the user (and the log) why a DCC isn't relayed.
func (r *Relay) refuse(t messages.Text) {
	r.log(t)
	if r.Reply != nil {
		r.Reply(t)
	}
}

// FromServer takes a CTCP someone (prefix) sent the user, before the
// user's clients get it, and returns what they get instead ("" for
// nothing). Anything but a DCC comes back as it is.
func (r *Relay) FromServer(prefix, text string) string {
	o, ok := Parse(text)
	if !ok {
		return text
	}
	nick, _, _ := strings.Cut(prefix, "!")
	switch o.Type {
	case "RESUME", "ACCEPT":
		return r.translate(nick, o, false, text)
	}
	cfg := r.config()
	if !r.ready(cfg, false) {
		return text // as without the module
	}
	if t := r.answerFor(nick, o); t != nil {
		return r.passiveAnswer(cfg, t, o)
	}
	if r.ClientIPs != nil && len(r.ClientIPs()) == 0 {
		return text // no client could take it: no listener for nobody
	}
	if !r.sizeOK(cfg, nick, o) {
		return ""
	}
	if o.Passive() {
		return r.incomingPassive(cfg, nick, o)
	}
	if o.Port == 0 {
		return text // nothing to connect to
	}
	return r.incoming(cfg, nick, o)
}

// FromClient takes a CTCP one of the user's IRC clients (at clientIP)
// sends target, and returns what goes to the server instead ("" for
// nothing).
func (r *Relay) FromClient(target, text, clientIP string) string {
	o, ok := Parse(text)
	if !ok || target == "" || strings.ContainsAny(target[:1], "#&+!") {
		return text
	}
	switch o.Type {
	case "RESUME", "ACCEPT":
		return r.translate(target, o, true, text)
	}
	cfg := r.config()
	if !r.ready(cfg, true) {
		// not without the module's say: the offer would give away the
		// client's address
		return ""
	}
	if !r.sizeOK(cfg, target, o) {
		return ""
	}
	ip, err := netip.ParseAddr(clientIP)
	if err != nil {
		r.refuse(messages.T("module.log.bouncedccRefused", "type", o.Type, "name", o.Name, "nick", target, "reason", messages.T("dcc.noClientAddress")))
		return ""
	}
	ip = ip.Unmap()
	if o.Port == 0 {
		if !o.Passive() {
			return ""
		}
		return r.outgoingPassive(cfg, target, o, ip)
	}
	return r.outgoing(cfg, target, o, ip)
}

// ready reports whether DCCs can be relayed: there's a public address.
// If not, the user is told: once, or always (their own offer was dropped).
func (r *Relay) ready(cfg config.DCC, always bool) bool {
	if len(cfg.Public) > 0 {
		return true
	}
	r.mu.Lock()
	warned := r.warned
	r.warned = true
	r.mu.Unlock()
	if !warned || always {
		r.refuse(messages.T("module.log.bouncedccNoAddress"))
	}
	return false
}

func (r *Relay) sizeOK(cfg config.DCC, nick string, o Offer) bool {
	if o.Type != "SEND" || cfg.MaxMB <= 0 || o.Size <= int64(cfg.MaxMB)<<20 {
		return true
	}
	r.refuse(messages.T("module.log.bouncedccRefused", "type", o.Type, "name", o.Name, "nick", nick,
		"reason", messages.T("dcc.tooBig", "max", cfg.MaxMB)))
	return false
}

// public is the address to tell someone at near (any family: IPv4 first).
func public(cfg config.DCC, near netip.Addr) netip.Addr {
	for _, a := range cfg.Public {
		if near.IsValid() && a.Is4() == near.Unmap().Is4() {
			return a
		}
	}
	for _, a := range cfg.Public {
		if a.Is4() {
			return a
		}
	}
	return cfg.Public[0]
}

// reachable reports whether evrd may connect to an address someone else
// gave: not a privileged port, nor inside evrd's own network unless the
// admin allows it.
func reachable(cfg config.DCC, a netip.AddrPort) bool {
	ip := a.Addr()
	if a.Port() < 1024 || !ip.IsValid() || ip.IsUnspecified() || ip.IsMulticast() {
		return false
	}
	return cfg.Private || !(ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsInterfaceLocalMulticast())
}

// clientAllowed is the check for a listener meant for the user's client.
func (r *Relay) clientAllowed() func(netip.Addr) bool {
	if r.ClientIPs == nil {
		return nil
	}
	return func(a netip.Addr) bool {
		for _, s := range r.ClientIPs() {
			if ip, err := netip.ParseAddr(s); err == nil && ip.Unmap() == a {
				return true
			}
		}
		return false
	}
}

// add keeps a transfer, if there's room.
func (r *Relay) add(t *transfer) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.transfers) >= MaxWaiting {
		return false
	}
	r.transfers = append(r.transfers, t)
	return true
}

func (r *Relay) remove(t *transfer) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, x := range r.transfers {
		if x == t {
			r.transfers = append(r.transfers[:i], r.transfers[i+1:]...)
			return
		}
	}
}

// start keeps a transfer and opens its first listener, or says why not.
func (r *Relay) start(cfg config.DCC, t *transfer) (*net.TCPListener, bool) {
	why := messages.T("dcc.busy")
	if r.add(t) {
		l, err := listen(cfg)
		if err == nil {
			return l, true
		}
		r.remove(t)
		why = messages.T("dcc.noPort")
	}
	r.refuse(messages.T("module.log.bouncedccRefused", "type", t.typ, "name", t.name, "nick", t.peer, "reason", why))
	return nil, false
}

// translate rewrites a RESUME or ACCEPT's port (and token) to the ones the
// other side knows.
func (r *Relay) translate(nick string, o Offer, fromClient bool, text string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, t := range r.transfers {
		if !strings.EqualFold(t.peer, nick) || t.answer {
			continue
		}
		from, to := t.remote, t.client
		if fromClient {
			from, to = t.client, t.remote
		}
		if from.matches(o) {
			o.Port, o.Token = to.port, to.token
			return o.String()
		}
	}
	return text
}

// answerFor is the user's passive offer that o (from nick) answers.
func (r *Relay) answerFor(nick string, o Offer) *transfer {
	if o.Port == 0 || o.Token == "" {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, t := range r.transfers {
		if t.answer && t.typ == o.Type && t.remote.token == o.Token && strings.EqualFold(t.peer, nick) {
			t.answer = false
			return t
		}
	}
	return nil
}

func (r *Relay) done(t *transfer, err error) {
	r.remove(t)
	switch {
	case err == nil:
		r.log(messages.T("module.log.bouncedccDone", "type", t.typ, "name", t.name, "nick", t.peer))
	case errors.Is(err, os.ErrDeadlineExceeded):
		r.log(messages.T("module.log.bouncedccExpired", "type", t.typ, "name", t.name, "nick", t.peer))
	case errors.Is(err, ErrTooBig):
		r.log(messages.T("module.log.bouncedccRefused", "type", t.typ, "name", t.name, "nick", t.peer,
			"reason", messages.T("dcc.tooBig", "max", r.config().MaxMB)))
	default:
		r.log(messages.T("module.log.bouncedccFailed", "type", t.typ, "name", t.name, "nick", t.peer, "error", err.Error()))
	}
}

// relay connects a side that connected (in) with the other side (out):
// whichever sent the file is the one whose bytes count towards the limit.
func (r *Relay) relay(cfg config.DCC, t *transfer, in, out net.Conn, inSends bool) {
	chat := t.typ == "CHAT"
	idle := cfg.Timeout
	if chat {
		idle = 0 // a chat can be quiet for hours
	}
	var max int64
	if t.typ == "SEND" && cfg.MaxMB > 0 {
		max = int64(cfg.MaxMB) << 20
	}
	var err error
	if inSends {
		_, err = pipe(in, out, chat, idle, max)
	} else {
		_, err = pipe(out, in, chat, idle, max)
	}
	r.done(t, err)
}

// incoming: someone offers the user a DCC. The clients are told to
// connect to evrd; when one does, evrd connects to the offer.
func (r *Relay) incoming(cfg config.DCC, nick string, o Offer) string {
	to := netip.AddrPortFrom(o.Addr, uint16(o.Port))
	if !reachable(cfg, to) {
		r.refuse(messages.T("module.log.bouncedccRefused", "type", o.Type, "name", o.Name, "nick", nick,
			"reason", messages.T("dcc.privateAddress", "address", to.String())))
		return ""
	}
	t := &transfer{peer: nick, name: o.Name, typ: o.Type, size: o.Size, remote: side{o.Port, o.Token}}
	l, ok := r.start(cfg, t)
	if !ok {
		return ""
	}
	port := portOf(l)
	r.mu.Lock()
	t.client = side{port, o.Token}
	r.mu.Unlock()
	allow := r.clientAllowed()
	go func() {
		in, err := acceptOne(l, time.Now().Add(cfg.Timeout), allow)
		if err != nil {
			r.done(t, err)
			return
		}
		out, err := dial(to, cfg.Timeout)
		if err != nil {
			in.Close()
			r.done(t, err)
			return
		}
		r.relay(cfg, t, in, out, false)
	}()
	r.log(messages.T("module.log.bouncedccIn", "type", o.Type, "name", o.Name, "nick", nick))
	o.Addr, o.Port = public(cfg, r.firstClient()), port
	return o.String()
}

// incomingPassive: someone offers the user a passive DCC (they can't
// listen). The clients get an ordinary offer to connect to evrd; when one
// does, evrd listens for them too and answers with its address.
func (r *Relay) incomingPassive(cfg config.DCC, nick string, o Offer) string {
	t := &transfer{peer: nick, name: o.Name, typ: o.Type, size: o.Size, remote: side{0, o.Token}}
	l, ok := r.start(cfg, t)
	if !ok {
		return ""
	}
	port := portOf(l)
	r.mu.Lock()
	t.client = side{port, ""}
	r.mu.Unlock()
	allow := r.clientAllowed()
	answer, near := o, o.Addr
	go func() {
		in, err := acceptOne(l, time.Now().Add(cfg.Timeout), allow)
		if err != nil {
			r.done(t, err)
			return
		}
		l2, err := listen(cfg)
		if err != nil {
			in.Close()
			r.done(t, err)
			return
		}
		answer.Addr, answer.Port = public(cfg, near), portOf(l2)
		r.Send("PRIVMSG " + nick + " :" + answer.String())
		out, err := acceptOne(l2, time.Now().Add(cfg.Timeout), nil)
		if err != nil {
			in.Close()
			r.done(t, err)
			return
		}
		r.relay(cfg, t, in, out, false)
	}()
	r.log(messages.T("module.log.bouncedccIn", "type", o.Type, "name", o.Name, "nick", nick))
	o.Addr, o.Port, o.Token = public(cfg, r.firstClient()), port, ""
	return o.String()
}

// outgoing: the user's client offers someone a DCC. They're told to
// connect to evrd; when they do, evrd connects to the client, at the
// address it attached from (not the one in the offer, which could be
// anyone's).
func (r *Relay) outgoing(cfg config.DCC, nick string, o Offer, client netip.Addr) string {
	t := &transfer{peer: nick, name: o.Name, typ: o.Type, size: o.Size, client: side{o.Port, o.Token}}
	l, ok := r.start(cfg, t)
	if !ok {
		return ""
	}
	port := portOf(l)
	r.mu.Lock()
	t.remote = side{port, o.Token}
	r.mu.Unlock()
	to := netip.AddrPortFrom(client, uint16(o.Port))
	go func() {
		in, err := acceptOne(l, time.Now().Add(cfg.Timeout), nil)
		if err != nil {
			r.done(t, err)
			return
		}
		out, err := dial(to, cfg.Timeout)
		if err != nil {
			in.Close()
			r.done(t, err)
			return
		}
		r.relay(cfg, t, in, out, false)
	}()
	r.log(messages.T("module.log.bouncedccOut", "type", o.Type, "name", o.Name, "nick", nick))
	o.Addr, o.Port = public(cfg, netip.Addr{}), port
	return o.String()
}

// outgoingPassive: the user's client offers a passive DCC (it can't
// listen). It goes on with evrd's address in it; the answer (passiveAnswer)
// is the one that's relayed.
func (r *Relay) outgoingPassive(cfg config.DCC, nick string, o Offer, client netip.Addr) string {
	t := &transfer{peer: nick, name: o.Name, typ: o.Type, size: o.Size, answer: true, clientIP: client,
		client: side{0, o.Token}, remote: side{0, o.Token}}
	if !r.add(t) {
		r.refuse(messages.T("module.log.bouncedccRefused", "type", o.Type, "name", o.Name, "nick", nick, "reason", messages.T("dcc.busy")))
		return ""
	}
	time.AfterFunc(cfg.Timeout, func() {
		r.mu.Lock()
		unanswered := t.answer
		r.mu.Unlock()
		if unanswered {
			r.remove(t)
		}
	})
	r.log(messages.T("module.log.bouncedccOut", "type", o.Type, "name", o.Name, "nick", nick))
	o.Addr = public(cfg, netip.Addr{})
	return o.String()
}

// passiveAnswer: someone answers the user's passive offer with where they
// listen. The client is told to connect to evrd instead; when it does,
// evrd connects to them.
func (r *Relay) passiveAnswer(cfg config.DCC, t *transfer, o Offer) string {
	to := netip.AddrPortFrom(o.Addr, uint16(o.Port))
	if !reachable(cfg, to) {
		r.remove(t)
		r.refuse(messages.T("module.log.bouncedccRefused", "type", o.Type, "name", o.Name, "nick", t.peer,
			"reason", messages.T("dcc.privateAddress", "address", to.String())))
		return ""
	}
	l, err := listen(cfg)
	if err != nil {
		r.remove(t)
		r.refuse(messages.T("module.log.bouncedccRefused", "type", o.Type, "name", o.Name, "nick", t.peer, "reason", messages.T("dcc.noPort")))
		return ""
	}
	client := t.clientIP
	go func() {
		in, err := acceptOne(l, time.Now().Add(cfg.Timeout), func(a netip.Addr) bool { return a == client })
		if err != nil {
			r.done(t, err)
			return
		}
		out, err := dial(to, cfg.Timeout)
		if err != nil {
			in.Close()
			r.done(t, err)
			return
		}
		r.relay(cfg, t, in, out, true)
	}()
	o.Addr, o.Port = public(cfg, client), portOf(l)
	return o.String()
}

// firstClient is the address of one of the user's clients, to pick the
// public address of its family.
func (r *Relay) firstClient() netip.Addr {
	if r.ClientIPs == nil {
		return netip.Addr{}
	}
	for _, s := range r.ClientIPs() {
		if ip, err := netip.ParseAddr(s); err == nil {
			return ip.Unmap()
		}
	}
	return netip.Addr{}
}
