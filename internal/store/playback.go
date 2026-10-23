package store

import (
	"context"
	"strings"

	"gorm.io/gorm/clause"
)

// BufferPlaybackRepository is the BufferPlaybackRepositoryImpl on the SQL database.
type BufferPlaybackRepository struct{ db *DB }

// NewBufferPlaybackRepository makes a BufferPlaybackRepository on db.
func NewBufferPlaybackRepository(db *DB) BufferPlaybackRepositoryImpl {
	return &BufferPlaybackRepository{db: db}
}

func (r *BufferPlaybackRepository) BufferPlaybacks(ctx context.Context, networkID int64) (map[string]BufferPlayback, error) {
	var rows []BufferPlayback
	if err := r.db.DB.WithContext(ctx).Where("network_id = ?", networkID).Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[string]BufferPlayback, len(rows))
	for _, r := range rows {
		out[r.Buffer] = r
	}
	return out, nil
}

func (r *BufferPlaybackRepository) UpdateBufferPlayback(ctx context.Context, networkID int64, buffers []string, f func(*BufferPlayback)) error {
	// One statement per buffer, not a transaction: SQLite would refuse a
	// write transaction while the network's other writes go on.
	have, err := r.BufferPlaybacks(ctx, networkID)
	if err != nil {
		return err
	}
	for _, b := range buffers {
		b = strings.ToLower(b)
		bp := have[b]
		f(&bp)
		bp.NetworkID, bp.Buffer = networkID, b
		if bp.Lines == nil && bp.AutoClear == nil && bp.ClearedAt == nil {
			err = r.db.DB.WithContext(ctx).Where("network_id = ? AND buffer = ?", networkID, b).Delete(&BufferPlayback{}).Error
		} else {
			err = r.db.DB.WithContext(ctx).Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "network_id"}, {Name: "buffer"}},
				DoUpdates: clause.AssignmentColumns([]string{"lines", "auto_clear", "cleared_at"}),
			}).Create(&bp).Error
		}
		if err != nil {
			return err
		}
	}
	return nil
}
