package store

import (
	"context"
	"time"
)

// LanguagePackRepository keeps translations of the web app (see LanguagePackRepositoryImpl).
type LanguagePackRepository struct{ db *DB }

// NewLanguagePackRepository is the translations of the web app repository on db.
func NewLanguagePackRepository(db *DB) LanguagePackRepositoryImpl {
	return &LanguagePackRepository{db: db}
}

func (r *LanguagePackRepository) LanguagePacks(ctx context.Context) ([]LanguagePack, error) {
	var out []LanguagePack
	return out, r.db.DB.WithContext(ctx).Omit("messages").Order("code").Find(&out).Error
}

func (r *LanguagePackRepository) LanguagePack(ctx context.Context, code string) (LanguagePack, error) {
	var p LanguagePack
	return p, notFound(r.db.DB.WithContext(ctx).First(&p, "code = ?", code).Error)
}

func (r *LanguagePackRepository) PutLanguagePack(ctx context.Context, p LanguagePack) error {
	p.UpdatedAt = time.Now()
	return r.db.DB.WithContext(ctx).Save(&p).Error
}

func (r *LanguagePackRepository) DeleteLanguagePack(ctx context.Context, code string) error {
	res := r.db.DB.WithContext(ctx).Delete(&LanguagePack{}, "code = ?", code)
	if res.Error == nil && res.RowsAffected == 0 {
		return ErrNotFound
	}
	return res.Error
}

func (r *LanguagePackRepository) UserLanguagePack(ctx context.Context, userID int64) (UserLanguagePack, error) {
	var p UserLanguagePack
	return p, notFound(r.db.DB.WithContext(ctx).First(&p, "user_id = ?", userID).Error)
}

func (r *LanguagePackRepository) PutUserLanguagePack(ctx context.Context, p UserLanguagePack) error {
	p.UpdatedAt = time.Now()
	return r.db.DB.WithContext(ctx).Save(&p).Error
}

func (r *LanguagePackRepository) DeleteUserLanguagePack(ctx context.Context, userID int64) error {
	res := r.db.DB.WithContext(ctx).Delete(&UserLanguagePack{}, "user_id = ?", userID)
	if res.Error == nil && res.RowsAffected == 0 {
		return ErrNotFound
	}
	return res.Error
}
