package api

// Running in a cluster: each user is owned by one node (internal/cluster),
// and only that node has their live state — upstream connections, attached
// clients, web chat sockets. A request about a user that reaches another node
// is reverse-proxied to the owner's internal endpoint, signed with the
// cluster secret and carrying the caller's identity; the owner handles it
// exactly as if the browser had come straight to it (the chat WebSocket
// included). Admin pages about every user gather the live numbers from each
// node.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"

	"github.com/4evrd/evrd/internal/auth"
	"github.com/4evrd/evrd/internal/cluster"
	"github.com/4evrd/evrd/internal/store"
)

// Cluster is what the API needs from internal/cluster.
type Cluster interface {
	Self() store.Node
	Owns(userID int64) bool
	WaitOwner(ctx context.Context, userID int64, max time.Duration) (store.Node, error)
	Nodes() []store.Node
}

// SetCluster makes the server forward requests to the owning node.
func (s *Server) SetCluster(cl Cluster, secret string) { s.cl, s.secret = cl, secret }

// InternalHandler serves requests forwarded from other nodes. It must only be
// reachable between nodes.
func (s *Server) InternalHandler() http.Handler {
	mux := http.NewServeMux()
	s.routes(mux)
	mux.HandleFunc("GET /internal/usage", s.handleInternalUsage)
	return s.auth.Forwarded(s.secret, traced(true, mux))
}

// ListenInternal runs the internal endpoint until ctx is cancelled.
func (s *Server) ListenInternal(ctx context.Context, addr string) error {
	return s.serve(ctx, "internal api", addr, s.InternalHandler(), nil)
}

// moving is the answer while a user is between nodes.
const moving = "your bouncer is moving to another server — try again in a moment"

// atOwner serves h on the node that owns the user who returns, forwarding
// the request there when that's another node.
func (s *Server) atOwner(user func(*http.Request) int64, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		uid := user(r)
		if s.cl == nil || s.cl.Owns(uid) {
			h(w, r)
			return
		}
		if auth.IsForwarded(r) {
			// Sent here as the owner, but we've let the user go since.
			writeErr(w, http.StatusMisdirectedRequest, moving)
			return
		}
		node, err := s.cl.WaitOwner(r.Context(), uid, 3*time.Second)
		switch {
		case errors.Is(err, cluster.ErrNoOwner) || (err == nil && node.ID == s.cl.Self().ID):
			// Nobody runs this user (suspended, or just deleted): there's no
			// live state, so the database is the whole truth — serve it here.
			h(w, r)
		case err != nil:
			s.fail(w, err)
		default:
			s.forward(w, r, node)
		}
	}
}

// forward reverse-proxies a signed-in request to another node.
func (s *Server) forward(w http.ResponseWriter, r *http.Request, node store.Node) {
	target, err := url.Parse(node.HTTPURL)
	if err != nil || node.HTTPURL == "" {
		writeErr(w, http.StatusServiceUnavailable, moving)
		return
	}
	caller, sess := auth.User(r).ID, auth.SessionOf(r).ID
	p := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			pr.Out.Host = pr.In.Host // the owner checks same-origin against it
			pr.SetXForwarded()
			pr.Out.Header.Del("Cookie")
			pr.Out.Header.Set(auth.HeaderSecret, s.secret)
			pr.Out.Header.Set(auth.HeaderCaller, strconv.FormatInt(caller, 10))
			pr.Out.Header.Set(auth.HeaderSession, sess)
			// the browser's address as this node sees it (the owner can't
			// tell from X-Forwarded-For whom to believe)
			pr.Out.Header.Set(auth.HeaderClient, s.auth.ClientIP(pr.In))
			// the owner's spans continue this request's trace
			otel.GetTextMapPropagator().Inject(pr.In.Context(), propagation.HeaderCarrier(pr.Out.Header))
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			s.log.Warn("forward to owner", "node", node.ID, "err", err)
			writeErr(w, http.StatusServiceUnavailable, moving)
		},
	}
	p.ServeHTTP(w, r)
}

// decodeJSON reads a JSON response body.
func decodeJSON(res *http.Response, v any) error { return json.NewDecoder(res.Body).Decode(v) }
