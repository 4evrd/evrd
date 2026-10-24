package store

import (
	"context"
	"errors"
	"strings"

	"github.com/glebarez/sqlite" // pure-Go SQLite (no CGO), via modernc
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// DB is the database connection the repositories share.
type DB struct{ DB *gorm.DB }

// gormStore is every repository on one DB (see Store).
type gormStore struct {
	UserRepositoryImpl
	SessionRepositoryImpl
	ClientPasswordRepositoryImpl
	NetworkRepositoryImpl
	ReadMarkerRepositoryImpl
	BufferPlaybackRepositoryImpl
	UserEventRepositoryImpl
	ClusterRepositoryImpl
	SettingRepositoryImpl
	CacheRepositoryImpl
	LanguagePackRepositoryImpl
	MFARepositoryImpl
	PendingSignInRepositoryImpl
	ClientDisconnectRepositoryImpl

	db *DB
}

var _ Store = (*gormStore)(nil)

// Open selects a backend by DSN scheme, opens it, and runs migrations.
//
//	sqlite:/var/lib/evrd/evrd.db   (or a bare path)   -> SQLite (default)
//	postgres://user:pass@host/db                      -> Postgres
func Open(dsn string) (Store, error) {
	db, err := NewDatabase(dsn)
	if err != nil {
		return nil, err
	}
	return newStore(db), nil
}

// NewDatabase opens the database at dsn (see Open), records every query's
// duration (see the metrics plugin) and runs migrations.
func NewDatabase(dsn string) (*DB, error) {
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
	if err := db.Use(metricsPlugin{}); err != nil {
		return nil, err
	}
	hadPlayback := db.Migrator().HasTable(&BufferPlayback{})
	if err := db.AutoMigrate(&User{}, &Session{}, &ClientPassword{}, &Network{}, &Node{}, &UserLease{}, &ReadMarker{}, &BufferPlayback{}, &UserEvent{}, &Setting{}, &NodeConf{}, &CertCacheEntry{}, &CacheEntry{}, &LanguagePack{}, &UserLanguagePack{}, &UserTOTP{}, &RecoveryCode{}, &PendingSignIn{}, &ClientDisconnect{}); err != nil {
		return nil, err
	}
	if !hadPlayback {
		// Auto-clearing buffers went by the account's last visit before each
		// network kept its own: start every network from there.
		if err := db.Exec(`INSERT INTO buffer_playbacks (network_id, buffer, cleared_at)
			SELECT networks.id, '', users.last_seen_at FROM networks JOIN users ON users.id = networks.user_id
			WHERE users.last_seen_at IS NOT NULL`).Error; err != nil {
			return nil, err
		}
	}
	return &DB{DB: db}, nil
}

// newStore builds every repository on db.
func newStore(db *DB) *gormStore {
	return &gormStore{
		UserRepositoryImpl:             NewUserRepository(db),
		SessionRepositoryImpl:          NewSessionRepository(db),
		ClientPasswordRepositoryImpl:   NewClientPasswordRepository(db),
		NetworkRepositoryImpl:          NewNetworkRepository(db),
		ReadMarkerRepositoryImpl:       NewReadMarkerRepository(db),
		BufferPlaybackRepositoryImpl:   NewBufferPlaybackRepository(db),
		UserEventRepositoryImpl:        NewUserEventRepository(db),
		ClusterRepositoryImpl:          NewClusterRepository(db),
		SettingRepositoryImpl:          NewSettingRepository(db),
		CacheRepositoryImpl:            NewCacheRepository(db),
		LanguagePackRepositoryImpl:     NewLanguagePackRepository(db),
		MFARepositoryImpl:              NewMFARepository(db),
		PendingSignInRepositoryImpl:    NewPendingSignInRepository(db),
		ClientDisconnectRepositoryImpl: NewClientDisconnectRepository(db),
		db:                             db,
	}
}

func (s *gormStore) Close() error {
	sqlDB, err := s.db.DB.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}

func (s *gormStore) Ping(ctx context.Context) error {
	db, err := s.db.DB.DB()
	if err != nil {
		return err
	}
	return db.PingContext(ctx)
}

// --- legacy history ------------------------------------------------------

func (s *gormStore) MigrateLegacyMessages(ctx context.Context, fn func(LegacyMessage) error) (int, error) {
	const table = "messages"
	db := s.db.DB.WithContext(ctx)
	if !db.Migrator().HasTable(table) {
		return 0, nil
	}
	n, lastID := 0, int64(0)
	for {
		var rows []LegacyMessage
		err := db.Table(table).Where("id > ?", lastID).Order("id").Limit(1000).Find(&rows).Error
		if err != nil {
			return n, err
		}
		if len(rows) == 0 {
			break
		}
		for _, r := range rows {
			if err := fn(r); err != nil {
				return n, err
			}
		}
		n += len(rows)
		lastID = rows[len(rows)-1].ID
	}
	return n, db.Migrator().DropTable(table)
}

// notFound maps GORM's missing-row error to ErrNotFound.
func notFound(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrNotFound
	}
	return err
}
