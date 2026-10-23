package http

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/4evrd/evrd/internal/accounts"
	"github.com/4evrd/evrd/internal/auth"
	"github.com/4evrd/evrd/internal/bouncer"
	"github.com/4evrd/evrd/internal/cluster"
	"github.com/4evrd/evrd/internal/config"
	"github.com/4evrd/evrd/internal/history"
	"github.com/4evrd/evrd/internal/languages"
	"github.com/4evrd/evrd/internal/store"
)

// newServer is the server on a fresh store, with no users. The handlers'
// own tests (internal/http/handlers) go through every route.
func newServer(t *testing.T) *Server {
	t.Helper()
	st, err := store.Open("sqlite:" + filepath.Join(t.TempDir(), "evrd.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	hist, err := history.Open(filepath.Join(t.TempDir(), "history"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { hist.Close() })
	cfg := config.Default()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	acc := accounts.New(st, cfg.Limits)
	am, err := auth.New(context.Background(), st, acc, cfg.Auth, "", log)
	if err != nil {
		t.Fatal(err)
	}
	svc := Services{Accounts: acc, Auth: am, Languages: languages.New(st), Cluster: cluster.NewStateService(st)}
	return New(bouncer.New(st, hist, log), svc, cfg, nil, log)
}

const clusterSecret = "cluster-secret"

// soloCluster is a cluster of one node, which owns every user.
type soloCluster struct{}

func (soloCluster) Self() store.Node    { return store.Node{ID: "me"} }
func (soloCluster) Owns(int64) bool     { return true }
func (soloCluster) Nodes() []store.Node { return []store.Node{{ID: "me"}} }
func (soloCluster) WaitOwner(context.Context, int64, time.Duration) (store.Node, error) {
	return store.Node{ID: "me"}, nil
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for end := time.Now().Add(5 * time.Second); time.Now().Before(end); time.Sleep(10 * time.Millisecond) {
		if cond() {
			return
		}
	}
	t.Fatalf("timed out waiting for %s", what)
}
