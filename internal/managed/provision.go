package managed

// Provisioning: the managing service creates, changes, disables and
// removes accounts (the managed API, /api/managed/users/…). Each account
// has the one network, set up from the config; the service sets the nick,
// ident, real name and the login to the network's services.

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/4evrd/evrd/internal/accounts"
	"github.com/4evrd/evrd/internal/bouncer"
	"github.com/4evrd/evrd/internal/store"
	"github.com/4evrd/evrd/internal/upstream"
)

// Bouncer is what provisioning needs of the running connections
// (*bouncer.Bouncer).
type Bouncer interface {
	Reconnect(networkID int64) error
	Status(networkID int64) upstream.Status
	CurrentNick(networkID int64) string
	StopUser(userID int64)
	ResumeUser(userID int64)
	DeleteUser(userID int64) error
	EditChannels(networkID int64, before, after []string)
	ClientsByUser() map[int64]int
}

// UserInput is a PUT's body: what to set. Fields left out stay as they are
// (or, for a new account, take their defaults).
type UserInput struct {
	// Subject is the account's user at the token issuer ("sub"): tokens
	// carrying it sign in to this account.
	Subject *string `json:"subject"`
	Email   *string `json:"email"`
	// Nick on the network (default: the username), AltNick tried when it's
	// taken (default: Nick + "_"), Ident the user part of nick!user@host
	// (default: the username) and Realname (default: Nick).
	Nick     *string `json:"nick"`
	AltNick  *string `json:"altNick"`
	Ident    *string `json:"ident"`
	Realname *string `json:"realname"`
	// Disabled cuts the account off: its clients and its connection go,
	// and it can't sign in until it's enabled again.
	Disabled *bool `json:"disabled"`
	// Auth is how the connection logs in to the network's services.
	Auth *AuthInput `json:"auth"`
	// Channels are joined on connect (replacing the list).
	Channels *[]string `json:"channels"`
}

// AuthInput is the login to the network's services: Method "sasl" (SASL
// PLAIN), "nickserv" (IDENTIFY once connected), "auto" (SASL when offered,
// else IDENTIFY) or "none"; Account the services account (default: the
// nick); Password its password ("" keeps the one set, with Method "none"
// it's cleared).
type AuthInput struct {
	Method   string `json:"method"`
	Account  string `json:"account"`
	Password string `json:"password"`
}

// User is an account as the managed API shows it.
type User struct {
	Username string `json:"username"`
	ID       int64  `json:"id"`
	Subject  string `json:"subject,omitempty"`
	Email    string `json:"email,omitempty"`
	Disabled bool   `json:"disabled"`
	Nick     string `json:"nick"`
	AltNick  string `json:"altNick,omitempty"`
	Ident    string `json:"ident"`
	Realname string `json:"realname"`
	Auth     struct {
		Method      string `json:"method"`
		Account     string `json:"account,omitempty"`
		HasPassword bool   `json:"hasPassword"`
	} `json:"auth"`
	Channels []string `json:"channels"`
	// Status is the connection's: disconnected (never connected yet, or
	// disabled), connecting, connected or reconnecting.
	Status string `json:"status"`
	// CurrentNick is the nick on the server now ("" while there's no
	// connection): Nick, or another one when Nick was taken or is
	// registered to someone else (the nick guard).
	CurrentNick string `json:"currentNick,omitempty"`
	// IdentifiedNick is the registered nick the user last identified for
	// with services during the nick guard's grace: tried first on the
	// next connections ("" none).
	IdentifiedNick string `json:"identifiedNick,omitempty"`
	// LastNick is the nick the user last changed to on the server: new
	// connections register with it ("" none; a new Nick clears it).
	LastNick string `json:"lastNick,omitempty"`
	// Clients is how many clients are attached now.
	Clients int `json:"clients"`
	// Created is set when this call made the account.
	Created bool `json:"created,omitempty"`
}

// Errors for the API to answer with.
var (
	ErrSubjectTaken = errors.New("another account has this subject")
	ErrBadInput     = errors.New("invalid input")
)

// authMethods maps the API's login methods to the network's.
var authMethods = map[string]string{"sasl": store.AuthSASL, "nickserv": store.AuthNickServ, "auto": store.AuthAuto, "none": store.AuthNone}

// Provisioner carries out the managed API.
type Provisioner struct {
	Mode     *Mode
	Accounts accounts.AccountServiceImpl
	Bouncer  Bouncer
	// Guests are counted in the health (nil: none).
	Guests *Guests
	// Store keeps the live settings (live.go).
	Store LiveStore
}

// Live is the live settings in effect.
func (p *Provisioner) Live() Live { return p.Mode.Live() }

