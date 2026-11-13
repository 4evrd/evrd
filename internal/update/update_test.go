package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/4evrd/evrd/internal/config"
	"github.com/4evrd/evrd/internal/store"
	"github.com/4evrd/evrd/internal/version"
)

func TestNewer(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"0.2.0", "0.1.0", true},
		{"v1.0.0", "0.99.99", true},
		{"0.10.0", "0.9.0", true},
		{"0.1.0", "0.1.0", false},
		{"0.1.0", "0.2.0", false},
		{"1.2.3", "1.2.3-rc.1", true},
		{"1.2.3-rc.1", "1.2.3", false},
		{"1.2.3-rc.2", "1.2.3-rc.1", true},
		{"1.2.3+build", "1.2.3", false},
		{"dev", "0.1.0", false},
		{"0.1.0", "dev", false},
		{"1.2", "1.0.0", false},
		{"01.2.3", "1.0.0", false},
		{"1.x.3", "1.0.0", false},
	} {
		if got := Newer(c.a, c.b); got != c.want {
			t.Errorf("Newer(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
	if !Valid("1.2.3") || Valid("dev") {
		t.Error("Valid")
	}
}

func setVersion(t *testing.T, v string) {
	old := version.Version
	version.Version = v
	t.Cleanup(func() { version.Version = old })
}

// fakeEvrd is an "evrd" that says its version, like the real one.
func fakeEvrd(v string) string { return "#!/bin/sh\necho evrd " + v + "\n" }

// archive builds a release archive: files under evrd_<os>_<arch>/.
func archive(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	top := strings.TrimSuffix(ArchiveName(), ".tar.gz") + "/"
	tw.WriteHeader(&tar.Header{Name: top, Typeflag: tar.TypeDir, Mode: 0o755})
	for name, body := range files {
		hd := &tar.Header{Name: top + name, Typeflag: tar.TypeReg, Mode: 0o755, Size: int64(len(body))}
		if strings.HasPrefix(name, "/") || strings.HasPrefix(name, "..") {
			hd.Name = name
		}
		if err := tw.WriteHeader(hd); err != nil {
			t.Fatal(err)
		}
		tw.Write([]byte(body))
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

// feed is a fake release server: the GitHub-style JSON, the archive and
// SHA256.
type feed struct {
	srv     *httptest.Server
	mu      sync.Mutex
	version string
	arc     []byte
	sums    string
	status  int
	hits    map[string]int
}

func newFeed(t *testing.T, v string, files map[string]string) *feed {
	f := &feed{version: v, hits: map[string]int{}}
	f.setArchive(t, files)
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.hits[r.URL.Path]++
		if f.status != 0 {
			w.WriteHeader(f.status)
			return
		}
		switch r.URL.Path {
		case "/latest":
			json.NewEncoder(w).Encode(map[string]any{
				"tag_name": "v" + f.version, "html_url": f.srv.URL + "/notes", "body": "Fixes.",
				"published_at": "2026-11-01T00:00:00Z",
				"assets": []map[string]string{
					{"name": ArchiveName(), "browser_download_url": f.srv.URL + "/a.tgz"},
					{"name": "SHA256", "browser_download_url": f.srv.URL + "/sums"},
					{"name": "bad", "browser_download_url": "ftp://x"},
				},
			})
		case "/a.tgz":
			w.Write(f.arc)
		case "/sums":
			io.WriteString(w, f.sums)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *feed) setArchive(t *testing.T, files map[string]string) {
	if files == nil {
		files = map[string]string{"evrd": fakeEvrd(f.version), "evrctl": "#!/bin/sh\n", "web/index.html": "<p>new</p>", "README.md": "x"}
	}
	a := archive(t, files)
	sum := sha256.Sum256(a)
	f.mu.Lock()
	f.arc = a
	f.sums = "SHA256 (other.tar.gz) = " + strings.Repeat("0", 64) + "\nSHA256 (" + ArchiveName() + ") = " + hex.EncodeToString(sum[:]) + "\n"
	f.mu.Unlock()
}

// memCache is the store's cache table.
type memCache struct {
	mu   sync.Mutex
	rows map[string]string
	err  error
}

func (c *memCache) Cached(_ context.Context, k string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err != nil {
		return "", c.err
	}
	v, ok := c.rows[k]
	if !ok {
		return "", store.ErrNotFound
	}
	return v, nil
}

func (c *memCache) PutCached(_ context.Context, k, v string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err != nil {
		return c.err
	}
	if c.rows == nil {
		c.rows = map[string]string{}
	}
	c.rows[k] = v
	return nil
}

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func needShell(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake evrd is a shell script")
	}
}

func TestCheckAndInstall(t *testing.T) {
	needShell(t)
	setVersion(t, "0.1.0")
	f := newFeed(t, "0.2.0", nil)
	dir := t.TempDir()
	cache := &memCache{}
	restarted := make(chan Restart, 1)
	u := New(Options{Source: f.srv.URL + "/latest", Dir: dir, Allowed: true, Mode: ModeExec}, cache, quiet(), func(r Restart) { restarted <- r })

	if st := u.Status(); st.Available || st.Latest != nil || !st.CanInstall {
		t.Fatalf("before a check: %+v", st)
	}
	if _, err := u.Request(context.Background(), "admin"); !errors.Is(err, ErrNothingNewer) {
		t.Fatalf("request before a check: %v", err)
	}
	if err := u.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	st := u.Status()
	if !st.Available || st.Latest.Version != "0.2.0" || st.Latest.Notes != "Fixes." || st.Latest.Assets != nil || st.CheckedAt.IsZero() {
		t.Fatalf("after a check: %+v", st)
	}
	if _, ok := cache.rows[latestKey]; !ok {
		t.Fatal("the check isn't shared")
	}

	v, err := u.Request(context.Background(), "admin")
	if err != nil || v != "0.2.0" {
		t.Fatalf("request: %q %v", v, err)
	}
	var r Restart
	select {
	case r = <-restarted:
	case <-time.After(10 * time.Second):
		t.Fatalf("no restart: %+v", u.Status())
	}
	if r.Mode != ModeExec || r.Version != "0.2.0" || r.Base != "0.1.0" || r.Bin != filepath.Join(dir, "0.2.0", "evrd") {
		t.Fatalf("restart: %+v", r)
	}
	for _, f := range []string{"0.2.0/evrd", "0.2.0/evrctl", "0.2.0/web/index.html", stateFile} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("%s: %v", f, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "0.2.0", "README.md")); err == nil {
		t.Error("unpacked a file it doesn't need")
	}
	s, err := readState(dir)
	if err != nil || s.Version != "0.2.0" || s.Base != "0.1.0" || s.By != "admin" {
		t.Fatalf("state: %+v %v", s, err)
	}
	var req request
	json.Unmarshal([]byte(cache.rows[installKey]), &req)
	if req.Version != "0.2.0" || req.By != "admin" {
		t.Fatalf("install request: %+v", req)
	}
	if st := u.Status(); st.Requested != "0.2.0" || st.Installing != "" || st.InstallError != "" {
		t.Fatalf("status after: %+v", st)
	}
	// done: no second install while restarting
	u.install(context.Background(), &Release{Version: "0.3.0"}, "x")
	if u.Status().Installing != "" {
		t.Fatal("installed again")
	}

	// a restarted node knows the release from the database at once
	u2 := New(Options{Source: f.srv.URL + "/latest"}, cache, quiet(), nil)
	u2.loadLatest(context.Background())
	if st := u2.Status(); st.Latest == nil || st.Latest.Version != "0.2.0" || st.CanInstall {
		t.Fatalf("loaded: %+v", st)
	}
	// …unless it was another feed's
	u3 := New(Options{Source: "https://elsewhere/latest"}, cache, quiet(), nil)
	u3.loadLatest(context.Background())
	if u3.Status().Latest != nil {
		t.Fatal("took another feed's release")
	}
}

func TestFollowRequest(t *testing.T) {
	needShell(t)
	setVersion(t, "0.1.0")
	f := newFeed(t, "0.2.0", nil)
	cache := &memCache{}
	b, _ := json.Marshal(request{Version: "0.2.0", By: "root", At: time.Now()})
	cache.rows = map[string]string{installKey: string(b)}

	restarted := make(chan Restart, 1)
	u := New(Options{Source: f.srv.URL + "/latest", Dir: t.TempDir(), Allowed: true, Mode: ModeRestart}, cache, quiet(), func(r Restart) { restarted <- r })
	u.follow(context.Background()) // hasn't seen 0.2.0: reads the feed, installs it
	select {
	case r := <-restarted:
		if r.Mode != ModeRestart || r.Version != "0.2.0" {
			t.Fatalf("%+v", r)
		}
	default:
		t.Fatalf("didn't follow: %+v", u.Status())
	}

	// a node that can't install leaves it
	u = New(Options{Source: f.srv.URL + "/latest", Dir: t.TempDir(), Allowed: false}, cache, quiet(), func(Restart) { t.Error("restarted") })
	u.follow(context.Background())
	if st := u.Status(); st.Requested != "0.2.0" || st.CanInstall {
		t.Fatalf("%+v", st)
	}

	// asked for a release the feed doesn't have (any more)
	b, _ = json.Marshal(request{Version: "0.3.0"})
	cache.rows[installKey] = string(b)
	u = New(Options{Source: f.srv.URL + "/latest", Dir: t.TempDir(), Allowed: true, Mode: ModeExec}, cache, quiet(), func(Restart) { t.Error("restarted") })
	u.follow(context.Background())
	if st := u.Status(); !strings.Contains(st.InstallError, "not 0.3.0") {
		t.Fatalf("%+v", st)
	}
	u.follow(context.Background()) // tried once: not again
	// a broken row, or no row, or a failing database
	cache.rows[installKey] = "{"
	u.follow(context.Background())
	delete(cache.rows, installKey)
	u.follow(context.Background())
	cache.err = errors.New("db down")
	u.follow(context.Background())
	New(Options{}, nil, quiet(), nil).follow(context.Background())
}

func TestInstallFailures(t *testing.T) {
	needShell(t)
	setVersion(t, "0.1.0")
	ctx := context.Background()
	hc := http.DefaultClient
	f := newFeed(t, "0.2.0", nil)
	r, err := fetchLatest(ctx, hc, f.srv.URL+"/latest", "test")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := r.Assets["bad"]; ok {
		t.Error("kept a non-http asset")
	}

	try := func(name string, r *Release, want string) {
		t.Helper()
		dir := t.TempDir()
		err := stage(ctx, hc, "test", dir, r, "0.1.0", "")
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v, want %q", name, err, want)
		}
		if _, err := os.Stat(filepath.Join(dir, stateFile)); err == nil {
			t.Errorf("%s: recorded a failed install", name)
		}
	}
	without := func(asset string) *Release {
		c := *r
		c.Assets = map[string]string{}
		for k, v := range r.Assets {
			if k != asset {
				c.Assets[k] = v
			}
		}
		return &c
	}
	try("no archive", without(ArchiveName()), "has no "+ArchiveName())
	try("no sums", without(checksumsAsset), "no SHA256")

	f.mu.Lock()
	f.sums = "nothing here\n"
	f.mu.Unlock()
	try("not in sums", r, "no checksum for")

	f.setArchive(t, nil)
	f.mu.Lock()
	f.arc = append([]byte{}, f.arc...)
	f.arc[len(f.arc)-1] ^= 1
	f.mu.Unlock()
	try("tampered", r, "checksum mismatch")

	f.setArchive(t, map[string]string{"evrd": fakeEvrd("0.2.0")})
	try("no evrctl", r, "no evrctl")

	f.setArchive(t, map[string]string{"evrd": fakeEvrd("0.9.9"), "evrctl": ""})
	try("wrong version", r, "not version 0.2.0")

	f.setArchive(t, map[string]string{"evrd": "#!/bin/sh\nexit 3\n", "evrctl": ""})
	try("doesn't run", r, "doesn't run here")

	f.setArchive(t, map[string]string{"evrd": fakeEvrd("0.2.0"), "evrctl": "", "../escape": "x", "/abs": "x"})
	dir := t.TempDir()
	if err := stage(ctx, hc, "test", filepath.Join(dir, "u"), r, "0.1.0", ""); err != nil {
		t.Fatalf("with odd paths: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "escape")); err == nil {
		t.Fatal("wrote outside the update directory")
	}

	f.mu.Lock()
	f.arc = []byte("not gzip")
	f.sums = fmt.Sprintf("%x  %s\n", sha256.Sum256(f.arc), ArchiveName()) // GNU style
	f.mu.Unlock()
	try("not an archive", r, ArchiveName()+": ")

	f.mu.Lock()
	f.status = http.StatusBadGateway
	f.mu.Unlock()
	try("server down", r, "502")
	if _, err := fetchLatest(ctx, hc, f.srv.URL+"/latest", "test"); err == nil || !strings.Contains(err.Error(), "502") {
		t.Errorf("feed down: %v", err)
	}

	// the updater reports a failed install and keeps running
	f.mu.Lock()
	f.status = 0
	f.mu.Unlock()
	f.setArchive(t, map[string]string{"evrd": fakeEvrd("0.9.9"), "evrctl": ""})
	u := New(Options{Source: f.srv.URL + "/latest", Dir: t.TempDir(), Allowed: true, Mode: ModeExec}, nil, quiet(), func(Restart) { t.Error("restarted") })
	u.Check(ctx)
	u.install(ctx, u.latest, "admin")
	if st := u.Status(); !strings.Contains(st.InstallError, "not version") || st.Installing != "" {
		t.Fatalf("%+v", st)
	}
}

func TestFeedErrors(t *testing.T) {
	setVersion(t, "0.1.0")
	ctx := context.Background()
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, body) }))
	defer srv.Close()
	for _, c := range []struct{ body, want string }{
		{"{", "the release feed"},
		{`{"tag_name":"v0.2.0","draft":true}`, "draft"},
		{`{"tag_name":"nightly"}`, "no version"},
	} {
		body = c.body
		if _, err := fetchLatest(ctx, http.DefaultClient, srv.URL, "t"); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v", c.body, err)
		}
	}
	body = `{"tag_name":"v0.2.0","body":"` + strings.Repeat("x", maxNotes+10) + `","html_url":"javascript:alert(1)"}`
	r, err := fetchLatest(ctx, http.DefaultClient, srv.URL, "t")
	if err != nil || len(r.Notes) != maxNotes || r.URL != "" {
		t.Fatalf("%v %d %q", err, len(r.Notes), r.URL)
	}
	if _, err := fetchLatest(ctx, http.DefaultClient, "http://127.0.0.1:1/x", "t"); err == nil {
		t.Error("no server")
	}
	if _, err := fetchLatest(ctx, http.DefaultClient, "::bad", "t"); err == nil {
		t.Error("bad URL")
	}

	// the updater: a failed check is reported, and it carries on
	body = "{"
	cache := &memCache{err: errors.New("db down")}
	u := New(Options{Source: srv.URL}, cache, quiet(), nil)
	if err := u.Check(ctx); err == nil || u.Status().Error == "" {
		t.Fatal("failure not reported")
	}
	body = `{"tag_name":"v0.2.0"}`
	if err := u.Check(ctx); err != nil || u.Status().Error != "" || !u.Status().Available {
		t.Fatalf("%v %+v", err, u.Status())
	}
	// no feed: nothing to check
	u = New(Options{}, nil, quiet(), nil)
	if err := u.Check(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := u.Request(ctx, "a"); !errors.Is(err, ErrCantInstall) {
		t.Fatal(err)
	}
	if hostOf("::bad/x") != "::bad" {
		t.Error(hostOf("::bad/x"))
	}
}

