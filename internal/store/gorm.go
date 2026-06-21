package store

import (
	"errors"
	"strings"

	"github.com/glebarez/sqlite" // pure-Go SQLite (no CGO), via modernc
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type gormStore struct{ db *gorm.DB }

var _ Store = (*gormStore)(nil)

// Open selects a backend by DSN scheme, opens it, and runs migrations.
//
//	sqlite:/var/lib/evrd/evrd.db   (or a bare path)   -> SQLite (default)
//	postgres://user:pass@host/db                      -> Postgres
func Open(dsn string) (Store, error) {
	var dialector gorm.Dialector
	switch {
	case strings.HasPrefix(dsn, "postgres://"), strings.HasPrefix(dsn, "postgresql://"):
		dialector = postgres.Open(dsn)
	default:
		dialector = sqlite.Open(strings.TrimPrefix(dsn, "sqlite:"))
	}

	db, err := gorm.Open(dialector, &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		return nil, err
	}
	if err := db.AutoMigrate(&User{}, &Network{}, &Message{}); err != nil {
		return nil, err
	}
	return &gormStore{db: db}, nil
}

func (s *gormStore) Close() error {
	sqlDB, err := s.db.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}

// --- users ---------------------------------------------------------------

func (s *gormStore) CreateUser(username, passwordHash string) (User, error) {
	u := User{Username: username, PasswordHash: passwordHash}
	if err := s.db.Create(&u).Error; err != nil {
		return User{}, err
	}
	return u, nil
}

func (s *gormStore) UserByUsername(username string) (User, error) {
	var u User
	err := s.db.Where("username = ?", username).First(&u).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return User{}, ErrNotFound
	}
	return u, err
}

func (s *gormStore) ListUsers() ([]User, error) {
	var users []User
	return users, s.db.Order("username").Find(&users).Error
}

// --- networks ------------------------------------------------------------

func (s *gormStore) ListNetworks(userID int64) ([]Network, error) {
	var nets []Network
	return nets, s.db.Where("user_id = ?", userID).Order("id").Find(&nets).Error
}

func (s *gormStore) AllEnabledNetworks() ([]Network, error) {
	var nets []Network
	return nets, s.db.Where("enabled = ?", true).Order("id").Find(&nets).Error
}

func (s *gormStore) NetworkByID(id int64) (Network, error) {
	var n Network
	err := s.db.First(&n, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return Network{}, ErrNotFound
	}
	return n, err
}

func (s *gormStore) CreateNetwork(n Network) (Network, error) {
	if err := s.db.Create(&n).Error; err != nil {
		return Network{}, err
	}
	return n, nil
}

func (s *gormStore) DeleteNetwork(userID, id int64) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("network_id = ?", id).Delete(&Message{}).Error; err != nil {
			return err
		}
		return tx.Where("id = ? AND user_id = ?", id, userID).Delete(&Network{}).Error
	})
}

func (s *gormStore) SetNetworkEnabled(id int64, enabled bool) error {
	return s.db.Model(&Network{}).Where("id = ?", id).Update("enabled", enabled).Error
}

// --- messages ------------------------------------------------------------

func (s *gormStore) AddMessage(m Message) error {
	return s.db.Create(&m).Error
}

// Buffers returns the distinct targets (channels and PM nicks) that have
// messages for a network.
func (s *gormStore) Buffers(networkID int64) ([]string, error) {
	var targets []string
	err := s.db.Model(&Message{}).
		Where("network_id = ?", networkID).
		Distinct().Pluck("target", &targets).Error
	return targets, err
}

func (s *gormStore) RecentMessages(networkID int64, target string, limit int) ([]Message, error) {
	var msgs []Message
	err := s.db.Where("network_id = ? AND target = ?", networkID, target).
		Order("id DESC").Limit(limit).Find(&msgs).Error
	if err != nil {
		return nil, err
	}
	// reverse to oldest-first
	for i, j := 0, len(msgs)-1; i < j; i, j = i+1, j-1 {
		msgs[i], msgs[j] = msgs[j], msgs[i]
	}
	return msgs, nil
}
