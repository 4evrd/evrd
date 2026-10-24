package messages

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/4evrd/evrd/internal/store"
)

// fakePacks is the language packs, counting reads.
type fakePacks struct {
	server map[string]store.LanguagePack
	user   map[int64]store.UserLanguagePack
	reads  int
	fail   bool
}

func (f *fakePacks) LanguagePacks(context.Context) ([]store.LanguagePack, error) {
	if f.fail {
		return nil, errors.New("down")
	}
	var out []store.LanguagePack
	for _, p := range f.server {
		out = append(out, p)
	}
	return out, nil
}

func (f *fakePacks) LanguagePack(_ context.Context, code string) (store.LanguagePack, error) {
	f.reads++
	if p, ok := f.server[code]; ok {
		return p, nil
	}
	return store.LanguagePack{}, store.ErrNotFound
}

func (f *fakePacks) PutLanguagePack(context.Context, store.LanguagePack) error { return nil }
func (f *fakePacks) DeleteLanguagePack(context.Context, string) error          { return nil }

func (f *fakePacks) UserLanguagePack(_ context.Context, id int64) (store.UserLanguagePack, error) {
	f.reads++
	if p, ok := f.user[id]; ok {
		return p, nil
	}
	return store.UserLanguagePack{}, store.ErrNotFound
}

func (f *fakePacks) PutUserLanguagePack(context.Context, store.UserLanguagePack) error { return nil }
func (f *fakePacks) DeleteUserLanguagePack(context.Context, int64) error               { return nil }

