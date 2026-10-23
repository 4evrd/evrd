// Package links keeps IRC connections up while evrd restarts.
//
// A small, rarely-updated process — the holder, evrd-links — owns the
// connections to the IRC networks. evrd opens each TCP connection itself
// (bind hosts, pacing and source addresses stay in internal/outbound) and
// hands the socket to the holder, which does the TLS handshake and from then
// on passes lines both ways over a unix-socket stream. While evrd is away
// (restarting) the holder keeps every link alive: it answers the server's
// PINGs and buffers what arrives. The next evrd attaches to each link again
// and carries on without re-registering: no QUIT, no JOINs, no lost nick.
//
// Each request is one connection to the holder's socket, starting with one
// JSON line:
//
//	{"op":"open","link":"n42","tls":true,"host":"irc.example.org","trust":{…}}
//	    + the connected TCP socket (SCM_RIGHTS) → {"ok":true}, then the stream
//	{"op":"attach","link":"n42"} → {"ok":true,"nick":…,"isupport":[…]},
//	    then lines buffered while detached, then the stream
//	{"op":"list"}                → {"ok":true,"links":[…]}
//	{"op":"close","link":"n42","quit":"bye"} → {"ok":true}
//
// A stream that closes leaves its link held (evrd went away); one that sent
// QUIT first closes the link.
//
// Version 2 holds evrd's IRC clients the same way (see clients.go): the
// holder owns the IRC listeners and the clients' TLS, and keeps each client
// connected while evrd restarts.
//
//	{"op":"listen","addr":":4353","tls":true} + the listening socket → {"ok":true}
//	{"op":"listeners"}            → {"ok":true,"listeners":[…]}
//	{"op":"cert","cert":…,"key":…} → {"ok":true} (PEM, for TLS listeners)
//	{"op":"clients"}              → {"ok":true}, then a JSON line per client
//	    waiting for evrd (new ones, and held ones with their state)
//	{"op":"attach-client","client":"c17"} → {"ok":true,"state":…}, then the
//	    lines the client sent meanwhile, then the stream
//
// On a client's stream evrd may send control lines, which start with a NUL
// and never reach the client: "\x00state <json>" saves the state the next
// evrd resumes the client with (a client without one isn't held), and
// "\x00close" closes the client once what came before it is written.
//
// Requests about links still say version 1, so an evrd talks to an older
// holder as before; one that refuses version 2 holds no clients, and evrd
// listens itself.
package links

import (
	"bufio"
	"encoding/json"
	"errors"
	"time"

	"github.com/4evrd/evrd/internal/irctls"
)

// Version is the newest protocol version; the holder takes requests of any
// version up to it. Link requests are version 1, client requests 2.
const Version = 2

const linkVersion = 1

type request struct {
	Version int          `json:"version"`
	Op      string       `json:"op"`
	Link    string       `json:"link,omitempty"`
	TLS     bool         `json:"tls,omitempty"`
	Host    string       `json:"host,omitempty"`
	Trust   irctls.Trust `json:"trust,omitempty"`
	Quit    string       `json:"quit,omitempty"`
	Addr    string       `json:"addr,omitempty"`   // listen
	Cert    string       `json:"cert,omitempty"`   // cert (PEM)
	Key     string       `json:"key,omitempty"`    // cert (PEM)
	Client  string       `json:"client,omitempty"` // attach-client
}

type response struct {
	OK          bool     `json:"ok,omitempty"`
	Error       string   `json:"error,omitempty"`
	Fingerprint string   `json:"fingerprint,omitempty"` // an untrusted certificate's
	Nick        string   `json:"nick,omitempty"`
	ISupport    []string `json:"isupport,omitempty"`
	ConnectedAt int64    `json:"connectedAt,omitempty"`
	Dropped     int      `json:"dropped,omitempty"`
	Links       []Info   `json:"links,omitempty"`

	Listeners []Listener      `json:"listeners,omitempty"`
	State     json.RawMessage `json:"state,omitempty"`
}

// Listener is an IRC listener the holder accepts clients on.
type Listener struct {
	Addr string `json:"addr"`
	TLS  bool   `json:"tls,omitempty"`
}

// HeldClient is an IRC client the holder has for evrd: a new one, or one a
// previous evrd served (State is what it saved, nil for a new client).
type HeldClient struct {
	Client string          `json:"client"`
	Addr   string          `json:"addr"`  // the client's address
	Local  string          `json:"local"` // the listener's
	TLS    bool            `json:"tls,omitempty"`
	State  json.RawMessage `json:"state,omitempty"`
}

// Info describes a link the holder keeps.
type Info struct {
	Link       string `json:"link"`
	Nick       string `json:"nick"`
	Registered bool   `json:"registered"`
	Attached   bool   `json:"attached"`
	Buffered   int    `json:"buffered"`
}

// Resumed is a link evrd attached to again: its state, as far as the
// holder tracks it, from before the restart.
type Resumed struct {
	Nick        string
	ISupport    []string // the server's 005 lines
	ConnectedAt time.Time
	Dropped     int // lines dropped because the buffer filled
}

// ErrNoLink means the holder has no (registered) link by that id.
var ErrNoLink = errors.New("links: no such link")

// ErrNoClient means the holder has no client by that id.
var ErrNoClient = errors.New("links: no such client")

// ErrNoClients means the holder is too old to hold clients (protocol 1).
var ErrNoClients = errors.New("links: the holder doesn't hold clients")

// ErrUnavailable means the holder couldn't be reached.
var ErrUnavailable = errors.New("links: holder not reachable")

func writeJSON(w *bufio.Writer, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if _, err := w.Write(append(b, '\n')); err != nil {
		return err
	}
	return w.Flush()
}
