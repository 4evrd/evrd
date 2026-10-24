// Package messages is what evrd says over IRC — *status's replies — in each
// user's language. The text is the irc.* area of the same language packs
// as the web app's (docs/translations.md): English and the built-in
// languages are embedded here (locales/<code>.json), and a server pack and
// the user's own pack change or add to them. For each key the first of
// these that has it wins: the user's own pack (when it's for the user's
// language), the server's pack for that language, the built-in language,
// English.
//
// A text fills {name}s from its arguments; an entry that is an object of
// plural forms ({"one": …, "other": …}) picks one by the "count" argument,
// with CLDR's rules for the language.
package messages

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/text/feature/plural"
	"golang.org/x/text/language"

	"github.com/4evrd/evrd/internal/languages"
	"github.com/4evrd/evrd/internal/store"
)

var tracer = otel.Tracer("evrd/messages")

// Area is the prefix of every key here, in a language pack.
const Area = "irc."

// packTTL is how long a pack read from the database is used before it's
// read again: another node may have changed it.
const packTTL = 30 * time.Second

//go:embed locales/*.json
var locales embed.FS

// catalog is a language's texts by key.
type catalog map[string]entry

// entry is a text, or plural forms of one.
type entry struct {
	text  string
	forms map[string]string
}

func (e *entry) UnmarshalJSON(b []byte) error {
	if err := json.Unmarshal(b, &e.text); err == nil {
		return nil
	}
	return json.Unmarshal(b, &e.forms)
}

// builtin are the embedded catalogs by language code; raw, their messages
// as in the files.
var builtin, raw = load()

func load() (map[string]catalog, map[string]map[string]json.RawMessage) {
	cats, raws := map[string]catalog{}, map[string]map[string]json.RawMessage{}
	files, _ := locales.ReadDir("locales")
	for _, f := range files {
		b, _ := locales.ReadFile("locales/" + f.Name())
		var file struct {
			Messages map[string]json.RawMessage `json:"messages"`
		}
		if err := json.Unmarshal(b, &file); err != nil {
			panic(fmt.Sprintf("messages: %s: %v", f.Name(), err))
		}
		code := strings.TrimSuffix(f.Name(), path.Ext(f.Name()))
		cats[code], raws[code] = decode(file.Messages), file.Messages
	}
	return cats, raws
}

// decode reads a pack's messages, keeping the irc.* ones that read as a
// text or plural forms with "other".
func decode(msgs map[string]json.RawMessage) catalog {
	c := catalog{}
	for k, v := range msgs {
		if !strings.HasPrefix(k, Area) {
			continue
		}
		var e entry
		if json.Unmarshal(v, &e) == nil && (e.text != "" || e.forms["other"] != "") {
			c[k] = e
		}
	}
	return c
}

// MessageServiceImpl is evrd's IRC texts.
type MessageServiceImpl interface {
	// For is a printer in u's language, with the packs that apply to u.
	For(ctx context.Context, u store.User) *Printer
	// Builtin is a built-in language's irc.* messages as in its file;
	// false for a language that isn't built in.
	Builtin(code string) (map[string]json.RawMessage, bool)
	// Coverage is how many irc.* texts English has, and how many of them
	// each language has (built in, or from the server's pack).
	Coverage(ctx context.Context) (total int, have map[string]int, err error)
	// Forget drops the packs read so far: this node just changed one.
	Forget()
}

// Service reads the packs through the language pack service.
type Service struct {
	Languages languages.LanguageServiceImpl

	mu     sync.Mutex
	server map[string]cached // by language code
	user   map[int64]cached
	now    func() time.Time
}

// cached is a pack's irc.* texts as read at a time (code "": none).
type cached struct {
	code string
	cat  catalog
	at   time.Time
}

// New is the IRC texts, with packs from langs (nil: built in only).
func New(langs languages.LanguageServiceImpl) MessageServiceImpl {
	return &Service{Languages: langs, server: map[string]cached{}, user: map[int64]cached{}, now: time.Now}
}

func (s *Service) For(ctx context.Context, u store.User) *Printer {
	ctx, span := tracer.Start(ctx, "MessageService.For", trace.WithAttributes(attribute.Int64("user.id", u.ID), attribute.String("language.code", u.Language)))
	defer span.End()
	code := u.Language
	if code == "" {
		code = "en"
	}
	return &Printer{layers: []catalog{s.userPack(ctx, u.ID, code), s.serverPack(ctx, code), builtin[code]}, tag: tagFor(code)}
}

// serverPack is the server's pack for code, nil when there's none.
func (s *Service) serverPack(ctx context.Context, code string) catalog {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c, ok := s.server[code]; ok && s.now().Sub(c.at) < packTTL {
		return c.cat
	}
	c := cached{code: code, at: s.now()}
	if s.Languages != nil {
		if p, err := s.Languages.LanguagePack(ctx, code); err == nil {
			c.cat = parse(p.Messages)
		}
	}
	s.server[code] = c
	return c.cat
}

