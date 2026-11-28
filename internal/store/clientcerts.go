package store

import (
	"context"
	"time"
)

// ClientCertRepository is the ClientCertRepositoryImpl on the SQL database.
type ClientCertRepository struct{ db *DB }

// NewClientCertRepository makes a ClientCertRepository on db.
func NewClientCertRepository(db *DB) ClientCertRepositoryImpl {
	return &ClientCertRepository{db: db}
}

func (r *ClientCertRepository) CreateClientCert(ctx context.Context, c ClientCert) (ClientCert, error) {
	if err := r.db.DB.WithContext(ctx).Create(&c).Error; err != nil {
		return ClientCert{}, err
	}
	return c, nil
}

func (r *ClientCertRepository) ListClientCerts(ctx context.Context, userID int64) ([]ClientCert, error) {
	var out []ClientCert
	return out, r.db.DB.WithContext(ctx).Where("user_id = ?", userID).Order("id").Find(&out).Error
}

func (r *ClientCertRepository) ClientCertsByFingerprint(ctx context.Context, fingerprints []string) ([]ClientCert, error) {
	var out []ClientCert
	if len(fingerprints) == 0 {
		return out, nil
	}
	return out, r.db.DB.WithContext(ctx).Where("fingerprint IN ?", fingerprints).Order("id").Find(&out).Error
}

func (r *ClientCertRepository) DeleteClientCert(ctx context.Context, userID, id int64) error {
	res := r.db.DB.WithContext(ctx).Where("id = ? AND user_id = ?", id, userID).Delete(&ClientCert{})
	if res.Error == nil && res.RowsAffected == 0 {
		return ErrNotFound
	}
	return res.Error
}

func (r *ClientCertRepository) TouchClientCert(ctx context.Context, id int64, at time.Time) error {
	return r.db.DB.WithContext(ctx).Model(&ClientCert{}).Where("id = ?", id).Update("last_used_at", at).Error
}
