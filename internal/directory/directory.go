package directory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/4evrd/evrd/internal/store"
	"github.com/4evrd/evrd/internal/version"
)

// cacheKey is the directory's row in the store's cache.
const cacheKey = "network-directory"

// Cache keeps the last good copy (the store implements it).
type Cache interface {
	Cached(ctx context.Context, key string) (string, error)
	PutCached(ctx context.Context, key, data string) error
}

// Status is how the directory is doing, for Admin → Settings.
type Status struct {
	Source   string `json:"source"`
	Networks int    `json:"networks"`
	// Updated is when the document says it was last changed.
	Updated time.Time `json:"updated"`
	// FetchedAt is when the source last answered with a good copy (or said
	// it hadn't changed); CheckedAt is the last attempt.
	FetchedAt time.Time `json:"fetchedAt"`
	CheckedAt time.Time `json:"checkedAt"`
	// Error is why the last attempt failed ("" when it didn't): the list
	// in use is then the last good one.
	Error string `json:"error,omitempty"`
	// Skipped are the entries left out because they didn't check out.
	Skipped []string `json:"skipped,omitempty"`
}

// Directory holds the network directory and keeps it fresh.
type Directory struct {
	// Interval is how often the source is checked; Retry, how soon after a
	// failure. MaxSize caps a document's bytes.
	Interval, Retry time.Duration
	MaxSize         int64
	// PathMap turns a file source's path into one evrd can open (inside
	// its chroot); nil leaves it.
	PathMap func(string) string

	cache Cache
	hc    *http.Client
	log   *slog.Logger
	kick  chan struct{}
	fetch sync.Mutex // one fetch at a time

	mu     sync.Mutex
	source string
	doc    *Document
	saved  cached // what's behind doc: its source, validators and bytes
	status Status
}

// cached is the stored copy.
type cached struct {
	Source       string          `json:"source"`
	ETag         string          `json:"etag,omitempty"`
	LastModified string          `json:"lastModified,omitempty"`
	FetchedAt    time.Time       `json:"fetchedAt"`
	Skipped      []string        `json:"skipped,omitempty"`
	Doc          json.RawMessage `json:"doc"`
}

// New returns a directory reading source: an http(s) URL, or a local file
// (a path or file:// URL). Empty turns it off. cache may be nil.
func New(source string, cache Cache, log *slog.Logger) *Directory {
	return &Directory{
		Interval: 12 * time.Hour, Retry: time.Hour, MaxSize: 1 << 20,
		cache: cache, log: log, source: strings.TrimSpace(source),
		hc:   &http.Client{Timeout: 30 * time.Second},
		kick: make(chan struct{}, 1),
	}
}

// SetTimeout bounds each fetch.
func (d *Directory) SetTimeout(t time.Duration) { d.hc.Timeout = t }

// Networks lists the directory (nil when it's off or never loaded).
func (d *Directory) Networks() []Network {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.doc == nil {
		return nil
	}
	return d.doc.Networks
}

// Lookup finds a network by id.
func (d *Directory) Lookup(id string) (Network, bool) {
	for _, n := range d.Networks() {
		if strings.EqualFold(n.ID, id) {
			return n, true
		}
	}
	return Network{}, false
}

// Status reports the source and the last fetch.
func (d *Directory) Status() Status {
	d.mu.Lock()
	defer d.mu.Unlock()
	s := d.status
	s.Source = d.source
	s.Networks = 0
	if d.doc != nil {
		s.Networks, s.Updated = len(d.doc.Networks), d.doc.Updated
	}
	return s
}

// SetSource switches to another source (a settings change) and fetches
// from it at once. The old source's list goes: it isn't what was asked for.
func (d *Directory) SetSource(source string) {
	source = strings.TrimSpace(source)
	d.mu.Lock()
	if source == d.source {
		d.mu.Unlock()
		return
	}
	d.source = source
	if d.saved.Source != source {
		d.doc, d.saved, d.status = nil, cached{}, Status{}
	}
	d.mu.Unlock()
	select {
	case d.kick <- struct{}{}:
	default:
	}
}

// Run loads the stored copy, then checks the source now and every
// Interval (Retry after a failure) until ctx ends.
func (d *Directory) Run(ctx context.Context) {
	d.Load()
	for {
		wait := d.Interval
		if err := d.Refresh(ctx); err != nil {
			wait = d.Retry
		}
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-t.C:
		case <-d.kick:
			t.Stop()
		}
	}
}

