package webpush

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/4evrd/evrd/internal/history"
	"github.com/4evrd/evrd/internal/store"
)

// Options are the webpush_* directives.
type Options struct {
	// Delay is how long a push waits: one for a line read meanwhile (its
	// buffer's read marker moved past it, on another client or in the web
	// app) isn't sent. 0 sends at once.
	Delay time.Duration
	// Subscriber is the VAPID subject: who push services contact about
	// evrd's pushes (an https: or mailto: URL).
	Subscriber string
	// MaxSubscriptions is how many an account may have on a network.
	MaxSubscriptions int
	// Expiration is how long a subscription lasts without its client
	// registering it again, or without a push to it going through.
	Expiration time.Duration
}

// Store is what the service keeps and reads: the subscriptions, and the
// read markers.
type Store interface {
	store.PushSubscriptionRepositoryImpl
	ReadMarkers(ctx context.Context, networkID int64) (map[string]time.Time, error)
}

// Urgency is RFC 8030's Urgency header.
type Urgency string

const (
	UrgencyNormal Urgency = "normal"
	UrgencyHigh   Urgency = "high"
)

// ttl is how long a push service keeps a push for a device that's away
// (Ergo's: a week).
const ttl = 7 * 24 * time.Hour

// Timeout bounds one push request.
const Timeout = 10 * time.Second

const (
	maxSending     = 16   // push requests in flight at once
	maxPending     = 1024 // pushes waiting out the delay, everyone's
	maxPendingUser = 16   // … and one account's
	maxCleared     = 10000
)

// ErrGone means the push service says the subscription is no more (404,
// 410): it's dropped.
var ErrGone = errors.New("webpush: the subscription is gone")

// ErrTestFailed means the test push sent on WEBPUSH REGISTER didn't go
// through.
var ErrTestFailed = errors.New("webpush: the test push failed")

// Service sends the pushes. A nil Service is off.
type Service struct {
	st   Store
	keys *Keys
	opts Options
	log  *slog.Logger
	// Attached reports whether the user has a client (IRC or web chat)
	// attached: then nothing is pushed.
	Attached func(userID int64) bool
	// HTTP sends the pushes; nil is the default, which goes only to
	// external addresses.
	HTTP *http.Client
	now  func() time.Time

	sending chan struct{}
	mu      sync.Mutex
	pending map[int64]int // pushes waiting, by user
	waiting int
	// pushed is when the newest line pushed for a buffer was, so a read
	// marker moving past it is pushed too (devices take the notification
	// away).
	pushed map[bufKey]time.Time
	wg     sync.WaitGroup
}

type bufKey struct {
	user, network int64
	buffer        string // lowercased
}

// New is the push service, signing with keys.
func New(st Store, keys *Keys, opts Options, log *slog.Logger) *Service {
	if opts.MaxSubscriptions <= 0 {
		opts.MaxSubscriptions = 8
	}
	if opts.Expiration <= 0 {
		opts.Expiration = 30 * 24 * time.Hour
	}
	if opts.Subscriber == "" {
		opts.Subscriber = "https://evrd.sh"
	}
	return &Service{
		st: st, keys: keys, opts: opts, log: log, now: time.Now,
		sending: make(chan struct{}, maxSending),
		pending: map[int64]int{}, pushed: map[bufKey]time.Time{},
	}
}

// Enabled reports whether push is on.
func (s *Service) Enabled() bool { return s != nil && s.keys != nil }

// PublicKey is the VAPID public key (ISUPPORT VAPID=), "" when off.
func (s *Service) PublicKey() string {
	if !s.Enabled() {
		return ""
	}
	return s.keys.PublicKey()
}

// Wait waits for the pushes under way (tests).
func (s *Service) Wait() { s.wg.Wait() }

