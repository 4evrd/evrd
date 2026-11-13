package handlers

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/4evrd/evrd/internal/auth"
	"github.com/4evrd/evrd/internal/update"
)

// SetUpdater tells admins about new releases and installs them.
func (s *Handlers) SetUpdater(u *update.Updater) { s.upd = u }

// UpdateStatus is the running version and the latest release (admins).
func (s *Handlers) UpdateStatus(w http.ResponseWriter, r *http.Request) {
	if s.upd == nil {
		s.say(w, r, http.StatusNotFound, "web.noUpdates")
		return
	}
	writeJSON(w, http.StatusOK, s.updateStatus())
}

// CheckUpdate reads the release feed now.
func (s *Handlers) CheckUpdate(w http.ResponseWriter, r *http.Request) {
	if s.upd == nil {
		s.say(w, r, http.StatusNotFound, "web.noUpdates")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
	defer cancel()
	s.upd.Check(ctx) // a failure is in the status
	writeJSON(w, http.StatusOK, s.updateStatus())
}

// InstallUpdate installs the latest release on every node; this one
// restarts into it once it's downloaded and checked (202: under way).
func (s *Handlers) InstallUpdate(w http.ResponseWriter, r *http.Request) {
	if s.upd == nil {
		s.say(w, r, http.StatusNotFound, "web.noUpdates")
		return
	}
	by := auth.User(r).Username
	v, err := s.upd.Request(r.Context(), by)
	switch {
	case errors.Is(err, update.ErrNothingNewer):
		s.say(w, r, http.StatusConflict, "web.nothingNewer")
		return
	case errors.Is(err, update.ErrCantInstall):
		s.say(w, r, http.StatusConflict, "web.cantInstall", "why", s.upd.Status().Why)
		return
	case err != nil:
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.log.Info("update requested", "admin", by, "version", v)
	writeJSON(w, http.StatusAccepted, s.updateStatus())
}

// updateStatus is update.Status for the web app (nil without an updater).
func (s *Handlers) updateStatus() any {
	if s.upd == nil {
		return nil
	}
	st := s.upd.Status()
	out := map[string]any{
		"current": st.Current, "base": st.Base, "source": st.Source, "available": st.Available,
		"checkedAt": unixT(st.CheckedAt), "error": st.Error,
		"canInstall": st.CanInstall, "why": st.Why, "mode": st.Mode,
		"installing": st.Installing, "installError": st.InstallError,
	}
	if update.Newer(st.Requested, st.Current) {
		out["requested"] = st.Requested
	}
	if l := st.Latest; l != nil {
		out["latest"] = map[string]any{"version": l.Version, "url": l.URL, "notes": l.Notes, "published": unixT(l.Published)}
	}
	return out
}
