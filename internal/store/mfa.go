package store

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// MFARepository is the MFARepositoryImpl on the SQL database.
type MFARepository struct{ db *DB }

// NewMFARepository makes an MFARepository on db.
func NewMFARepository(db *DB) MFARepositoryImpl { return &MFARepository{db: db} }

func (r *MFARepository) TOTP(ctx context.Context, userID int64) (UserTOTP, error) {
	var t UserTOTP
	err := r.db.DB.WithContext(ctx).Where("user_id = ?", userID).First(&t).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return UserTOTP{}, ErrNotFound
	}
	return t, err
}

func (r *MFARepository) PutTOTP(ctx context.Context, t UserTOTP) error {
	return r.db.DB.WithContext(ctx).Clauses(clause.OnConflict{UpdateAll: true}).Create(&t).Error
}

func (r *MFARepository) DeleteTOTP(ctx context.Context, userID int64) error {
	return r.db.DB.WithContext(ctx).Where("user_id = ?", userID).Delete(&UserTOTP{}).Error
}

func (r *MFARepository) UseTOTPStep(ctx context.Context, userID, step int64) (bool, error) {
	res := r.db.DB.WithContext(ctx).Model(&UserTOTP{}).Where("user_id = ? AND last_step < ?", userID, step).Update("last_step", step)
	return res.RowsAffected == 1, res.Error
}

func (r *MFARepository) RecoveryCodes(ctx context.Context, userID int64) ([]RecoveryCode, error) {
	var out []RecoveryCode
	return out, r.db.DB.WithContext(ctx).Where("user_id = ?", userID).Order("id").Find(&out).Error
}

func (r *MFARepository) ReplaceRecoveryCodes(ctx context.Context, userID int64, hashes []string) error {
	return r.db.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("user_id = ?", userID).Delete(&RecoveryCode{}).Error; err != nil {
			return err
		}
		for _, h := range hashes {
			if err := tx.Create(&RecoveryCode{UserID: userID, Hash: h}).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func (r *MFARepository) UseRecoveryCode(ctx context.Context, id int64, at time.Time) (bool, error) {
	res := r.db.DB.WithContext(ctx).Model(&RecoveryCode{}).Where("id = ? AND used_at IS NULL", id).Update("used_at", at)
	return res.RowsAffected == 1, res.Error
}
