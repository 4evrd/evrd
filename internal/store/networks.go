package store

import (
	"context"

	"gorm.io/gorm"
)

// NetworkRepository keeps users' IRC networks and their channels (see NetworkRepositoryImpl).
type NetworkRepository struct{ db *DB }

// NewNetworkRepository is the users' IRC networks and their channels repository on db.
func NewNetworkRepository(db *DB) NetworkRepositoryImpl { return &NetworkRepository{db: db} }

func (r *NetworkRepository) ListNetworks(ctx context.Context, userID int64) ([]Network, error) {
	var nets []Network
	return nets, r.db.DB.WithContext(ctx).Where("user_id = ?", userID).Order("id").Find(&nets).Error
}

func (r *NetworkRepository) CountNetworks(ctx context.Context, userID int64) (int64, error) {
	var n int64
	return n, r.db.DB.WithContext(ctx).Model(&Network{}).Where("user_id = ?", userID).Count(&n).Error
}

func (r *NetworkRepository) NetworkCounts(ctx context.Context) (map[int64]int64, error) {
	var rows []struct {
		UserID int64
		N      int64
	}
	err := r.db.DB.WithContext(ctx).Model(&Network{}).Select("user_id, count(*) as n").Group("user_id").Scan(&rows).Error
	out := make(map[int64]int64, len(rows))
	for _, r := range rows {
		out[r.UserID] = r.N
	}
	return out, err
}

func (r *NetworkRepository) AllNetworks(ctx context.Context) ([]Network, error) {
	var nets []Network
	return nets, r.db.DB.WithContext(ctx).Order("id").Find(&nets).Error
}

func (r *NetworkRepository) AllEnabledNetworks(ctx context.Context) ([]Network, error) {
	var nets []Network
	return nets, r.db.DB.WithContext(ctx).Where("enabled = ?", true).Order("id").Find(&nets).Error
}

func (r *NetworkRepository) NetworkByID(ctx context.Context, id int64) (Network, error) {
	var n Network
	return n, notFound(r.db.DB.WithContext(ctx).First(&n, id).Error)
}

func (r *NetworkRepository) CreateNetwork(ctx context.Context, n Network) (Network, error) {
	// Create skips zero values of columns that have a default (TrustPKI,
	// FloodRate…), so write the settings again explicitly.
	// (Create also copies those defaults back into the struct, so keep the
	// values asked for.)
	want := n
	err := r.db.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&n).Error; err != nil {
			return err
		}
		want.ID = n.ID
		return tx.Model(&Network{ID: n.ID}).Select("*").Omit("id", "user_id").Updates(&want).Error
	})
	if err != nil {
		return Network{}, err
	}
	return want, nil
}

func (r *NetworkRepository) DeleteNetwork(ctx context.Context, userID, id int64) error {
	return r.db.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		res := tx.Where("id = ? AND user_id = ?", id, userID).Delete(&Network{})
		if res.Error != nil || res.RowsAffected == 0 {
			return res.Error
		}
		if err := tx.Where("network_id = ?", id).Delete(&ReadMarker{}).Error; err != nil {
			return err
		}
		return tx.Where("network_id = ?", id).Delete(&BufferPlayback{}).Error
	})
}

func (r *NetworkRepository) SetNetworkEnabled(ctx context.Context, id int64, enabled bool) error {
	return r.db.DB.WithContext(ctx).Model(&Network{}).Where("id = ?", id).Update("enabled", enabled).Error
}

func (r *NetworkRepository) UpdateNetwork(ctx context.Context, n Network) error {
	// Everything but ownership and the on/off switch (SetNetworkEnabled).
	return r.db.DB.WithContext(ctx).Model(&Network{ID: n.ID}).Select("*").Omit("id", "user_id", "enabled").Updates(&n).Error
}

func (r *NetworkRepository) SetNetworkChannelSettings(ctx context.Context, id int64, settings map[string]ChannelSettings) error {
	return r.db.DB.WithContext(ctx).Model(&Network{ID: id}).Select("channel_settings").Updates(&Network{ChannelSettings: settings}).Error
}

func (r *NetworkRepository) SetNetworkChannels(ctx context.Context, id int64, channels []string) error {
	if channels == nil {
		channels = []string{}
	}
	return r.db.DB.WithContext(ctx).Model(&Network{ID: id}).Select("channels").Updates(&Network{Channels: channels}).Error
}
