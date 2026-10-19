// Package store is evrd's account database: users, their sessions and IRC
// client passwords, and their IRC networks. Chat history lives elsewhere, in
// internal/history.
//
// Store is an interface; the implementation uses GORM (see gorm.go), so the
// backend is chosen by DSN scheme — postgres in deployments, sqlite for tests
// and single-file installs — with no change to callers.
package store

import (
	"errors"
	"strings"
	"time"
)

// ErrNotFound is returned when a row does not exist.
var ErrNotFound = errors.New("store: not found")

// Roles a user can have.
const (
	RoleUser  = "user"
	RoleAdmin = "admin"
)

// User is a bouncer account.
type User struct {
	ID       int64  `gorm:"primaryKey"`
	Username string `gorm:"uniqueIndex;not null"`
	Email    string
	// PasswordHash is the bcrypt hash of the local password; empty when the
	// account only signs in through the identity provider.
	PasswordHash string
	// OIDCSubject links the account to its identity-provider user (the ID
	// token's "sub"). Nil for local-only accounts.
	OIDCSubject *string `gorm:"column:oidc_subject;uniqueIndex"`
	Role        string  `gorm:"not null;default:user"`
	// SSOClaimable marks an account made ahead of its owner's first sign-in
	// (by the provisioning API): the first SSO sign-in with the same username
	// and email links to it, even without oidc_link_existing.
	SSOClaimable bool `gorm:"not null;default:false"`

	Suspended       bool `gorm:"not null;default:false"`
	SuspendedReason string

	// Per-user limits. Nil means "use the server default"; 0 means unlimited.
	MaxNetworks  *int
	MaxClients   *int
	BacklogLines *int

	// Defaults that prefill a new network.
	DefaultNick     string
	DefaultAltNick  string // tried when the nick is taken
	DefaultRealname string
	DefaultIdent    string
	// QuitMsg is sent when a network is disconnected (networks can override).
	QuitMsg string
	// BindHost is the default local address networks connect from.
	BindHost string
	// DenySetBindHost stops the user choosing a bind host (admins set it).
	DenySetBindHost bool `gorm:"not null;default:false"`
	// AutoAway marks the user away on every network while no client (IRC or
	// web) is attached.
	AutoAway bool `gorm:"not null;default:false"`

	// Playback to IRC clients: how many lines per channel and query, and
	// whether a buffer, once played back, starts empty next time (only lines
	// since the last playback are sent).
	PlaybackLines        int  `gorm:"not null;default:50"`
	AutoClearChanBuffer  bool `gorm:"not null;default:false"`
	AutoClearQueryBuffer bool `gorm:"not null;default:true"`
	// Timestamps for clients without server-time: the time is added to the
	// text (before or after) in TimestampFormat (strftime) and Timezone.
	PrependTimestamp bool   `gorm:"not null;default:true"`
	AppendTimestamp  bool   `gorm:"not null;default:false"`
	TimestampFormat  string `gorm:"not null;default:'[%H:%M:%S]'"`
	Timezone         string
	// CTCPReplies answers CTCP requests (VERSION, TIME, …) for every network
	// with a fixed text; an empty text blocks the request.
	CTCPReplies map[string]string `gorm:"serializer:json"`
	// AllowedHosts restricts where clients may sign in from (IPs, CIDRs or
	// wildcards like 192.168.*); empty allows anywhere.
	AllowedHosts []string `gorm:"serializer:json"`

	// MustChangePassword is set for a temporary password an admin chose.
	MustChangePassword bool `gorm:"not null;default:false"`

	CreatedAt   time.Time
	UpdatedAt   time.Time
	LastLoginAt *time.Time
	// LastSeenAt is the last time a client attached or detached, or the web
	// app was used.
	LastSeenAt *time.Time
}

// IsAdmin reports whether the user has the admin role.
func (u User) IsAdmin() bool { return u.Role == RoleAdmin }