// SetLive changes the live settings.
func (p *Provisioner) SetLive(ctx context.Context, in LiveInput) (Live, error) {
	return p.Mode.SetLive(ctx, p.Store, in)
}

// ResetLive goes back to the config's settings.
func (p *Provisioner) ResetLive(ctx context.Context) (Live, error) {
	return p.Mode.ResetLive(ctx, p.Store)
}

// ErrGuestAccount is a username that's a guest's: the managed API leaves
// guests alone.
var ErrGuestAccount = fmt.Errorf("%w: that username is a guest's", ErrBadInput)

// Put creates or changes username's account and its network.
func (p *Provisioner) Put(ctx context.Context, username string, in UserInput) (User, error) {
	name, err := accounts.NormalizeUsername(username)
	if err != nil {
		return User{}, err
	}
	if err := checkInput(in); err != nil {
		return User{}, err
	}
	u, err := p.Accounts.UserByUsername(ctx, name)
	if err == nil && u.Guest {
		return User{}, ErrGuestAccount
	}
	created := false
	switch {
	case errors.Is(err, store.ErrNotFound):
		ci := accounts.CreateInput{Username: name, Role: store.RoleUser}
		if in.Subject != nil {
			if err := p.subjectFree(ctx, *in.Subject, 0); err != nil {
				return User{}, err
			}
			ci.Subject = strings.TrimSpace(*in.Subject)
		}
		if in.Email != nil {
			ci.Email = *in.Email
		}
		if u, err = p.Accounts.Create(ci); err != nil {
			return User{}, err
		}
		created = true
	case err != nil:
		return User{}, err
	}
	before := u
	if in.Subject != nil {
		sub := strings.TrimSpace(*in.Subject)
		if err := p.subjectFree(ctx, sub, u.ID); err != nil {
			return User{}, err
		}
		if sub == "" {
			u.OIDCSubject = nil
		} else {
			u.OIDCSubject = &sub
		}
	}
	if in.Email != nil {
		u.Email = strings.TrimSpace(*in.Email)
	}
	if in.Disabled != nil {
		u.Suspended = *in.Disabled
		u.SuspendedReason = ""
	}
	if !created || in.Disabled != nil {
		if err := p.Accounts.UpdateUser(ctx, u); err != nil {
			return User{}, err
		}
	}

	n, had, err := p.network(ctx, u.ID)
	if err != nil {
		return User{}, err
	}
	old := n
	p.Mode.Apply(&n)
	if !had {
		n.UserID, n.Nick, n.Username = u.ID, u.Username, u.Username
		n.FloodRate, n.FloodBurst = 2, 9
	}
	applyInput(&n, in)
	if n.Realname == "" {
		n.Realname = n.Nick
	}
	if had {
		err = p.Accounts.UpdateNetwork(ctx, n)
	} else {
		n, err = p.Accounts.CreateNetwork(ctx, n) // connects on the first client
	}
	if err != nil {
		return User{}, err
	}

	switch {
	case u.Suspended && !before.Suspended:
		p.Bouncer.StopUser(u.ID)
	case !u.Suspended && before.Suspended:
		p.Bouncer.ResumeUser(u.ID)
	case had && !u.Suspended && p.Bouncer.Status(n.ID) != upstream.StatusDisconnected:
		if relinks(old, n) {
			if err := p.Bouncer.Reconnect(n.ID); err != nil && !errors.Is(err, bouncer.ErrNotOwner) {
				return User{}, err
			}
		} else if !slices.Equal(old.Channels, n.Channels) {
			p.Bouncer.EditChannels(n.ID, old.Channels, n.Channels)
		}
	}
	out := p.view(u, n)
	out.Created = created
	return out, nil
}

// subjectFree checks no account but id has sub.
func (p *Provisioner) subjectFree(ctx context.Context, sub string, id int64) error {
	sub = strings.TrimSpace(sub)
	if sub == "" {
		return nil
	}
	o, err := p.Accounts.UserBySubject(ctx, sub)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return nil
	case err != nil:
		return err
	case o.ID != id:
		return ErrSubjectTaken
	}
	return nil
}

// network is the user's managed network (had false: a new one to create).
// A lone network under another name (the config renamed it) is it too.
func (p *Provisioner) network(ctx context.Context, userID int64) (store.Network, bool, error) {
	nets, err := p.Accounts.ListNetworks(ctx, userID)
	if err != nil {
		return store.Network{}, false, err
	}
	for _, n := range nets {
		if p.Mode.IsNetwork(n) {
			return n, true, nil
		}
	}
	if len(nets) == 1 {
		return nets[0], true, nil
	}
	return store.Network{}, false, nil
}

