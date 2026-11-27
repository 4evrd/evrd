package managed

// Guests (managed_guests): with them on, a client that signs in without
// credentials gets an ephemeral session of its own, the way the managing
// service's web client lets guests in. A guest:
//
//   - gets an account of its own, marked as a guest (store.User.Guest),
//     with the one network: its own connection, reaching the server with
//     WEBIRC and the guest's address, on the nick it asked for (guarded:
//     a guest never keeps a registered nick, unless it identifies for it
//     within the grace);
//   - keeps nothing: when its client goes, the connection lingers for
//     managed_guest_linger, so a page refresh (the same address, asking
//     for the same nick) picks it up again, then quits, and the account
//     goes with its network and history;
//   - is counted: managed_guests_max at once, managed_guests_per_ip from
//     one address, and at most guestRate new ones a minute from one
//     address.
//
// The managed API never shows guests among the accounts; its health
// counts them.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/4evrd/evrd/internal/accounts"
	"github.com/4evrd/evrd/internal/store"
)

// Errors a guest's sign-in is refused with.
var (
	ErrGuestsOff   = errors.New("guests aren't allowed")
	ErrGuestsFull  = errors.New("too many guests")
	ErrGuestsPerIP = errors.New("too many guests from this address")
	ErrGuestsRate  = errors.New("too many new guests from this address")
)

// guestRate is how many new guests one address may start a minute.
const guestRate = 10

// guestPrefix starts every guest account's name.
const guestPrefix = "guest-"

// GuestBouncer is what guests need of the running connections
// (*bouncer.Bouncer).
type GuestBouncer interface {
	DeleteUser(userID int64) error
	ClientsByUser() map[int64]int
	Owns(userID int64) bool
}

// Guests keeps the guests this node runs.
type Guests struct {
	Mode     *Mode
	Accounts accounts.AccountServiceImpl
	Bouncer  GuestBouncer
	Log      *slog.Logger

	mu      sync.Mutex
	active  map[int64]*guest
	pending map[*guest]bool        // being made
	starts  map[string][]time.Time // new guests by address, this last minute
	now     func() time.Time
}

// guest is one guest's session.
type guest struct {
	id        int64
	ip, nick  string // as it signed in (nick lowercased)
	current   func() string
	lingering bool
	timer     *time.Timer
}

// GuestInput is what a guest's client sent: its address (WEBIRC's or the
// socket's), nick, user name and real name.
type GuestInput struct {
	IP, Nick, User, Realname string
}

func (g *Guests) clock() time.Time {
	if g.now != nil {
		return g.now()
	}
	return time.Now()
}

// On reports whether guests may sign in now.
func (g *Guests) On() bool { return g != nil && g.Mode.Config().Guests }

// SignIn is the account for a guest: the lingering one the same address
// left on the same nick (resumed), else a new one.
func (g *Guests) SignIn(ctx context.Context, in GuestInput) (u store.User, resumed bool, err error) {
	if !g.On() {
		return store.User{}, false, ErrGuestsOff
	}
	c := g.Mode.Config()
	nick := guestNick(in.Nick)
	g.mu.Lock()
	if g.active == nil {
		g.active, g.pending, g.starts = map[int64]*guest{}, map[*guest]bool{}, map[string][]time.Time{}
	}
	for _, s := range g.active {
		if s.lingering && s.ip == in.IP && (s.nick == strings.ToLower(nick) || strings.EqualFold(s.current(), nick)) {
			s.lingering = false
			if s.timer != nil {
				s.timer.Stop()
			}
			id := s.id
			g.mu.Unlock()
			u, err := g.Accounts.UserByID(ctx, id)
			if err == nil {
				return u, true, nil
			}
			g.forget(id)
			return g.SignIn(ctx, in)
		}
	}
	perIP := 0
	for _, s := range g.active {
		if s.ip == in.IP {
			perIP++
		}
	}
	for s := range g.pending {
		if s.ip == in.IP {
			perIP++
		}
	}
	now := g.clock()
	recent := g.starts[in.IP][:0]
	for _, t := range g.starts[in.IP] {
		if now.Sub(t) < time.Minute {
			recent = append(recent, t)
		}
	}
	g.starts[in.IP] = recent
	switch {
	case c.GuestsMax > 0 && len(g.active)+len(g.pending) >= c.GuestsMax:
		err = ErrGuestsFull
	case c.GuestsPerIP > 0 && perIP >= c.GuestsPerIP:
		err = ErrGuestsPerIP
	case len(recent) >= guestRate:
		err = ErrGuestsRate
	}
	if err != nil {
		g.mu.Unlock()
		return store.User{}, false, err
	}
	g.starts[in.IP] = append(recent, now)
	s := &guest{ip: in.IP, nick: strings.ToLower(nick), current: func() string { return "" }}
	g.pending[s] = true
	g.mu.Unlock()

	u, err = g.create(ctx, nick, in)
	g.mu.Lock()
	delete(g.pending, s)
	if err == nil {
		s.id = u.ID
		g.active[u.ID] = s
	}
	g.mu.Unlock()
	return u, false, err
}

