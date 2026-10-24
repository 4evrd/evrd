package store

import (
	"context"
	"time"

	"gorm.io/gorm"
)

// ClientDisconnectRepository is the ClientDisconnectRepositoryImpl on the
// SQL database.
type ClientDisconnectRepository struct{ db *DB }

// NewClientDisconnectRepository makes a ClientDisconnectRepository on db.
func NewClientDisconnectRepository(db *DB) ClientDisconnectRepositoryImpl {
	return &ClientDisconnectRepository{db: db}
}

func (r *ClientDisconnectRepository) AddClientDisconnect(ctx context.Context, d ClientDisconnect, since time.Time) (time.Duration, error) {
	var gap time.Duration
	err := r.db.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var prev ClientDisconnect
		err := tx.Where("user_id = ? AND device_key = ? AND reconnected_at IS NULL AND ended_at > ? AND ended_at <= ?",
			d.UserID, d.DeviceKey, since, d.ConnectedAt).Order("ended_at DESC").Limit(1).Find(&prev).Error
		if err != nil {
			return err
		}
		if prev.ID != 0 {
			at := d.ConnectedAt
			if err := tx.Model(&ClientDisconnect{}).Where("id = ?", prev.ID).Update("reconnected_at", &at).Error; err != nil {
				return err
			}
			gap = at.Sub(prev.EndedAt)
		}
		return tx.Create(&d).Error
	})
	return gap, err
}

func (r *ClientDisconnectRepository) ClientDisconnects(ctx context.Context, userID int64, since time.Time) ([]ClientDisconnect, error) {
	q := r.db.DB.WithContext(ctx).Where("ended_at > ?", since)
	if userID != 0 {
		q = q.Where("user_id = ?", userID)
	}
	var out []ClientDisconnect
	return out, q.Order("ended_at DESC").Find(&out).Error
}

func (r *ClientDisconnectRepository) DeleteClientDisconnectsBefore(ctx context.Context, before time.Time) error {
	return r.db.DB.WithContext(ctx).Where("ended_at < ?", before).Delete(&ClientDisconnect{}).Error
}
