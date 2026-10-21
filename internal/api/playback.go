package api

// Playback buffers in the web app: the network's default line count, each
// buffer's own lines and auto-clear, what's waiting, and clearing — the same
// settings as *status setbuffer / autoclear / clearbuffer.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/4evrd/evrd/internal/auth"
	"github.com/4evrd/evrd/internal/store"
)

// playbackDTO is a network's playback settings as the web app shows them.
func (s *Server) playbackDTO(n store.Network, u store.User) (map[string]any, error) {
	p, err := s.b.Playback(n, u, "")
	if err != nil {
		return nil, err
	}
	bufs := []map[string]any{}
	for _, name := range s.b.BufferNames(n) {
		own := p.Own(name)
		bufs = append(bufs, map[string]any{
			"name": name, "channel": isChannelName(name),
			"lines": own.Lines, "autoClear": own.AutoClear,
			"playsLines": p.Lines(name), "autoClears": p.AutoClears(name), "autoClearDefault": p.AutoClearDefault(name),
			"clearedAt": unixT(p.ClearedAt(name)), "waiting": s.b.Waiting(p, n.ID, name),
		})
	}
	return map[string]any{
		"lines": p.Own("").Lines, "playsLines": p.NetworkLines(),
		"defaults": map[string]any{"lines": u.PlaybackLines, "autoClearChannels": u.AutoClearChanBuffer, "autoClearQueries": u.AutoClearQueryBuffer},
		"lastOn":   unixT(p.LastOn), "buffers": bufs, "max": maxPlaybackLines,
	}, nil
}

func (s *Server) writePlayback(w http.ResponseWriter, r *http.Request, n store.Network) {
	out, err := s.playbackDTO(n, auth.User(r))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handlePlayback(w http.ResponseWriter, r *http.Request) {
	if n, ok := s.ownedNetwork(w, r); ok {
		s.writePlayback(w, r, n)
	}
}

// handleSetPlayback changes playback settings: {buffers, lines?, autoClear?}.
// Buffer "" is the network's default. A field left out is unchanged; null
// goes back to the default.
func (s *Server) handleSetPlayback(w http.ResponseWriter, r *http.Request) {
	n, ok := s.ownedNetwork(w, r)
	if !ok {
		return
	}
	var in struct {
		Buffers   []string        `json:"buffers"`
		Lines     json.RawMessage `json:"lines"`
		AutoClear json.RawMessage `json:"autoClear"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&in); err != nil || len(in.Buffers) == 0 {
		writeErr(w, http.StatusBadRequest, "send the buffers to change")
		return
	}
	null := func(raw json.RawMessage) bool { return bytes.Equal(bytes.TrimSpace(raw), []byte("null")) }
	var lines *int
	if len(in.Lines) > 0 && !null(in.Lines) {
		var v int
		if json.Unmarshal(in.Lines, &v) != nil || v < 0 || v > maxPlaybackLines {
			writeErr(w, http.StatusBadRequest, fmt.Sprintf("lines is a number from 0 to %d", maxPlaybackLines))
			return
		}
		lines = &v
	}
	var autoClear *bool
	if len(in.AutoClear) > 0 && !null(in.AutoClear) {
		var v bool
		if json.Unmarshal(in.AutoClear, &v) != nil {
			writeErr(w, http.StatusBadRequest, "autoClear is true, false or null")
			return
		}
		autoClear = &v
	}
	for _, b := range in.Buffers {
		if len(b) > 255 || strings.ContainsAny(b, " \r\n") {
			writeErr(w, http.StatusBadRequest, "that isn't a buffer name")
			return
		}
		if b == "" && len(in.AutoClear) > 0 {
			writeErr(w, http.StatusBadRequest, "auto-clear is set per buffer (or for the account)")
			return
		}
	}
	err := s.b.UpdatePlayback(n.ID, in.Buffers, func(bp *store.BufferPlayback) {
		if len(in.Lines) > 0 {
			bp.Lines = lines
		}
		if len(in.AutoClear) > 0 {
			bp.AutoClear = autoClear
		}
	})
	if err != nil {
		s.fail(w, err)
		return
	}
	s.writePlayback(w, r, n)
}

// handleClearPlayback clears buffers ({buffers}; none: every buffer), so
// playback starts over from now. History keeps the lines.
func (s *Server) handleClearPlayback(w http.ResponseWriter, r *http.Request) {
	n, ok := s.ownedNetwork(w, r)
	if !ok {
		return
	}
	var in struct {
		Buffers []string `json:"buffers"`
	}
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&in)
	bufs := in.Buffers
	if len(bufs) == 0 {
		bufs = s.b.BufferNames(n)
	}
	if len(bufs) > 0 {
		if err := s.b.ClearBuffers(n.ID, bufs); err != nil {
			s.fail(w, err)
			return
		}
	}
	s.writePlayback(w, r, n)
}
