package middleware

// Running in a cluster: each user is owned by one node (internal/cluster),
// and only that node has their live state — upstream connections, attached
// clients, web chat sockets. A request about a user that reaches another node
// is reverse-proxied to the owner's internal endpoint, signed with the
// cluster secret and carrying the caller's identity; the owner handles it
// exactly as if the browser had come straight to it (the chat WebSocket
// included).

import (
	"context"
	"errors"
	"log/slog"
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

// Moving is the answer while a user is between nodes.
const Moving = "your bouncer is moving to another server — try again in a moment"

// Cluster is what Owner needs from internal/cluster.
type Cluster interface {
	Self() store.Node
	Owns(userID int64) bool
	WaitOwner(ctx context.Context, userID int64, max time.Duration) (store.Node, error)
}

// Owner serves requests on the node that owns the user they're about. Set
// its fields before serving.
type Owner struct {
	Cluster  Cluster                    // nil: a single node that serves every user
	Secret   string                     // the cluster secret
	ClientIP func(*http.Request) string // the browser's address, as this node sees it
	Log      *slog.Logger
}

// At serves h on the node that owns the user who returns, forwarding the
// request there when that's another node.
func (o *Owner) At(user func(*http.Request) int64, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		uid := user(r)
		if o.Cluster == nil || o.Cluster.Owns(uid) {
			h(w, r)
			return
		}
		if auth.IsForwarded(r) {
			// Sent here as the owner, but we've let the user go since.
			writeErr(w, http.StatusMisdirectedRequest, Moving)
			return
		}
		node, err := o.Cluster.WaitOwner(r.Context(), uid, 3*time.Second)
		switch {
		case errors.Is(err, cluster.ErrNoOwner) || (err == nil && node.ID == o.Cluster.Self().ID):
			// Nobody runs this user (suspended, or just deleted): there's no
			// live state, so the database is the whole truth — serve it here.
			h(w, r)
		case err != nil:
			o.Log.Error("api", "err", err)
			writeErr(w, http.StatusInternalServerError, "something went wrong")
		default:
			o.forward(w, r, node)
		}
	}
}

// forward reverse-proxies a signed-in request to another node.
func (o *Owner) forward(w http.ResponseWriter, r *http.Request, node store.Node) {
	target, err := url.Parse(node.HTTPURL)
	if err != nil || node.HTTPURL == "" {
		writeErr(w, http.StatusServiceUnavailable, Moving)
		return
	}
	caller, sess := auth.User(r).ID, auth.SessionOf(r).ID
	p := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			pr.Out.Host = pr.In.Host // the owner checks same-origin against it
			pr.SetXForwarded()
			pr.Out.Header.Del("Cookie")
			pr.Out.Header.Set(auth.HeaderSecret, o.Secret)
			pr.Out.Header.Set(auth.HeaderCaller, strconv.FormatInt(caller, 10))
			pr.Out.Header.Set(auth.HeaderSession, sess)
			// the browser's address as this node sees it (the owner can't
			// tell from X-Forwarded-For whom to believe)
			pr.Out.Header.Set(auth.HeaderClient, o.ClientIP(pr.In))
			// the owner's spans continue this request's trace
			otel.GetTextMapPropagator().Inject(pr.In.Context(), propagation.HeaderCarrier(pr.Out.Header))
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			o.Log.Warn("forward to owner", "node", node.ID, "err", err)
			writeErr(w, http.StatusServiceUnavailable, Moving)
		},
	}
	p.ServeHTTP(w, r)
}
