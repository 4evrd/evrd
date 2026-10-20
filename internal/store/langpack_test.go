package store

import (
	"errors"
	"testing"
)

func TestLanguagePacks(t *testing.T) {
	st := open(t)
	if _, err := st.LanguagePack("de"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing pack: %v", err)
	}
	if err := st.PutLanguagePack(LanguagePack{Code: "de", Name: "Deutsch", Messages: `{"a.b":"c"}`, UpdatedBy: "root"}); err != nil {
		t.Fatal(err)
	}
	st.PutLanguagePack(LanguagePack{Code: "de", Name: "Deutsch (neu)", Messages: `{"a.b":"d"}`, UpdatedBy: "root"})
	st.PutLanguagePack(LanguagePack{Code: "fr", Name: "Français", Messages: `{}`})
	list, err := st.LanguagePacks()
	if err != nil || len(list) != 2 || list[0].Code != "de" || list[0].Name != "Deutsch (neu)" || list[0].Messages != "" || list[0].UpdatedAt.IsZero() {
		t.Fatalf("list: %v %+v", err, list)
	}
	if p, _ := st.LanguagePack("de"); p.Messages != `{"a.b":"d"}` || p.UpdatedBy != "root" {
		t.Fatalf("pack: %+v", p)
	}
	if err := st.DeleteLanguagePack("de"); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteLanguagePack("de"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete twice: %v", err)
	}

	u, _ := st.CreateUser(User{Username: "alice"})
	other, _ := st.CreateUser(User{Username: "bob"})
	if _, err := st.UserLanguagePack(u.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing user pack: %v", err)
	}
	st.PutUserLanguagePack(UserLanguagePack{UserID: u.ID, Code: "it", Name: "Mio", Messages: `{}`})
	st.PutUserLanguagePack(UserLanguagePack{UserID: u.ID, Code: "xx", Name: "Mine", Messages: `{"a.b":"x"}`})
	st.PutUserLanguagePack(UserLanguagePack{UserID: other.ID, Code: "it", Name: "Bob's", Messages: `{}`})
	if p, _ := st.UserLanguagePack(u.ID); p.Code != "xx" || p.Messages != `{"a.b":"x"}` {
		t.Fatalf("user pack: %+v", p)
	}
	st.DeleteUser(u.ID)
	if _, err := st.UserLanguagePack(u.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("pack survived its user: %v", err)
	}
	if err := st.DeleteUserLanguagePack(other.ID); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteUserLanguagePack(other.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete twice: %v", err)
	}
}
