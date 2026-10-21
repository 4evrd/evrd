package store

import (
	"errors"
	"strings"
	"time"

	"github.com/glebarez/sqlite" // pure-Go SQLite (no CGO), via modernc
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
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
	hadPlayback := db.Migrator().HasTable(&BufferPlayback{})
	if err := db.AutoMigrate(&User{}, &Session{}, &ClientPassword{}, &Network{}, &Node{}, &UserLease{}, &ReadMarker{}, &BufferPlayback{}, &UserEvent{}, &Setting{}, &NodeConf{}, &CertCacheEntry{}, &CacheEntry{}, &LanguagePack{}, &UserLanguagePack{}); err != nil {
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

func (s *gormStore) CreateUser(u User) (User, error) {
	if u.Role == "" {
		u.Role = RoleUser
	}
	if u.TimestampFormat == "" { // a new user: ZNC-like playback defaults
		u.PlaybackLines, u.AutoClearQueryBuffer = 50, true
		u.PrependTimestamp, u.TimestampFormat = true, "[%H:%M:%S]"
	}
	if err := s.db.Create(&u).Error; err != nil {
		return User{}, err
	}
	return u, nil
}

func (s *gormStore) UserByID(id int64) (User, error) {
	var u User
	return u, notFound(s.db.First(&u, id).Error)
}

func (s *gormStore) UserByUsername(username string) (User, error) {
	var u User
	return u, notFound(s.db.Where("username = ?", username).First(&u).Error)
}

func (s *gormStore) UserBySubject(subject string) (User, error) {
	var u User
	return u, notFound(s.db.Where("oidc_subject = ?", subject).First(&u).Error)
}

func (s *gormStore) ListUsers() ([]User, error) {
	var users []User
	return users, s.db.Order("username").Find(&users).Error
}

// UpdateUser saves every field, including zero values (Select("*")), so
// clearing a limit or un-suspending sticks.
func (s *gormStore) UpdateUser(u User) error {
	return s.db.Model(&u).Select("*").Omit("created_at").Updates(&u).Error
}

func (s *gormStore) DeleteUser(id int64) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		var netIDs []int64
		if err := tx.Model(&Network{}).Where("user_id = ?", id).Pluck("id", &netIDs).Error; err != nil {
			return err
		}
		if len(netIDs) > 0 {
			for _, m := range []any{&ReadMarker{}, &BufferPlayback{}} {
				if err := tx.Where("network_id IN ?", netIDs).Delete(m).Error; err != nil {
					return err
				}
			}
		}
		for _, m := range []any{&Network{}, &Session{}, &ClientPassword{}, &UserEvent{}, &UserLanguagePack{}} {
			if err := tx.Where("user_id = ?", id).Delete(m).Error; err != nil {
				return err
			}
		}
		return tx.Delete(&User{}, id).Error
	})
}

func (s *gormStore) CountAdmins() (int64, error) {
	var n int64
	return n, s.db.Model(&User{}).Where("role = ? AND suspended = ?", RoleAdmin, false).Count(&n).Error
}

// --- sessions ------------------------------------------------------------

func (s *gormStore) CreateSession(sess Session) error {
	return s.db.Create(&sess).Error
}

func (s *gormStore) SessionByID(id string) (Session, error) {
	var sess Session
	return sess, notFound(s.db.Where("id = ?", id).First(&sess).Error)
}

func (s *gormStore) TouchSession(id string, at time.Time) error {
	return s.db.Model(&Session{}).Where("id = ?", id).Update("last_seen_at", at).Error
}

func (s *gormStore) ListSessions(userID int64) ([]Session, error) {
	var out []Session
	return out, s.db.Where("user_id = ?", userID).Order("last_seen_at DESC").Find(&out).Error
}

func (s *gormStore) DeleteSession(userID int64, id string) error {
	return s.db.Where("id = ? AND user_id = ?", id, userID).Delete(&Session{}).Error
}

func (s *gormStore) DeleteUserSessions(userID int64, keep string) error {
	return s.db.Where("user_id = ? AND id <> ?", userID, keep).Delete(&Session{}).Error
}

func (s *gormStore) DeleteExpiredSessions(now time.Time) error {
	return s.db.Where("expires_at < ?", now).Delete(&Session{}).Error
}

// --- IRC client passwords --------------------------------------------------

func (s *gormStore) CreateClientPassword(c ClientPassword) (ClientPassword, error) {
	if err := s.db.Create(&c).Error; err != nil {
		return ClientPassword{}, err
	}
	return c, nil
}

func (s *gormStore) ListClientPasswords(userID int64) ([]ClientPassword, error) {
	var out []ClientPassword
	return out, s.db.Where("user_id = ?", userID).Order("id").Find(&out).Error
}

