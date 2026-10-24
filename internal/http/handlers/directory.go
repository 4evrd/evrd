package handlers

import (
	"context"
	"net/http"
	"time"

	"github.com/4evrd/evrd/internal/directory"
)

// SetDirectory serves the network directory users pick from when adding a
// network.
func (s *Handlers) SetDirectory(d *directory.Directory) { s.dir = d }

// Directory lists the directory's networks. Every node has its own
// copy, so it's served here.
func (s *Handlers) Directory(w http.ResponseWriter, r *http.Request) {
	nets := []directory.Network{}
	out := map[string]any{"source": "", "updated": nil, "fetchedAt": nil}
	if s.dir != nil {
		st := s.dir.Status()
		if n := s.dir.Networks(); n != nil {
			nets = n
		}
		out["source"], out["updated"], out["fetchedAt"] = st.Source, unixT(st.Updated), unixT(st.FetchedAt)
	}
	out["networks"] = nets
	writeJSON(w, http.StatusOK, out)
}

// RefreshDirectory checks the source now (Admin → Settings).
func (s *Handlers) RefreshDirectory(w http.ResponseWriter, r *http.Request) {
	if s.dir == nil {
		s.say(w, r, http.StatusNotFound, "web.noDirectory")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
	defer cancel()
	s.dir.Refresh(ctx) // a failure is in the status
	writeJSON(w, http.StatusOK, s.directoryStatus())
}

// directoryStatus is the source and its last fetch, for Admin → Settings.
func (s *Handlers) directoryStatus() map[string]any {
	if s.dir == nil {
		return nil
	}
	st := s.dir.Status()
	return map[string]any{
		"source": st.Source, "networks": st.Networks, "updated": unixT(st.Updated),
		"fetchedAt": unixT(st.FetchedAt), "checkedAt": unixT(st.CheckedAt),
		"error": st.Error, "skipped": st.Skipped,
	}
}
