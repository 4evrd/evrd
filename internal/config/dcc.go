package config

// The dcc_* directives: how the bouncedcc module (internal/dcc) relays
// DCC chats and file transfers through evrd.

import (
	"errors"
	"fmt"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"time"
)

// DCC configures relayed DCCs.
type DCC struct {
	// Public are the addresses DCC offers point at: an IPv4 one, an IPv6
	// one, or both (dcc_public). None: bouncedcc relays nothing.
	Public []netip.Addr
	// Listen is the local address DCC listeners bind ("": all).
	Listen string
	// Ports are the port ranges listeners use (none: any free port).
	Ports []PortRange
	// Timeout is how long a listener waits for its connection, and a file
	// transfer for its next byte.
	Timeout time.Duration
	// MaxMB is the largest file relayed, in MB (0: any size).
	MaxMB int
	// Private lets offers point at loopback and private addresses.
	Private bool
}

// DefaultDCCTimeout is ZNC's: a minute to connect.
const DefaultDCCTimeout = time.Minute

// PortRange is the ports from Lo to Hi.
type PortRange struct{ Lo, Hi int }

func (r PortRange) String() string {
	if r.Lo == r.Hi {
		return strconv.Itoa(r.Lo)
	}
	return fmt.Sprintf("%d-%d", r.Lo, r.Hi)
}

// PortsText is dcc_ports in directive syntax.
func (d DCC) PortsText() string {
	var out []string
	for _, r := range d.Ports {
		out = append(out, r.String())
	}
	return strings.Join(out, " ")
}

// PublicText is dcc_public in directive syntax.
func (d DCC) PublicText() string {
	var out []string
	for _, a := range d.Public {
		out = append(out, a.String())
	}
	return strings.Join(out, " ")
}

// setDCC sets a dcc_* directive; false if key isn't one.
func (c *Config) setDCC(key, val string) (bool, error) {
	var err error
	switch key {
	case "dcc_public": // several per line
		for _, f := range strings.Fields(val) {
			a, perr := netip.ParseAddr(f)
			if perr != nil || a.Zone() != "" {
				return true, fmt.Errorf("%q isn't an IP address", f)
			}
			a = a.Unmap()
			for _, have := range c.DCC.Public {
				if have.Is4() == a.Is4() {
					return true, errors.New("one IPv4 and one IPv6 address at most")
				}
			}
			c.DCC.Public = append(c.DCC.Public, a)
		}
	case "dcc_listen":
		c.DCC.Listen = strings.TrimSpace(val)
	case "dcc_ports": // several per line
		for _, f := range strings.Fields(val) {
			r, perr := parsePorts(f)
			if perr != nil {
				return true, perr
			}
			c.DCC.Ports = append(c.DCC.Ports, r)
		}
	case "dcc_timeout":
		c.DCC.Timeout, err = time.ParseDuration(val)
		if err == nil && c.DCC.Timeout < time.Second {
			err = errors.New("a second at least")
		}
	case "dcc_max_mb":
		c.DCC.MaxMB, err = strconv.Atoi(val)
	case "dcc_private":
		c.DCC.Private, err = parseBool(val)
	default:
		return false, nil
	}
	return true, err
}

// parsePorts reads "50000-50100", or one port.
func parsePorts(v string) (PortRange, error) {
	a, b, ok := strings.Cut(v, "-")
	if !ok {
		b = a
	}
	lo, err1 := strconv.Atoi(a)
	hi, err2 := strconv.Atoi(b)
	if err1 != nil || err2 != nil || lo < 1024 || hi > 65535 || lo > hi {
		return PortRange{}, errors.New("port ranges from 1024 to 65535, e.g. 50000-50100")
	}
	return PortRange{lo, hi}, nil
}

// dccEnv applies EVRD_DCC_PUBLIC and EVRD_DCC_PORTS: they can differ per
// node, so a cluster sets them in each node's environment.
func (c *Config) dccEnv() error {
	if v := os.Getenv("EVRD_DCC_PUBLIC"); v != "" {
		c.DCC.Public = nil
		if _, err := c.setDCC("dcc_public", v); err != nil {
			return fmt.Errorf("EVRD_DCC_PUBLIC: %w", err)
		}
	}
	if v := os.Getenv("EVRD_DCC_PORTS"); v != "" {
		c.DCC.Ports = nil
		if _, err := c.setDCC("dcc_ports", v); err != nil {
			return fmt.Errorf("EVRD_DCC_PORTS: %w", err)
		}
	}
	return nil
}
