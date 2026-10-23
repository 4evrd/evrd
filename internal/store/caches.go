package store

import (
	"context"
	"time"
)

// CacheRepository is the CacheRepositoryImpl on the SQL database.
type CacheRepository struct{ db *DB }

// NewCacheRepository makes a CacheRepository on db.
func NewCacheRepository(db *DB) CacheRepositoryImpl { return &CacheRepository{db: db} }

func (r *CacheRepository) CertCache(ctx context.Context, key string) (string, error) {
	var e CertCacheEntry
	if err := r.db.DB.WithContext(ctx).First(&e, "key = ?", key).Error; err != nil {
		return "", notFound(err)
	}
	return e.Data, nil
}

func (r *CacheRepository) PutCertCache(ctx context.Context, key, data string) error {
	return r.db.DB.WithContext(ctx).Save(&CertCacheEntry{Key: key, Data: data, UpdatedAt: time.Now()}).Error
}

func (r *CacheRepository) DeleteCertCache(ctx context.Context, key string) error {
	return r.db.DB.WithContext(ctx).Delete(&CertCacheEntry{}, "key = ?", key).Error
}

func (r *CacheRepository) Cached(ctx context.Context, key string) (string, error) {
	var e CacheEntry
	if err := r.db.DB.WithContext(ctx).First(&e, "key = ?", key).Error; err != nil {
		return "", notFound(err)
	}
	return e.Data, nil
}

func (r *CacheRepository) PutCached(ctx context.Context, key, data string) error {
	return r.db.DB.WithContext(ctx).Save(&CacheEntry{Key: key, Data: data, UpdatedAt: time.Now()}).Error
}