// create makes a guest's account and network.
func (g *Guests) create(ctx context.Context, nick string, in GuestInput) (store.User, error) {
	var u store.User
	var err error
	for range 5 {
		u, err = g.Accounts.Create(accounts.CreateInput{Username: guestPrefix + randomHex(5), Role: store.RoleUser, Guest: true})
		if !errors.Is(err, accounts.ErrUsernameTaken) {
			break
		}
	}
	if err != nil {
		return store.User{}, err
	}
	one := 1
	u.MaxClients = &one // a guest is one client
	if err := g.Accounts.UpdateUser(ctx, u); err != nil {
		g.Bouncer.DeleteUser(u.ID)
		return store.User{}, err
	}
	n := store.Network{UserID: u.ID, Nick: nick, Username: guestIdent(in.User), Realname: guestRealname(in.Realname, nick),
		AuthMethod: store.AuthNone, FloodRate: 2, FloodBurst: 9}
	g.Mode.Apply(&n)
	if _, err := g.Accounts.CreateNetwork(ctx, n); err != nil {
		g.Bouncer.DeleteUser(u.ID)
		return store.User{}, err
	}
	g.Log.Info("guest signed in", "guest", u.Username, "ip", in.IP, "nick", nick)
	return u, nil
}

// Track tells the guest's session how to read its nick on the network now
// (for picking it up again after a refresh).
func (g *Guests) Track(userID int64, current func() string) {
	g.mu.Lock()
	if s := g.active[userID]; s != nil && current != nil {
		s.current = current
	}
	g.mu.Unlock()
}

// IsGuest reports whether userID is a guest this node runs.
func (g *Guests) IsGuest(userID int64) bool {
	if g == nil {
		return false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.active[userID] != nil
}

// Count is how many guests this node runs (lingering ones too).
func (g *Guests) Count() int {
	if g == nil {
		return 0
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.active)
}

// Left is the guest's client gone: once no client of it is left, its
// session ends after the linger (unless the guest is back first).
func (g *Guests) Left(userID int64) {
	if g == nil || g.Bouncer.ClientsByUser()[userID] > 0 {
		return
	}
	linger := g.Mode.Config().GuestLinger
	g.mu.Lock()
	s := g.active[userID]
	if s == nil || s.lingering {
		g.mu.Unlock()
		return
	}
	s.lingering = true
	if linger <= 0 {
		g.mu.Unlock()
		g.end(userID)
		return
	}
	s.timer = time.AfterFunc(linger, func() {
		g.mu.Lock()
		still := g.active[userID] == s && s.lingering
		g.mu.Unlock()
		if still && g.Bouncer.ClientsByUser()[userID] == 0 {
			g.end(userID)
		}
	})
	g.mu.Unlock()
}

// end ends a guest's session: its connection quits, and the account goes
// with its network and history.
func (g *Guests) end(userID int64) {
	g.forget(userID)
	if err := g.Bouncer.DeleteUser(userID); err != nil && !errors.Is(err, store.ErrNotFound) {
		g.Log.Warn("end a guest's session", "user", userID, "err", err)
		return
	}
	g.Log.Info("guest left", "user", userID)
}

func (g *Guests) forget(userID int64) {
	g.mu.Lock()
	if s := g.active[userID]; s != nil && s.timer != nil {
		s.timer.Stop()
	}
	delete(g.active, userID)
	g.mu.Unlock()
}

// Sweep removes guest accounts no session here runs and no client uses:
// left over from before a restart (or a crash). In a cluster, only this
// node's.
func (g *Guests) Sweep(ctx context.Context) (removed int, err error) {
	us, err := g.Accounts.ListUsers(ctx)
	if err != nil {
		return 0, err
	}
	clients := g.Bouncer.ClientsByUser()
	for _, u := range us {
		if !u.Guest || g.IsGuest(u.ID) || clients[u.ID] > 0 || !g.Bouncer.Owns(u.ID) {
			continue
		}
		if err := g.Bouncer.DeleteUser(u.ID); err != nil {
			return removed, err
		}
		removed++
	}
	return removed, nil
}

// Run sweeps every interval until ctx ends.
func (g *Guests) Run(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if n, err := g.Sweep(ctx); err != nil && ctx.Err() == nil {
				g.Log.Warn("sweep guests", "err", err)
			} else if n > 0 {
				g.Log.Info("removed guests left over", "guests", n)
			}
		}
	}
}

// guestNick is the nick a guest asked for, if it can go on an IRC line,
// else "guest".
func guestNick(nick string) string {
	nick = strings.TrimSpace(nick)
	if nick == "" || len(nick) > 64 || strings.ContainsAny(nick, " \r\n\x00:!@,*?#&") || strings.ContainsAny(nick[:1], "0123456789-$+%~") {
		return "guest"
	}
	return nick
}

// guestIdent is the user name a guest's client sent, letters and digits
// only (cut to 10), else "guest".
func guestIdent(user string) string {
	var b strings.Builder
	for _, r := range strings.TrimPrefix(user, "~") {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
		if b.Len() == 10 {
			break
		}
	}
	if b.Len() == 0 {
		return "guest"
	}
	return b.String()
}

// guestRealname is the real name a guest's client sent (one line), else
// its nick.
func guestRealname(real, nick string) string {
	real = strings.TrimSpace(strings.NewReplacer("\r", " ", "\n", " ", "\x00", "").Replace(real))
	if real == "" {
		return nick
	}
	if len(real) > 100 {
		real = real[:100]
	}
	return real
}

func randomHex(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// IsGuestName reports whether username is shaped like a guest's account.
func IsGuestName(username string) bool {
	return strings.HasPrefix(strings.ToLower(username), guestPrefix)
}
