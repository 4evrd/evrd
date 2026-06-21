// Package server accepts client connections (IRC and WebSocket) and hands them
// to the bouncer core.
package server

import (
	"context"
	"log/slog"
)

// Server owns the configured listeners.
type Server struct {
	listen []string
	log    *slog.Logger
}

// New returns a Server bound to the given listener URLs.
func New(listen []string, log *slog.Logger) *Server {
	return &Server{listen: listen, log: log}
}

// Start begins listening and blocks until ctx is cancelled.
//
// Skeleton: this logs the listeners it would bind. Binding each scheme
// (ircs / irc+insecure / wss / ws / unix) and the client session loop land
// incrementally on top of this.
func (s *Server) Start(ctx context.Context) error {
	for _, l := range s.listen {
		s.log.Info("listener configured (not yet bound)", "addr", l)
	}
	<-ctx.Done()
	return nil
}
