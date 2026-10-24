package store

import (
	"context"
	"time"

	"gorm.io/gorm/clause"
)

// DevicePositionRepository is the DevicePositionRepositoryImpl on the SQL
// database.
type DevicePositionRepository struct{ db *DB }

// NewDevicePositionRepository makes a DevicePositionRepository on db.
func NewDevicePositionRepository(db *DB) DevicePositionRepositoryImpl {
	return &DevicePositionRepository{db: db}
}

func (r *DevicePositionRepository) DevicePosition(ctx context.Context, networkID int64, deviceKey string) (time.Time, bool, error) {
	var ps []DevicePosition
	err := r.db.DB.WithContext(ctx).Where("network_id = ? AND device_key = ?", networkID, deviceKey).Limit(1).Find(&ps).Error
	if err != nil || len(ps) == 0 {
		return time.Time{}, false, err
	}
	return ps[0].At, true, nil
}

func (r *DevicePositionRepository) SetDevicePosition(ctx context.Context, networkID int64, deviceKey string, at time.Time) error {
	return r.db.DB.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "network_id"}, {Name: "device_key"}},
		DoUpdates: clause.AssignmentColumns([]string{"at"}),
	}).Create(&DevicePosition{NetworkID: networkID, DeviceKey: deviceKey, At: at}).Error
}
