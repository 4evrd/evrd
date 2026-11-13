package update

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Release is a published evrd version.
type Release struct {
	Version   string    `json:"version"` // without the "v"
	URL       string    `json:"url"`     // its release page
	Notes     string    `json:"notes,omitempty"`
	Published time.Time `json:"published"`
	// Assets are its files: name → download URL.
	Assets map[string]string `json:"assets,omitempty"`
}

// maxNotes caps the release notes kept (they're shown, not parsed).
const maxNotes = 16 << 10

// githubRelease is the part of GitHub's release JSON evrd reads
// (GET /repos/{owner}/{repo}/releases/latest).
type githubRelease struct {
	TagName     string    `json:"tag_name"`
	HTMLURL     string    `json:"html_url"`
	Body        string    `json:"body"`
	PublishedAt time.Time `json:"published_at"`
	Draft       bool      `json:"draft"`
	Prerelease  bool      `json:"prerelease"`
	Assets      []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
	} `json:"assets"`
}

// fetchLatest reads the release feed at src.
func fetchLatest(ctx context.Context, hc *http.Client, src, ua string) (*Release, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, src, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", ua)
	req.Header.Set("Accept", "application/vnd.github+json, application/json")
	res, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s answered %s", hostOf(src), res.Status)
	}
	var g githubRelease
	if err := json.NewDecoder(io.LimitReader(res.Body, 4<<20)).Decode(&g); err != nil {
		return nil, fmt.Errorf("the release feed: %w", err)
	}
	if g.Draft || g.Prerelease {
		return nil, fmt.Errorf("the release feed's latest is a draft or a pre-release")
	}
	v := clean(g.TagName)
	if !Valid(v) {
		return nil, fmt.Errorf("the release feed's latest has no version (%q)", g.TagName)
	}
	r := &Release{Version: v, URL: httpURL(g.HTMLURL), Published: g.PublishedAt, Assets: map[string]string{}}
	if len(g.Body) > maxNotes {
		g.Body = g.Body[:maxNotes]
	}
	r.Notes = g.Body
	for _, a := range g.Assets {
		if u := httpURL(a.URL); u != "" && a.Name != "" {
			r.Assets[a.Name] = u
		}
	}
	return r, nil
}

// httpURL is u when it's an http(s) URL, else "".
func httpURL(u string) string {
	p, err := url.Parse(u)
	if err != nil || (p.Scheme != "https" && p.Scheme != "http") || p.Host == "" {
		return ""
	}
	return u
}

func hostOf(u string) string {
	if p, err := url.Parse(u); err == nil && p.Host != "" {
		return p.Host
	}
	return strings.SplitN(u, "/", 2)[0]
}