func TestRunAndSettings(t *testing.T) {
	setVersion(t, "0.1.0")
	var mu sync.Mutex
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits++
		mu.Unlock()
		io.WriteString(w, `{"tag_name":"v0.1.0"}`)
	}))
	defer srv.Close()
	u := New(Options{Source: srv.URL}, &memCache{}, quiet(), nil)
	u.Poll = 10 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { u.Run(ctx); close(done) }()
	waitFor(t, func() bool { return !u.Status().CheckedAt.IsZero() })
	if u.Status().Available {
		t.Fatal("the same version isn't an update")
	}
	u.SetSource(srv.URL + "/other") // checks the new feed at once
	u.SetSource(srv.URL + "/other")
	waitFor(t, func() bool { mu.Lock(); defer mu.Unlock(); return hits >= 2 })
	u.SetAllowed(true, ModeExec, "")
	if st := u.Status(); !st.CanInstall || st.Mode != ModeExec {
		t.Fatalf("%+v", st)
	}
	u.SetAllowed(false, "", "self_update is off")
	if st := u.Status(); st.CanInstall || st.Why == "" {
		t.Fatalf("%+v", st)
	}
	cancel()
	<-done
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	for i := 0; i < 500; i++ {
		if ok() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timed out")
}

func TestModeFor(t *testing.T) {
	setVersion(t, "0.1.0")
	dir := t.TempDir()
	for _, c := range []struct {
		name                      string
		allowed                   bool
		dir                       string
		sandboxed, chroot, docker bool
		mode, why                 string
	}{
		{"off", false, dir, false, false, false, "", "self_update is off"},
		{"chroot", true, dir, true, true, false, "", "chroot"},
		{"no dir", true, "", false, false, false, "", "no update_dir"},
		{"exec", true, dir, false, false, false, ModeExec, ""},
		{"docker, user switched", true, dir, true, false, true, ModeRestart, ""},
		{"user switched", true, dir, true, false, false, "", "switches user"},
		{"not writable", true, "/dev/null/x", false, false, false, "", "isn't writable"},
	} {
		if runtime.GOOS == "openbsd" && (c.sandboxed || c.chroot) {
			continue
		}
		mode, why := ModeFor(c.allowed, c.dir, c.sandboxed, c.chroot, c.docker)
		if mode != c.mode || !strings.Contains(why, c.why) || (c.why == "" && why != "") {
			t.Errorf("%s: %q %q", c.name, mode, why)
		}
	}
	setVersion(t, "dev")
	if _, why := ModeFor(true, dir, false, false, false); !strings.Contains(why, "development build") {
		t.Error(why)
	}
}

