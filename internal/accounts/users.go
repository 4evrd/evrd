package accounts

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/4evrd/evrd/internal/store"
)

// Users and their IRC client passwords, for the web API, *status and evrctl.

func (s *Service) UserByID(ctx context.Context, id int64) (store.User, error) {
	ctx, span := tracer.Start(ctx, "AccountService.UserByID", trace.WithAttributes(attribute.Int64("user.id", id)))
	defer span.End()
	return s.users.UserByID(ctx, id)
}

func (s *Service) UserByUsername(ctx context.Context, username string) (store.User, error) {
	ctx, span := tracer.Start(ctx, "AccountService.UserByUsername", trace.WithAttributes(attribute.String("user.name", username)))
	defer span.End()
	return s.users.UserByUsername(ctx, username)
}

func (s *Service) ListUsers(ctx context.Context) ([]store.User, error) {
	ctx, span := tracer.Start(ctx, "AccountService.ListUsers")
	defer span.End()
	return s.users.ListUsers(ctx)
}

// UpdateUser saves every field of u.
func (s *Service) UpdateUser(ctx context.Context, u store.User) error {
	ctx, span := tracer.Start(ctx, "AccountService.UpdateUser", trace.WithAttributes(attribute.Int64("user.id", u.ID)))
	defer span.End()
	return s.users.UpdateUser(ctx, u)
}

// DeleteUser removes a user and everything they own (see
// store.UserRepositoryImpl). bouncer.DeleteUser also stops their networks;
// this is for when no bouncer runs (evrctl).
func (s *Service) DeleteUser(ctx context.Context, id int64) error {
	ctx, span := tracer.Start(ctx, "AccountService.DeleteUser", trace.WithAttributes(attribute.Int64("user.id", id)))
	defer span.End()
	return s.users.DeleteUser(ctx, id)
}

func (s *Service) ListClientPasswords(ctx context.Context, userID int64) ([]store.ClientPassword, error) {
	ctx, span := tracer.Start(ctx, "AccountService.ListClientPasswords", trace.WithAttributes(attribute.Int64("user.id", userID)))
	defer span.End()
	return s.passwords.ListClientPasswords(ctx, userID)
}

func (s *Service) DeleteClientPassword(ctx context.Context, userID, id int64) error {
	ctx, span := tracer.Start(ctx, "AccountService.DeleteClientPassword", trace.WithAttributes(attribute.Int64("user.id", userID)))
	defer span.End()
	return s.passwords.DeleteClientPassword(ctx, userID, id)
}
