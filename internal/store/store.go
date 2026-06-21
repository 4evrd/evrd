// Package store is evrd's persistence layer: users, their IRC networks, and
// buffered message history.
//
// Store is an interface; the implementation uses GORM (see gorm.go), so the
// backend is chosen by DSN scheme — sqlite by default, postgres when the DSN
// says so — with no change to callers.
package store

import (
	"errors"
	"time"
)

// ErrNotFound is returned when a row does not exist.
var ErrNotFound = errors.New("store: not found")

// User is a bouncer account.
type User struct {
	ID           int64  `gorm:"primaryKey"`
	Username     string `gorm:"uniqueIndex;not null"`
	PasswordHash string `gorm:"not null"`
	CreatedAt    time.Time
}

// Network is one IRC network configured by a user.
type Network struct {
	ID       int64    `gorm:"primaryKey"`
	UserID   int64    `gorm:"index;not null"`
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

// Message is a single buffered line for a target (channel or nick).
type Message struct {
	ID        int64  `gorm:"primaryKey"`
	NetworkID int64  `gorm:"not null;index:idx_net_target,priority:1"`
	Target    string `gorm:"index:idx_net_target,priority:2"`
	Sender    string
	Kind      string // message | notice | action | system
	Text      string
	Time      time.Time
	Origin    string `gorm:"-"` // transient: which session sent it (not persisted)
}

// Store is the persistence contract. Implementations must be safe for
// concurrent use.
type Store interface {
	// users
	CreateUser(username, passwordHash string) (User, error)
	UserByUsername(username string) (User, error)
	ListUsers() ([]User, error)

	// networks
	ListNetworks(userID int64) ([]Network, error)
	AllEnabledNetworks() ([]Network, error)
	NetworkByID(id int64) (Network, error)
	CreateNetwork(n Network) (Network, error)
	DeleteNetwork(userID, id int64) error
	SetNetworkEnabled(id int64, enabled bool) error

	// messages
	AddMessage(m Message) error
	RecentMessages(networkID int64, target string, limit int) ([]Message, error)
	Buffers(networkID int64) ([]string, error)

	Close() error
}