func TestLaunch(t *testing.T) {
	setVersion(t, "0.1.0")
	t.Setenv(envRunning, "")
	os.Unsetenv(envRunning)
	t.Setenv(envBase, "x")
	var ran []string
	var env []string
	old := execve
	execve = func(bin string, argv, e []string) error { ran, env = argv, e; return errors.New("stub") }
	t.Cleanup(func() { execve = old })
	dir := t.TempDir()

	if stale, err := Launch(dir, "evrd", []string{"evrd"}); stale || err != nil || ran != nil {
		t.Fatalf("nothing installed: %v %v %v", stale, err, ran)
	}
	if stale, err := Launch("", "evrd", nil); stale || err != nil {
		t.Fatal("no dir")
	}
	writeState(dir, installed{Version: "0.2.0", Base: "0.1.0"})
	if _, err := Launch(dir, "evrd", []string{"evrd"}); err == nil {
		t.Fatal("ran a missing program")
	}
	os.MkdirAll(filepath.Join(dir, "0.2.0"), 0o755)
	os.WriteFile(filepath.Join(dir, "0.2.0", "evrd"), []byte(fakeEvrd("0.2.0")), 0o755)
	if _, err := Launch(dir, "evrd", []string{"/usr/local/bin/evrd", "-config", "x"}); err == nil || err.Error() != "stub" {
		t.Fatalf("exec: %v", err)
	}
	if want := filepath.Join(dir, "0.2.0", "evrd"); len(ran) != 3 || ran[0] != want || ran[2] != "x" {
		t.Fatalf("argv %v", ran)
	}
	e := strings.Join(env, "\n")
	if !strings.Contains(e, envRunning+"=0.2.0") || !strings.Contains(e, envBase+"=0.1.0") || strings.Contains(e, envBase+"=x") {
		t.Fatalf("env %v", env)
	}

	// another program now (the image changed): stale, and evrd clears it
	setVersion(t, "0.3.0")
	if stale, err := Launch(dir, "evrd", []string{"evrd"}); !stale || err != nil {
		t.Fatalf("stale: %v %v", stale, err)
	}
	var out bytes.Buffer
	t.Setenv("EVRD_CONFIG", "")
	t.Setenv("EVRD_UPDATE_DIR", dir)
	Start([]string{"evrd"}, "evrctl", false, &out) // evrctl leaves it
	if _, err := readState(dir); err != nil {
		t.Fatal("evrctl cleared it")
	}
	Start([]string{"evrd"}, "evrd", true, &out)
	if _, err := readState(dir); err == nil || !strings.Contains(out.String(), "clearing it") {
		t.Fatalf("not cleared: %q", out.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "0.2.0")); err == nil {
		t.Fatal("kept the old version")
	}
	if err := Clear(dir); err != nil {
		t.Fatal(err)
	}

	// a broken record
	os.WriteFile(filepath.Join(dir, stateFile), []byte("{}"), 0o644)
	out.Reset()
	Start([]string{"evrd"}, "evrd", true, &out)
	if !strings.Contains(out.String(), "can't run the installed update") {
		t.Fatalf("%q", out.String())
	}
	os.WriteFile(filepath.Join(dir, stateFile), []byte("{"), 0o644)
	if _, err := readState(dir); err == nil {
		t.Fatal("read a broken record")
	}

	// the update itself doesn't launch again
	t.Setenv(envRunning, "0.3.0")
	if !Running() || Base() != "x" {
		t.Fatalf("running %v base %q", Running(), Base())
	}
	if stale, err := Launch(dir, "evrd", nil); stale || err != nil {
		t.Fatal("launched itself")
	}
}

