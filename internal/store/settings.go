package store

import (
	"context"
	"time"

	"gorm.io/gorm"
)

// SettingRepository keeps settings changed in the admin UI (see SettingRepositoryImpl).
type SettingRepository struct{ db *DB }

// NewSettingRepository is the settings changed in the admin UI repository on db.
func NewSettingRepository(db *DB) SettingRepositoryImpl { return &SettingRepository{db: db} }

func (r *SettingRepository) Settings(ctx context.Context) ([]Setting, error) {
	var out []Setting
	return out, r.db.DB.WithContext(ctx).Order("key").Find(&out).Error
}

func (r *SettingRepository) SaveSettings(ctx context.Context, set map[string]string, del []string, by string) error {
	now := time.Now()
	return r.db.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for k, v := range set {
			if err := tx.Save(&Setting{Key: k, Value: v, UpdatedAt: now, UpdatedBy: by}).Error; err != nil {
				return err
			}
		}
		if len(del) > 0 {
			return tx.Where("key IN ?", del).Delete(&Setting{}).Error
		}
		return nil
	})
}
