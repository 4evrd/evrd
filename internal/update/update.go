// Package update tells admins about new evrd releases and installs them
// from the web app.
//
// The release feed (GitHub's "latest release", by default) is checked at
// start and twice a day. Installing downloads this platform's release
// archive, checks it against the release's SHA256 file, unpacks evrd,
// evrctl and the web app into the update directory (the data volume, in
// the image) and records it there. evrd then restarts into it: in place
// (exec) when it can, else by exiting for its supervisor (Docker's restart
// policy) to start it again. Each start runs the installed update instead
// of the program it was installed over (Launch), so it survives restarts
// of the container; a different program at start (a newer image pulled, or
// an older one pinned) makes it stale, and it's cleared.
//
// In a cluster, an install is recorded in the database and every node
// installs it within a minute.
package update

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/4evrd/evrd/internal/store"
	"github.com/4evrd/evrd/internal/version"
)

// How a node takes on an installed update.
const (
	// ModeExec: evrd stops, then runs the update in its own process.
	ModeExec = "exec"
	// ModeRestart: evrd exits (status RestartStatus) and its supervisor
	// starts it again; the start runs the update.
	ModeRestart = "restart"
)

// RestartStatus is the exit status of a ModeRestart restart: not 0, so a
// restart policy of on-failure restarts it too.
const RestartStatus = 75

// Cache rows, shared by every node.
const (
	latestKey  = "update-latest"  // the last release the feed announced
	installKey = "update-install" // the version admins asked every node to install
)

// Cache is where the shared rows live (the store implements it).
type Cache interface {
	Cached(ctx context.Context, key string) (string, error)
	PutCached(ctx context.Context, key, data string) error
}

// Options are how this node is set up.
type Options struct {
	// Source is the release feed ("" for none).
	Source string
	// Dir is the update directory.
	Dir string
	// Allowed: admins may install (self_update).
	Allowed bool
	// Mode is how this node restarts into an update; Why, when it can't
	// install, says why not.
	Mode, Why string
}

// Status is what the web app shows.
type Status struct {
	Current   string    `json:"current"`
	Base      string    `json:"base,omitempty"` // the program the running update was installed over
	Source    string    `json:"source"`
	Latest    *Release  `json:"latest,omitempty"`
	Available bool      `json:"available"` // Latest is newer than Current
	CheckedAt time.Time `json:"checkedAt"`
	Error     string    `json:"error,omitempty"` // the last check's
	// CanInstall: this node can install updates; Why not, when it can't.
	CanInstall bool   `json:"canInstall"`
	Why        string `json:"why,omitempty"`
	Mode       string `json:"mode,omitempty"`
	// The install in progress or last tried here.
	Installing   string `json:"installing,omitempty"`
	InstallError string `json:"installError,omitempty"`
	// Requested is the version every node was asked to install.
	Requested string `json:"requested,omitempty"`
}

// request is the installKey row.
type request struct {
	Version string    `json:"version"`
	By      string    `json:"by"`
	At      time.Time `json:"at"`
}

// Updater checks for releases and installs them.
type Updater struct {
	// Interval is how often the feed is checked; Retry, how soon after a
	// failure; Poll, how often the cluster's install request is read.
	Interval, Retry, Poll time.Duration

	cache   Cache
	hc      *http.Client
	log     *slog.Logger
	restart func(Restart)
	kick    chan struct{}
	check   sync.Mutex // one check at a time

	mu         sync.Mutex
	opt        Options
	latest     *Release
	checkedAt  time.Time
	err        string
	installing string
	installErr string
	tried      string // the requested version this node last tried
	requested  string
	done       bool // restarting: no more installs
}

// Restart is the update a node is restarting into.
type Restart struct {
	Mode    string
	Bin     string // the new evrd
	Version string
	Base    string
}

// New returns an updater for this node. restart is called once an update
// is installed here: it should stop evrd and then restart it as r says.
func New(opt Options, cache Cache, log *slog.Logger, restart func(r Restart)) *Updater {
	return &Updater{
		Interval: 12 * time.Hour, Retry: time.Hour, Poll: 30 * time.Second,
		cache: cache, log: log, restart: restart, opt: opt,
		hc:   &http.Client{Timeout: 10 * time.Minute},
		kick: make(chan struct{}, 1),
	}
}

