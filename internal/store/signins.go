package store

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
)

// PendingSignInRepository is the PendingSignInRepositoryImpl on the SQL
// database.
type PendingSignInRepository struct{ db *DB }

// NewPendingSignInRepository makes a PendingSignInRepository on db.
func NewPendingSignInRepository(db *DB) PendingSignInRepositoryImpl {
	return &PendingSignInRepository{db: db}
}

func (r *PendingSignInRepository) CreatePendingSignIn(ctx context.Context, p PendingSignIn) error {
	return r.db.DB.WithContext(ctx).Create(&p).Error
}

func (r *PendingSignInRepository) PendingSignIn(ctx context.Context, id string) (PendingSignIn, error) {
	return r.first(ctx, "id = ?", id)
}

func (r *PendingSignInRepository) PendingSignInByToken(ctx context.Context, tokenHash string) (PendingSignIn, error) {
	return r.first(ctx, "token_hash = ?", tokenHash)
}

func (r *PendingSignInRepository) first(ctx context.Context, where string, arg any) (PendingSignIn, error) {
	var p PendingSignIn
	err := r.db.DB.WithContext(ctx).Where(where, arg).First(&p).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return PendingSignIn{}, ErrNotFound
	}
	return p, err
}

func (r *PendingSignInRepository) DecidePendingSignIn(ctx context.Context, id, state string, userID int64, device string) (bool, error) {
	res := r.db.DB.WithContext(ctx).Model(&PendingSignIn{}).Where("id = ? AND state = ?", id, "pending").
		Updates(map[string]any{"state": state, "allowed_user": userID, "device": device})
	return res.RowsAffected == 1, res.Error
}

func (r *PendingSignInRepository) DeletePendingSignIn(ctx context.Context, id string) error {
	return r.db.DB.WithContext(ctx).Where("id = ?", id).Delete(&PendingSignIn{}).Error
}

func (r *PendingSignInRepository) CountPendingSignIns(ctx context.Context, ip, username string, now time.Time) (byIP, byUser int64, err error) {
	q := r.db.DB.WithContext(ctx).Model(&PendingSignIn{}).Where("state = ? AND expires_at > ?", "pending", now)
	if err = q.Session(&gorm.Session{}).Where("ip = ?", ip).Count(&byIP).Error; err != nil || username == "" {
		return byIP, 0, err
	}
	err = q.Session(&gorm.Session{}).Where("username = ?", username).Count(&byUser).Error
	return byIP, byUser, err
}

func (r *PendingSignInRepository) DeleteExpiredSignIns(ctx context.Context, now time.Time) error {
	return r.db.DB.WithContext(ctx).Where("expires_at <= ?", now).Delete(&PendingSignIn{}).Error
}
