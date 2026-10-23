package languages

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/4evrd/evrd/internal/store"
)

func TestLanguagePacks(t *testing.T) {
	st, err := store.Open("sqlite:" + filepath.Join(t.TempDir(), "evrd.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	s, ctx := New(st), t.Context()

	if _, err := s.LanguagePack(ctx, "fr"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("missing pack: %v", err)
	}
	if err := s.PutLanguagePack(ctx, store.LanguagePack{Code: "fr", Name: "Français", Messages: `{"a.b":"c"}`}); err != nil {
		t.Fatal(err)
	}
	if p, err := s.LanguagePack(ctx, "fr"); err != nil || p.Messages != `{"a.b":"c"}` {
		t.Fatalf("pack %+v, %v", p, err)
	}
	if ps, err := s.LanguagePacks(ctx); err != nil || len(ps) != 1 || ps[0].Code != "fr" || ps[0].Messages != "" {
		t.Fatalf("packs %+v, %v", ps, err)
	}
	if err := s.DeleteLanguagePack(ctx, "fr"); err != nil {
		t.Fatal(err)
	}
	if ps, _ := s.LanguagePacks(ctx); len(ps) != 0 {
		t.Fatalf("after delete: %+v", ps)
	}

	// a user's own
	if _, err := s.UserLanguagePack(ctx, 7); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("missing user pack: %v", err)
	}
	if err := s.PutUserLanguagePack(ctx, store.UserLanguagePack{UserID: 7, Code: "de", Messages: "{}"}); err != nil {
		t.Fatal(err)
	}
	if p, err := s.UserLanguagePack(ctx, 7); err != nil || p.Code != "de" {
		t.Fatalf("user pack %+v, %v", p, err)
	}
	if err := s.DeleteUserLanguagePack(ctx, 7); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UserLanguagePack(ctx, 7); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("after delete: %v", err)
	}
}
