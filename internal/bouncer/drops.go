package bouncer

// IRC client disconnects (issue #64): each attached IRC client that goes is
// recorded with its device and why, so the IRC clients page can say how
// often a phone drops and the admin overview how clients fare overall.

import (
	"context"
	"math"
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
	out, _ := byDevice(ds, b.attachedSince(userID))
	return out, nil
}

// attachedSince is when each of userID's devices (DeviceKey) attached, for
// those attached right now: they came back after their last disconnect.
func (b *Bouncer) attachedSince(userID int64) map[string]time.Time {
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
	return back
}

// byDevice groups one user's disconnects (newest first) by device, most
// drops first, and says how long the device was gone after each one (by
// the disconnect's reason). back is attachedSince.
func byDevice(ds []store.ClientDisconnect, back map[string]time.Time) ([]DeviceDrops, []gap) {
	byKey := map[string]*DeviceDrops{}
	gaps := map[string][]float64{}
	var all []gap
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
		g := gap{reason: d.Reason, secs: -1}
		switch {
		case d.ReconnectedAt != nil:
			g.secs = d.ReconnectedAt.Sub(d.EndedAt).Seconds()
		case d.EndedAt.Equal(dd.Last) && back[d.DeviceKey].After(d.EndedAt):
			// its latest one, and it's attached again now
			g.secs = back[d.DeviceKey].Sub(d.EndedAt).Seconds()
		}
		if g.secs >= 0 {
			gaps[d.DeviceKey] = append(gaps[d.DeviceKey], g.secs)
		}
		all = append(all, g)
	}
	out := make([]DeviceDrops, 0, len(order))
	for _, k := range order {
		dd := byKey[k]
		dd.MainReason = mainReason(dd.Reasons)
		dd.MedianGap = median(gaps[k])
		out = append(out, *dd)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Drops > out[j].Drops })
	return out, all
}

// gap is how long a device was gone after a disconnect (secs -1: it didn't
// come back within the period).
type gap struct {
	reason string
	secs   float64
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

// GapStats is how long devices were gone after disconnects for one reason,
// in seconds, over the ones they came back from.
type GapStats struct {
	Back   int     `json:"back"`   // came back within the period
	Gone   int     `json:"gone"`   // didn't (yet)
	Median float64 `json:"median"` // 0 when none came back
	P90    float64 `json:"p90"`
}

// GapBuckets are the upper bounds (seconds) the admin view sorts gaps into;
// a further bucket holds the longer ones, and a last one those not back.
var GapBuckets = []float64{10, 60, 300, 3600}

// UserDeviceDrops is a device's drops, with whose it is.
type UserDeviceDrops struct {
	UserID int64 `json:"userId"`
	DeviceDrops
}

// AllDrops is every user's IRC client disconnects over a period, for the
// admin view: by reason, how long devices were gone, and the devices that
// drop most.
type AllDrops struct {
	Drops    int                 `json:"drops"`
	Accounts int                 `json:"accounts"`
	Devices  int                 `json:"devices"`
	Reasons  map[string]int      `json:"reasons"`
	Gaps     map[string]GapStats `json:"gaps"` // by reason
	// Buckets counts disconnects by how long the device was gone (one per
	// GapBuckets bound, then longer, then not back), by reason.
	Buckets []map[string]int  `json:"buckets"`
	Top     []UserDeviceDrops `json:"top"`
	// Timeline counts disconnects per time slot, by reason: Slots slots of
	// Step each from Start, the last one the slot in progress.
	Timeline map[string][]int `json:"timeline"`
}

// Slots is how AllClientDrops lays its timeline out: n slots of step each
// from start. UserID, when set, narrows it all to that user.
type Slots struct {
	Start  time.Time
	Step   time.Duration
	N      int
	UserID int64
}

// AllClientDrops sums up every user's IRC client disconnects since since,
// with the limit devices that dropped most (or most for one reason, when
// by is one of DropReasons).
func (b *Bouncer) AllClientDrops(ctx context.Context, slots Slots, by string, limit int) (AllDrops, error) {
	since := slots.Start
	ds, err := b.store.ClientDisconnects(ctx, slots.UserID, since)
	if err != nil {
		return AllDrops{}, err
	}
	perUser := map[int64][]store.ClientDisconnect{}
	var users []int64
	timeline := map[string][]int{}
	for _, r := range DropReasons {
		timeline[r] = make([]int, slots.N)
	}
	for _, d := range ds {
		if i := int(d.EndedAt.Sub(slots.Start) / slots.Step); i >= 0 && i < slots.N && timeline[d.Reason] != nil {
			timeline[d.Reason][i]++
		}
	}
	for _, d := range ds { // newest first, kept so per user
		if perUser[d.UserID] == nil {
			users = append(users, d.UserID)
		}
		perUser[d.UserID] = append(perUser[d.UserID], d)
	}
	a := AllDrops{Drops: len(ds), Accounts: len(users), Reasons: map[string]int{}, Gaps: map[string]GapStats{}, Timeline: timeline,
		Buckets: make([]map[string]int, len(GapBuckets)+2), Top: []UserDeviceDrops{}}
	for i := range a.Buckets {
		a.Buckets[i] = map[string]int{}
	}
	secs := map[string][]float64{}
	for _, uid := range users {
		devs, gaps := byDevice(perUser[uid], b.attachedSince(uid))
		for _, d := range devs {
			a.Top = append(a.Top, UserDeviceDrops{UserID: uid, DeviceDrops: d})
		}
		for _, g := range gaps {
			a.Reasons[g.reason]++
			st := a.Gaps[g.reason]
			if g.secs < 0 {
				st.Gone++
				a.Buckets[len(a.Buckets)-1][g.reason]++
			} else {
				st.Back++
				secs[g.reason] = append(secs[g.reason], g.secs)
				a.Buckets[bucket(g.secs)][g.reason]++
			}
			a.Gaps[g.reason] = st
		}
	}
	for r, xs := range secs {
		st := a.Gaps[r]
		st.Median, st.P90 = median(xs), percentile(xs, 0.9)
		a.Gaps[r] = st
	}
	a.Devices = len(a.Top)
	count := func(d UserDeviceDrops) int { return d.Drops }
	if slices.Contains(DropReasons, by) {
		count = func(d UserDeviceDrops) int { return d.Reasons[by] }
		// by a reason: only the devices that had it
		a.Top = slices.DeleteFunc(a.Top, func(d UserDeviceDrops) bool { return count(d) == 0 })
	}
	sort.SliceStable(a.Top, func(i, j int) bool {
		if ci, cj := count(a.Top[i]), count(a.Top[j]); ci != cj {
			return ci > cj
		}
		return a.Top[i].Drops > a.Top[j].Drops
	})
	if len(a.Top) > limit {
		a.Top = a.Top[:limit]
	}
	return a, nil
}

// bucket is the GapBuckets bucket a gap of secs falls in.
func bucket(secs float64) int {
	for i, le := range GapBuckets {
		if secs <= le {
			return i
		}
	}
	return len(GapBuckets)
}

// percentile is the p-th (0–1) of xs, by nearest rank.
func percentile(xs []float64, p float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	xs = slices.Clone(xs)
	slices.Sort(xs)
	i := int(math.Ceil(p*float64(len(xs)))) - 1
	return xs[max(0, min(i, len(xs)-1))]
}
