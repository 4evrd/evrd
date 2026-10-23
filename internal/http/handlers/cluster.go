package handlers

// Running in a cluster: requests about a user are served on the node that
// owns them (see middleware.Owner). Admin pages about every user gather the
// live numbers from each node.

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/4evrd/evrd/internal/store"
)

// Cluster is what the API needs from internal/cluster.
type Cluster interface {
	Self() store.Node
	Owns(userID int64) bool
	WaitOwner(ctx context.Context, userID int64, max time.Duration) (store.Node, error)
	Nodes() []store.Node
}

// SetCluster is the cluster this node is in, and its secret.
func (s *Handlers) SetCluster(cl Cluster, secret string) { s.cl, s.secret = cl, secret }

// decodeJSON reads a JSON response body.
func decodeJSON(res *http.Response, v any) error { return json.NewDecoder(res.Body).Decode(v) }
