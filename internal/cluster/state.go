package cluster

import (
	"context"
	"time"

	"go.opentelemetry.io/otel"

	"github.com/4evrd/evrd/internal/store"
)

var tracer = otel.Tracer("evrd/cluster")

// StateServiceImpl reads the cluster's shared state for the admin pages:
// which nodes are live, who holds which user, and what each node wrote to
// its config file. A single node has it too (it heartbeats and holds
// everyone), so it works with or without a Cluster.
type StateServiceImpl interface {
	// LiveNodes lists nodes that sent a heartbeat after since.
	LiveNodes(ctx context.Context, since time.Time) ([]store.Node, error)
	// Leases lists every unexpired lease.
	Leases(ctx context.Context, now time.Time) ([]store.UserLease, error)
	NodeConfs(ctx context.Context) ([]store.NodeConf, error)
}

// StateService reads that state from its repository.
type StateService struct {
	Repository store.ClusterRepositoryImpl
}

// NewStateService is the cluster state service on repo.
func NewStateService(repo store.ClusterRepositoryImpl) StateServiceImpl {
	return &StateService{Repository: repo}
}

func (s *StateService) LiveNodes(ctx context.Context, since time.Time) ([]store.Node, error) {
	ctx, span := tracer.Start(ctx, "StateService.LiveNodes")
	defer span.End()
	return s.Repository.LiveNodes(ctx, since)
}

func (s *StateService) Leases(ctx context.Context, now time.Time) ([]store.UserLease, error) {
	ctx, span := tracer.Start(ctx, "StateService.Leases")
	defer span.End()
	return s.Repository.Leases(ctx, now)
}

func (s *StateService) NodeConfs(ctx context.Context) ([]store.NodeConf, error) {
	ctx, span := tracer.Start(ctx, "StateService.NodeConfs")
	defer span.End()
	return s.Repository.NodeConfs(ctx)
}
