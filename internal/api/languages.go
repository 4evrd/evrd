package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"slices"
	"unicode/utf8"

	"github.com/4evrd/evrd/internal/auth"
	"github.com/4evrd/evrd/internal/store"
)

// Language packs: translations of the web app uploaded as a JSON file
// (docs/translations.md). An admin's apply to everyone; a user's own only to
// them, on top. The web app converts other formats to this one before
// uploading, and knows the English keys; here only the shape is checked.

// MaxPackSize is the largest language pack file accepted.
const MaxPackSize = 512 << 10

var (
	packCode = regexp.MustCompile(`^[a-z]{2,3}(-[A-Za-z0-9]{2,8})?$`)
	packKey  = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,100}$`)
	// pluralForms are the Intl plural categories a plural entry may have.
	pluralForms = []string{"zero", "one", "two", "few", "many", "other"}
)

const maxPackText = 2000

// languagePack is a pack file: {"code", "name", "messages"}.
type languagePack struct {
	Code     string                     `json:"code"`
	Name     string                     `json:"name"`
	Messages map[string]json.RawMessage `json:"messages"`
}

// readPack reads and checks a pack file from the request body, answering
// 422 itself when it isn't one. messages is the canonical JSON to store.
func readPack(w http.ResponseWriter, r *http.Request) (p languagePack, messages string, ok bool) {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxPackSize))
	var tooBig *http.MaxBytesError
	if errors.As(err, &tooBig) {
		writeErr(w, http.StatusUnprocessableEntity, fmt.Sprintf("the file is larger than %d KiB", MaxPackSize>>10))
		return p, "", false
	} else if err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return p, "", false
	}
	p, messages, err = parsePack(raw)
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return p, "", false
	}
	return p, messages, true
}

// parsePack checks a pack file and returns it with its messages as compact
// JSON.
func parsePack(raw []byte) (languagePack, string, error) {
	var p languagePack
	if len(raw) > MaxPackSize {
		return p, "", fmt.Errorf("the file is larger than %d KiB", MaxPackSize>>10)
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return p, "", errors.New("that isn't a language pack: expected JSON like {\"code\": \"de\", \"name\": \"Deutsch\", \"messages\": {…}}")
	}
	if !packCode.MatchString(p.Code) {
		return p, "", fmt.Errorf("%q isn't a language code: use 2 or 3 lowercase letters, optionally with a region (de, pt-BR)", p.Code)
	}
	if n := utf8.RuneCountInString(p.Name); n < 1 || n > 40 {
		return p, "", errors.New("the language's name must be 1 to 40 characters")
	}
	if p.Messages == nil {
		return p, "", errors.New("the file has no \"messages\" object")
	}
	out := make(map[string]any, len(p.Messages))
	for k, v := range p.Messages {
		if !packKey.MatchString(k) {
			return p, "", fmt.Errorf("%q isn't a message key: use letters, digits, _ . and - (at most 100)", k)
		}
		msg, err := packMessage(v)
		if err != nil {
			return p, "", fmt.Errorf("%s: %s", k, err)
		}
		out[k] = msg
	}
	b, _ := json.Marshal(out)
	return p, string(b), nil
}

// packMessage is one message: text, or plural forms with "other".
func packMessage(raw json.RawMessage) (any, error) {
	var s string
	if bytes.HasPrefix(bytes.TrimSpace(raw), []byte(`"`)) {
		if err := json.Unmarshal(raw, &s); err != nil {
			return nil, errors.New("not a string")
		}
		return s, packText(s)
	}
	var forms map[string]string
	if err := json.Unmarshal(raw, &forms); err != nil || forms == nil {
		return nil, errors.New("must be text, or plural forms like {\"one\": \"…\", \"other\": \"…\"}")
	}
	for f, text := range forms {
		if !slices.Contains(pluralForms, f) {
			return nil, fmt.Errorf("%q isn't a plural form: use zero, one, two, few, many or other", f)
		}
		if err := packText(text); err != nil {
			return nil, err
		}
	}
	if _, ok := forms["other"]; !ok {
		return nil, errors.New("plural forms need \"other\"")
	}
	return forms, nil
}

func packText(s string) error {
	if utf8.RuneCountInString(s) > maxPackText {
		return fmt.Errorf("longer than %d characters", maxPackText)
	}
	return nil
}

