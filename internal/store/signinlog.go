package store

import (
	"context"
	"time"
)

// IRCSignInRepository is the IRCSignInRepositoryImpl on the SQL database.
type IRCSignInRepository struct{ db *DB }

// NewIRCSignInRepository makes an IRCSignInRepository on db.
func NewIRCSignInRepository(db *DB) IRCSignInRepositoryImpl { return &IRCSignInRepository{db: db} }

func (r *IRCSignInRepository) AddIRCSignIn(ctx context.Context, s IRCSignIn) (int64, error) {
	err := r.db.DB.WithContext(ctx).Create(&s).Error
	return s.ID, err
}

func (r *IRCSignInRepository) IRCSignIns(ctx context.Context, userID int64, since time.Time, limit int) ([]IRCSignIn, error) {
	q := r.db.DB.WithContext(ctx).Where("at > ?", since)
	switch {
	case userID > 0:
		q = q.Where("user_id = ?", userID)
	case userID < 0:
		q = q.Where("user_id = 0")
	}
	var out []IRCSignIn
	return out, q.Order("at DESC, id DESC").Limit(limit).Find(&out).Error
}

func (r *IRCSignInRepository) SettleIRCSignIn(ctx context.Context, id, userID int64, result string) error {
	db := r.db.DB.WithContext(ctx).Model(&IRCSignIn{})
	if err := db.Where("id = ? AND user_id = 0", id).Update("user_id", userID).Error; err != nil {
		return err
	}
	return r.db.DB.WithContext(ctx).Model(&IRCSignIn{}).Where("id = ?", id).Update("result", result).Error
}

func (r *IRCSignInRepository) DeleteIRCSignInsBefore(ctx context.Context, before time.Time) error {
	return r.db.DB.WithContext(ctx).Where("at < ?", before).Delete(&IRCSignIn{}).Error
}
