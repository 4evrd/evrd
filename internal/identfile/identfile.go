// Package identfile writes an oidentd user config (~/.oidentd.conf) so IRC
// networks see each user's ident instead of the system user evrd runs as.
//
// Like ZNC's setups, it keys on the source address: a user with a bind host
// of their own gets "from <address> { reply "<ident>" }" for each address it
// resolves to. Addresses shared by more than one user, and the host's default
// source addresses (everyone without a bind host connects from those), are
// left out, as is anything else; for those, "global { hide }" makes oidentd
// answer HIDDEN-USER so networks use the ident from USER (shown with a ~).
//
// oidentd must let evrd's system user spoof and hide (see docs), and find the
// file as that user's ~/.oidentd.conf.
package identfile

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/4evrd/evrd/internal/store"
)

// Interval is how often the file is brought up to date.
const Interval = time.Minute

// Source is what the file is made from (the store implements it).
type Source interface {
	ListUsers() ([]store.User, error)
	AllNetworks() ([]store.Network, error)
}

// Writer keeps the file at Path up to date.
type Writer struct {
	Path string
	// Exclude are source addresses never written (the node's own default
	// and spread addresses); Run adds the host's default routes' ones.
	Exclude []netip.Addr
	// Resolve looks up a bind host's addresses; nil uses the system resolver.
	Resolve func(ctx context.Context, host string) ([]netip.Addr, error)

	src  Source
	log  *slog.Logger
	last string
}

// New returns a Writer for path.
func New(path string, src Source, log *slog.Logger) *Writer {
	return &Writer{Path: path, src: src, log: log}
}

// Run writes the file now and every Interval until ctx ends.
func (w *Writer) Run(ctx context.Context) {
	w.Exclude = append(w.Exclude, defaultSources()...)
	for {
		if err := w.Update(ctx); err != nil {
			w.log.Warn("ident file not written", "path", w.Path, "err", err)
		}
		t := time.NewTimer(Interval)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-t.C:
		}
	}
}

// Update writes the file if what it should say has changed.
func (w *Writer) Update(ctx context.Context) error {
	users, err := w.src.ListUsers()
	if err != nil {
		return err
	}
	nets, err := w.src.AllNetworks()
	if err != nil {
		return err
	}
	resolve := w.Resolve
	if resolve == nil {
		resolve = func(ctx context.Context, host string) ([]netip.Addr, error) {
			return net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		}
	}
	text := Render(Entries(ctx, users, nets, resolve, w.Exclude))
	if text == w.last {
		return nil
	}
	if err := writeFile(w.Path, text); err != nil {
		return err
	}
	w.last = text
	w.log.Info("ident file written", "path", w.Path)
	return nil
}

// Entry is one source address and the ident it answers with.
type Entry struct {
	Addr  netip.Addr
	Ident string
}

// Entries works out the addresses that belong to exactly one user, and the
// ident each answers with: the networks' ident when they agree, else the
// user's default ident, else the username.
func Entries(ctx context.Context, users []store.User, nets []store.Network, resolve func(context.Context, string) ([]netip.Addr, error), exclude []netip.Addr) []Entry {
	byID := map[int64]store.User{}
	for _, u := range users {
		byID[u.ID] = u
	}
	type use struct {
		users  map[int64]bool
		idents map[string]bool
	}
	uses := map[netip.Addr]*use{}
	looked := map[string][]netip.Addr{}
	add := func(u store.User, bind, ident string) {
		bind = strings.TrimSpace(bind)
		if bind == "" {
			return
		}
		addrs, ok := looked[strings.ToLower(bind)]
		if !ok {
			if a, err := netip.ParseAddr(bind); err == nil {
				addrs = []netip.Addr{a}
			} else {
				rctx, cancel := context.WithTimeout(ctx, 5*time.Second)
				addrs, _ = resolve(rctx, bind)
				cancel()
			}
			looked[strings.ToLower(bind)] = addrs
		}
		for _, a := range addrs {
			a = a.Unmap()
			if uses[a] == nil {
				uses[a] = &use{users: map[int64]bool{}, idents: map[string]bool{}}
			}
			uses[a].users[u.ID] = true
			if ident != "" {
				uses[a].idents[ident] = true
			}
		}
	}
	for _, u := range users {
		add(u, u.BindHost, "")
	}
	for _, n := range nets {
		u, ok := byID[n.UserID]
		if !ok {
			continue
		}
		bind := n.BindHost
		if bind == "" {
			bind = u.BindHost
		}
		add(u, bind, clean(n.Username))
	}

	var out []Entry
	for a, us := range uses {
		if len(us.users) != 1 || slices.ContainsFunc(exclude, func(x netip.Addr) bool { return x.Unmap() == a }) {
			continue
		}
		var u store.User
		for id := range us.users {
			u = byID[id]
		}
		ident := ""
		if len(us.idents) == 1 {
			for i := range us.idents {
				ident = i
			}
		}
		if ident == "" {
			ident = clean(u.DefaultIdent)
		}
		if ident == "" {
			ident = clean(u.Username)
		}
		if ident != "" {
			out = append(out, Entry{Addr: a, Ident: ident})
		}
	}
	slices.SortFunc(out, func(x, y Entry) int { return x.Addr.Compare(y.Addr) })
	return out
}

// clean keeps an ident to what's safe in the file and on IRC: letters,
// digits, '.', '_' and '-', at most 10 (servers cut idents there anyway).
func clean(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r < 0x80 && (r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '_' || r == '-') {
			b.WriteRune(r)
		}
		if b.Len() == 10 {
			break
		}
	}
	return b.String()
}

// Render is the file's text.
func Render(entries []Entry) string {
	var b strings.Builder
	b.WriteString("# Written by evrd from users' bind hosts; changes here are overwritten.\n")
	b.WriteString("global { hide }\n")
	for _, e := range entries {
		fmt.Fprintf(&b, "from %s { reply \"%s\" }\n", e.Addr, e.Ident)
	}
	return b.String()
}

// writeFile replaces path's contents in one step, readable by oidentd.
func writeFile(path, text string) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".oidentd-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(text); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// defaultSources are the addresses the host connects from when nothing is
// bound: what the routes to the internet pick (no packets are sent).
func defaultSources() []netip.Addr {
	var out []netip.Addr
	for _, to := range []string{"192.0.2.1:9", "[2001:db8::1]:9"} {
		c, err := net.Dial("udp", to)
		if err != nil {
			continue
		}
		if a, ok := c.LocalAddr().(*net.UDPAddr); ok {
			if ip, ok := netip.AddrFromSlice(a.IP); ok {
				out = append(out, ip.Unmap())
			}
		}
		c.Close()
	}
	return out
}
