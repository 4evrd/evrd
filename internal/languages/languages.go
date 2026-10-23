// Package languages keeps the web app's translations: the language packs an
// admin uploads for everyone, and each user's own.
package languages

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/4evrd/evrd/internal/store"
)

var tracer = otel.Tracer("evrd/languages")

// LanguageServiceImpl is the language packs (see store.LanguagePack).
// LanguagePacks leaves out the messages; LanguagePack and UserLanguagePack
// return store.ErrNotFound when there's none.
type LanguageServiceImpl interface {
	LanguagePacks(ctx context.Context) ([]store.LanguagePack, error)
	LanguagePack(ctx context.Context, code string) (store.LanguagePack, error)
	PutLanguagePack(ctx context.Context, p store.LanguagePack) error
	DeleteLanguagePack(ctx context.Context, code string) error
	UserLanguagePack(ctx context.Context, userID int64) (store.UserLanguagePack, error)
	PutUserLanguagePack(ctx context.Context, p store.UserLanguagePack) error
	DeleteUserLanguagePack(ctx context.Context, userID int64) error
}

// Service keeps language packs in their repository.
type Service struct {
	Repository store.LanguagePackRepositoryImpl
}

// New is the language pack service on repo.
func New(repo store.LanguagePackRepositoryImpl) LanguageServiceImpl {
	return &Service{Repository: repo}
}

func (s *Service) LanguagePacks(ctx context.Context) ([]store.LanguagePack, error) {
	ctx, span := tracer.Start(ctx, "LanguageService.LanguagePacks")
	defer span.End()
	return s.Repository.LanguagePacks(ctx)
}

func (s *Service) LanguagePack(ctx context.Context, code string) (store.LanguagePack, error) {
	ctx, span := tracer.Start(ctx, "LanguageService.LanguagePack", trace.WithAttributes(attribute.String("language.code", code)))
	defer span.End()
	return s.Repository.LanguagePack(ctx, code)
}

func (s *Service) PutLanguagePack(ctx context.Context, p store.LanguagePack) error {
	ctx, span := tracer.Start(ctx, "LanguageService.PutLanguagePack")
	defer span.End()
	return s.Repository.PutLanguagePack(ctx, p)
}

func (s *Service) DeleteLanguagePack(ctx context.Context, code string) error {
	ctx, span := tracer.Start(ctx, "LanguageService.DeleteLanguagePack", trace.WithAttributes(attribute.String("language.code", code)))
	defer span.End()
	return s.Repository.DeleteLanguagePack(ctx, code)
}

func (s *Service) UserLanguagePack(ctx context.Context, userID int64) (store.UserLanguagePack, error) {
	ctx, span := tracer.Start(ctx, "LanguageService.UserLanguagePack", trace.WithAttributes(attribute.Int64("user.id", userID)))
	defer span.End()
	return s.Repository.UserLanguagePack(ctx, userID)
}

func (s *Service) PutUserLanguagePack(ctx context.Context, p store.UserLanguagePack) error {
	ctx, span := tracer.Start(ctx, "LanguageService.PutUserLanguagePack")
	defer span.End()
	return s.Repository.PutUserLanguagePack(ctx, p)
}

func (s *Service) DeleteUserLanguagePack(ctx context.Context, userID int64) error {
	ctx, span := tracer.Start(ctx, "LanguageService.DeleteUserLanguagePack", trace.WithAttributes(attribute.Int64("user.id", userID)))
	defer span.End()
	return s.Repository.DeleteUserLanguagePack(ctx, userID)
}