// userPack is the user's own pack's texts when it's for code, else nil.
func (s *Service) userPack(ctx context.Context, userID int64, code string) catalog {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.user[userID]
	if !ok || s.now().Sub(c.at) >= packTTL {
		c = cached{at: s.now()}
		if s.Languages != nil {
			if p, err := s.Languages.UserLanguagePack(ctx, userID); err == nil {
				c.code, c.cat = p.Code, parse(p.Messages)
			}
		}
		s.user[userID] = c
	}
	if c.code != code {
		return nil
	}
	return c.cat
}

func parse(messages string) catalog {
	var msgs map[string]json.RawMessage
	if json.Unmarshal([]byte(messages), &msgs) != nil {
		return nil
	}
	return decode(msgs)
}

func (s *Service) Builtin(code string) (map[string]json.RawMessage, bool) {
	m, ok := raw[code]
	return m, ok
}

func (s *Service) Coverage(ctx context.Context) (int, map[string]int, error) {
	ctx, span := tracer.Start(ctx, "MessageService.Coverage")
	defer span.End()
	codes := Languages()
	if s.Languages != nil {
		packs, err := s.Languages.LanguagePacks(ctx)
		if err != nil {
			return 0, nil, err
		}
		for _, p := range packs {
			codes = append(codes, p.Code)
		}
	}
	en, have := builtin["en"], map[string]int{}
	for _, code := range codes {
		pack, own, n := s.serverPack(ctx, code), builtin[code], 0
		for k := range en {
			if _, ok := pack[k]; ok {
				n++
			} else if _, ok := own[k]; ok {
				n++
			}
		}
		have[code] = n
	}
	return len(en), have, nil
}

func (s *Service) Forget() {
	s.mu.Lock()
	defer s.mu.Unlock()
	clear(s.server)
	clear(s.user)
}

// Keys are the English keys, sorted.
func Keys() []string {
	keys := make([]string, 0, len(builtin["en"]))
	for k := range builtin["en"] {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// Languages are the built-in languages' codes, sorted.
func Languages() []string {
	codes := make([]string, 0, len(builtin))
	for c := range builtin {
		codes = append(codes, c)
	}
	sort.Strings(codes)
	return codes
}

func tagFor(code string) language.Tag {
	if t, err := language.Parse(code); err == nil {
		return t
	}
	return language.English
}

// Printer is texts in one language. A nil *Printer is English.
type Printer struct {
	layers []catalog // the first that has a key wins; English after them
	tag    language.Tag
}

// English is the English printer.
var English = &Printer{tag: language.English}

// T is key's text with its {name}s filled from args, name/value pairs
// ("count", 3, "network", "libera"). A Text, or an error that says what it
// is in words (Texter), is put in p's language too. A key no language has
// is itself.
func (p *Printer) T(key string, args ...any) string {
	if p == nil {
		p = English
	}
	vars := map[string]any{}
	for i := 0; i+1 < len(args); i += 2 {
		v := args[i+1]
		if t, ok := asText(v); ok {
			v = p.Text(t)
		}
		vars[fmt.Sprint(args[i])] = v
	}
	return fill(p.pick(key, vars), vars)
}

// pick is key's raw text, its plural form chosen by vars["count"].
func (p *Printer) pick(key string, vars map[string]any) string {
	for _, c := range p.layers {
		if t, ok := c.pick(key, p.tag, vars["count"]); ok {
			return t
		}
	}
	if t, ok := builtin["en"].pick(key, language.English, vars["count"]); ok {
		return t
	}
	return key
}

func (c catalog) pick(key string, tag language.Tag, count any) (string, bool) {
	e, ok := c[key]
	switch {
	case !ok:
		return "", false
	case e.forms == nil:
		return e.text, true
	}
	if t := e.forms[form(tag, count)]; t != "" {
		return t, true
	}
	return e.forms["other"], true
}

// form is the CLDR plural category of n (an int, or one as text) in the
// language.
func form(tag language.Tag, n any) string {
	i, ok := n.(int)
	if !ok {
		i, _ = strconv.Atoi(fmt.Sprint(n))
	}
	switch plural.Cardinal.MatchPlural(tag, i, 0, 0, 0, 0) {
	case plural.Zero:
		return "zero"
	case plural.One:
		return "one"
	case plural.Two:
		return "two"
	case plural.Few:
		return "few"
	case plural.Many:
		return "many"
	}
	return "other"
}

var hole = regexp.MustCompile(`\{(\w+)\}`)

func fill(text string, vars map[string]any) string {
	return hole.ReplaceAllStringFunc(text, func(h string) string {
		if v, ok := vars[h[1:len(h)-1]]; ok {
			return fmt.Sprint(v)
		}
		return h
	})
}

// Holes are a text's {placeholders}, sorted, each once.
func Holes(text string) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range hole.FindAllStringSubmatch(text, -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			out = append(out, m[1])
		}
	}
	sort.Strings(out)
	return out
}