func pack(t *testing.T, msgs map[string]any) string {
	t.Helper()
	b, err := json.Marshal(msgs)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// Every built-in language has every English key, and only those, with
// English's placeholders and plural forms that end in other.
func TestBuiltinCatalogs(t *testing.T) {
	en := raw["en"]
	if len(en) < 100 {
		t.Fatalf("English has %d texts", len(en))
	}
	for _, code := range []string{"de", "en", "es", "id", "it", "ja", "tr"} {
		msgs, ok := New(nil).Builtin(code)
		if !ok {
			t.Fatalf("%s isn't built in", code)
		}
		for k := range en {
			if _, ok := msgs[k]; !ok {
				t.Errorf("%s: no %s", code, k)
			}
		}
		for k, v := range msgs {
			want, ok := en[k]
			if !ok {
				t.Errorf("%s: %s isn't an English key", code, k)
				continue
			}
			wantHoles := holesOf(t, want)
			for form, text := range texts(t, code+" "+k, v) {
				if !slices.Contains([]string{"zero", "one", "two", "few", "many", "other", ""}, form) {
					t.Errorf("%s %s: plural form %q", code, k, form)
				}
				if got := strings.Join(Holes(text), ","); got != wantHoles {
					t.Errorf("%s %s (%s): placeholders %q, English has %q", code, k, form, got, wantHoles)
				}
			}
		}
	}
	if !slices.Equal(Languages(), []string{"de", "en", "es", "id", "it", "ja", "tr"}) {
		t.Errorf("languages %v", Languages())
	}
	if keys := Keys(); len(keys) != len(en) || !slices.IsSorted(keys) {
		t.Errorf("keys: %d", len(keys))
	}
}

// texts are a message's texts by plural form ("" for a plain text).
func texts(t *testing.T, what string, v json.RawMessage) map[string]string {
	t.Helper()
	var s string
	if json.Unmarshal(v, &s) == nil {
		return map[string]string{"": s}
	}
	var forms map[string]string
	if err := json.Unmarshal(v, &forms); err != nil || forms["other"] == "" {
		t.Fatalf("%s: %s", what, v)
	}
	return forms
}

func holesOf(t *testing.T, v json.RawMessage) string {
	t.Helper()
	for _, text := range texts(t, "English", v) {
		return strings.Join(Holes(text), ",")
	}
	return ""
}

func TestPrinter(t *testing.T) {
	ctx := context.Background()
	s := New(nil)
	en := s.For(ctx, store.User{})
	if got := en.T("irc.connect.connecting", "network", "libera", "host", "irc.libera.chat", "port", 6697); got != "Connecting to libera (irc.libera.chat:6697)…" {
		t.Errorf("English: %q", got)
	}
	if got := en.T("irc.count.channels", "count", 1); got != "1 channel" {
		t.Errorf("one: %q", got)
	}
	if got := en.T("irc.count.channels", "count", 3); got != "3 channels" {
		t.Errorf("other: %q", got)
	}
	it := s.For(ctx, store.User{Language: "it"})
	if got := it.T("irc.notOn", "channel", "#evrd"); got != "Non sei su #evrd." {
		t.Errorf("Italian: %q", got)
	}
	if got := it.T("irc.count.networks", "count", 2); got != "2 reti" {
		t.Errorf("Italian plural: %q", got)
	}
	// a language without the key, or without the language: English
	if got := s.For(ctx, store.User{Language: "xx"}).T("irc.notOn", "channel", "#a"); got != "You're not on #a." {
		t.Errorf("unknown language: %q", got)
	}
	// unknown keys show themselves; a {name} without a value stays
	if got := it.T("irc.nope"); got != "irc.nope" {
		t.Errorf("missing key: %q", got)
	}
	if got := it.T("irc.notOn"); got != "Non sei su {channel}." {
		t.Errorf("no value: %q", got)
	}
	var nilPrinter *Printer
	if got := nilPrinter.T("irc.adminsOnly"); got != "Admins only." {
		t.Errorf("nil printer: %q", got)
	}
}

func TestPacksLayer(t *testing.T) {
	ctx := context.Background()
	f := &fakePacks{
		server: map[string]store.LanguagePack{
			"it": {Code: "it", Messages: pack(t, map[string]any{"irc.notOn": "Server: {channel}", "irc.adminsOnly": "Solo admin (server).", "chat.x": "web only"})},
			"pl": {Code: "pl", Messages: pack(t, map[string]any{"irc.count.channels": map[string]string{"one": "{count} kanał", "few": "{count} kanały", "many": "{count} kanałów", "other": "{count} kanału"}})},
		},
		user: map[int64]store.UserLanguagePack{
			7: {UserID: 7, Code: "it", Messages: pack(t, map[string]any{"irc.notOn": "Mio: {channel}"})},
			8: {UserID: 8, Code: "de", Messages: pack(t, map[string]any{"irc.notOn": "Meins: {channel}"})},
		},
	}
	s := New(f)
	// the user's own pack wins, then the server's, then built in
	p := s.For(ctx, store.User{ID: 7, Language: "it"})
	if got := p.T("irc.notOn", "channel", "#a"); got != "Mio: #a" {
		t.Errorf("own pack: %q", got)
	}
	if got := p.T("irc.adminsOnly"); got != "Solo admin (server)." {
		t.Errorf("server pack: %q", got)
	}
	if got := p.T("irc.networkGone"); got != "Questa rete non esiste più." {
		t.Errorf("built in: %q", got)
	}
	// a user's own pack for another language doesn't apply
	if got := s.For(ctx, store.User{ID: 8, Language: "it"}).T("irc.notOn", "channel", "#a"); got != "Server: #a" {
		t.Errorf("other language's own pack: %q", got)
	}
	// a language only a pack has, with its own plural forms
	pl := s.For(ctx, store.User{ID: 9, Language: "pl"})
	for n, want := range map[int]string{1: "1 kanał", 3: "3 kanały", 5: "5 kanałów", 22: "22 kanały"} {
		if got := pl.T("irc.count.channels", "count", n); got != want {
			t.Errorf("Polish %d: %q, want %q", n, got, want)
		}
	}
	if got := pl.T("irc.adminsOnly"); got != "Admins only." {
		t.Errorf("Polish falls back to English: %q", got)
	}

	// packs are read again only after packTTL, or after Forget
	now := time.Now()
	s.(*Service).now = func() time.Time { return now }
	s.Forget()
	f.reads = 0
	s.For(ctx, store.User{ID: 7, Language: "it"})
	s.For(ctx, store.User{ID: 7, Language: "it"})
	if f.reads != 2 {
		t.Errorf("reads %d, want 2 (server and own pack, once)", f.reads)
	}
	f.server["it"] = store.LanguagePack{Code: "it", Messages: pack(t, map[string]any{"irc.adminsOnly": "Cambiato."})}
	if got := s.For(ctx, store.User{ID: 7, Language: "it"}).T("irc.adminsOnly"); got != "Solo admin (server)." {
		t.Errorf("within the TTL: %q", got)
	}
	now = now.Add(packTTL)
	if got := s.For(ctx, store.User{ID: 7, Language: "it"}).T("irc.adminsOnly"); got != "Cambiato." {
		t.Errorf("after the TTL: %q", got)
	}
	f.server["it"] = store.LanguagePack{Code: "it", Messages: `{"irc.adminsOnly":"Di nuovo."}`}
	s.Forget()
	if got := s.For(ctx, store.User{ID: 7, Language: "it"}).T("irc.adminsOnly"); got != "Di nuovo." {
		t.Errorf("after Forget: %q", got)
	}
	// a pack that isn't JSON is no pack
	f.server["it"] = store.LanguagePack{Code: "it", Messages: "{"}
	s.Forget()
	if got := s.For(ctx, store.User{ID: 9, Language: "it"}).T("irc.adminsOnly"); got != "Solo per gli admin." {
		t.Errorf("broken pack: %q", got)
	}
}

func TestCoverage(t *testing.T) {
	ctx := context.Background()
	f := &fakePacks{server: map[string]store.LanguagePack{
		"pl": {Code: "pl", Messages: `{"irc.adminsOnly":"Tylko admini.","irc.nope":"x","chat.y":"z"}`},
	}}
	total, have, err := New(f).Coverage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if total != len(raw["en"]) || have["it"] != total || have["en"] != total || have["pl"] != 1 {
		t.Errorf("total %d, have %v", total, have)
	}
	f.fail = true
	if _, _, err := New(f).Coverage(ctx); err == nil {
		t.Error("no error when the packs can't be listed")
	}
	if total, have, err := New(nil).Coverage(ctx); err != nil || have["ja"] != total || len(have) != 7 {
		t.Errorf("no packs: %d %v %v", total, have, err)
	}
	if _, ok := New(nil).Builtin("pl"); ok {
		t.Error("pl is built in?")
	}
}

func TestHoles(t *testing.T) {
	if got := Holes("{b} and {a}, {b} again, {}"); !slices.Equal(got, []string{"a", "b"}) {
		t.Errorf("holes %v", got)
	}
}
