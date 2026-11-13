package directory

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Usage is what an install shares with evrd.sh about itself (usage_stats):
// counts and kinds, never names, addresses or messages.
type Usage struct {
	Users     int // accounts
	Active    int // accounts with a network switched on
	Networks  int // networks, every account's
	Connected int // networks connected now (this node's)
	Clients   int // IRC clients attached now (this node's)
	Nodes     int
	Uptime    time.Duration // this node's
	DB        string        // postgres, sqlite
	History   string        // scylla, pebble
	Holder    bool          // the connection holder runs
	SSO       bool          // single sign-on is set up
	// OnDirectory counts networks by the directory network they connect
	// to (its id): only networks from the public list are named.
	OnDirectory map[string]int
}

// maxListed caps the directory networks sent (the most used).
const maxListed = 20

// Header is u as the X-Evrd-Usage header:
// "users=12; active=9; …; nets=libera:5,oftc:2".
func (u Usage) Header() string {
	onOff := func(b bool) string {
		if b {
			return "on"
		}
		return "off"
	}
	parts := []string{
		fmt.Sprintf("users=%d", u.Users),
		fmt.Sprintf("active=%d", u.Active),
		fmt.Sprintf("networks=%d", u.Networks),
		fmt.Sprintf("connected=%d", u.Connected),
		fmt.Sprintf("clients=%d", u.Clients),
		fmt.Sprintf("nodes=%d", u.Nodes),
		fmt.Sprintf("uptime=%d", int64(u.Uptime.Seconds())),
		"db=" + token(u.DB),
		"history=" + token(u.History),
		"holder=" + onOff(u.Holder),
		"sso=" + onOff(u.SSO),
	}
	if len(u.OnDirectory) > 0 {
		ids := make([]string, 0, len(u.OnDirectory))
		for id := range u.OnDirectory {
			if idRe.MatchString(id) {
				ids = append(ids, id)
			}
		}
		sort.Slice(ids, func(i, j int) bool {
			a, b := u.OnDirectory[ids[i]], u.OnDirectory[ids[j]]
			return a > b || (a == b && ids[i] < ids[j])
		})
		if len(ids) > maxListed {
			ids = ids[:maxListed]
		}
		for i, id := range ids {
			ids[i] = fmt.Sprintf("%s:%d", id, u.OnDirectory[id])
		}
		if len(ids) > 0 {
			parts = append(parts, "nets="+strings.Join(ids, ","))
		}
	}
	return strings.Join(parts, "; ")
}

// token keeps a kind to letters and digits (it goes in a header).
func token(s string) string {
	s = strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, strings.ToLower(s))
	if s == "" {
		return "unknown"
	}
	return s
}

// SetUsage says what to share with evrd.sh (nil: only the install ID).
func (d *Directory) SetUsage(fn func(context.Context) Usage) {
	d.mu.Lock()
	d.usage = fn
	d.mu.Unlock()
}

// SetSharing turns usage_stats on or off. Off, evrd.sh gets no install ID,
// no counts, and a User-Agent without the version or platform.
func (d *Directory) SetSharing(on bool) {
	d.mu.Lock()
	d.private = !on
	d.mu.Unlock()
}

// HostIDs maps the directory's server host names to their network id, to
// tell which public network a user's network is.
func (d *Directory) HostIDs() map[string]string {
	out := map[string]string{}
	for _, n := range d.Networks() {
		for _, s := range n.Servers {
			out[strings.ToLower(s.Host)] = n.ID
		}
	}
	return out
}
