package store

import (
	"context"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ClientIP is an address one of a user's IRC clients attached from, for
// clientnotify's "new addresses only": only the newest few are kept.
type ClientIP struct {
	UserID int64  `gorm:"primaryKey;autoIncrement:false"`
	IP     string `gorm:"primaryKey;size:64"`
	SeenAt time.Time
}

// ClientIPRepositoryImpl keeps the addresses users' clients attached from.
type ClientIPRepositoryImpl interface {
	// SeeClientIP records that a user's client attached from ip at at,
	// keeping only the user's keep newest addresses, and reports whether
	// ip wasn't among them before.
	SeeClientIP(ctx context.Context, userID int64, ip string, at time.Time, keep int) (bool, error)
}

// ClientIPRepository is the ClientIPRepositoryImpl on the SQL database.
type ClientIPRepository struct{ db *DB }

// NewClientIPRepository makes a ClientIPRepository on db.
func NewClientIPRepository(db *DB) ClientIPRepositoryImpl { return &ClientIPRepository{db: db} }

func (r *ClientIPRepository) SeeClientIP(ctx context.Context, userID int64, ip string, at time.Time, keep int) (bool, error) {
	fresh := false
	err := r.db.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var n int64
		if err := tx.Model(&ClientIP{}).Where("user_id = ? AND ip = ?", userID, ip).Count(&n).Error; err != nil {
			return err
		}
		fresh = n == 0
		if err := tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "user_id"}, {Name: "ip"}},
			DoUpdates: clause.AssignmentColumns([]string{"seen_at"}),
		}).Create(&ClientIP{UserID: userID, IP: ip, SeenAt: at}).Error; err != nil {
			return err
		}
		// forget all but the newest keep
		var old []string
		if err := tx.Model(&ClientIP{}).Where("user_id = ?", userID).Order("seen_at DESC").Offset(keep).Limit(1000).Pluck("ip", &old).Error; err != nil {
			return err
		}
		if len(old) == 0 {
			return nil
		}
		return tx.Where("user_id = ? AND ip IN ?", userID, old).Delete(&ClientIP{}).Error
	})
	return fresh, err
}
