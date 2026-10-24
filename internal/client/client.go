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
	"github.com/4evrd/evrd/internal/auth"
	"github.com/4evrd/evrd/internal/store"
	"log/slog"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/4evrd/evrd/internal/accounts"
	"github.com/4evrd/evrd/internal/bouncer"
	"github.com/4evrd/evrd/internal/directory"
	"github.com/4evrd/evrd/internal/links"
	"github.com/4evrd/evrd/internal/listen"
	"github.com/4evrd/evrd/internal/messages"
)

const serverName = "evrd"

type Server struct {
	b       *bouncer.Bouncer
	cl      Cluster // nil: a single node that serves every user
	secret  string
	acc     accounts.AccountServiceImpl
	log     *slog.Logger
	tlsCert string
	tlsKey  string

	tlsOnce sync.Once
	tlsConf *tls.Config
	tlsErr  error

	dir *directory.Directory // nil: none

	publicURL string // the web app's address; "" when unknown

	// followEvery is how often a session checks its network for a new
	// connection and changed capabilities (3s; tests shorten it).
	followEvery time.Duration
	// lsWait is how long a CAP LS answer waits for the network (lsWait;
	// tests shorten it).
	lsWait time.Duration
	// pingTimeout is client_ping_timeout + 1 (0: the default); pingQuiet
	// and writeStall shorten liveness.go's waits in tests.
	pingTimeout atomic.Int64
	pingQuiet   time.Duration
	writeStall  time.Duration

	sts stsState

	msgs messages.MessageServiceImpl // *status's texts; nil: English

	signIns SignIns   // nil: no SSO passwords, no lockout (tests)
	mfa     mfaState  // second factors for IRC sign-ins (mfa.go)
	link    linkState // sign-in by link (link.go)

	// the connection holder, holding clients (hold.go); nil: none
	holder      *links.Client
	readyOnce   sync.Once
	readyClose  sync.Once
	readyC      chan struct{}
	heldMu      sync.Mutex
	held        map[string]bool // holder clients this evrd serves
	certMu      sync.Mutex
	lastCert    []byte // the certificate the holder has (its leaf)
	certsPushed bool
}

// SignIns is what IRC sign-in shares with the web's (auth.Manager): the
// failed sign-in lockout and the SSO password check.
type SignIns interface {
	Failures() *auth.FailureLimiter
	SSOPasswordsOn() bool
	SSOPasswordMethod() string
	CheckSSOPassword(ctx context.Context, u store.User, password string) error
}

// SetSignIns shares the web sign-in's lockout and SSO password check.
func (s *Server) SetSignIns(si SignIns) { s.signIns = si }

// SetMessages has *status answer in each user's language.
func (s *Server) SetMessages(m messages.MessageServiceImpl) { s.msgs = m }

// SetDirectory gives *status the network directory (addnetwork <id>).
func (s *Server) SetDirectory(d *directory.Directory) { s.dir = d }

// directory is the network directory, an empty one when there's none.
func (s *Server) directory() *directory.Directory {
	if s.dir == nil {
		return directory.New("", nil, nil)
	}
	return s.dir
}

// SetPublicURL is where the web app is served (config public_url), named in
// sign-in hints.
func (s *Server) SetPublicURL(u string) { s.publicURL = strings.TrimRight(u, "/") }

// clientsPage is where users make IRC client passwords.
func (s *Server) clientsPage() string {
	if s.publicURL == "" {
		return "the web app's IRC clients page"
	}
	return s.publicURL + "/clients"
}

func NewServer(b *bouncer.Bouncer, acc accounts.AccountServiceImpl, log *slog.Logger, tlsCert, tlsKey string) *Server {
	return &Server{b: b, acc: acc, log: log, tlsCert: tlsCert, tlsKey: tlsKey}
}

// ListenAndServe binds each IRC listener URL and serves until ctx is cancelled.
// Non-IRC schemes (e.g. ws+insecure) are skipped.
func (s *Server) ListenAndServe(ctx context.Context, listeners []string) error {
	if s.holder != nil && s.serveHeld(ctx, listeners) {
		return nil
	}
	s.ready() // no clients to wait for
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
		ln, err = listen.TCP(addr)
	case "ircs", "tls":
		var conf *tls.Config
		if conf, err = s.tlsConfig(); err == nil {
			if ln, err = listen.TCP(addr); err == nil {
				s.tlsPort(ln.Addr().String())
				ln = tls.NewListener(ln, conf)
			}
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
		tuneClientConn(conn)
		go newSession(s, conn).serve(ctx)
	}
}

func parseListen(u string) (scheme, addr string) {
	if i := strings.Index(u, "://"); i >= 0 {
		return u[:i], u[i+3:]
	}
	return "tcp", u
}