// ModeFor works out whether, and how, this node can install updates.
// sandboxed: evrd switched user (or chrooted) at start, so it can't run
// the new program with the privileges it started with; docker: it runs in a
// container, whose restart policy starts it again.
func ModeFor(allowed bool, dir string, sandboxed, chroot, docker bool) (mode, why string) {
	switch {
	case !allowed:
		return "", "self_update is off"
	case !Valid(version.Version):
		return "", "this is a development build (" + version.Version + ")"
	case runtime.GOOS == "openbsd" && (sandboxed || chroot):
		return "", "evrd is sandboxed (pledge): upgrade with the installer"
	case chroot:
		return "", "evrd runs in a chroot"
	case dir == "":
		return "", "no update_dir"
	}
	if err := writable(dir); err != nil {
		return "", "the update directory isn't writable: " + err.Error()
	}
	if sandboxed {
		if docker {
			return ModeRestart, ""
		}
		return "", "evrd switches user at start, so it can't restart itself"
	}
	return ModeExec, ""
}

func writable(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".probe-*")
	if err != nil {
		return err
	}
	f.Close()
	return os.Remove(f.Name())
}

// SetSource switches release feeds and checks the new one now.
func (u *Updater) SetSource(src string) {
	src = strings.TrimSpace(src)
	u.mu.Lock()
	if src == u.opt.Source {
		u.mu.Unlock()
		return
	}
	u.opt.Source, u.latest, u.err, u.checkedAt = src, nil, "", time.Time{}
	u.mu.Unlock()
	u.nudge()
}

// SetAllowed turns installing from the web app on or off (self_update).
func (u *Updater) SetAllowed(on bool, mode, why string) {
	u.mu.Lock()
	u.opt.Allowed, u.opt.Mode, u.opt.Why = on, mode, why
	u.mu.Unlock()
}

func (u *Updater) nudge() {
	select {
	case u.kick <- struct{}{}:
	default:
	}
}

// Run checks the feed now and every Interval, and follows install
// requests, until ctx ends.
func (u *Updater) Run(ctx context.Context) {
	u.loadLatest(ctx)
	next := time.Now()
	poll := time.NewTicker(u.Poll)
	defer poll.Stop()
	for {
		if !time.Now().Before(next) {
			wait := u.Interval
			if err := u.Check(ctx); err != nil {
				wait = u.Retry
			}
			next = time.Now().Add(wait)
		}
		u.follow(ctx)
		t := time.NewTimer(time.Until(next))
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-t.C:
		case <-poll.C:
			t.Stop()
		case <-u.kick:
			t.Stop()
			next = time.Now()
		}
	}
}

// Check reads the release feed once.
func (u *Updater) Check(ctx context.Context) error {
	u.check.Lock()
	defer u.check.Unlock()
	u.mu.Lock()
	src := u.opt.Source
	u.mu.Unlock()
	if src == "" {
		return nil
	}
	r, err := fetchLatest(ctx, u.hc, src, userAgent())
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.opt.Source != src {
		return nil
	}
	u.checkedAt = time.Now()
	if err != nil {
		u.err = err.Error()
		u.log.Warn("update check", "source", src, "err", err)
		return err
	}
	u.err = ""
	if u.latest == nil || u.latest.Version != r.Version {
		if Newer(r.Version, version.Version) {
			u.log.Info("a new evrd release is out", "version", r.Version, "running", version.Version, "url", r.URL)
		}
	}
	u.latest = r
	if u.cache != nil {
		if b, err := json.Marshal(struct {
			Source string    `json:"source"`
			At     time.Time `json:"at"`
			*Release
		}{src, u.checkedAt, r}); err == nil {
			if err := u.cache.PutCached(ctx, latestKey, string(b)); err != nil {
				u.log.Warn("update check: store the result", "err", err)
			}
		}
	}
	return nil
}

