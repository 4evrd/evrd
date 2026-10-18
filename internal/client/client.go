// Package client is evrd's downstream IRC listener: it accepts native IRC
// clients (Hexchat, irssi, WeeChat, …), authenticates them against bouncer
// accounts, attaches them to one of the user's networks, replays backlog, and
// bridges live traffic to/from the upstream connection.
//
// Auth uses the server password (PASS): "user:secret", or
// "user/network:secret" to pick a specific network, where secret is the
// account password or an IRC client password. Each user may attach up to
// their client limit at once.
package client

import (
	"context"
	"crypto/tls"
	"log/slog"
	"net"
	"strings"
	"sync"

	"code.ircforever.org/ircforever/evrd/internal/accounts"
	"code.ircforever.org/ircforever/evrd/internal/bouncer"
)

const serverName = "evrd"

type Server struct {
	b       *bouncer.Bouncer
	cl      Cluster // nil: a single node that serves every user
	secret  string
	acc     *accounts.Service
	log     *slog.Logger
	tlsCert string
	tlsKey  string

	tlsOnce sync.Once
	tlsConf *tls.Config
	tlsErr  error
}

func NewServer(b *bouncer.Bouncer, acc *accounts.Service, log *slog.Logger, tlsCert, tlsKey string) *Server {
	return &Server{b: b, acc: acc, log: log, tlsCert: tlsCert, tlsKey: tlsKey}
}

// ListenAndServe binds each IRC listener URL and serves until ctx is cancelled.
// Non-IRC schemes (e.g. ws+insecure) are skipped.
func (s *Server) ListenAndServe(ctx context.Context, listeners []string) error {
	for _, l := range listeners {
		go s.listen(ctx, l)
	}
	<-ctx.Done()
	return nil
}

func (s *Server) listen(ctx context.Context, url string) {
	scheme, addr := parseListen(url)

	var ln net.Listener
	var err error
	switch scheme {
	case "irc", "irc+insecure", "tcp":
		ln, err = net.Listen("tcp", addr)
	case "ircs", "tls":
		var conf *tls.Config
		if conf, err = s.tlsConfig(); err == nil {
			ln, err = tls.Listen("tcp", addr, conf)
		}
	default:
		s.log.Warn("skipping unsupported IRC listener", "scheme", scheme, "addr", addr)
		return
	}
	if err != nil {
		s.log.Error("irc listen", "addr", addr, "scheme", scheme, "err", err)
		return
	}
	s.log.Info("irc listener", "addr", addr, "scheme", scheme)
	go func() {
		<-ctx.Done()
		ln.Close()
	}()
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		go newSession(s, conn).serve(ctx)
	}
}

func parseListen(u string) (scheme, addr string) {
	if i := strings.Index(u, "://"); i >= 0 {
		return u[:i], u[i+3:]
	}
	return "tcp", u
}