func (s *gormStore) DeleteClientPassword(userID, id int64) error {
	res := s.db.Where("id = ? AND user_id = ?", id, userID).Delete(&ClientPassword{})
	if res.Error == nil && res.RowsAffected == 0 {
		return ErrNotFound
	}
	return res.Error
}

func (s *gormStore) TouchClientPassword(id int64, at time.Time) error {
	return s.db.Model(&ClientPassword{}).Where("id = ?", id).Update("last_used_at", at).Error
}

// --- networks ------------------------------------------------------------

func (s *gormStore) ListNetworks(userID int64) ([]Network, error) {
	var nets []Network
	return nets, s.db.Where("user_id = ?", userID).Order("id").Find(&nets).Error
}

func (s *gormStore) CountNetworks(userID int64) (int64, error) {
	var n int64
	return n, s.db.Model(&Network{}).Where("user_id = ?", userID).Count(&n).Error
}

func (s *gormStore) NetworkCounts() (map[int64]int64, error) {
	var rows []struct {
		UserID int64
		N      int64
	}
	err := s.db.Model(&Network{}).Select("user_id, count(*) as n").Group("user_id").Scan(&rows).Error
	out := make(map[int64]int64, len(rows))
	for _, r := range rows {
		out[r.UserID] = r.N
	}
	return out, err
}

func (s *gormStore) AllNetworks() ([]Network, error) {
	var nets []Network
	return nets, s.db.Order("id").Find(&nets).Error
}

func (s *gormStore) AllEnabledNetworks() ([]Network, error) {
	var nets []Network
	return nets, s.db.Where("enabled = ?", true).Order("id").Find(&nets).Error
}

func (s *gormStore) NetworkByID(id int64) (Network, error) {
	var n Network
	return n, notFound(s.db.First(&n, id).Error)
}

func (s *gormStore) CreateNetwork(n Network) (Network, error) {
	// Create skips zero values of columns that have a default (TrustPKI,
	// FloodRate…), so write the settings again explicitly.
	// (Create also copies those defaults back into the struct, so keep the
	// values asked for.)
	want := n
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&n).Error; err != nil {
			return err
		}
		want.ID = n.ID
		return tx.Model(&Network{ID: n.ID}).Select("*").Omit("id", "user_id").Updates(&want).Error
	})
	if err != nil {
		return Network{}, err
	}
	return want, nil
}

func (s *gormStore) DeleteNetwork(userID, id int64) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		res := tx.Where("id = ? AND user_id = ?", id, userID).Delete(&Network{})
		if res.Error != nil || res.RowsAffected == 0 {
			return res.Error
		}
		if err := tx.Where("network_id = ?", id).Delete(&ReadMarker{}).Error; err != nil {
			return err
		}
		return tx.Where("network_id = ?", id).Delete(&BufferPlayback{}).Error
	})
}

func (s *gormStore) SetNetworkEnabled(id int64, enabled bool) error {
	return s.db.Model(&Network{}).Where("id = ?", id).Update("enabled", enabled).Error
}

func (s *gormStore) UpdateNetwork(n Network) error {
	// Everything but ownership and the on/off switch (SetNetworkEnabled).
	return s.db.Model(&Network{ID: n.ID}).Select("*").Omit("id", "user_id", "enabled").Updates(&n).Error
}

func (s *gormStore) SetNetworkChannelSettings(id int64, settings map[string]ChannelSettings) error {
	return s.db.Model(&Network{ID: id}).Select("channel_settings").Updates(&Network{ChannelSettings: settings}).Error
}

func (s *gormStore) SetNetworkChannels(id int64, channels []string) error {
	if channels == nil {
		channels = []string{}
	}
	return s.db.Model(&Network{ID: id}).Select("channels").Updates(&Network{Channels: channels}).Error
}

// --- read markers, activity, presence --------------------------------------

func (s *gormStore) SetReadMarker(networkID int64, buffer string, at time.Time) error {
	return s.db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "network_id"}, {Name: "buffer"}},
		DoUpdates: clause.AssignmentColumns([]string{"read_at"}),
	}).Create(&ReadMarker{NetworkID: networkID, Buffer: strings.ToLower(buffer), ReadAt: at}).Error
}

func (s *gormStore) ReadMarkers(networkID int64) (map[string]time.Time, error) {
	var rows []ReadMarker
	if err := s.db.Where("network_id = ?", networkID).Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[string]time.Time, len(rows))
	for _, r := range rows {
		out[r.Buffer] = r.ReadAt
	}
	return out, nil
}

