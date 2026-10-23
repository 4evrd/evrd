package store

import (
	"context"
	"time"
)

// UserEventRepository is the UserEventRepositoryImpl on the SQL database.
type UserEventRepository struct{ db *DB }

// NewUserEventRepository makes a UserEventRepository on db.
func NewUserEventRepository(db *DB) UserEventRepositoryImpl { return &UserEventRepository{db: db} }

func (r *UserEventRepository) AddUserEvent(ctx context.Context, e UserEvent) error {
	return r.db.DB.WithContext(ctx).Create(&e).Error
}

func (r *UserEventRepository) UserEvents(ctx context.Context, userID int64, since time.Time, limit int) ([]UserEvent, error) {
	var out []UserEvent
	return out, r.db.DB.WithContext(ctx).Where("user_id = ? AND at >= ?", userID, since).Order("at DESC").Limit(limit).Find(&out).Error
}

func (r *UserEventRepository) PruneUserEvents(ctx context.Context, before time.Time) error {
	return r.db.DB.WithContext(ctx).Where("at < ?", before).Delete(&UserEvent{}).Error
}
