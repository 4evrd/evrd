package accounts

import (
	"context"

	"github.com/4evrd/evrd/internal/store"
)

// Users and their IRC client passwords, for the web API, *status and evrctl.

func (s *Service) UserByID(ctx context.Context, id int64) (store.User, error) {
	return s.st.UserByID(ctx, id)
}

func (s *Service) UserByUsername(ctx context.Context, username string) (store.User, error) {
	return s.st.UserByUsername(ctx, username)
}

func (s *Service) ListUsers(ctx context.Context) ([]store.User, error) {
	return s.st.ListUsers(ctx)
}

// UpdateUser saves every field of u.
func (s *Service) UpdateUser(ctx context.Context, u store.User) error {
	return s.st.UpdateUser(ctx, u)
}

// DeleteUser removes a user and everything they own (see
// store.UserRepositoryImpl). bouncer.DeleteUser also stops their networks;
// this is for when no bouncer runs (evrctl).
func (s *Service) DeleteUser(ctx context.Context, id int64) error {
	return s.st.DeleteUser(ctx, id)
}

func (s *Service) ListClientPasswords(ctx context.Context, userID int64) ([]store.ClientPassword, error) {
	return s.st.ListClientPasswords(ctx, userID)
}

func (s *Service) DeleteClientPassword(ctx context.Context, userID, id int64) error {
	return s.st.DeleteClientPassword(ctx, userID, id)
}
