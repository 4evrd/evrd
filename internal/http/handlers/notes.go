package handlers

// The user's notes (Account → Notes), the same ones /msg *notes edits.

import (
	"net/http"

	"github.com/4evrd/evrd/internal/auth"
	"github.com/4evrd/evrd/internal/bouncer"
	"github.com/4evrd/evrd/internal/store"
)

func noteDTO(n store.UserNote) map[string]any {
	return map[string]any{"key": n.Key, "text": n.Text, "updatedAt": n.UpdatedAt.Unix()}
}

// MyNotes is GET /api/me/notes: the user's notes, by key.
func (s *Handlers) MyNotes(w http.ResponseWriter, r *http.Request) {
	notes, err := s.b.Notes(auth.User(r).ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := make([]map[string]any, 0, len(notes))
	for _, n := range notes {
		out = append(out, noteDTO(n))
	}
	writeJSON(w, http.StatusOK, out)
}

// PutMyNote is PUT /api/me/notes/{key} {text}: adds the note, or changes
// the one with that key.
func (s *Handlers) PutMyNote(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Text string `json:"text"`
	}
	if !s.decode(w, r, &in) {
		return
	}
	uid, key := auth.User(r).ID, r.PathValue("key")
	if err := s.b.SetNote(uid, key, in.Text, bouncer.NoteSet); err != nil {
		s.fail(w, r, err)
		return
	}
	s.MyNotes(w, r)
}

// DeleteMyNote is DELETE /api/me/notes/{key}.
func (s *Handlers) DeleteMyNote(w http.ResponseWriter, r *http.Request) {
	if err := s.b.DeleteNote(auth.User(r).ID, r.PathValue("key")); err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