func (s *gormStore) BufferPlaybacks(networkID int64) (map[string]BufferPlayback, error) {
	var rows []BufferPlayback
	if err := s.db.Where("network_id = ?", networkID).Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[string]BufferPlayback, len(rows))
	for _, r := range rows {
		out[r.Buffer] = r
	}
	return out, nil
}

func (s *gormStore) UpdateBufferPlayback(networkID int64, buffers []string, f func(*BufferPlayback)) error {
	// One statement per buffer, not a transaction: SQLite would refuse a
	// write transaction while the network's other writes go on.
	have, err := s.BufferPlaybacks(networkID)
	if err != nil {
		return err
	}
	for _, b := range buffers {
		b = strings.ToLower(b)
		bp := have[b]
		f(&bp)
		bp.NetworkID, bp.Buffer = networkID, b
		if bp.Lines == nil && bp.AutoClear == nil && bp.ClearedAt == nil {
			err = s.db.Where("network_id = ? AND buffer = ?", networkID, b).Delete(&BufferPlayback{}).Error
		} else {
			err = s.db.Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "network_id"}, {Name: "buffer"}},
				DoUpdates: clause.AssignmentColumns([]string{"lines", "auto_clear", "cleared_at"}),
			}).Create(&bp).Error
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *gormStore) AddUserEvent(e UserEvent) error {
	return s.db.Create(&e).Error
}

func (s *gormStore) UserEvents(userID int64, since time.Time, limit int) ([]UserEvent, error) {
	var out []UserEvent
	return out, s.db.Where("user_id = ? AND at >= ?", userID, since).Order("at DESC").Limit(limit).Find(&out).Error
}

func (s *gormStore) PruneUserEvents(before time.Time) error {
	return s.db.Where("at < ?", before).Delete(&UserEvent{}).Error
}

func (s *gormStore) TouchUserSeen(userID int64, at time.Time) error {
	return s.db.Model(&User{}).Where("id = ?", userID).Update("last_seen_at", at).Error
}

func (s *gormStore) Ping() error {
	db, err := s.db.DB()
	if err != nil {
		return err
	}
	return db.Ping()
}

// --- cluster -------------------------------------------------------------

func (s *gormStore) Leases(now time.Time) ([]UserLease, error) {
	var out []UserLease
	return out, s.db.Where("expires_at > ?", now).Find(&out).Error
}

func (s *gormStore) UpsertNode(n Node) error {
	return s.db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "id"}},
		DoUpdates: clause.AssignmentColumns([]string{"http_url", "irc_addr", "started_at", "heartbeat_at"}),
	}).Create(&n).Error
}

func (s *gormStore) DeleteNodesBefore(t time.Time) error {
	return s.db.Where("heartbeat_at < ?", t).Delete(&Node{}).Error
}

func (s *gormStore) LiveNodes(since time.Time) ([]Node, error) {
	var out []Node
	return out, s.db.Where("heartbeat_at > ?", since).Order("id").Find(&out).Error
}

func (s *gormStore) AcquireLease(userID int64, node string, until, now time.Time) (bool, error) {
	res := s.db.Clauses(clause.OnConflict{DoNothing: true}).
		Create(&UserLease{UserID: userID, NodeID: node, Epoch: 1, ExpiresAt: until})
	if res.Error != nil {
		return false, res.Error
	}
	if res.RowsAffected > 0 {
		return true, nil
	}
	// Taken before: take it over if it's expired, or extend it if it's ours.
	// The WHERE is re-checked under the row lock, so two nodes can't both win.
	res = s.db.Model(&UserLease{}).
		Where("user_id = ? AND (expires_at < ? OR node_id = ?)", userID, now, node).
		Updates(map[string]any{
			"epoch":      gorm.Expr("CASE WHEN node_id = ? THEN epoch ELSE epoch + 1 END", node),
			"node_id":    node,
			"expires_at": until,
		})
	return res.RowsAffected > 0, res.Error
}

func (s *gormStore) RenewLeases(node string, until, now time.Time) error {
	return s.db.Model(&UserLease{}).Where("node_id = ? AND expires_at > ?", node, now).
		Update("expires_at", until).Error
}

func (s *gormStore) HeldLeases(node string, now time.Time) ([]int64, error) {
	var ids []int64
	return ids, s.db.Model(&UserLease{}).Where("node_id = ? AND expires_at > ?", node, now).
		Pluck("user_id", &ids).Error
}

func (s *gormStore) ReleaseLease(userID int64, node string, now time.Time) error {
	return s.db.Model(&UserLease{}).Where("user_id = ? AND node_id = ?", userID, node).
		Update("expires_at", now).Error
}

