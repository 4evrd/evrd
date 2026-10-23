package accounts

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/4evrd/evrd/internal/store"
)

var tracer = otel.Tracer("evrd/accounts")

// NetworkServiceImpl is users' IRC networks, for the web API, *status and
// BOUNCER commands. Checking a network's settings is NormalizeNetwork's job.
type NetworkServiceImpl interface {
	ListNetworks(ctx context.Context, userID int64) ([]store.Network, error)
	NetworkByID(ctx context.Context, id int64) (store.Network, error)
	AllNetworks(ctx context.Context) ([]store.Network, error)
	CountNetworks(ctx context.Context, userID int64) (int64, error)
	// NetworkCounts is the number of networks per user ID.
	NetworkCounts(ctx context.Context) (map[int64]int64, error)
	CreateNetwork(ctx context.Context, n store.Network) (store.Network, error)
	// UpdateNetwork saves a network's editable settings (not Enabled).
	UpdateNetwork(ctx context.Context, n store.Network) error
	SetNetworkEnabled(ctx context.Context, id int64, enabled bool) error
}

// NetworkService keeps networks in their repository.
type NetworkService struct {
	Repository store.NetworkRepositoryImpl
}

// NewNetworkService is the network service on repo.
func NewNetworkService(repo store.NetworkRepositoryImpl) NetworkServiceImpl {
	return &NetworkService{Repository: repo}
}

func (s *NetworkService) ListNetworks(ctx context.Context, userID int64) ([]store.Network, error) {
	ctx, span := tracer.Start(ctx, "NetworkService.ListNetworks", trace.WithAttributes(attribute.Int64("user.id", userID)))
	defer span.End()
	return s.Repository.ListNetworks(ctx, userID)
}

func (s *NetworkService) NetworkByID(ctx context.Context, id int64) (store.Network, error) {
	ctx, span := tracer.Start(ctx, "NetworkService.NetworkByID", trace.WithAttributes(attribute.Int64("network.id", id)))
	defer span.End()
	return s.Repository.NetworkByID(ctx, id)
}

func (s *NetworkService) AllNetworks(ctx context.Context) ([]store.Network, error) {
	ctx, span := tracer.Start(ctx, "NetworkService.AllNetworks")
	defer span.End()
	return s.Repository.AllNetworks(ctx)
}

func (s *NetworkService) CountNetworks(ctx context.Context, userID int64) (int64, error) {
	ctx, span := tracer.Start(ctx, "NetworkService.CountNetworks", trace.WithAttributes(attribute.Int64("user.id", userID)))
	defer span.End()
	return s.Repository.CountNetworks(ctx, userID)
}

func (s *NetworkService) NetworkCounts(ctx context.Context) (map[int64]int64, error) {
	ctx, span := tracer.Start(ctx, "NetworkService.NetworkCounts")
	defer span.End()
	return s.Repository.NetworkCounts(ctx)
}

func (s *NetworkService) CreateNetwork(ctx context.Context, n store.Network) (store.Network, error) {
	ctx, span := tracer.Start(ctx, "NetworkService.CreateNetwork", trace.WithAttributes(attribute.Int64("user.id", n.UserID)))
	defer span.End()
	return s.Repository.CreateNetwork(ctx, n)
}

func (s *NetworkService) UpdateNetwork(ctx context.Context, n store.Network) error {
	ctx, span := tracer.Start(ctx, "NetworkService.UpdateNetwork", trace.WithAttributes(attribute.Int64("network.id", n.ID)))
	defer span.End()
	return s.Repository.UpdateNetwork(ctx, n)
}

func (s *NetworkService) SetNetworkEnabled(ctx context.Context, id int64, enabled bool) error {
	ctx, span := tracer.Start(ctx, "NetworkService.SetNetworkEnabled", trace.WithAttributes(attribute.Int64("network.id", id)))
	defer span.End()
	return s.Repository.SetNetworkEnabled(ctx, id, enabled)
}
