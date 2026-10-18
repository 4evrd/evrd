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

	Suspended       bool `gorm:"not null;default:false"`
	SuspendedReason string

	// Per-user limits. Nil means "use the server default"; 0 means unlimited.
	MaxNetworks  *int
	MaxClients   *int
	BacklogLines *int

	// Defaults that prefill a new network.
	DefaultNick     string
	DefaultRealname string

	CreatedAt   time.Time
	UpdatedAt   time.Time
	LastLoginAt *time.Time
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
	ID       int64 `gorm:"primaryKey"`
	UserID   int64 `gorm:"index;not null"`
	Name     string
	Host     string
	Port     int
	TLS      bool
	Nick     string
	Username string
	Realname string
	Password string
	Channels []string `gorm:"serializer:json"`
	Enabled  bool
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
	NetworkByID(id int64) (Network, error)
	CreateNetwork(n Network) (Network, error)
	DeleteNetwork(userID, id int64) error
	SetNetworkEnabled(id int64, enabled bool) error
	SetNetworkChannels(id int64, channels []string) error
	// UpdateNetwork saves a network's editable settings (not Enabled).
	UpdateNetwork(n Network) error

	// cluster: nodes and user leases
	UpsertNode(n Node) error
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
	// LeaseOwner is the node holding a user's unexpired lease, if any.
	LeaseOwner(userID int64, now time.Time) (Node, UserLease, bool, error)
	// DeleteOrphanLeases drops leases of users that no longer exist.
	DeleteOrphanLeases() error

	// MigrateLegacyMessages hands every row of the old messages table to fn,
	// in order, then drops the table. A no-op once the table is gone.
	MigrateLegacyMessages(fn func(LegacyMessage) error) (int, error)

	Close() error
}