func (s *Service) client() *http.Client {
	if s.HTTP != nil {
		return s.HTTP
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.HTTP == nil {
		s.HTTP = externalClient(Timeout)
	}
	return s.HTTP
}

// --- registering -------------------------------------------------------------

// Register adds or renews the user's subscription for endpoint on a network
// (0: a bouncer connection on none), as WEBPUSH REGISTER asks. One that's
// new, or has new keys, first gets a test push (ErrTestFailed when it
// doesn't go through); a renewal only moves its expiry. Past the limit is
// store.ErrPushLimit.
func (s *Service) Register(ctx context.Context, userID, networkID int64, endpoint string, keys SubKeys) error {
	p256, auth := keys.Strings()
	now := s.now()
	subs, err := s.st.PushSubscriptions(ctx, userID)
	if err != nil {
		return err
	}
	n := 0
	for _, sub := range subs {
		if sub.NetworkID != networkID {
			continue
		}
		n++
		if sub.Endpoint == endpoint && sub.P256DH == p256 && sub.Auth == auth {
			sub.LastRefresh = now
			_, err := s.st.SavePushSubscription(ctx, sub, 0)
			return err
		}
		if sub.Endpoint == endpoint {
			n-- // replaced, not added
		}
	}
	if n >= s.opts.MaxSubscriptions {
		return store.ErrPushLimit
	}
	if err := s.send(ctx, endpoint, keys, UrgencyHigh, pingMessage); err != nil {
		s.log.Debug("webpush: test push failed", "user", userID, "endpoint", endpoint, "err", err)
		return ErrTestFailed
	}
	_, err = s.st.SavePushSubscription(ctx, store.PushSubscription{
		UserID: userID, NetworkID: networkID, Endpoint: endpoint, P256DH: p256, Auth: auth,
		LastRefresh: now, LastSuccess: now,
	}, s.opts.MaxSubscriptions)
	return err
}

// Unregister drops the user's subscription for endpoint on a network.
func (s *Service) Unregister(ctx context.Context, userID, networkID int64, endpoint string) error {
	return s.st.DeletePushEndpoint(ctx, userID, networkID, endpoint)
}

// --- pushing -----------------------------------------------------------------

// Notify pushes m, a DM or highlight for the user that arrived on n (me:
// their nick there), to their subscriptions for n and their bouncer
// connections' — after the delay, unless a client attached or the buffer
// was read meanwhile. The bouncer calls it while none of the user's clients
// is attached.
func (s *Service) Notify(n store.Network, me string, m history.Message) {
	if !s.Enabled() || s.attached(n.UserID) {
		return
	}
	s.mu.Lock()
	if s.waiting >= maxPending || s.pending[n.UserID] >= maxPendingUser {
		s.mu.Unlock()
		s.log.Debug("webpush: too many pushes waiting, dropped one", "user", n.UserID)
		return
	}
	s.waiting++
	s.pending[n.UserID]++
	s.wg.Add(1)
	s.mu.Unlock()
	line := MessageLine(n.Name, me, m)
	key := bufKey{n.UserID, n.ID, strings.ToLower(m.Target)}
	wait := s.opts.Delay - s.now().Sub(m.Time)
	time.AfterFunc(max(wait, 0), func() {
		defer s.done(n.UserID)
		s.deliver(key, m.Time, line)
	})
}

func (s *Service) done(userID int64) {
	s.mu.Lock()
	s.waiting--
	if s.pending[userID]--; s.pending[userID] <= 0 {
		delete(s.pending, userID)
	}
	s.mu.Unlock()
	s.wg.Done()
}

func (s *Service) attached(userID int64) bool { return s.Attached != nil && s.Attached(userID) }

// deliver sends a line that waited out the delay, unless it's been seen.
func (s *Service) deliver(key bufKey, at time.Time, line []byte) {
	if s.attached(key.user) {
		return // the client has it (playback)
	}
	ctx := context.Background()
	if s.opts.Delay > 0 {
		marks, err := s.st.ReadMarkers(ctx, key.network)
		// read markers are to the millisecond (@time's precision)
		if err == nil && !at.Truncate(time.Millisecond).After(marks[key.buffer]) {
			return
		}
	}
	if s.broadcast(ctx, key.user, key.network, line, UrgencyHigh, "") > 0 {
		s.mu.Lock()
		if _, ok := s.pushed[key]; ok || len(s.pushed) < maxCleared {
			if at.After(s.pushed[key]) {
				s.pushed[key] = at
			}
		}
		s.mu.Unlock()
	}
}

// MarkRead tells the user's devices that a buffer was read up to at, when a
// push for it went out that's now read (the client that read it, with
// origin as its endpoint, doesn't need telling). server is the name lines
// from evrd come from.
func (s *Service) MarkRead(userID, networkID int64, target string, at time.Time, origin, server string) {
	if !s.Enabled() {
		return
	}
	key := bufKey{userID, networkID, strings.ToLower(target)}
	s.mu.Lock()
	pushedAt, ok := s.pushed[key]
	if ok && !pushedAt.Truncate(time.Millisecond).After(at) {
		delete(s.pushed, key)
	} else {
		ok = false
	}
	s.mu.Unlock()
	if !ok {
		return
	}
	line := markReadLine(server, target, at, s.now())
	s.wg.Go(func() { s.broadcast(context.Background(), userID, networkID, line, UrgencyNormal, origin) })
}

// broadcast sends line to the user's subscriptions for a network and their
// bouncer connections' (each endpoint once, skipping skip), dropping those
// that are gone. It returns how many went through.
func (s *Service) broadcast(ctx context.Context, userID, networkID int64, line []byte, u Urgency, skip string) int {
	subs, err := s.st.PushSubscriptions(ctx, userID)
	if err != nil {
		s.log.Warn("webpush: list subscriptions", "user", userID, "err", err)
		return 0
	}
	seen := map[string]bool{skip: true}
	sent := 0
	for _, sub := range subs {
		if sub.NetworkID != networkID && sub.NetworkID != 0 || seen[sub.Endpoint] {
			continue
		}
		seen[sub.Endpoint] = true
		if s.sendTo(ctx, sub, line, u) {
			sent++
		}
	}
	return sent
}

// sendTo pushes to a stored subscription and keeps track: its last success,
// or it's dropped when gone.
func (s *Service) sendTo(ctx context.Context, sub store.PushSubscription, line []byte, u Urgency) bool {
	keys, err := DecodeKeys(sub.P256DH, sub.Auth)
	if err == nil {
		err = s.send(ctx, sub.Endpoint, keys, u, line)
	}
	switch {
	case err == nil:
		if err := s.st.TouchPushSubscription(ctx, sub.ID, s.now()); err != nil {
			s.log.Warn("webpush: record a push", "err", err)
		}
		return true
	case errors.Is(err, ErrGone), errors.Is(err, ErrInvalidKeys):
		s.log.Info("webpush: subscription gone, dropped", "user", sub.UserID, "endpoint", sub.Endpoint)
		if err := s.st.DeletePushSubscription(ctx, sub.ID); err != nil {
			s.log.Warn("webpush: drop a subscription", "err", err)
		}
	default:
		s.log.Debug("webpush: push failed", "user", sub.UserID, "endpoint", sub.Endpoint, "err", err)
	}
	return false
}

// send is one push: payload encrypted to keys, signed, POSTed to endpoint.
func (s *Service) send(ctx context.Context, endpoint string, keys SubKeys, u Urgency, payload []byte) error {
	if err := CheckEndpoint(endpoint); err != nil {
		return err
	}
	body, err := encrypt(payload, keys)
	if err != nil {
		return err
	}
	auth, err := s.keys.authorization(endpoint, s.opts.Subscriber, s.now())
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Encoding", "aes128gcm")
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("TTL", strconv.Itoa(int(ttl/time.Second)))
	req.Header.Set("Urgency", string(u))
	req.Header.Set("Authorization", auth)
	select {
	case s.sending <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-s.sending }()
	resp, err := s.client().Do(req)
	if err != nil {
		return err
	}
	io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone:
		return ErrGone
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return nil
	}
	return fmt.Errorf("webpush: %s", resp.Status)
}