// checkInput refuses what can't go on an IRC line.
func checkInput(in UserInput) error {
	bad := func(field, why string) error { return fmt.Errorf("%w: %s %s", ErrBadInput, field, why) }
	word := func(field string, v *string) error {
		if v != nil && (*v == "" || strings.ContainsAny(*v, " \r\n\x00:!@,") || len(*v) > 64) {
			return bad(field, "must be one word, without : ! @ or commas")
		}
		return nil
	}
	for _, f := range []struct {
		name string
		v    *string
	}{{"nick", in.Nick}, {"ident", in.Ident}} {
		if err := word(f.name, f.v); err != nil {
			return err
		}
	}
	if in.AltNick != nil && *in.AltNick != "" {
		if err := word("altNick", in.AltNick); err != nil {
			return err
		}
	}
	if in.Realname != nil && strings.ContainsAny(*in.Realname, "\r\n\x00") {
		return bad("realname", "must be one line")
	}
	if a := in.Auth; a != nil {
		if _, ok := authMethods[a.Method]; !ok {
			return bad("auth.method", "must be sasl, nickserv, auto or none")
		}
		if strings.ContainsAny(a.Account+a.Password, "\r\n\x00") || strings.Contains(a.Account, " ") {
			return bad("auth", "account and password must be one line (the account one word)")
		}
	}
	if in.Channels != nil {
		for _, ch := range *in.Channels {
			if ch == "" || !strings.ContainsRune("#&+!", rune(ch[0])) || strings.ContainsAny(ch, " ,\r\n\x00\x07") {
				return bad("channels", fmt.Sprintf("%q isn't a channel name", ch))
			}
		}
	}
	return nil
}

// applyInput sets what in gives on n.
func applyInput(n *store.Network, in UserInput) {
	if in.Nick != nil {
		n.Nick = *in.Nick
	}
	if in.AltNick != nil {
		n.AltNick = *in.AltNick
	}
	if in.Ident != nil {
		n.Username = *in.Ident
	}
	if in.Realname != nil {
		n.Realname = strings.TrimSpace(*in.Realname)
	}
	if a := in.Auth; a != nil {
		n.AuthMethod, n.SASLUser = authMethods[a.Method], a.Account
		switch {
		case a.Method == "none":
			n.Password = ""
		case a.Password != "":
			n.Password = a.Password
		}
		if a.Method == "nickserv" && a.Account != "" {
			n.IdentifyArgs = store.IdentifyAccount
		}
	}
	if in.Channels != nil {
		n.Channels = slices.Clone(*in.Channels)
	}
}

// relinks reports whether going from a to b needs a new connection: the
// server, the identity or the login changed.
func relinks(a, b store.Network) bool {
	return a.Host != b.Host || a.Port != b.Port || a.TLS != b.TLS || !slices.Equal(a.Servers, b.Servers) ||
		a.Nick != b.Nick || a.Username != b.Username || a.Realname != b.Realname ||
		a.AuthMethod != b.AuthMethod || a.SASLUser != b.SASLUser || a.Password != b.Password ||
		a.TrustPKI != b.TrustPKI || !slices.Equal(a.Fingerprints, b.Fingerprints)
}

// view is u and its network n for the API.
func (p *Provisioner) view(u store.User, n store.Network) User {
	out := User{
		Username: u.Username, ID: u.ID, Email: u.Email, Disabled: u.Suspended,
		Nick: n.Nick, AltNick: n.AltNick, Ident: orElse(n.Username, n.Nick), Realname: orElse(n.Realname, n.Nick),
		Channels: append([]string{}, n.Channels...), Status: string(upstream.StatusDisconnected),
		IdentifiedNick: n.IdentifiedNick, LastNick: n.LastNick,
	}
	if u.OIDCSubject != nil {
		out.Subject = *u.OIDCSubject
	}
	for k, v := range authMethods {
		if v == n.Auth() {
			out.Auth.Method = k
		}
	}
	out.Auth.Account, out.Auth.HasPassword = n.SASLUser, n.Password != ""
	if n.ID != 0 {
		out.Status = string(p.Bouncer.Status(n.ID))
		if out.Status != string(upstream.StatusDisconnected) {
			out.CurrentNick = p.Bouncer.CurrentNick(n.ID)
		}
	}
	out.Clients = p.Bouncer.ClientsByUser()[u.ID]
	return out
}

// Get is username's account.
func (p *Provisioner) Get(ctx context.Context, username string) (User, error) {
	u, err := p.Accounts.UserByUsername(ctx, strings.ToLower(strings.TrimSpace(username)))
	if err == nil && u.Guest {
		err = store.ErrNotFound
	}
	if err != nil {
		return User{}, err
	}
	n, _, err := p.network(ctx, u.ID)
	if err != nil {
		return User{}, err
	}
	return p.view(u, n), nil
}

