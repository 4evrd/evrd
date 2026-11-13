package update

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

// The release files evrd installs from: one archive per platform (what
// scripts/dist.sh builds) and their checksums.
const checksumsAsset = "SHA256"

// maxArchive caps a downloaded archive.
const maxArchive = 512 << 20

// ArchiveName is this platform's release archive.
func ArchiveName() string { return "evrd_" + runtime.GOOS + "_" + runtime.GOARCH + ".tar.gz" }

// stateFile records the installed update in Dir.
const stateFile = "current.json"

// installed is what stateFile holds.
type installed struct {
	// Version is the update's; Base is the program it was installed over
	// (the image's evrd). A different program at start (a new or pinned
	// image) means the update no longer applies.
	Version     string    `json:"version"`
	Base        string    `json:"base"`
	InstalledAt time.Time `json:"installedAt"`
	By          string    `json:"by,omitempty"`
}

// stage downloads r's archive for this platform into dir, checks it
// against the release's checksums, unpacks evrd, evrctl and the web app
// into dir/<version>, checks the new evrd runs, and records it as the
// update to run over base.
func stage(ctx context.Context, hc *http.Client, ua, dir string, r *Release, base, by string) error {
	name := ArchiveName()
	archiveURL, sumsURL := r.Assets[name], r.Assets[checksumsAsset]
	if archiveURL == "" {
		return fmt.Errorf("release %s has no %s", r.Version, name)
	}
	if sumsURL == "" {
		return fmt.Errorf("release %s has no %s file to check the download against", r.Version, checksumsAsset)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	sums, err := download(ctx, hc, ua, sumsURL, 1<<20)
	if err != nil {
		return fmt.Errorf("checksums: %w", err)
	}
	want, err := checksumFor(sums, name)
	if err != nil {
		return err
	}

	tmp, err := os.CreateTemp(dir, ".download-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	h := sha256.New()
	if err := downloadTo(ctx, hc, ua, archiveURL, maxArchive, io.MultiWriter(tmp, h)); err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != want {
		return fmt.Errorf("%s: checksum mismatch (got %s, the release says %s)", name, got, want)
	}

	work := filepath.Join(dir, "."+r.Version+".tmp")
	os.RemoveAll(work)
	defer os.RemoveAll(work)
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		return err
	}
	if err := unpack(tmp, work); err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	if err := checkRuns(ctx, filepath.Join(work, "evrd"), r.Version); err != nil {
		return err
	}
	final := filepath.Join(dir, r.Version)
	os.RemoveAll(final)
	if err := os.Rename(work, final); err != nil {
		return err
	}
	if err := writeState(dir, installed{Version: r.Version, Base: base, InstalledAt: time.Now().UTC(), By: by}); err != nil {
		return err
	}
	prune(dir, r.Version)
	return nil
}

// download reads url, up to max bytes.
func download(ctx context.Context, hc *http.Client, ua, url string, max int64) ([]byte, error) {
	var b bytes.Buffer
	err := downloadTo(ctx, hc, ua, url, max, &b)
	return b.Bytes(), err
}

func downloadTo(ctx context.Context, hc *http.Client, ua, url string, max int64, w io.Writer) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", ua)
	res, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("%s answered %s", hostOf(url), res.Status)
	}
	n, err := io.Copy(w, io.LimitReader(res.Body, max+1))
	if err != nil {
		return err
	}
	if n > max {
		return fmt.Errorf("bigger than %d bytes", max)
	}
	return nil
}

var (
	bsdSum = regexp.MustCompile(`^SHA256 \((.+)\) = ([0-9a-fA-F]{64})$`)
	gnuSum = regexp.MustCompile(`^([0-9a-fA-F]{64}) [ *]?(.+)$`)
)

// checksumFor finds name's SHA-256 in a checksums file, BSD style
// ("SHA256 (name) = hex", what dist.sh writes) or GNU style ("hex  name").
func checksumFor(sums []byte, name string) (string, error) {
	sc := bufio.NewScanner(bytes.NewReader(sums))
	for sc.Scan() {
		l := strings.TrimSpace(sc.Text())
		if m := bsdSum.FindStringSubmatch(l); m != nil && path.Base(m[1]) == name {
			return strings.ToLower(m[2]), nil
		}
		if m := gnuSum.FindStringSubmatch(l); m != nil && path.Base(strings.TrimSpace(m[2])) == name {
			return strings.ToLower(m[1]), nil
		}
	}
	return "", fmt.Errorf("the release's %s has no checksum for %s", checksumsAsset, name)
}

// unpack extracts the archive's evrd, evrctl and web app (under its one
// top directory, evrd_<os>_<arch>/) into dir. Anything else is left out.
func unpack(r io.Reader, dir string) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	var got []string
	for {
		hd, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		name := path.Clean(strings.TrimPrefix(hd.Name, "./"))
		_, rel, ok := strings.Cut(name, "/") // drop the top directory
		if !ok || rel == "" || strings.HasPrefix(rel, "../") || rel == ".." || path.IsAbs(rel) {
			continue
		}
		keep := rel == "evrd" || rel == "evrctl" || rel == "web" || strings.HasPrefix(rel, "web/")
		if !keep {
			continue
		}
		dst := filepath.Join(dir, filepath.FromSlash(rel))
		switch hd.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(dst, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				return err
			}
			mode := os.FileMode(0o644)
			if rel == "evrd" || rel == "evrctl" {
				mode = 0o755
			}
			f, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
			if err != nil {
				return err
			}
			_, err = io.Copy(f, io.LimitReader(tr, maxArchive))
			if cerr := f.Close(); err == nil {
				err = cerr
			}
			if err != nil {
				return err
			}
			got = append(got, rel)
		}
	}
	for _, need := range []string{"evrd", "evrctl"} {
		if _, err := os.Stat(filepath.Join(dir, need)); err != nil {
			return fmt.Errorf("the archive has no %s", need)
		}
	}
	return nil
}

// checkRuns runs the new evrd -version: it must start on this machine and
// be the version the release says.
func checkRuns(ctx context.Context, bin, version string) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "-version").CombinedOutput()
	if err != nil {
		return fmt.Errorf("the new evrd doesn't run here: %v %s", err, strings.TrimSpace(string(out)))
	}
	if got := strings.TrimSpace(string(out)); got != "evrd "+version {
		return fmt.Errorf("the new evrd says %q, not version %s", got, version)
	}
	return nil
}

func writeState(dir string, s installed) error {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(dir, "."+stateFile+".tmp")
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, stateFile))
}

// readState is the installed update, if any.
func readState(dir string) (installed, error) {
	var s installed
	b, err := os.ReadFile(filepath.Join(dir, stateFile))
	if err != nil {
		return s, err
	}
	if err := json.Unmarshal(b, &s); err != nil {
		return s, err
	}
	if !Valid(s.Version) {
		return s, fmt.Errorf("%s: no version", stateFile)
	}
	return s, nil
}

// prune removes the other versions' directories (a running one's program
// stays usable until it exits).
func prune(dir, keep string) {
	ents, _ := os.ReadDir(dir)
	for _, e := range ents {
		if e.IsDir() && e.Name() != keep && Valid(e.Name()) {
			os.RemoveAll(filepath.Join(dir, e.Name()))
		}
	}
}

// Clear forgets the installed update: the next start runs the program
// that was installed (the image's evrd).
func Clear(dir string) error {
	err := os.Remove(filepath.Join(dir, stateFile))
	prune(dir, "")
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