// Session is a signed-in web session. ID is the SHA-256 of the cookie token,
// so a leaked database row can't be replayed as a cookie.
type Session struct {
	ID         string `gorm:"primaryKey;size:64"`
	UserID     int64  `gorm:"index;not null"`
	Method     string // password | oidc
	IDToken    string // OIDC id_token, kept only as the logout hint
	IP         string
	UserAgent  string
	CreatedAt  time.Time
	LastSeenAt time.Time
	ExpiresAt  time.Time `gorm:"index"`
}

// ClientPassword lets a native IRC client sign in (PASS user:secret) — the
// only way to attach for accounts that sign in to the web through SSO.
type ClientPassword struct {
	ID         int64  `gorm:"primaryKey"`
	UserID     int64  `gorm:"index;not null"`
	Name       string `gorm:"not null"`
	Hash       string `gorm:"not null"`
	CreatedAt  time.Time
	LastUsedAt *time.Time
}

// Network is one IRC network configured by a user.
type Network struct {
	ID     int64 `gorm:"primaryKey"`
	UserID int64 `gorm:"index;not null"`
	Name   string
	// Host, Port and TLS mirror the first entry of Servers (kept as columns
	// for listings and older rows).
	Host string
	Port int
	TLS  bool
	// Servers are tried in order: each reconnect moves on to the next one.
	Servers []Server `gorm:"serializer:secretservers"`

	Nick     string
	AltNick  string // tried when Nick is taken; empty = the user's default
	Username string // ident: the user part of nick!user@host
	Realname string
	QuitMsg  string // empty = the user's default
	// BindHost is the local address (or a hostname for one) to connect
	// from; empty = the user's default, then the server's policy.
	BindHost string

	// Password is the SASL PLAIN password; SASLUser the account name (empty =
	// the ident, then the nick). Without SASL support it's sent as PASS.
	Password string `gorm:"serializer:secret"`
	SASLUser string

	// Server certificates: accepted if the SHA-256 fingerprint is listed, or
	// TrustAllCerts is set, or (TrustPKI) the chain verifies for the host.
	TrustAllCerts bool
	TrustPKI      bool     `gorm:"not null;default:true"`
	Fingerprints  []string `gorm:"serializer:json"` // lowercase hex, no colons

	// Encoding is a charset name for networks that don't speak UTF-8.
	// "^name" only decodes incoming text that isn't valid UTF-8 and sends
	// UTF-8; "name" decodes and sends in that charset.
	Encoding string

	// FloodRate is lines per second sent once FloodBurst lines have gone out
	// at once; 0 turns flood protection off.
	FloodRate  float64 `gorm:"not null;default:2"`
	FloodBurst int     `gorm:"not null;default:9"`
	JoinDelay  int     // seconds to wait after connecting before joining

	Channels        []string                   `gorm:"serializer:json"`
	ChannelSettings map[string]ChannelSettings `gorm:"serializer:json"` // by lowercased name
	Enabled         bool

	// Modules are the network's modules (internal/modules) by name: on or
	// off, and their settings.
	Modules map[string]ModuleConfig `gorm:"serializer:json"`
}

// ModuleConfig is one module on a network.
type ModuleConfig struct {
	Enabled  bool              `json:"enabled"`
	Settings map[string]string `json:"settings,omitempty"`
}

// Server is one of a network's servers.
type Server struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	TLS      bool   `json:"tls"`
	Password string `json:"password,omitempty"` // sent as PASS
}

// ChannelSettings are per-channel options.
type ChannelSettings struct {
	Key string `json:"key,omitempty"`
	// Detached channels stay joined but are hidden from IRC clients until
	// they /join them again.
	Detached bool `json:"detached,omitempty"`
	// Disabled channels keep their settings but aren't joined on connect.
	Disabled bool `json:"disabled,omitempty"`
}

// ServerList is the network's servers, falling back to Host/Port/TLS for
// rows saved before Servers existed.
func (n Network) ServerList() []Server {
	if len(n.Servers) > 0 {
		return n.Servers
	}
	if n.Host == "" {
		return nil
	}
	return []Server{{Host: n.Host, Port: n.Port, TLS: n.TLS}}
}