// List is every account (admins too: they may have the network).
func (p *Provisioner) List(ctx context.Context) ([]User, error) {
	us, err := p.Accounts.ListUsers(ctx)
	if err != nil {
		return nil, err
	}
	out := []User{}
	for _, u := range us {
		if u.Guest {
			continue
		}
		n, _, err := p.network(ctx, u.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, p.view(u, n))
	}
	return out, nil
}

// Delete removes username's account, with its network and history.
func (p *Provisioner) Delete(ctx context.Context, username string) error {
	u, err := p.Accounts.UserByUsername(ctx, strings.ToLower(strings.TrimSpace(username)))
	if err == nil && u.Guest {
		err = store.ErrNotFound
	}
	if err != nil {
		return err
	}
	return p.Bouncer.DeleteUser(u.ID)
}

// Reconcile brings every account's network in line with the config (run at
// startup, before connecting: the server or its TLS may have changed).
func (p *Provisioner) Reconcile(ctx context.Context) (changed int, err error) {
	us, err := p.Accounts.ListUsers(ctx)
	if err != nil {
		return 0, err
	}
	for _, u := range us {
		n, had, err := p.network(ctx, u.ID)
		if err != nil {
			return changed, err
		}
		if !had {
			continue
		}
		old := n
		p.Mode.Apply(&n)
		if relinks(old, n) || old.Name != n.Name || old.BindHost != n.BindHost || len(old.Modules) > 0 {
			if err := p.Accounts.UpdateNetwork(ctx, n); err != nil {
				return changed, err
			}
			changed++
		}
	}
	return changed, nil
}

// Health is how managed mode is doing.
type Health struct {
	OK      bool          `json:"ok"`
	Network HealthNetwork `json:"network"`
	Users   struct {
		Total    int `json:"total"`
		Disabled int `json:"disabled"`
		// Attached are the accounts with a client attached now.
		Attached int `json:"attached"`
	} `json:"users"`
	Clients int `json:"clients"`
	// Connections counts the network connections by status.
	Connections map[string]int `json:"connections"`
	// Tokens says whether client sign-in by access token is set up.
	Tokens bool `json:"tokens"`
	// WebIRC says whether connections send WEBIRC.
	WebIRC bool `json:"webirc"`
	// Guests: whether they may sign in, and how many there are now on
	// this node (lingering ones too). They aren't in Users or
	// Connections.
	Guests HealthGuests `json:"guests"`
}

// HealthGuests is the guests' part of the health.
type HealthGuests struct {
	Enabled bool `json:"enabled"`
	Active  int  `json:"active"`
	Max     int  `json:"max"`
	PerIP   int  `json:"perIP"`
}

// HealthNetwork is the configured network.
type HealthNetwork struct {
	Name string `json:"name"`
	Host string `json:"host"`
	Port int    `json:"port"`
	TLS  bool   `json:"tls"`
}

// Health reports on every account's connection. OK means sign-in by token
// is set up and no connection that should be up is failing: none is
// reconnecting, or at least one is connected.
func (p *Provisioner) Health(ctx context.Context) (Health, error) {
	c := p.Mode.Config()
	h := Health{Network: HealthNetwork{c.Network, c.Host, c.Port, c.TLS}, Connections: map[string]int{},
		Tokens: p.Mode.Tokens() != nil, WebIRC: c.WebIRCPassword != "",
		Guests: HealthGuests{Enabled: c.Guests, Active: p.Guests.Count(), Max: c.GuestsMax, PerIP: c.GuestsPerIP}}
	for _, s := range []upstream.Status{upstream.StatusConnected, upstream.StatusConnecting, upstream.StatusReconnecting, upstream.StatusDisconnected} {
		h.Connections[string(s)] = 0
	}
	us, err := p.Accounts.ListUsers(ctx)
	if err != nil {
		return h, err
	}
	clients := p.Bouncer.ClientsByUser()
	for _, u := range us {
		if u.Guest {
			continue
		}
		h.Users.Total++
		if u.Suspended {
			h.Users.Disabled++
		}
		if clients[u.ID] > 0 {
			h.Users.Attached++
			h.Clients += clients[u.ID]
		}
		n, had, err := p.network(ctx, u.ID)
		if err != nil {
			return h, err
		}
		if had {
			h.Connections[string(p.Bouncer.Status(n.ID))]++
		}
	}
	failing := h.Connections[string(upstream.StatusReconnecting)] > 0 && h.Connections[string(upstream.StatusConnected)] == 0
	h.OK = h.Tokens && !failing
	return h, nil
}

func orElse(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
