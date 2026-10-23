package directory

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/4evrd/evrd/internal/store"
)

const good = `{
  "$schema": "https://evrd.sh/api/networks/v1.schema.json",
  "version": 1,
  "updated": "2026-10-19T00:00:00Z",
  "networks": [
    {"id": "libera", "name": "Libera.Chat", "description": "Free and open-source software", "homepage": "https://libera.chat",
     "servers": [{"host": "irc.libera.chat", "port": 6697, "tls": true}, {"host": "irc.libera.chat", "port": 6667, "tls": false}],
     "sasl": true, "services": "atheme", "channels": ["#libera"], "tags": ["foss"], "region": "global", "future": {"x": 1}},
    {"id": "efnet", "name": "EFnet", "servers": [{"host": "irc.efnet.org", "port": 6667, "tls": false}], "region": "global"}
  ]
}`

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

// memCache is a Cache in memory.
type memCache struct {
	mu   sync.Mutex
	data map[string]string
	err  error
}

func (m *memCache) Cached(_ context.Context, k string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return "", m.err
	}
	v, ok := m.data[k]
	if !ok {
		return "", store.ErrNotFound
	}
	return v, nil
}

func (m *memCache) PutCached(_ context.Context, k, v string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.data == nil {
		m.data = map[string]string{}
	}
	m.data[k] = v
	return m.err
}

func TestParse(t *testing.T) {
	doc, skipped, err := Parse([]byte(good))
	if err != nil || len(skipped) != 0 {
		t.Fatal(err, skipped)
	}
	if len(doc.Networks) != 2 || doc.Networks[0].ID != "libera" || !doc.Networks[0].SASL ||
		doc.Networks[0].Services != "atheme" || doc.Updated.Year() != 2026 {
		t.Fatalf("%+v", doc)
	}
	for _, bad := range []string{`nope`, `{"version": 2, "networks": []}`, `{"version": 1, "networks": []}`} {
		if _, _, err := Parse([]byte(bad)); err == nil {
			t.Errorf("%s: no error", bad)
		}
	}
}

// The dev stack's and e2e suite's directory must stay valid.
func TestDevFixture(t *testing.T) {
	data, err := os.ReadFile("../../deploy/networks.dev.json")
	if err != nil {
		t.Fatal(err)
	}
	doc, skipped, err := Parse(data)
	if err != nil || len(skipped) > 0 || len(doc.Networks) != 5 {
		t.Fatalf("%v %q", err, skipped)
	}
}

