package accounts

import (
	"context"

	"github.com/4evrd/evrd/internal/store"
)

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
	return s.Repository.ListNetworks(ctx, userID)
}

func (s *NetworkService) NetworkByID(ctx context.Context, id int64) (store.Network, error) {
	return s.Repository.NetworkByID(ctx, id)
}

func (s *NetworkService) AllNetworks(ctx context.Context) ([]store.Network, error) {
	return s.Repository.AllNetworks(ctx)
}

func (s *NetworkService) CountNetworks(ctx context.Context, userID int64) (int64, error) {
	return s.Repository.CountNetworks(ctx, userID)
}

func (s *NetworkService) NetworkCounts(ctx context.Context) (map[int64]int64, error) {
	return s.Repository.NetworkCounts(ctx)
}

func (s *NetworkService) CreateNetwork(ctx context.Context, n store.Network) (store.Network, error) {
	return s.Repository.CreateNetwork(ctx, n)
}

func (s *NetworkService) UpdateNetwork(ctx context.Context, n store.Network) error {
	return s.Repository.UpdateNetwork(ctx, n)
}

func (s *NetworkService) SetNetworkEnabled(ctx context.Context, id int64, enabled bool) error {
	return s.Repository.SetNetworkEnabled(ctx, id, enabled)
}
