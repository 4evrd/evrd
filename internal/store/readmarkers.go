package store

import (
	"context"
	"strings"
	"time"

	"gorm.io/gorm/clause"
)

// ReadMarkerRepository keeps how far each buffer is read (see ReadMarkerRepositoryImpl).
type ReadMarkerRepository struct{ db *DB }

// NewReadMarkerRepository is the how far each buffer is read repository on db.
func NewReadMarkerRepository(db *DB) ReadMarkerRepositoryImpl { return &ReadMarkerRepository{db: db} }

func (r *ReadMarkerRepository) SetReadMarker(ctx context.Context, networkID int64, buffer string, at time.Time) error {
	return r.db.DB.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "network_id"}, {Name: "buffer"}},
		DoUpdates: clause.AssignmentColumns([]string{"read_at"}),
	}).Create(&ReadMarker{NetworkID: networkID, Buffer: strings.ToLower(buffer), ReadAt: at}).Error
}

func (r *ReadMarkerRepository) ReadMarkers(ctx context.Context, networkID int64) (map[string]time.Time, error) {
	var rows []ReadMarker
	if err := r.db.DB.WithContext(ctx).Where("network_id = ?", networkID).Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[string]time.Time, len(rows))
	for _, r := range rows {
		out[r.Buffer] = r.ReadAt
	}
	return out, nil
}
