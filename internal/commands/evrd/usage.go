package evrd

import (
	"context"
	"strings"
	"time"

	"github.com/4evrd/evrd/internal/bouncer"
	"github.com/4evrd/evrd/internal/directory"
	"github.com/4evrd/evrd/internal/store"
)

// usageSource is what the usage report is counted from.
type usageSource struct {
	users    func(context.Context) ([]store.User, error)
	networks func(context.Context) ([]store.Network, error)
	live     func() bouncer.Usage
	nodes    func() int
	hosts    func() map[string]string // directory host → network id
	started  time.Time
	db, hist string // the DSNs
	holder   bool
	sso      func() bool
}

// report counts what usage_stats shares with evrd.sh. A count that can't
// be read is left at 0.
func (u usageSource) report(ctx context.Context) directory.Usage {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	r := directory.Usage{
		Nodes: u.nodes(), Uptime: time.Since(u.started).Truncate(time.Minute),
		DB: dsnKind(u.db, "sqlite"), History: dsnKind(u.hist, "pebble"),
		Holder: u.holder, SSO: u.sso(),
	}
	live := u.live()
	r.Connected, r.Clients = live.ConnectedNetworks, live.AttachedClients
	if users, err := u.users(ctx); err == nil {
		r.Users = len(users)
	}
	nets, err := u.networks(ctx)
	if err != nil {
		return r
	}
	r.Networks = len(nets)
	hosts := u.hosts()
	active := map[int64]bool{}
	for _, n := range nets {
		if n.Enabled {
			active[n.UserID] = true
		}
		hs := []string{n.Host}
		for _, s := range n.Servers {
			hs = append(hs, s.Host)
		}
		for _, h := range hs {
			if id, ok := hosts[strings.ToLower(h)]; ok {
				if r.OnDirectory == nil {
					r.OnDirectory = map[string]int{}
				}
				r.OnDirectory[id]++
				break
			}
		}
	}
	r.Active = len(active)
	return r
}

// dsnKind is a store DSN's kind: its scheme (postgres, scylla…), else the
// local default (sqlite:, pebble: or a bare path).
func dsnKind(dsn, local string) string {
	scheme, _, ok := strings.Cut(dsn, "://")
	if ok {
		switch scheme {
		case "postgresql":
			return "postgres"
		case "cassandra":
			return "scylla"
		}
		return scheme
	}
	return local
}