func TestParseSkipsBadEntries(t *testing.T) {
	srv := `"servers": [{"host": "irc.example.org", "port": 6697, "tls": true}]`
	doc, skipped, err := Parse([]byte(`{"version": 1, "networks": [
		{"id": "ok", "name": "OK", ` + srv + `},
		{"id": "ok", "name": "Twice", ` + srv + `},
		{"id": "Bad Id", "name": "x", ` + srv + `},
		{"id": "noname", "name": " ", ` + srv + `},
		{"id": "longdesc", "name": "x", "description": "` + strings.Repeat("x", 501) + `", ` + srv + `},
		{"id": "home", "name": "x", "homepage": "javascript:alert(1)", ` + srv + `},
		{"id": "noservers", "name": "x", "servers": []},
		{"id": "badhost", "name": "x", "servers": [{"host": "not a host", "port": 6697}]},
		{"id": "badport", "name": "x", "servers": [{"host": "irc.example.org", "port": 70000}]},
		{"id": "badchan", "name": "x", "channels": ["nohash"], ` + srv + `},
		{"id": "tags", "name": "x", "tags": [` + strings.Repeat(`"t",`, 16) + `"t"], ` + srv + `}
	]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Networks) != 1 || doc.Networks[0].Name != "OK" {
		t.Fatalf("kept %+v", doc.Networks)
	}
	if len(skipped) != 10 || !strings.Contains(skipped[0], "twice") {
		t.Fatalf("skipped %q", skipped)
	}
}

func TestConnectPrefersTLS(t *testing.T) {
	doc, _, _ := Parse([]byte(good))
	if s := doc.Networks[0].Connect(); len(s) != 1 || !s[0].TLS || s[0].Port != 6697 {
		t.Fatalf("libera: %+v", s)
	}
	if s := doc.Networks[1].Connect(); len(s) != 1 || s[0].TLS {
		t.Fatalf("efnet: %+v", s)
	}
}

func TestSearch(t *testing.T) {
	doc, _, _ := Parse([]byte(good))
	for q, want := range map[string]int{"": 2, "LIBERA": 1, "free software": 1, "foss": 1, "global": 2, "nothing": 0} {
		if got := Search(doc.Networks, q); len(got) != want {
			t.Errorf("%q: %d, want %d", q, len(got), want)
		}
	}
}

// server serves good with an ETag, counting requests and answering 304 to
// a matching If-None-Match.
func server(t *testing.T, body *atomic.Value) (*httptest.Server, *atomic.Int32) {
	var hits atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		b := body.Load().(string)
		etag := `"` + string(rune('a'+len(b)%26)) + `"`
		if r.Header.Get("If-None-Match") == etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		if !strings.HasPrefix(r.Header.Get("User-Agent"), "evrd/") {
			t.Errorf("user agent %q", r.Header.Get("User-Agent"))
		}
		w.Header().Set("ETag", etag)
		w.Header().Set("Last-Modified", "Mon, 19 Oct 2026 00:00:00 GMT")
		io.WriteString(w, b)
	}))
	t.Cleanup(s.Close)
	return s, &hits
}

func TestRefreshETagAndBadDocuments(t *testing.T) {
	var body atomic.Value
	body.Store(good)
	s, hits := server(t, &body)
	cache := &memCache{}
	d := New(s.URL, cache, quiet)
	ctx := context.Background()
	if err := d.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if len(d.Networks()) != 2 {
		t.Fatalf("networks: %d", len(d.Networks()))
	}
	if n, ok := d.Lookup("LIBERA"); !ok || n.Name != "Libera.Chat" {
		t.Fatal("lookup")
	}
	if _, ok := d.Lookup("nope"); ok {
		t.Fatal("lookup nope")
	}
	first := d.Status()
	if first.Error != "" || first.Networks != 2 || first.FetchedAt.IsZero() || first.Source != s.URL {
		t.Fatalf("status %+v", first)
	}

	// unchanged: a 304, the list stays
	if err := d.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if hits.Load() != 2 || len(d.Networks()) != 2 {
		t.Fatalf("hits %d networks %d", hits.Load(), len(d.Networks()))
	}

	// a broken document doesn't replace the good one
	for _, bad := range []string{`{"version": 1, "networks": [`, `{"version": 9, "networks": []}`, `<html>oops</html>`} {
		body.Store(bad)
		if err := d.Refresh(ctx); err == nil {
			t.Fatalf("%s: no error", bad)
		}
		if st := d.Status(); len(d.Networks()) != 2 || st.Error == "" || st.Networks != 2 {
			t.Fatalf("%s: networks %d status %+v", bad, len(d.Networks()), st)
		}
	}

	// the stored copy carries the next instance (and its ETag) over a restart
	body.Store(good)
	d2 := New(s.URL, cache, quiet)
	d2.Load()
	if len(d2.Networks()) != 2 || d2.Status().FetchedAt.IsZero() {
		t.Fatal("not loaded from the cache")
	}
	before := hits.Load()
	if err := d2.Refresh(ctx); err != nil || hits.Load() != before+1 {
		t.Fatal(err)
	}
	// ...but not into an instance with another source
	d3 := New("https://elsewhere.example/v1.json", cache, quiet)
	d3.Load()
	if d3.Networks() != nil {
		t.Fatal("loaded another source's copy")
	}
}

func TestRefreshHTTPErrors(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/big":
			io.WriteString(w, `{"version": 1, "networks": [`+strings.Repeat(" ", 2000)+`]}`)
		case "/slow":
			select {
			case <-r.Context().Done():
			case <-time.After(5 * time.Second):
			}
		case "/304":
			w.WriteHeader(http.StatusNotModified)
		default:
			http.NotFound(w, r)
		}
	}))
	defer s.Close()
	for path, want := range map[string]string{"/big": "bigger than", "/slow": "Timeout", "/missing": "404", "/304": "304"} {
		d := New(s.URL+path, nil, quiet)
		d.MaxSize = 1000
		d.SetTimeout(100 * time.Millisecond)
		err := d.Refresh(context.Background())
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v, want %q", path, err, want)
		}
		if d.Status().Error == "" || d.Networks() != nil {
			t.Errorf("%s: status %+v", path, d.Status())
		}
	}
	d := New("http://%zz", nil, quiet)
	if err := d.Refresh(context.Background()); err == nil {
		t.Error("bad URL: no error")
	}
}

func TestFileSource(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "networks.json")
	os.WriteFile(path, []byte(good), 0o644)
	for _, src := range []string{path, "file://" + path} {
		d := New(src, nil, quiet)
		if err := d.Refresh(context.Background()); err != nil || len(d.Networks()) != 2 {
			t.Fatalf("%s: %v", src, err)
		}
	}
	// a chroot maps the path
	d := New("/networks.json", nil, quiet)
	d.PathMap = func(p string) string { return filepath.Join(dir, p) }
	if err := d.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, src := range []string{filepath.Join(dir, "missing.json"), "ftp://example.org/x.json"} {
		if err := New(src, nil, quiet).Refresh(context.Background()); err == nil {
			t.Errorf("%s: no error", src)
		}
	}
}

func TestOffAndSetSource(t *testing.T) {
	off := New("", nil, quiet)
	if err := off.Refresh(context.Background()); err != nil || off.Networks() != nil || off.Status().Source != "" {
		t.Fatal("off")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "a.json")
	os.WriteFile(path, []byte(good), 0o644)
	d := New(path, nil, quiet)
	d.Refresh(context.Background())
	d.SetSource(path) // unchanged: keeps the list
	if len(d.Networks()) != 2 {
		t.Fatal("dropped on an unchanged source")
	}
	d.SetSource("")
	if d.Networks() != nil || d.Status().Networks != 0 {
		t.Fatal("kept a list after turning off")
	}
}

func TestRun(t *testing.T) {
	var body atomic.Value
	body.Store(good)
	s, hits := server(t, &body)
	cache := &memCache{}
	d := New("", cache, quiet)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { d.Run(ctx); close(done) }()
	d.SetSource(s.URL) // fetches at once
	deadline := time.Now().Add(5 * time.Second)
	for len(d.Networks()) != 2 {
		if time.Now().After(deadline) {
			t.Fatal("never fetched")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
	if hits.Load() < 1 {
		t.Fatal("no request")
	}
	if _, err := cache.Cached(context.Background(), cacheKey); err != nil {
		t.Fatal("not stored", err)
	}
}

func TestCacheProblems(t *testing.T) {
	// unreadable or corrupt stored copies are ignored
	for _, c := range []*memCache{
		{err: errors.New("db down")},
		{data: map[string]string{cacheKey: "not json"}},
		{data: map[string]string{cacheKey: `{"source": "x", "doc": {"version": 3}}`}},
	} {
		d := New("x", c, quiet)
		d.Load()
		if d.Networks() != nil {
			t.Fatal("loaded a bad copy")
		}
	}
	// a cache that can't store doesn't stop the list
	dir := t.TempDir()
	path := filepath.Join(dir, "a.json")
	os.WriteFile(path, []byte(good), 0o644)
	d := New(path, &memCache{err: errors.New("read-only")}, quiet)
	if err := d.Refresh(context.Background()); err != nil || len(d.Networks()) != 2 {
		t.Fatal(err)
	}
}