// Channel returns a channel's settings.
func (n Network) Channel(name string) ChannelSettings {
	return n.ChannelSettings[strings.ToLower(name)]
}

// ReadMarker is how far a user has read a buffer.
type ReadMarker struct {
	NetworkID int64  `gorm:"primaryKey;autoIncrement:false"`
	Buffer    string `gorm:"primaryKey;size:255"` // lowercased
	ReadAt    time.Time
}

// UserEvent is an entry in a user's activity feed (network lost, netsplit,
// client attached…).
type UserEvent struct {
	ID     int64     `gorm:"primaryKey"`
	UserID int64     `gorm:"index:idx_user_event,priority:1;not null"`
	At     time.Time `gorm:"index:idx_user_event,priority:2"`
	Kind   string
	Title  string
	Detail string
}

// Node is an evrd process in the cluster, as it advertises itself.
type Node struct {
	ID string `gorm:"primaryKey;size:128"`
	// HTTPURL and IRCAddr are the node's internal endpoints: where other
	// nodes forward a user's web requests and IRC clients.
	HTTPURL     string
	IRCAddr     string
	StartedAt   time.Time
	HeartbeatAt time.Time `gorm:"index"`
}

// Setting is a server setting changed in the admin UI: a config directive
// and its value, shared by every node (see config.Specs).
type Setting struct {
	Key       string `gorm:"primaryKey;size:64"`
	Value     string `gorm:"serializer:secret"` // encrypted like other secrets (client secrets live here)
	UpdatedAt time.Time
	UpdatedBy string
}

// CertCacheEntry is a TLS certificate and key evrd keeps for itself: Let's
// Encrypt's (the ACME client's cache) or the self-signed one, shared by
// every node and encrypted like other secrets.
type CertCacheEntry struct {
	Key       string `gorm:"primaryKey;size:255"`
	Data      string `gorm:"serializer:secret"`
	UpdatedAt time.Time
}

// NodeConf is a node's last attempt to write the settings into its config
// file (settings_write_conf).
type NodeConf struct {
	NodeID  string `gorm:"primaryKey;size:128"`
	Path    string
	WroteAt time.Time
	Error   string
	// Keys are the settings last written, so a node that was down when one
	// was reset still removes it from its file.
	Keys []string `gorm:"serializer:json"`
}

// UserLease says which node owns a user's live state (their upstream
// connections, attached clients and web chat sockets) until ExpiresAt. Epoch
// grows each time the lease changes hands.
type UserLease struct {
	UserID    int64  `gorm:"primaryKey;autoIncrement:false"`
	NodeID    string `gorm:"index;size:128;not null"`
	Epoch     int64  `gorm:"not null"`
	ExpiresAt time.Time
}

// LegacyMessage is a row of the messages table that held chat history before
// it moved to internal/history. Only read, once, to migrate it.
type LegacyMessage struct {
	ID        int64
	NetworkID int64
	Target    string
	Sender    string
	Kind      string
	Text      string
	Extra     string
	Time      time.Time
}

