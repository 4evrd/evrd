package store

import (
	"context"
	"time"

	"gorm.io/gorm"
)

// UserRepository is the UserRepositoryImpl on the SQL database.
type UserRepository struct{ db *DB }

// NewUserRepository makes a UserRepository on db.
func NewUserRepository(db *DB) UserRepositoryImpl { return &UserRepository{db: db} }

func (r *UserRepository) CreateUser(ctx context.Context, u User) (User, error) {
	if err := r.db.DB.WithContext(ctx).Create(&u).Error; err != nil {
		return User{}, err
	}
	return u, nil
}

func (r *UserRepository) UserByID(ctx context.Context, id int64) (User, error) {
	var u User
	return u, notFound(r.db.DB.WithContext(ctx).First(&u, id).Error)
}

func (r *UserRepository) UserByUsername(ctx context.Context, username string) (User, error) {
	var u User
	return u, notFound(r.db.DB.WithContext(ctx).Where("username = ?", username).First(&u).Error)
}

func (r *UserRepository) UserBySubject(ctx context.Context, subject string) (User, error) {
	var u User
	return u, notFound(r.db.DB.WithContext(ctx).Where("oidc_subject = ?", subject).First(&u).Error)
}

func (r *UserRepository) UserByLoginName(ctx context.Context, name string) (User, error) {
	u, err := r.UserByUsername(ctx, name)
	if err != ErrNotFound {
		return u, err
	}
	return u, notFound(r.db.DB.WithContext(ctx).Where("sso_username = ?", name).First(&u).Error)
}

func (r *UserRepository) SetSSOUsername(ctx context.Context, userID int64, name *string) error {
	return r.db.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if name != nil {
			if err := tx.Model(&User{}).Where("sso_username = ? AND id <> ?", *name, userID).Update("sso_username", nil).Error; err != nil {
				return err
			}
		}
		return tx.Model(&User{}).Where("id = ?", userID).Update("sso_username", name).Error
	})
}

func (r *UserRepository) ListUsers(ctx context.Context) ([]User, error) {
	var users []User
	return users, r.db.DB.WithContext(ctx).Order("username").Find(&users).Error
}

// UpdateUser saves every field, including zero values (Select("*")), so
// clearing a limit or un-suspending sticks — but the SSO username, which
// only SetSSOUsername changes (a stale copy could take it back).
func (r *UserRepository) UpdateUser(ctx context.Context, u User) error {
	return r.db.DB.WithContext(ctx).Model(&u).Select("*").Omit("created_at", "sso_username").Updates(&u).Error
}

func (r *UserRepository) DeleteUser(ctx context.Context, id int64) error {
	return r.db.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var netIDs []int64
		if err := tx.Model(&Network{}).Where("user_id = ?", id).Pluck("id", &netIDs).Error; err != nil {
			return err
		}
		if len(netIDs) > 0 {
			for _, m := range []any{&ReadMarker{}, &BufferPlayback{}, &DevicePosition{}} {
				if err := tx.Where("network_id IN ?", netIDs).Delete(m).Error; err != nil {
					return err
				}
			}
		}
		for _, m := range []any{&Network{}, &Session{}, &ClientPassword{}, &UserEvent{}, &UserLanguagePack{}, &UserTOTP{}, &RecoveryCode{}, &ClientDisconnect{}} {
			if err := tx.Where("user_id = ?", id).Delete(m).Error; err != nil {
				return err
			}
		}
		return tx.Delete(&User{}, id).Error
	})
}

func (r *UserRepository) CountAdmins(ctx context.Context) (int64, error) {
	var n int64
	return n, r.db.DB.WithContext(ctx).Model(&User{}).Where("role = ? AND suspended = ?", RoleAdmin, false).Count(&n).Error
}

func (r *UserRepository) TouchUserSeen(ctx context.Context, userID int64, at time.Time) error {
	return r.db.DB.WithContext(ctx).Model(&User{}).Where("id = ?", userID).Update("last_seen_at", at).Error
}