// Load takes the stored copy, if it came from the current source.
func (d *Directory) Load() {
	if d.cache == nil {
		return
	}
	raw, err := d.cache.Cached(context.Background(), cacheKey)
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			d.log.Warn("network directory: read the stored copy", "err", err)
		}
		return
	}
	var c cached
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		d.log.Warn("network directory: stored copy", "err", err)
		return
	}
	doc, _, err := Parse(c.Doc)
	if err != nil {
		d.log.Warn("network directory: stored copy", "err", err)
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if c.Source != d.source {
		return
	}
	d.doc, d.saved = doc, c
	d.status = Status{FetchedAt: c.FetchedAt, Skipped: c.Skipped}
}

// Refresh fetches the source once. On any failure the list in use stays.
func (d *Directory) Refresh(ctx context.Context) error {
	d.fetch.Lock()
	defer d.fetch.Unlock()
	d.mu.Lock()
	src, prev := d.source, d.saved
	d.mu.Unlock()
	if src == "" {
		return nil
	}
	if prev.Source != src {
		prev = cached{} // its validators are another source's
	}

	data, etag, lastMod, err := d.get(ctx, src, prev)
	now := time.Now()
	var doc *Document
	var skipped []string
	switch {
	case err == nil && data == nil: // not modified
		doc, err = d.current(), nil
		if doc == nil {
			err = errors.New("not modified, but there's no copy to keep")
		}
		etag, lastMod, skipped, data = prev.ETag, prev.LastModified, prev.Skipped, prev.Doc
	case err == nil:
		doc, skipped, err = Parse(data)
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	if d.source != src {
		return nil // changed meanwhile: the next fetch is for the new one
	}
	d.status.CheckedAt = now
	if err != nil {
		d.status.Error = err.Error()
		d.log.Warn("network directory: keeping the last good copy", "source", src, "err", err)
		return err
	}
	d.doc = doc
	d.saved = cached{Source: src, ETag: etag, LastModified: lastMod, FetchedAt: now, Skipped: skipped, Doc: data}
	d.status = Status{FetchedAt: now, CheckedAt: now, Skipped: skipped}
	if len(skipped) > 0 {
		d.log.Warn("network directory: entries skipped", "source", src, "skipped", skipped)
	}
	if d.cache != nil {
		if b, err := json.Marshal(d.saved); err == nil {
			if err := d.cache.PutCached(ctx, cacheKey, string(b)); err != nil {
				d.log.Warn("network directory: store a copy", "err", err)
			}
		}
	}
	return nil
}

func (d *Directory) current() *Document {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.doc
}

// get reads the source. data is nil when an http source says it hasn't
// changed since prev.
func (d *Directory) get(ctx context.Context, src string, prev cached) (data []byte, etag, lastMod string, err error) {
	if !strings.HasPrefix(src, "http://") && !strings.HasPrefix(src, "https://") {
		data, err = d.readFile(src)
		return data, "", "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, src, nil)
	if err != nil {
		return nil, "", "", err
	}
	req.Header.Set("User-Agent", "evrd/"+version.Version+" (+https://evrd.sh)")
	req.Header.Set("Accept", "application/json")
	if prev.ETag != "" {
		req.Header.Set("If-None-Match", prev.ETag)
	}
	if prev.LastModified != "" {
		req.Header.Set("If-Modified-Since", prev.LastModified)
	}
	res, err := d.hc.Do(req)
	if err != nil {
		return nil, "", "", err
	}
	defer res.Body.Close()
	switch {
	case res.StatusCode == http.StatusNotModified && len(prev.Doc) > 0:
		return nil, prev.ETag, prev.LastModified, nil
	case res.StatusCode != http.StatusOK:
		return nil, "", "", fmt.Errorf("%s answered %s", src, res.Status)
	}
	data, err = d.readAll(res.Body)
	return data, res.Header.Get("ETag"), res.Header.Get("Last-Modified"), err
}

func (d *Directory) readFile(src string) ([]byte, error) {
	if strings.Contains(src, "://") && !strings.HasPrefix(src, "file://") {
		return nil, fmt.Errorf("%s: use an http(s) URL or a file", src)
	}
	path := strings.TrimPrefix(src, "file://")
	if d.PathMap != nil {
		path = d.PathMap(path)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return d.readAll(f)
}

// readAll reads up to MaxSize bytes, refusing anything bigger.
func (d *Directory) readAll(r io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, d.MaxSize+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > d.MaxSize {
		return nil, fmt.Errorf("bigger than %d bytes", d.MaxSize)
	}
	return data, nil
}

// Search lists the networks whose id, name, description, tags or region
// contain every word of query (all of them for an empty query).
func Search(nets []Network, query string) []Network {
	words := strings.Fields(strings.ToLower(query))
	var out []Network
	for _, n := range nets {
		hay := strings.ToLower(strings.Join(append([]string{n.ID, n.Name, n.Description, n.Region}, n.Tags...), " "))
		if !slices.ContainsFunc(words, func(w string) bool { return !strings.Contains(hay, w) }) {
			out = append(out, n)
		}
	}
	return out
}
