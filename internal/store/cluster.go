package store

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ClusterRepository is the ClusterRepositoryImpl on the SQL database.
type ClusterRepository struct{ db *DB }

// NewClusterRepository makes a ClusterRepository on db.
func NewClusterRepository(db *DB) ClusterRepositoryImpl { return &ClusterRepository{db: db} }

func (r *ClusterRepository) Leases(ctx context.Context, now time.Time) ([]UserLease, error) {
	var out []UserLease
	return out, r.db.DB.WithContext(ctx).Where("expires_at > ?", now).Find(&out).Error
}

func (r *ClusterRepository) UpsertNode(ctx context.Context, n Node) error {
	return r.db.DB.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "id"}},
		DoUpdates: clause.AssignmentColumns([]string{"http_url", "irc_addr", "started_at", "heartbeat_at"}),
	}).Create(&n).Error
}

func (r *ClusterRepository) DeleteNodesBefore(ctx context.Context, t time.Time) error {
	return r.db.DB.WithContext(ctx).Where("heartbeat_at < ?", t).Delete(&Node{}).Error
}

func (r *ClusterRepository) LiveNodes(ctx context.Context, since time.Time) ([]Node, error) {
	var out []Node
	return out, r.db.DB.WithContext(ctx).Where("heartbeat_at > ?", since).Order("id").Find(&out).Error
}

func (r *ClusterRepository) AcquireLease(ctx context.Context, userID int64, node string, until, now time.Time) (bool, error) {
	res := r.db.DB.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).
		Create(&UserLease{UserID: userID, NodeID: node, Epoch: 1, ExpiresAt: until})
	if res.Error != nil {
		return false, res.Error
	}
	if res.RowsAffected > 0 {
		return true, nil
	}
	// Taken before: take it over if it's expired, or extend it if it's ours.
	// The WHERE is re-checked under the row lock, so two nodes can't both win.
	res = r.db.DB.WithContext(ctx).Model(&UserLease{}).
		Where("user_id = ? AND (expires_at < ? OR node_id = ?)", userID, now, node).
		Updates(map[string]any{
			"epoch":      gorm.Expr("CASE WHEN node_id = ? THEN epoch ELSE epoch + 1 END", node),
			"node_id":    node,
			"expires_at": until,
		})
	return res.RowsAffected > 0, res.Error
}

func (r *ClusterRepository) RenewLeases(ctx context.Context, node string, until, now time.Time) error {
	return r.db.DB.WithContext(ctx).Model(&UserLease{}).Where("node_id = ? AND expires_at > ?", node, now).
		Update("expires_at", until).Error
}

func (r *ClusterRepository) HeldLeases(ctx context.Context, node string, now time.Time) ([]int64, error) {
	var ids []int64
	return ids, r.db.DB.WithContext(ctx).Model(&UserLease{}).Where("node_id = ? AND expires_at > ?", node, now).
		Pluck("user_id", &ids).Error
}

func (r *ClusterRepository) ReleaseLease(ctx context.Context, userID int64, node string, now time.Time) error {
	return r.db.DB.WithContext(ctx).Model(&UserLease{}).Where("user_id = ? AND node_id = ?", userID, node).
		Update("expires_at", now).Error
}

func (r *ClusterRepository) LeaseOwner(ctx context.Context, userID int64, now time.Time) (Node, UserLease, bool, error) {
	var l UserLease
	err := r.db.DB.WithContext(ctx).Where("user_id = ? AND expires_at > ?", userID, now).First(&l).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return Node{}, UserLease{}, false, nil
	} else if err != nil {
		return Node{}, UserLease{}, false, err
	}
	var n Node
	if err := r.db.DB.WithContext(ctx).Where("id = ?", l.NodeID).First(&n).Error; err != nil {
		return Node{}, UserLease{}, false, notFound(err)
	}
	return n, l, true, nil
}

func (r *ClusterRepository) DeleteOrphanLeases(ctx context.Context) error {
	return r.db.DB.WithContext(ctx).Where("user_id NOT IN (?)", r.db.DB.WithContext(ctx).Model(&User{}).Select("id")).Delete(&UserLease{}).Error
}

func (r *ClusterRepository) SetNodeConf(ctx context.Context, c NodeConf) error {
	return r.db.DB.WithContext(ctx).Save(&c).Error
}

func (r *ClusterRepository) NodeConfs(ctx context.Context) ([]NodeConf, error) {
	var out []NodeConf
	return out, r.db.DB.WithContext(ctx).Order("node_id").Find(&out).Error
}