// --- upkeep ------------------------------------------------------------------

// Maintain expires subscriptions: one its client hasn't registered again,
// or none of whose pushes went through, within the expiration. One with no
// push through for half of it gets a test push (Ergo's upkeep).
func (s *Service) Maintain(ctx context.Context) {
	if !s.Enabled() {
		return
	}
	subs, err := s.st.AllPushSubscriptions(ctx)
	if err != nil {
		s.log.Warn("webpush: list subscriptions", "err", err)
		return
	}
	now, exp := s.now(), s.opts.Expiration
	for _, sub := range subs {
		switch {
		case now.Sub(sub.LastRefresh) > exp || now.Sub(sub.LastSuccess) > exp:
			s.log.Info("webpush: subscription expired", "user", sub.UserID, "endpoint", sub.Endpoint)
			if err := s.st.DeletePushSubscription(ctx, sub.ID); err != nil {
				s.log.Warn("webpush: drop a subscription", "err", err)
			}
		case now.Sub(sub.LastSuccess) > exp/2:
			s.sendTo(ctx, sub, pingMessage, UrgencyNormal)
		}
	}
}

// Run does the upkeep every interval, on the node leader says is the
// cluster's leader (nil: this one), until ctx ends.
func (s *Service) Run(ctx context.Context, every time.Duration, leader func() bool) {
	if !s.Enabled() {
		return
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if leader == nil || leader() {
				s.Maintain(ctx)
			}
		}
	}
}
