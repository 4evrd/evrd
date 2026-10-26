package store

import (
	"context"
	"time"
)

// SessionRepository is the SessionRepositoryImpl on the SQL database.
type SessionRepository struct{ db *DB }

// NewSessionRepository makes a SessionRepository on db.
func NewSessionRepository(db *DB) SessionRepositoryImpl { return &SessionRepository{db: db} }

func (r *SessionRepository) CreateSession(ctx context.Context, sess Session) error {
	return r.db.DB.WithContext(ctx).Create(&sess).Error
}

func (r *SessionRepository) SessionByID(ctx context.Context, id string) (Session, error) {
	var sess Session
	return sess, notFound(r.db.DB.WithContext(ctx).Where("id = ?", id).First(&sess).Error)
}

func (r *SessionRepository) TouchSession(ctx context.Context, id string, at time.Time) error {
	return r.db.DB.WithContext(ctx).Model(&Session{}).Where("id = ?", id).Update("last_seen_at", at).Error
}

func (r *SessionRepository) RenewSession(ctx context.Context, id string, at, expires time.Time) error {
	return r.db.DB.WithContext(ctx).Model(&Session{}).Where("id = ?", id).Updates(map[string]any{"last_seen_at": at, "expires_at": expires}).Error
}

func (r *SessionRepository) ListSessions(ctx context.Context, userID int64) ([]Session, error) {
	var out []Session
	return out, r.db.DB.WithContext(ctx).Where("user_id = ?", userID).Order("last_seen_at DESC").Find(&out).Error
}

func (r *SessionRepository) DeleteSession(ctx context.Context, userID int64, id string) error {
	return r.db.DB.WithContext(ctx).Where("id = ? AND user_id = ?", id, userID).Delete(&Session{}).Error
}

func (r *SessionRepository) DeleteUserSessions(ctx context.Context, userID int64, keep string) error {
	return r.db.DB.WithContext(ctx).Where("user_id = ? AND id <> ?", userID, keep).Delete(&Session{}).Error
}

func (r *SessionRepository) DeleteExpiredSessions(ctx context.Context, now time.Time) error {
	return r.db.DB.WithContext(ctx).Where("expires_at < ?", now).Delete(&Session{}).Error
}
