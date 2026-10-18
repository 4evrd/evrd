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
package links

import (
	"bufio"
	"encoding/json"
	"errors"
	"time"

	"github.com/4evrd/evrd/internal/irctls"
)

// Version is the protocol version; the holder refuses a different one.
const Version = 1

type request struct {
	Version int          `json:"version"`
	Op      string       `json:"op"`
	Link    string       `json:"link,omitempty"`
	TLS     bool         `json:"tls,omitempty"`
	Host    string       `json:"host,omitempty"`
	Trust   irctls.Trust `json:"trust,omitempty"`
	Quit    string       `json:"quit,omitempty"`
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
