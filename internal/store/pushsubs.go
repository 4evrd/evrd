package store

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
)

// ErrPushLimit means an account has as many push subscriptions on a network
// as it may.
var ErrPushLimit = errors.New("store: too many push subscriptions")

// PushSubscriptionRepository is the PushSubscriptionRepositoryImpl on the
// SQL database.
type PushSubscriptionRepository struct{ db *DB }

// NewPushSubscriptionRepository makes a PushSubscriptionRepository on db.
func NewPushSubscriptionRepository(db *DB) PushSubscriptionRepositoryImpl {
	return &PushSubscriptionRepository{db: db}
}

func (r *PushSubscriptionRepository) PushSubscriptions(ctx context.Context, userID int64) ([]PushSubscription, error) {
	var out []PushSubscription
	return out, r.db.DB.WithContext(ctx).Where("user_id = ?", userID).Order("id").Find(&out).Error
}

func (r *PushSubscriptionRepository) AllPushSubscriptions(ctx context.Context) ([]PushSubscription, error) {
	var out []PushSubscription
	return out, r.db.DB.WithContext(ctx).Order("id").Find(&out).Error
}

func (r *PushSubscriptionRepository) SavePushSubscription(ctx context.Context, s PushSubscription, max int) (PushSubscription, error) {
	err := r.db.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var cur []PushSubscription
		if err := tx.Where("user_id = ? AND network_id = ? AND endpoint = ?", s.UserID, s.NetworkID, s.Endpoint).Limit(1).Find(&cur).Error; err != nil {
			return err
		}
		if len(cur) > 0 {
			s.ID, s.CreatedAt = cur[0].ID, cur[0].CreatedAt
			if s.LastSuccess.IsZero() {
				s.LastSuccess = cur[0].LastSuccess
			}
			return tx.Save(&s).Error
		}
		if max > 0 {
			var n int64
			if err := tx.Model(&PushSubscription{}).Where("user_id = ? AND network_id = ?", s.UserID, s.NetworkID).Count(&n).Error; err != nil {
				return err
			}
			if n >= int64(max) {
				return ErrPushLimit
			}
		}
		s.ID = 0
		return tx.Create(&s).Error
	})
	return s, err
}

func (r *PushSubscriptionRepository) TouchPushSubscription(ctx context.Context, id int64, success time.Time) error {
	return r.db.DB.WithContext(ctx).Model(&PushSubscription{}).Where("id = ?", id).Update("last_success", success).Error
}

func (r *PushSubscriptionRepository) DeletePushSubscription(ctx context.Context, id int64) error {
	return r.db.DB.WithContext(ctx).Delete(&PushSubscription{}, id).Error
}

func (r *PushSubscriptionRepository) DeletePushEndpoint(ctx context.Context, userID, networkID int64, endpoint string) error {
	return r.db.DB.WithContext(ctx).Where("user_id = ? AND network_id = ? AND endpoint = ?", userID, networkID, endpoint).Delete(&PushSubscription{}).Error
}
