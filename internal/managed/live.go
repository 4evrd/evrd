package managed

// Live settings: the few managed settings the managing service changes
// while evrd runs, through the managed API (GET/PUT/DELETE
// /api/managed/settings): whether guests may sign in and their limits,
// and the nick guard's grace. A change is saved in the database (shared
// by every node, kept across restarts) over what the config says, and
// applies at once: guests from their next sign-in, the grace from each
// connection's next connect.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/4evrd/evrd/internal/config"
	"github.com/4evrd/evrd/internal/store"
)

// liveKey is the settings row the overrides live in (not an admin
// setting: the settings manager skips keys it doesn't know).
const liveKey = "managed_live"

// LiveInput is a PUT's body: the settings to change. Fields left out stay
// as they are.
type LiveInput struct {
	Guests                *bool `json:"guests"`
	GuestsMax             *int  `json:"guestsMax"`
	GuestsPerIP           *int  `json:"guestsPerIP"`
	GuestLingerSeconds    *int  `json:"guestLingerSeconds"`
	NickGuardGraceSeconds *int  `json:"nickGuardGraceSeconds"`
}

// Live is the settings in effect, as the managed API shows them.
type Live struct {
	Guests                bool `json:"guests"`
	GuestsMax             int  `json:"guestsMax"`
	GuestsPerIP           int  `json:"guestsPerIP"`
	GuestLingerSeconds    int  `json:"guestLingerSeconds"`
	NickGuardGraceSeconds int  `json:"nickGuardGraceSeconds"`
	// Set are the fields set through the API (the rest follow the config).
	Set []string `json:"set"`
}

// LiveStore is where the overrides are kept (store.SettingRepositoryImpl).
type LiveStore interface {
	Settings(ctx context.Context) ([]store.Setting, error)
	SaveSettings(ctx context.Context, set map[string]string, del []string, by string) error
}

// live is the overrides in effect (nil fields: the config's).
type live struct {
	mu  sync.Mutex
	in  LiveInput
	raw string
}

// apply puts in over c.
func (in LiveInput) apply(c *config.Managed) {
	if in.Guests != nil {
		c.Guests = *in.Guests
	}
	if in.GuestsMax != nil {
		c.GuestsMax = *in.GuestsMax
	}
	if in.GuestsPerIP != nil {
		c.GuestsPerIP = *in.GuestsPerIP
	}
	if in.GuestLingerSeconds != nil {
		c.GuestLinger = time.Duration(*in.GuestLingerSeconds) * time.Second
	}
	if in.NickGuardGraceSeconds != nil {
		c.NickGuardGrace = time.Duration(*in.NickGuardGraceSeconds) * time.Second
	}
}

// merge is in with what next sets over it.
func (in LiveInput) merge(next LiveInput) LiveInput {
	if next.Guests != nil {
		in.Guests = next.Guests
	}
	if next.GuestsMax != nil {
		in.GuestsMax = next.GuestsMax
	}
	if next.GuestsPerIP != nil {
		in.GuestsPerIP = next.GuestsPerIP
	}
	if next.GuestLingerSeconds != nil {
		in.GuestLingerSeconds = next.GuestLingerSeconds
	}
	if next.NickGuardGraceSeconds != nil {
		in.NickGuardGraceSeconds = next.NickGuardGraceSeconds
	}
	return in
}

// check refuses negative numbers and silly sizes.
func (in LiveInput) check() error {
	for name, v := range map[string]*int{"guestsMax": in.GuestsMax, "guestsPerIP": in.GuestsPerIP,
		"guestLingerSeconds": in.GuestLingerSeconds, "nickGuardGraceSeconds": in.NickGuardGraceSeconds} {
		if v != nil && (*v < 0 || *v > 1_000_000) {
			return fmt.Errorf("%w: %s must be 0 or more", ErrBadInput, name)
		}
	}
	return nil
}

// set lists the fields in sets.
func (in LiveInput) set() []string {
	out := []string{}
	for _, f := range []struct {
		name string
		on   bool
	}{{"guests", in.Guests != nil}, {"guestsMax", in.GuestsMax != nil}, {"guestsPerIP", in.GuestsPerIP != nil},
		{"guestLingerSeconds", in.GuestLingerSeconds != nil}, {"nickGuardGraceSeconds", in.NickGuardGraceSeconds != nil}} {
		if f.on {
			out = append(out, f.name)
		}
	}
	return out
}

// Live is the live settings in effect.
func (m *Mode) Live() Live {
	c := m.Config()
	m.live.mu.Lock()
	set := m.live.in.set()
	m.live.mu.Unlock()
	return Live{Guests: c.Guests, GuestsMax: c.GuestsMax, GuestsPerIP: c.GuestsPerIP,
		GuestLingerSeconds: int(c.GuestLinger / time.Second), NickGuardGraceSeconds: int(c.NickGuardGrace / time.Second), Set: set}
}

// NickGuardGrace is the nick guard's grace in effect.
func (m *Mode) NickGuardGrace() time.Duration { return m.Config().NickGuardGrace }

// SetLive changes the live settings (saved in st, so other nodes and the
// next start have them).
func (m *Mode) SetLive(ctx context.Context, st LiveStore, in LiveInput) (Live, error) {
	if err := in.check(); err != nil {
		return Live{}, err
	}
	m.live.mu.Lock()
	next := m.live.in.merge(in)
	m.live.mu.Unlock()
	b, err := json.Marshal(next)
	if err != nil {
		return Live{}, err
	}
	if err := st.SaveSettings(ctx, map[string]string{liveKey: string(b)}, nil, "managed-api"); err != nil {
		return Live{}, err
	}
	m.setLive(next, string(b))
	return m.Live(), nil
}

// ResetLive drops every override: the config's settings again.
func (m *Mode) ResetLive(ctx context.Context, st LiveStore) (Live, error) {
	if err := st.SaveSettings(ctx, nil, []string{liveKey}, "managed-api"); err != nil {
		return Live{}, err
	}
	m.setLive(LiveInput{}, "")
	return m.Live(), nil
}

func (m *Mode) setLive(in LiveInput, raw string) {
	m.live.mu.Lock()
	m.live.in, m.live.raw = in, raw
	m.live.mu.Unlock()
}

// LoadLive reads the overrides saved in st (another node's, or before a
// restart). It reports whether they changed.
func (m *Mode) LoadLive(ctx context.Context, st LiveStore) (bool, error) {
	rows, err := st.Settings(ctx)
	if err != nil {
		return false, err
	}
	raw := ""
	for _, r := range rows {
		if r.Key == liveKey {
			raw = r.Value
		}
	}
	m.live.mu.Lock()
	same := raw == m.live.raw
	m.live.mu.Unlock()
	if same {
		return false, nil
	}
	var in LiveInput
	if raw != "" {
		if err := json.Unmarshal([]byte(raw), &in); err != nil {
			return false, errors.Join(errors.New("managed: the saved live settings don't read"), err)
		}
	}
	m.setLive(in, raw)
	return true, nil
}

// FollowLive keeps the overrides in step with st (changes another node
// saved) every interval, until ctx ends.
func (m *Mode) FollowLive(ctx context.Context, st LiveStore, every time.Duration, log *slog.Logger) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if changed, err := m.LoadLive(ctx, st); err != nil && ctx.Err() == nil {
				log.Warn("managed: live settings", "err", err)
			} else if changed {
				log.Info("managed: live settings changed", "settings", m.Live())
			}
		}
	}
}
