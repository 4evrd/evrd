package store

import (
	"errors"
	"testing"
)

func TestLanguagePacks(t *testing.T) {
	st := open(t)
	if _, err := st.LanguagePack(t.Context(), "de"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing pack: %v", err)
	}
	if err := st.PutLanguagePack(t.Context(), LanguagePack{Code: "de", Name: "Deutsch", Messages: `{"a.b":"c"}`, UpdatedBy: "root"}); err != nil {
		t.Fatal(err)
	}
	st.PutLanguagePack(t.Context(), LanguagePack{Code: "de", Name: "Deutsch (neu)", Messages: `{"a.b":"d"}`, UpdatedBy: "root"})
	st.PutLanguagePack(t.Context(), LanguagePack{Code: "fr", Name: "Français", Messages: `{}`})
	list, err := st.LanguagePacks(t.Context())
	if err != nil || len(list) != 2 || list[0].Code != "de" || list[0].Name != "Deutsch (neu)" || list[0].Messages != "" || list[0].UpdatedAt.IsZero() {
		t.Fatalf("list: %v %+v", err, list)
	}
	if p, _ := st.LanguagePack(t.Context(), "de"); p.Messages != `{"a.b":"d"}` || p.UpdatedBy != "root" {
		t.Fatalf("pack: %+v", p)
	}
	if err := st.DeleteLanguagePack(t.Context(), "de"); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteLanguagePack(t.Context(), "de"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete twice: %v", err)
	}

	u, _ := st.CreateUser(t.Context(), User{Username: "alice"})
	other, _ := st.CreateUser(t.Context(), User{Username: "bob"})
	if _, err := st.UserLanguagePack(t.Context(), u.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing user pack: %v", err)
	}
	st.PutUserLanguagePack(t.Context(), UserLanguagePack{UserID: u.ID, Code: "it", Name: "Mio", Messages: `{}`})
	st.PutUserLanguagePack(t.Context(), UserLanguagePack{UserID: u.ID, Code: "xx", Name: "Mine", Messages: `{"a.b":"x"}`})
	st.PutUserLanguagePack(t.Context(), UserLanguagePack{UserID: other.ID, Code: "it", Name: "Bob's", Messages: `{}`})
	if p, _ := st.UserLanguagePack(t.Context(), u.ID); p.Code != "xx" || p.Messages != `{"a.b":"x"}` {
		t.Fatalf("user pack: %+v", p)
	}
	st.DeleteUser(t.Context(), u.ID)
	if _, err := st.UserLanguagePack(t.Context(), u.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("pack survived its user: %v", err)
	}
	if err := st.DeleteUserLanguagePack(t.Context(), other.ID); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteUserLanguagePack(t.Context(), other.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete twice: %v", err)
	}
}