// loadLatest takes the last check's result from the database, so a
// restart (or another node) knows at once.
func (u *Updater) loadLatest(ctx context.Context) {
	if u.cache == nil {
		return
	}
	raw, err := u.cache.Cached(ctx, latestKey)
	if err != nil {
		return
	}
	var c struct {
		Source string    `json:"source"`
		At     time.Time `json:"at"`
		Release
	}
	if json.Unmarshal([]byte(raw), &c) != nil || !Valid(c.Version) {
		return
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	if c.Source == u.opt.Source && u.latest == nil {
		r := c.Release
		u.latest, u.checkedAt = &r, c.At
	}
}

// Status reports the running version, the feed's latest and this node's
// install state.
func (u *Updater) Status() Status {
	u.mu.Lock()
	defer u.mu.Unlock()
	s := Status{
		Current: version.Version, Source: u.opt.Source, CheckedAt: u.checkedAt, Error: u.err,
		CanInstall: u.opt.Allowed && u.opt.Mode != "", Mode: u.opt.Mode, Why: u.opt.Why,
		Installing: u.installing, InstallError: u.installErr, Requested: u.requested,
	}
	if Running() {
		s.Base = Base()
	}
	if u.latest != nil {
		r := *u.latest
		r.Assets = nil
		s.Latest = &r
		s.Available = Newer(r.Version, version.Version)
	}
	return s
}

// ErrNothingNewer: there's no newer release to install.
var ErrNothingNewer = errors.New("there's no newer release to install")

// ErrCantInstall: this node can't install updates (Status.Why says why).
var ErrCantInstall = errors.New("this evrd can't install updates")

// Request asks every node to install the feed's latest release, by the
// admin by, and starts installing it here.
func (u *Updater) Request(ctx context.Context, by string) (string, error) {
	u.mu.Lock()
	r, ok := u.latest, u.opt.Allowed && u.opt.Mode != ""
	u.mu.Unlock()
	if !ok {
		return "", ErrCantInstall
	}
	if r == nil || !Newer(r.Version, version.Version) {
		return "", ErrNothingNewer
	}
	if u.cache != nil {
		b, _ := json.Marshal(request{Version: r.Version, By: by, At: time.Now().UTC()})
		if err := u.cache.PutCached(ctx, installKey, string(b)); err != nil {
			return "", err
		}
	}
	u.mu.Lock()
	u.requested, u.tried = r.Version, "" // a request again retries a failed install
	u.mu.Unlock()
	go u.install(context.WithoutCancel(ctx), r, by)
	return r.Version, nil
}

// follow installs the version the cluster asked for, once, if it's newer
// than this node's.
func (u *Updater) follow(ctx context.Context) {
	if u.cache == nil {
		return
	}
	raw, err := u.cache.Cached(ctx, installKey)
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			u.log.Warn("update: read the install request", "err", err)
		}
		return
	}
	var req request
	if json.Unmarshal([]byte(raw), &req) != nil || !Valid(req.Version) {
		return
	}
	u.mu.Lock()
	u.requested = req.Version
	r := u.latest
	skip := !Newer(req.Version, version.Version) || u.tried == req.Version || u.installing != "" || u.done ||
		!u.opt.Allowed || u.opt.Mode == ""
	u.mu.Unlock()
	if skip {
		return
	}
	if r == nil || r.Version != req.Version {
		// asked for a release this node hasn't seen: read the feed
		if err := u.Check(ctx); err != nil {
			return
		}
		u.mu.Lock()
		r = u.latest
		u.mu.Unlock()
		if r == nil || r.Version != req.Version {
			u.mu.Lock()
			u.tried = req.Version
			u.installErr = "the release feed's latest is " + versionOf(r) + ", not " + req.Version
			u.mu.Unlock()
			return
		}
	}
	u.install(ctx, r, req.By)
}

func versionOf(r *Release) string {
	if r == nil {
		return "unknown"
	}
	return r.Version
}

// install stages r and restarts into it.
func (u *Updater) install(ctx context.Context, r *Release, by string) {
	u.mu.Lock()
	if u.installing != "" || u.done {
		u.mu.Unlock()
		return
	}
	u.installing, u.installErr, u.tried = r.Version, "", r.Version
	dir, mode := u.opt.Dir, u.opt.Mode
	u.mu.Unlock()

	base := Base()
	u.log.Info("installing an update", "version", r.Version, "running", version.Version, "by", by, "dir", dir)
	err := stage(ctx, u.hc, userAgent(), dir, r, base, by)
	u.mu.Lock()
	u.installing = ""
	if err != nil {
		u.installErr = err.Error()
		u.mu.Unlock()
		u.log.Error("update failed: still running the current version", "version", r.Version, "err", err)
		return
	}
	u.done = true
	u.mu.Unlock()
	bin, _ := filepath.Abs(filepath.Join(dir, r.Version, "evrd"))
	u.log.Info("update installed: restarting into it", "version", r.Version, "mode", mode)
	u.restart(Restart{Mode: mode, Bin: bin, Version: r.Version, Base: base})
}

// WebDir is the installed update's web app, when this process is that
// update and it has one ("" otherwise).
func WebDir(dir string) string {
	if !Running() || dir == "" {
		return ""
	}
	w := filepath.Join(dir, version.Version, "web")
	if _, err := os.Stat(filepath.Join(w, "index.html")); err != nil {
		return ""
	}
	abs, _ := filepath.Abs(w)
	return abs
}

func userAgent() string {
	return "evrd/" + version.Version + " (" + runtime.GOOS + "/" + runtime.GOARCH + "; +https://evrd.sh)"
}