func (s *gormStore) LeaseOwner(userID int64, now time.Time) (Node, UserLease, bool, error) {
	var l UserLease
	err := s.db.Where("user_id = ? AND expires_at > ?", userID, now).First(&l).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return Node{}, UserLease{}, false, nil
	} else if err != nil {
		return Node{}, UserLease{}, false, err
	}
	var n Node
	if err := s.db.Where("id = ?", l.NodeID).First(&n).Error; err != nil {
		return Node{}, UserLease{}, false, notFound(err)
	}
	return n, l, true, nil
}

func (s *gormStore) DeleteOrphanLeases() error {
	return s.db.Where("user_id NOT IN (?)", s.db.Model(&User{}).Select("id")).Delete(&UserLease{}).Error
}

// --- legacy history ------------------------------------------------------

func (s *gormStore) MigrateLegacyMessages(fn func(LegacyMessage) error) (int, error) {
	const table = "messages"
	if !s.db.Migrator().HasTable(table) {
		return 0, nil
	}
	n, lastID := 0, int64(0)
	for {
		var rows []LegacyMessage
		err := s.db.Table(table).Where("id > ?", lastID).Order("id").Limit(1000).Find(&rows).Error
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
	return n, s.db.Migrator().DropTable(table)
}

// notFound maps GORM's missing-row error to ErrNotFound.
func notFound(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrNotFound
	}
	return err
}

func (s *gormStore) Settings() ([]Setting, error) {
	var out []Setting
	return out, s.db.Order("key").Find(&out).Error
}

func (s *gormStore) SaveSettings(set map[string]string, del []string, by string) error {
	now := time.Now()
	return s.db.Transaction(func(tx *gorm.DB) error {
		for k, v := range set {
			if err := tx.Save(&Setting{Key: k, Value: v, UpdatedAt: now, UpdatedBy: by}).Error; err != nil {
				return err
			}
		}
		if len(del) > 0 {
			return tx.Where("key IN ?", del).Delete(&Setting{}).Error
		}
		return nil
	})
}

func (s *gormStore) SetNodeConf(c NodeConf) error { return s.db.Save(&c).Error }

func (s *gormStore) CertCache(key string) (string, error) {
	var e CertCacheEntry
	if err := s.db.First(&e, "key = ?", key).Error; err != nil {
		return "", notFound(err)
	}
	return e.Data, nil
}

func (s *gormStore) PutCertCache(key, data string) error {
	return s.db.Save(&CertCacheEntry{Key: key, Data: data, UpdatedAt: time.Now()}).Error
}

func (s *gormStore) DeleteCertCache(key string) error {
	return s.db.Delete(&CertCacheEntry{}, "key = ?", key).Error
}

func (s *gormStore) Cached(key string) (string, error) {
	var e CacheEntry
	if err := s.db.First(&e, "key = ?", key).Error; err != nil {
		return "", notFound(err)
	}
	return e.Data, nil
}

func (s *gormStore) PutCached(key, data string) error {
	return s.db.Save(&CacheEntry{Key: key, Data: data, UpdatedAt: time.Now()}).Error
}

func (s *gormStore) NodeConfs() ([]NodeConf, error) {
	var out []NodeConf
	return out, s.db.Order("node_id").Find(&out).Error
}

// --- language packs --------------------------------------------------------

func (s *gormStore) LanguagePacks() ([]LanguagePack, error) {
	var out []LanguagePack
	return out, s.db.Omit("messages").Order("code").Find(&out).Error
}

func (s *gormStore) LanguagePack(code string) (LanguagePack, error) {
	var p LanguagePack
	return p, notFound(s.db.First(&p, "code = ?", code).Error)
}

func (s *gormStore) PutLanguagePack(p LanguagePack) error {
	p.UpdatedAt = time.Now()
	return s.db.Save(&p).Error
}

func (s *gormStore) DeleteLanguagePack(code string) error {
	res := s.db.Delete(&LanguagePack{}, "code = ?", code)
	if res.Error == nil && res.RowsAffected == 0 {
		return ErrNotFound
	}
	return res.Error
}

func (s *gormStore) UserLanguagePack(userID int64) (UserLanguagePack, error) {
	var p UserLanguagePack
	return p, notFound(s.db.First(&p, "user_id = ?", userID).Error)
}

func (s *gormStore) PutUserLanguagePack(p UserLanguagePack) error {
	p.UpdatedAt = time.Now()
	return s.db.Save(&p).Error
}

func (s *gormStore) DeleteUserLanguagePack(userID int64) error {
	res := s.db.Delete(&UserLanguagePack{}, "user_id = ?", userID)
	if res.Error == nil && res.RowsAffected == 0 {
		return ErrNotFound
	}
	return res.Error
}