// Store is the persistence contract. Implementations must be safe for
// concurrent use.
type Store interface {
	// users
	CreateUser(u User) (User, error)
	UserByID(id int64) (User, error)
	UserByUsername(username string) (User, error)
	UserBySubject(subject string) (User, error)
	ListUsers() ([]User, error)
	UpdateUser(u User) error
	// DeleteUser removes the user and everything they own: networks, their
	// history, sessions and client passwords.
	DeleteUser(id int64) error
	// CountAdmins counts admins who are not suspended.
	CountAdmins() (int64, error)

	// sessions
	CreateSession(s Session) error
	SessionByID(id string) (Session, error)
	TouchSession(id string, at time.Time) error
	ListSessions(userID int64) ([]Session, error)
	DeleteSession(userID int64, id string) error
	// DeleteUserSessions signs a user out everywhere except keep ("" = all).
	DeleteUserSessions(userID int64, keep string) error
	DeleteExpiredSessions(now time.Time) error

	// IRC client passwords
	CreateClientPassword(c ClientPassword) (ClientPassword, error)
	ListClientPasswords(userID int64) ([]ClientPassword, error)
	DeleteClientPassword(userID, id int64) error
	TouchClientPassword(id int64, at time.Time) error

	// networks
	ListNetworks(userID int64) ([]Network, error)
	CountNetworks(userID int64) (int64, error)
	// NetworkCounts is the number of networks per user ID.
	NetworkCounts() (map[int64]int64, error)
	AllEnabledNetworks() ([]Network, error)
	AllNetworks() ([]Network, error)
	NetworkByID(id int64) (Network, error)
	CreateNetwork(n Network) (Network, error)
	DeleteNetwork(userID, id int64) error
	SetNetworkEnabled(id int64, enabled bool) error
	SetNetworkChannels(id int64, channels []string) error
	SetNetworkChannelSettings(id int64, settings map[string]ChannelSettings) error
	// EncryptSecrets re-encrypts network secrets under the current key.
	EncryptSecrets() (int, error)
	// UpdateNetwork saves a network's editable settings (not Enabled).
	UpdateNetwork(n Network) error

	// read markers
	SetReadMarker(networkID int64, buffer string, at time.Time) error
	// ReadMarkers is how far each of a network's buffers is read (by
	// lowercased buffer name).
	ReadMarkers(networkID int64) (map[string]time.Time, error)

	// activity feed
	AddUserEvent(e UserEvent) error
	// UserEvents lists a user's newest events since since, newest first.
	UserEvents(userID int64, since time.Time, limit int) ([]UserEvent, error)
	// PruneUserEvents drops events older than before.
	PruneUserEvents(before time.Time) error

	TouchUserSeen(userID int64, at time.Time) error

	// Ping checks the database is reachable.
	Ping() error

	// cluster: nodes and user leases
	UpsertNode(n Node) error

	// Settings changed in the admin UI. SaveSettings sets and deletes keys
	// in one go.
	Settings() ([]Setting, error)
	SaveSettings(set map[string]string, del []string, by string) error
	SetNodeConf(c NodeConf) error
	NodeConfs() ([]NodeConf, error)

	// TLS certificates evrd gets or makes (see CertCacheEntry). CertCache
	// returns ErrNotFound for a missing key.
	CertCache(key string) (string, error)
	PutCertCache(key, data string) error
	DeleteCertCache(key string) error
	// DeleteNodesBefore drops nodes last heard from before t.
	DeleteNodesBefore(t time.Time) error
	// LiveNodes lists nodes that sent a heartbeat after since.
	LiveNodes(since time.Time) ([]Node, error)
	// AcquireLease takes a user's lease for node until until, if it's free
	// (missing or expired at now) or already node's. Reports whether node
	// holds it afterwards.
	AcquireLease(userID int64, node string, until, now time.Time) (bool, error)
	// RenewLeases extends every lease node still holds (unexpired at now).
	RenewLeases(node string, until, now time.Time) error
	// HeldLeases lists the users whose unexpired lease node holds.
	HeldLeases(node string, now time.Time) ([]int64, error)
	// ReleaseLease expires node's lease on a user so another node can take it.
	ReleaseLease(userID int64, node string, now time.Time) error
	// Leases lists every unexpired lease.
	Leases(now time.Time) ([]UserLease, error)
	// LeaseOwner is the node holding a user's unexpired lease, if any.
	LeaseOwner(userID int64, now time.Time) (Node, UserLease, bool, error)
	// DeleteOrphanLeases drops leases of users that no longer exist.
	DeleteOrphanLeases() error

	// MigrateLegacyMessages hands every row of the old messages table to fn,
	// in order, then drops the table. A no-op once the table is gone.
	MigrateLegacyMessages(fn func(LegacyMessage) error) (int, error)

	Close() error
}
