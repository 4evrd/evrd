// Package directory keeps the network directory: a list of public IRC
// networks and their servers, published at https://evrd.sh/api/networks/v1.json,
// that users pick from when they add a network instead of typing server
// details. evrd fetches it at startup and twice a day, keeps the last good
// copy in the database for when the source is out of reach, and never lets
// a bad document replace a good one. See docs/network-directory.md.
package directory

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Version is the document format this evrd reads. A newer major format is
// published at a new path (v2.json), so older instances keep the old one.
const Version = 1

// Document is the published directory.
type Document struct {
	Schema   string    `json:"$schema,omitempty"`
	Version  int       `json:"version"`
	Updated  time.Time `json:"updated"`
	Networks []Network `json:"networks"`
}

// Network is one entry. Fields evrd doesn't know are ignored, so the format
// can grow (login methods, say) without breaking older instances.
type Network struct {
	// ID is short and stable (libera, oftc): it's the name a picked network
	// gets, and what *status addnetwork takes.
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Homepage    string `json:"homepage,omitempty"`
	// Servers in order of preference: the first is the one to use.
	Servers []Server `json:"servers"`
	// SASL says the network's services take SASL logins; Services names
	// them (atheme, anope, x, q, none…) for login hints.
	SASL     bool   `json:"sasl,omitempty"`
	Services string `json:"services,omitempty"`
	// Channels are suggested to join, like the network's help channel.
	Channels []string `json:"channels,omitempty"`
	Tags     []string `json:"tags,omitempty"`
	Region   string   `json:"region,omitempty"`
}

// Server is one way to reach a network.
type Server struct {
	Host string `json:"host"`
	Port int    `json:"port"`
	TLS  bool   `json:"tls"`
}

// Connect is the servers to save for a picked network: the TLS ones when
// there are any (a plain-text fallback would quietly give up TLS), else all.
func (n Network) Connect() []Server {
	var tls []Server
	for _, s := range n.Servers {
		if s.TLS {
			tls = append(tls, s)
		}
	}
	if len(tls) > 0 {
		return tls
	}
	return n.Servers
}

var (
	idRe   = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)
	hostRe = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.-]{0,251}[A-Za-z0-9])?$`)
	chanRe = regexp.MustCompile(`^[#&+!][^\s,\x00-\x1f]{1,49}$`)
)

// Parse decodes and checks a document. Entries that don't check out are
// dropped (and named in skipped), so one typo doesn't hide the rest; a
// document with the wrong version, or nothing usable, is an error.
func Parse(data []byte) (doc *Document, skipped []string, err error) {
	doc = &Document{}
	if err := json.Unmarshal(data, doc); err != nil {
		return nil, nil, fmt.Errorf("not a directory: %w", err)
	}
	if doc.Version != Version {
		return nil, nil, fmt.Errorf("format version %d, this evrd reads %d", doc.Version, Version)
	}
	seen := map[string]bool{}
	kept := doc.Networks[:0]
	for i, n := range doc.Networks {
		n = n.clean()
		if err := n.check(); err != nil {
			skipped = append(skipped, fmt.Sprintf("entry %d (%s): %v", i+1, n.ID, err))
			continue
		}
		if seen[n.ID] {
			skipped = append(skipped, fmt.Sprintf("entry %d (%s): the id is used twice", i+1, n.ID))
			continue
		}
		seen[n.ID] = true
		kept = append(kept, n)
	}
	doc.Networks = kept
	if len(kept) == 0 {
		return nil, skipped, errors.New("no usable networks in it")
	}
	return doc, skipped, nil
}

// clean trims what's sloppy but harmless.
func (n Network) clean() Network {
	n.ID = strings.TrimSpace(n.ID)
	n.Name = strings.TrimSpace(n.Name)
	n.Description = strings.TrimSpace(n.Description)
	for i := range n.Servers {
		n.Servers[i].Host = strings.TrimSpace(n.Servers[i].Host)
	}
	return n
}

func (n Network) check() error {
	switch {
	case !idRe.MatchString(n.ID):
		return errors.New("id: lowercase letters, digits and -, up to 32")
	case n.Name == "" || len(n.Name) > 64:
		return errors.New("name: 1 to 64 characters")
	case len(n.Description) > 500:
		return errors.New("description: up to 500 characters")
	case n.Homepage != "" && !strings.HasPrefix(n.Homepage, "https://") && !strings.HasPrefix(n.Homepage, "http://"):
		return errors.New("homepage: an http(s) URL")
	case len(n.Servers) == 0 || len(n.Servers) > 16:
		return errors.New("servers: 1 to 16")
	case len(n.Channels) > 16 || len(n.Tags) > 16:
		return errors.New("up to 16 channels and tags")
	}
	for _, s := range n.Servers {
		if !hostRe.MatchString(s.Host) {
			return fmt.Errorf("server %q: not a host name", s.Host)
		}
		if s.Port < 1 || s.Port > 65535 {
			return fmt.Errorf("server %s: port %d", s.Host, s.Port)
		}
	}
	for _, c := range n.Channels {
		if !chanRe.MatchString(c) {
			return fmt.Errorf("channel %q", c)
		}
	}
	return nil
}
