// Package dcc relays DCC chats and file transfers through evrd, like ZNC's
// bouncedcc: an offer is rewritten to point at a listener of evrd's, and
// when that's connected evrd connects to the other side and copies bytes
// between them. Neither side learns the other's address, and a client
// behind NAT can take offers it couldn't reach itself.
package dcc

import (
	"net/netip"
	"strconv"
	"strings"
)

// Offer is a DCC request as a CTCP carries it:
//
//	DCC CHAT chat <ip> <port> [token]
//	DCC SEND <file> <ip> <port> [size] [token]
//	DCC RESUME <file> <port> <position> [token]
//	DCC ACCEPT <file> <port> <position> [token]
//
// The IP is an IPv4 address as one number, or an IPv6 address. Port 0 with
// a token is a passive (reverse) DCC: the one offering can't listen, so the
// other side listens and answers with the same offer, its address, its port
// and the token.
type Offer struct {
	Type  string     // CHAT, SEND, RESUME or ACCEPT
	Name  string     // the file ("chat" for a CHAT)
	Addr  netip.Addr // CHAT and SEND
	Port  int
	Size  int64 // SEND: the file's size (-1: not given); RESUME, ACCEPT: the position
	Token string
}

// Passive reports whether the offer asks the other side to listen.
func (o Offer) Passive() bool { return o.Port == 0 && o.Token != "" }

// Parse reads a CTCP DCC message (with or without its \x01s); false if it
// isn't a DCC CHAT, SEND, RESUME or ACCEPT.
func Parse(text string) (Offer, bool) {
	text = strings.TrimSuffix(strings.TrimPrefix(text, "\x01"), "\x01")
	f := strings.SplitN(text, " ", 3)
	if len(f) < 3 || !strings.EqualFold(f[0], "DCC") {
		return Offer{}, false
	}
	o := Offer{Type: strings.ToUpper(f[1]), Size: -1}
	name, rest, ok := splitName(strings.TrimSpace(f[2]), o.Type)
	if !ok {
		return Offer{}, false
	}
	o.Name = name
	switch o.Type {
	case "CHAT", "SEND":
		addr, ok1 := parseAddr(rest[0])
		port, ok2 := parsePort(rest[1])
		if !ok1 || !ok2 {
			return Offer{}, false
		}
		o.Addr, o.Port = addr, port
		rest = rest[2:]
		if o.Type == "SEND" && len(rest) > 0 {
			n, err := strconv.ParseInt(rest[0], 10, 64)
			if err != nil || n < 0 {
				return Offer{}, false
			}
			o.Size, rest = n, rest[1:]
		}
	case "RESUME", "ACCEPT":
		port, ok1 := parsePort(rest[0])
		pos, err := strconv.ParseInt(rest[1], 10, 64)
		if !ok1 || err != nil || pos < 0 {
			return Offer{}, false
		}
		o.Port, o.Size, rest = port, pos, rest[2:]
	default:
		return Offer{}, false
	}
	if len(rest) > 1 {
		return Offer{}, false
	}
	if len(rest) == 1 {
		o.Token = rest[0]
	}
	return o, true
}

// splitName takes the file name off the front: "quoted, with spaces", or
// the words before the address (or port) that fit, for clients that don't
// quote names with spaces. rest has at least the two fields after it.
func splitName(s, typ string) (name string, rest []string, ok bool) {
	if strings.HasPrefix(s, `"`) {
		end := strings.Index(s[1:], `"`)
		if end < 0 {
			return "", nil, false
		}
		rest = strings.Fields(s[end+2:])
		return s[1 : end+1], rest, len(rest) >= 2
	}
	f := strings.Fields(s)
	most := 2 // fields after the two: size and token, or a token
	if typ == "CHAT" || typ == "RESUME" || typ == "ACCEPT" {
		most = 1
	}
	for i := 1; i+2 <= len(f); i++ {
		if len(f)-i-2 > most {
			continue
		}
		first := false
		if typ == "CHAT" || typ == "SEND" {
			_, first = parseAddr(f[i])
		} else {
			_, first = parsePort(f[i])
		}
		if _, second := parsePort(f[i+1]); first && second {
			return strings.Join(f[:i], " "), f[i:], true
		}
	}
	return "", nil, false
}

// parseAddr reads an IPv4 address as a number, or any IP address.
func parseAddr(s string) (netip.Addr, bool) {
	if n, err := strconv.ParseUint(s, 10, 32); err == nil {
		return netip.AddrFrom4([4]byte{byte(n >> 24), byte(n >> 16), byte(n >> 8), byte(n)}), true
	}
	a, err := netip.ParseAddr(s)
	if err != nil || a.Zone() != "" {
		return netip.Addr{}, false
	}
	return a.Unmap(), true
}

func parsePort(s string) (int, bool) {
	n, err := strconv.Atoi(s)
	return n, err == nil && n >= 0 && n <= 65535
}

// FormatAddr is an address as DCC has it: IPv4 as one number.
func FormatAddr(a netip.Addr) string {
	a = a.Unmap()
	if a.Is4() {
		b := a.As4()
		return strconv.FormatUint(uint64(b[0])<<24|uint64(b[1])<<16|uint64(b[2])<<8|uint64(b[3]), 10)
	}
	return a.String()
}

// String is the offer as a CTCP, \x01s and all.
func (o Offer) String() string {
	name := o.Name
	if strings.ContainsAny(name, " \t") {
		name = `"` + name + `"`
	}
	f := []string{"DCC", o.Type, name}
	switch o.Type {
	case "CHAT", "SEND":
		f = append(f, FormatAddr(o.Addr), strconv.Itoa(o.Port))
		if o.Type == "SEND" && (o.Size >= 0 || o.Token != "") {
			f = append(f, strconv.FormatInt(max(o.Size, 0), 10))
		}
	default:
		f = append(f, strconv.Itoa(o.Port), strconv.FormatInt(max(o.Size, 0), 10))
	}
	if o.Token != "" {
		f = append(f, o.Token)
	}
	return "\x01" + strings.Join(f, " ") + "\x01"
}
