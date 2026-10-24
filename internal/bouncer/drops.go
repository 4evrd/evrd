package bouncer

// IRC client disconnects (issue #64): each attached IRC client that goes is
// recorded with its device and why, so the IRC clients page can say how
// often a phone drops and the admin overview how clients fare overall.

import (
	"context"
	"slices"
	"sort"
	"time"

	"github.com/4evrd/evrd/internal/store"
	"github.com/4evrd/evrd/internal/telemetry"
)

// Why an IRC client went (store.ClientDisconnect.Reason).
const (
	DropQuit         = "quit"          // the client said QUIT
	DropClosed       = "closed"        // the client closed the connection without QUIT
	DropReset        = "reset"         // the connection was reset or broke
	DropPingTimeout  = "ping_timeout"  // the client stopped answering
	DropWriteStalled = "write_stalled" // the client stopped reading what evrd sent
	DropClientLimit  = "client_limit"  // refused: the account's client limit
	DropEvrdStopping = "evrd_stopping" // evrd stopped, with no holder to keep it
	DropReplaced     = "replaced"      // the same device connected again
	DropRemoved      = "removed"       // evrd closed it: account suspended, deleted or stopped
)

// DropReasons are the reasons, in the order the website lists them.
var DropReasons = []string{DropPingTimeout, DropReset, DropWriteStalled, DropClosed, DropQuit, DropReplaced, DropClientLimit, DropEvrdStopping, DropRemoved}

// dropsKept is how long disconnects are kept.
const dropsKept = 30 * 24 * time.Hour

// gapWithin is how long after a disconnect a device's next connection still
// counts as it coming back.
const gapWithin = 24 * time.Hour

// DeviceKey tells one device's disconnects apart: its client password, or
// its software on a network when it used none (the closest thing to a
// device evrd can tell).
func DeviceKey(info ClientInfo, network string) string {
	if info.Device != "" {
		return "password:" + info.Device
	}
	return "software:" + info.Software + "/" + network
}

// RecordDisconnect records an IRC client of userID's going: connected at
// info.Since, gone at ended, for reason.
func (b *Bouncer) RecordDisconnect(userID int64, info ClientInfo, network, reason string, ended time.Time) {
	telemetry.ClientDisconnects.Add(context.Background(), 1, telemetry.Attrs("reason", reason))
	since := info.Since
	if since.IsZero() {
		since = ended
	}
	d := store.ClientDisconnect{UserID: userID, DeviceKey: DeviceKey(info, network), Device: info.Device, Software: info.Software,
		Network: network, IP: info.IP, Reason: reason, ConnectedAt: since, EndedAt: ended}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	gap, err := b.store.AddClientDisconnect(ctx, d, since.Add(-gapWithin))
	if err != nil {
		b.log.Warn("record client disconnect", "user", userID, "err", err)
		return
	}
	if gap > 0 {
		telemetry.ReconnectGap.Record(context.Background(), gap.Seconds())
	}
}

// DeviceDrops is one device's disconnects over a period.
type DeviceDrops struct {
	Device   string `json:"device"` // the client password's name, if any
	Software string `json:"software"`
	Network  string `json:"network"`
	Drops    int    `json:"drops"`
	// Reasons counts the disconnects by reason; MainReason is the commonest.
	Reasons    map[string]int `json:"reasons"`
	MainReason string         `json:"mainReason"`
	// MedianGap is how long the device was typically gone before it came
	// back, in seconds (0: it never came back in the period).
	MedianGap float64   `json:"medianGap"`
	Last      time.Time `json:"last"`
	Connected bool      `json:"connected"` // attached right now
}

// ClientDrops is userID's IRC clients' disconnects since since, by device,
// most drops first.
func (b *Bouncer) ClientDrops(ctx context.Context, userID int64, since time.Time) ([]DeviceDrops, error) {
	ds, err := b.store.ClientDisconnects(ctx, userID, since)
	if err != nil {
		return nil, err
	}
	// a device attached right now came back after its last disconnect
	back := map[string]time.Time{}
	for _, c := range b.ClientsOf(userID) {
		if c.Kind != "irc" {
			continue
		}
		for _, n := range append(c.Networks, "") {
			k := DeviceKey(c, n)
			if at, ok := back[k]; !ok || c.Since.Before(at) {
				back[k] = c.Since
			}
		}
	}
	byKey := map[string]*DeviceDrops{}
	gaps := map[string][]float64{}
	var order []string
	for _, d := range ds { // newest first
		dd := byKey[d.DeviceKey]
		if dd == nil {
			dd = &DeviceDrops{Device: d.Device, Software: d.Software, Network: d.Network, Reasons: map[string]int{}, Last: d.EndedAt}
			_, dd.Connected = back[d.DeviceKey]
			byKey[d.DeviceKey] = dd
			order = append(order, d.DeviceKey)
		}
		dd.Drops++
		dd.Reasons[d.Reason]++
		switch {
		case d.ReconnectedAt != nil:
			gaps[d.DeviceKey] = append(gaps[d.DeviceKey], d.ReconnectedAt.Sub(d.EndedAt).Seconds())
		case d.EndedAt.Equal(dd.Last) && back[d.DeviceKey].After(d.EndedAt):
			// its latest one, and it's attached again now
			gaps[d.DeviceKey] = append(gaps[d.DeviceKey], back[d.DeviceKey].Sub(d.EndedAt).Seconds())
		}
	}
	out := make([]DeviceDrops, 0, len(order))
	for _, k := range order {
		dd := byKey[k]
		dd.MainReason = mainReason(dd.Reasons)
		dd.MedianGap = median(gaps[k])
		out = append(out, *dd)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Drops > out[j].Drops })
	return out, nil
}

// DropSummary is every IRC client disconnect since since: how many, by
// reason, and on how many accounts.
type DropSummary struct {
	Drops    int            `json:"drops"`
	Reasons  map[string]int `json:"reasons"`
	Accounts int            `json:"accounts"`
}

// ClientDropSummary sums up every user's IRC client disconnects since since.
func (b *Bouncer) ClientDropSummary(ctx context.Context, since time.Time) (DropSummary, error) {
	ds, err := b.store.ClientDisconnects(ctx, 0, since)
	if err != nil {
		return DropSummary{}, err
	}
	s := DropSummary{Reasons: map[string]int{}}
	users := map[int64]bool{}
	for _, d := range ds {
		s.Drops++
		s.Reasons[d.Reason]++
		users[d.UserID] = true
	}
	s.Accounts = len(users)
	return s, nil
}

// mainReason is the commonest reason, ties going by DropReasons' order.
func mainReason(rs map[string]int) string {
	best, n := "", 0
	for _, r := range DropReasons {
		if rs[r] > n {
			best, n = r, rs[r]
		}
	}
	return best
}

func median(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	xs = slices.Clone(xs)
	slices.Sort(xs)
	if len(xs)%2 == 1 {
		return xs[len(xs)/2]
	}
	return (xs[len(xs)/2-1] + xs[len(xs)/2]) / 2
}

// LastDisconnect is the latest disconnect of a device (DeviceKey) of
// userID's since since (ok false: none).
func (b *Bouncer) LastDisconnect(ctx context.Context, userID int64, deviceKey string, since time.Time) (store.ClientDisconnect, bool) {
	ds, err := b.store.ClientDisconnects(ctx, userID, since)
	if err != nil {
		return store.ClientDisconnect{}, false
	}
	for _, d := range ds { // newest first
		if d.DeviceKey == deviceKey {
			return d, true
		}
	}
	return store.ClientDisconnect{}, false
}
