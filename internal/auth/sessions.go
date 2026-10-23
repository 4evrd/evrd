package auth

import (
	"context"

	"github.com/4evrd/evrd/internal/store"
)

// SessionServiceImpl is users' web sign-in sessions: listing them and
// signing out.
type SessionServiceImpl interface {
	ListSessions(ctx context.Context, userID int64) ([]store.Session, error)
	// DeleteSession signs out one of a user's sessions.
	DeleteSession(ctx context.Context, userID int64, id string) error
	// DeleteUserSessions signs a user out everywhere except keep ("" = all).
	DeleteUserSessions(ctx context.Context, userID int64, keep string) error
}

// SessionService keeps sessions in their repository.
type SessionService struct {
	Repository store.SessionRepositoryImpl
}

// NewSessionService is the session service on repo.
func NewSessionService(repo store.SessionRepositoryImpl) SessionServiceImpl {
	return &SessionService{Repository: repo}
}

func (s *SessionService) ListSessions(ctx context.Context, userID int64) ([]store.Session, error) {
	return s.Repository.ListSessions(ctx, userID)
}

func (s *SessionService) DeleteSession(ctx context.Context, userID int64, id string) error {
	return s.Repository.DeleteSession(ctx, userID, id)
}

func (s *SessionService) DeleteUserSessions(ctx context.Context, userID int64, keep string) error {
	return s.Repository.DeleteUserSessions(ctx, userID, keep)
}