func TestDir(t *testing.T) {
	t.Setenv("EVRD_UPDATE_DIR", "")
	os.Unsetenv("EVRD_UPDATE_DIR")
	t.Setenv("EVRD_CONFIG", "")
	conf := filepath.Join(t.TempDir(), "evrd.conf")
	os.WriteFile(conf, []byte("update_dir /data/up\n"), 0o644)
	for _, args := range [][]string{
		{"evrd", "-config", conf},
		{"evrd", "--config=" + conf},
		{"evrd", "-config=" + conf, "--", "-config", "nope"},
	} {
		if d := Dir(args); d != "/data/up" {
			t.Errorf("%v: %q", args, d)
		}
	}
	if d := Dir([]string{"evrd"}); d != config.DefaultUpdateDir {
		t.Errorf("default: %q", d)
	}
	if d := Dir([]string{"evrd", "-config", "/no/such/file"}); d != "" {
		t.Errorf("missing file: %q", d)
	}
	os.WriteFile(conf, []byte("self_update no\n"), 0o644)
	if d := Dir([]string{"evrd", "-config", conf}); d != "" {
		t.Errorf("self_update off: %q", d)
	}
	t.Setenv("EVRD_CONFIG", conf)
	os.WriteFile(conf, []byte("chroot /var/evrd\n"), 0o644)
	if d := Dir([]string{"evrd"}); d != "" {
		t.Errorf("chroot: %q", d)
	}
}

func TestWebDir(t *testing.T) {
	setVersion(t, "0.2.0")
	dir := t.TempDir()
	t.Setenv(envRunning, "")
	os.Unsetenv(envRunning)
	if WebDir(dir) != "" {
		t.Fatal("not an update")
	}
	t.Setenv(envRunning, "0.2.0")
	if WebDir(dir) != "" || WebDir("") != "" {
		t.Fatal("no web app")
	}
	os.MkdirAll(filepath.Join(dir, "0.2.0", "web"), 0o755)
	os.WriteFile(filepath.Join(dir, "0.2.0", "web", "index.html"), nil, 0o644)
	if WebDir(dir) != filepath.Join(dir, "0.2.0", "web") {
		t.Fatal(WebDir(dir))
	}
	if !strings.HasPrefix(userAgent(), "evrd/0.2.0 (") {
		t.Fatal(userAgent())
	}
}
