package store

import (
	"context"
	"time"
)

// ClientPasswordRepository keeps IRC client passwords (see ClientPasswordRepositoryImpl).
type ClientPasswordRepository struct{ db *DB }

// NewClientPasswordRepository is the IRC client passwords repository on db.
func NewClientPasswordRepository(db *DB) ClientPasswordRepositoryImpl {
	return &ClientPasswordRepository{db: db}
}

func (r *ClientPasswordRepository) CreateClientPassword(ctx context.Context, c ClientPassword) (ClientPassword, error) {
	if err := r.db.DB.WithContext(ctx).Create(&c).Error; err != nil {
		return ClientPassword{}, err
	}
	return c, nil
}

func (r *ClientPasswordRepository) ListClientPasswords(ctx context.Context, userID int64) ([]ClientPassword, error) {
	var out []ClientPassword
	return out, r.db.DB.WithContext(ctx).Where("user_id = ?", userID).Order("id").Find(&out).Error
}

func (r *ClientPasswordRepository) DeleteClientPassword(ctx context.Context, userID, id int64) error {
	res := r.db.DB.WithContext(ctx).Where("id = ? AND user_id = ?", id, userID).Delete(&ClientPassword{})
	if res.Error == nil && res.RowsAffected == 0 {
		return ErrNotFound
	}
	return res.Error
}

func (r *ClientPasswordRepository) TouchClientPassword(ctx context.Context, id int64, at time.Time) error {
	return r.db.DB.WithContext(ctx).Model(&ClientPassword{}).Where("id = ?", id).Update("last_used_at", at).Error
}
