package managed

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/4evrd/evrd/internal/store"
)

func TestLiveSettingsOverTheConfig(t *testing.T) {
	_, st := testAccounts(t)
	ctx := context.Background()
	a, b := New(testConfig(nil)), New(testConfig(nil)) // two nodes
	if l := a.Live(); l.Guests || l.NickGuardGraceSeconds != 60 || l.GuestLingerSeconds != 30 || len(l.Set) != 0 {
		t.Fatalf("defaults %+v", l)
	}
	l, err := a.SetLive(ctx, st, LiveInput{Guests: ptr(true), GuestsMax: ptr(5)})
	if err != nil || !l.Guests || l.GuestsMax != 5 || l.GuestsPerIP != 3 {
		t.Fatalf("set %+v %v", l, err)
	}
	l, _ = a.SetLive(ctx, st, LiveInput{GuestsPerIP: ptr(1), GuestLingerSeconds: ptr(0), NickGuardGraceSeconds: ptr(0)})
	if !l.Guests || l.GuestsMax != 5 || l.GuestsPerIP != 1 || l.GuestLingerSeconds != 0 || a.NickGuardGrace() != 0 || len(l.Set) != 5 {
		t.Fatalf("merged %+v", l)
	}
	if _, err := a.SetLive(ctx, st, LiveInput{GuestsMax: ptr(-1)}); !errors.Is(err, ErrBadInput) {
		t.Errorf("negative: %v", err)
	}
	// the other node follows
	if changed, err := b.LoadLive(ctx, st); !changed || err != nil || !b.Config().Guests || b.Config().GuestsPerIP != 1 {
		t.Fatalf("other node: %v %v %+v", changed, err, b.Live())
	}
	if changed, _ := b.LoadLive(ctx, st); changed {
		t.Error("changed twice")
	}
	if _, err := a.ResetLive(ctx, st); err != nil || a.Config().Guests {
		t.Fatal(err)
	}
	rctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		b.FollowLive(rctx, st, 5*time.Millisecond, slog.New(slog.NewTextHandler(io.Discard, nil)))
		close(done)
	}()
	deadline := time.Now().Add(2 * time.Second)
	for b.Config().Guests && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	<-done
	if b.Config().Guests {
		t.Error("the reset didn't reach the other node")
	}
	// a row that doesn't read
	st.SaveSettings(ctx, map[string]string{liveKey: "{"}, nil, "test")
	if _, err := b.LoadLive(ctx, st); err == nil {
		t.Error("a bad row loaded")
	}
}

// failingStore can't save.
type failingStore struct{}

func (failingStore) Settings(context.Context) ([]store.Setting, error) {
	return nil, errors.New("down")
}
func (failingStore) SaveSettings(context.Context, map[string]string, []string, string) error {
	return errors.New("down")
}

func TestLiveSettingsStoreDown(t *testing.T) {
	m := New(testConfig(nil))
	ctx := context.Background()
	if _, err := m.SetLive(ctx, failingStore{}, LiveInput{Guests: ptr(true)}); err == nil || m.Config().Guests {
		t.Error("set without saving")
	}
	if _, err := m.ResetLive(ctx, failingStore{}); err == nil {
		t.Error("reset")
	}
	if _, err := m.LoadLive(ctx, failingStore{}); err == nil {
		t.Error("load")
	}
}

func TestProvisionerLive(t *testing.T) {
	p, _, _ := testProvisioner(t)
	_, st := testAccounts(t)
	p.Store = st
	ctx := context.Background()
	if l, err := p.SetLive(ctx, LiveInput{Guests: ptr(true)}); err != nil || !l.Guests || !p.Live().Guests {
		t.Fatal(l, err)
	}
	if l, err := p.ResetLive(ctx); err != nil || l.Guests {
		t.Fatal(l, err)
	}
}