// packDTO is a pack as the web app reads it; messages is nil in listings.
func packDTO(code, name, messages string, updated int64) map[string]any {
	out := map[string]any{"code": code, "name": name, "updatedAt": updated}
	if messages != "" {
		out["messages"] = json.RawMessage(messages)
	}
	return out
}

// --- the server's packs ----------------------------------------------------

// handleLanguages lists the server's packs. Public: the sign-in page uses it.
func (s *Server) handleLanguages(w http.ResponseWriter, r *http.Request) {
	list, err := s.langs.LanguagePacks(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	out := make([]map[string]any, 0, len(list))
	for _, p := range list {
		out = append(out, packDTO(p.Code, p.Name, "", p.UpdatedAt.Unix()))
	}
	writeJSON(w, http.StatusOK, out)
}

// handleLanguage is one server pack with its messages. Public.
func (s *Server) handleLanguage(w http.ResponseWriter, r *http.Request) {
	p, err := s.langs.LanguagePack(r.Context(), r.PathValue("code"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, packDTO(p.Code, p.Name, p.Messages, p.UpdatedAt.Unix()))
}

func (s *Server) handlePutLanguage(w http.ResponseWriter, r *http.Request) {
	p, messages, ok := readPack(w, r)
	if !ok {
		return
	}
	if p.Code != r.PathValue("code") {
		writeErr(w, http.StatusUnprocessableEntity, fmt.Sprintf("the file is for %q, not %q", p.Code, r.PathValue("code")))
		return
	}
	admin := auth.User(r).Username
	if err := s.langs.PutLanguagePack(r.Context(), store.LanguagePack{Code: p.Code, Name: p.Name, Messages: messages, UpdatedBy: admin}); err != nil {
		s.fail(w, err)
		return
	}
	s.log.Info("language pack saved", "admin", admin, "code", p.Code, "messages", len(p.Messages))
	saved, err := s.langs.LanguagePack(r.Context(), p.Code)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, packDTO(saved.Code, saved.Name, "", saved.UpdatedAt.Unix()))
}

func (s *Server) handleDeleteLanguage(w http.ResponseWriter, r *http.Request) {
	code := r.PathValue("code")
	if err := s.langs.DeleteLanguagePack(r.Context(), code); err != nil {
		s.fail(w, err)
		return
	}
	s.log.Info("language pack deleted", "admin", auth.User(r).Username, "code", code)
	w.WriteHeader(http.StatusNoContent)
}

// --- the user's own pack -----------------------------------------------------

// myPackDTO is what /api/me says about the user's own pack (nil: none).
func (s *Server) myPackDTO(userID int64) any {
	p, err := s.langs.UserLanguagePack(context.Background(), userID)
	if err != nil {
		return nil
	}
	return packDTO(p.Code, p.Name, "", p.UpdatedAt.Unix())
}

// handleMyLanguagePack is the user's own pack with its messages; 404 when
// they have none.
func (s *Server) handleMyLanguagePack(w http.ResponseWriter, r *http.Request) {
	p, err := s.langs.UserLanguagePack(r.Context(), auth.User(r).ID)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, packDTO(p.Code, p.Name, p.Messages, p.UpdatedAt.Unix()))
}

func (s *Server) handlePutMyLanguagePack(w http.ResponseWriter, r *http.Request) {
	p, messages, ok := readPack(w, r)
	if !ok {
		return
	}
	id := auth.User(r).ID
	if err := s.langs.PutUserLanguagePack(r.Context(), store.UserLanguagePack{UserID: id, Code: p.Code, Name: p.Name, Messages: messages}); err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.myPackDTO(id))
}

func (s *Server) handleDeleteMyLanguagePack(w http.ResponseWriter, r *http.Request) {
	if err := s.langs.DeleteUserLanguagePack(r.Context(), auth.User(r).ID); err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// languageAllowed says whether u may pick code: a built-in language, a
// server pack or their own pack's.
func (s *Server) languageAllowed(u store.User, code string) bool {
	if slices.Contains(Languages, code) {
		return true
	}
	packs, _ := s.langs.LanguagePacks(context.Background())
	if slices.ContainsFunc(packs, func(p store.LanguagePack) bool { return p.Code == code }) {
		return true
	}
	p, err := s.langs.UserLanguagePack(context.Background(), u.ID)
	return err == nil && p.Code == code
}
