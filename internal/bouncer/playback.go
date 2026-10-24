package bouncer

// Playback buffers, per network and per channel or query, like ZNC's: how
// many lines each plays back when an IRC client attaches, whether it plays
// only what's new (auto-clear), and its clear mark. The lines stay in
// history either way; these only change what playback sends. *status and
// the web app both go through here.

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/4evrd/evrd/internal/store"
)

// Playback is a network's buffer settings, resolved against the account's.
type Playback struct {
	Buffers map[string]store.BufferPlayback // by lowercased name; "" is the network's own
	User    store.User
	// LastOn is when an IRC client was last on the network: now while one
	// is, else when one last left (zero: never).
	LastOn time.Time
	// Device is the device asking (its client password; "" for a client
	// without one), and DeviceAt how far it had been given the network's
	// lines when it was last on (nil: never, or no device).
	Device   string
	DeviceAt *time.Time
}

// DevicePlayback is Playback for a device: its auto-clearing buffers play
// from where it left off rather than from when any client was last on.
func (b *Bouncer) DevicePlayback(n store.Network, u store.User, except, device string) (Playback, error) {
	p, err := b.Playback(n, u, except)
	if err != nil || device == "" {
		return p, err
	}
	p.Device = device
	at, ok, err := b.store.DevicePosition(context.Background(), n.ID, device)
	if ok {
		p.DeviceAt = &at
	}
	return p, err
}

// SetDevicePosition records that a device has been given a network's lines
// up to at.
func (b *Bouncer) SetDevicePosition(networkID int64, device string, at time.Time) error {
	return b.store.SetDevicePosition(context.Background(), networkID, device, at)
}

// Playback loads a network's playback settings for u. except is an IRC
// client not to count as on the network (the one asking, as it attaches).
func (b *Bouncer) Playback(n store.Network, u store.User, except string) (Playback, error) {
	bps, err := b.store.BufferPlaybacks(context.Background(), n.ID)
	p := Playback{Buffers: bps, User: u}
	if at := bps[""].ClearedAt; at != nil {
		p.LastOn = *at
	}
	for _, c := range b.ClientsOf(n.UserID) {
		if c.Kind == "irc" && c.ID != except && slices.Contains(c.Networks, n.Name) {
			p.LastOn = time.Now()
		}
	}
	return p, err
}

// Own is a buffer's own settings ("" for the network's).
func (p Playback) Own(name string) store.BufferPlayback { return p.Buffers[strings.ToLower(name)] }

// NetworkLines is the network's line count: its own, else the account's.
func (p Playback) NetworkLines() int {
	if l := p.Buffers[""].Lines; l != nil {
		return *l
	}
	return p.User.PlaybackLines
}

// Lines is how many lines a buffer plays back: its own setting, else the
// network's, else the account's.
func (p Playback) Lines(name string) int {
	if l := p.Own(name).Lines; l != nil {
		return *l
	}
	return p.NetworkLines()
}

// AutoClears reports whether a buffer plays only what's new: its own
// setting, else the account's for channels or private messages.
func (p Playback) AutoClears(name string) bool {
	if a := p.Own(name).AutoClear; a != nil {
		return *a
	}
	return p.AutoClearDefault(name)
}

// AutoClearDefault is the account's auto-clear setting for a buffer's kind.
func (p Playback) AutoClearDefault(name string) bool {
	if isChannelName(name) {
		return p.User.AutoClearChanBuffer
	}
	return p.User.AutoClearQueryBuffer
}

// ClearedAt is a buffer's clear mark (zero: never cleared).
func (p Playback) ClearedAt(name string) time.Time {
	if at := p.Own(name).ClearedAt; at != nil {
		return *at
	}
	return time.Time{}
}

// From is what a buffer plays back from: after its clear mark and, when it
// auto-clears, after the device was last given the network's lines — or,
// for a client without a device, after an IRC client was last on the
// network. A device's first visit gets the normal backlog.
func (p Playback) From(name string) time.Time {
	since := p.ClearedAt(name)
	if !p.AutoClears(name) {
		return since
	}
	switch {
	case p.Device == "":
		if p.LastOn.After(since) {
			since = p.LastOn
		}
	case p.DeviceAt != nil && p.DeviceAt.After(since):
		since = *p.DeviceAt
	}
	return since
}

// Waiting is how many lines a buffer would play back to an IRC client
// attaching now.
func (b *Bouncer) Waiting(p Playback, networkID int64, name string) int {
	n := p.Lines(name)
	if n <= 0 {
		return 0
	}
	msgs, _ := b.history.Recent(networkID, name, n)
	since, count := p.From(name), 0
	for _, m := range msgs {
		if m.Time.After(since) {
			count++
		}
	}
	return count
}

// UpdatePlayback changes buffers' playback settings ("" is the network's).
func (b *Bouncer) UpdatePlayback(networkID int64, buffers []string, f func(*store.BufferPlayback)) error {
	return b.store.UpdateBufferPlayback(context.Background(), networkID, buffers, f)
}

// ClearBuffers starts buffers' playback over from now.
func (b *Bouncer) ClearBuffers(networkID int64, buffers []string) error {
	now := time.Now()
	return b.store.UpdateBufferPlayback(context.Background(), networkID, buffers, func(bp *store.BufferPlayback) { bp.ClearedAt = &now })
}

// MarkOnNetwork records that an IRC client is (or was, until now) on a
// network, so its auto-clearing buffers start over from here.
func (b *Bouncer) MarkOnNetwork(networkID int64) error {
	now := time.Now()
	return b.store.UpdateBufferPlayback(context.Background(), networkID, []string{""}, func(bp *store.BufferPlayback) { bp.ClearedAt = &now })
}

// BufferNames is a network's buffers: those with history, and channels
// (saved or joined) whether or not anything was said.
func (b *Bouncer) BufferNames(n store.Network) []string {
	names, _ := b.history.Buffers(n.ID)
	chans := n.Channels
	if c, ok := b.ConnFor(n.ID); ok {
		chans = c.Channels()
	}
	seen := map[string]bool{}
	var out []string
	for _, name := range append(chans, names...) {
		if name != "" && !seen[strings.ToLower(name)] {
			seen[strings.ToLower(name)] = true
			out = append(out, name)
		}
	}
	slices.SortFunc(out, func(a, b string) int {
		if ca, cb := isChannelName(a), isChannelName(b); ca != cb {
			if ca {
				return -1
			}
			return 1
		}
		return strings.Compare(strings.ToLower(a), strings.ToLower(b))
	})
	return out
}
