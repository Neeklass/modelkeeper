// Package update looks for a newer release on GitHub (TODO 6.4). It only
// reads the public release list; downloading and installing stay with the
// user (the release page opens in the browser), so nothing on the PC changes.
package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// URL of the newest release (a variable for tests).
var URL = "https://api.github.com/repos/Neeklass/modelkeeper/releases/latest"

// Release is a published version.
type Release struct {
	Version   string `json:"version"` // e.g. "v0.12.0"
	URL       string `json:"url"`     // release page
	Published string `json:"published"`
	Notes     string `json:"notes"` // release notes (shortened)
}

// ErrUnavailable: no release could be read (offline, none published yet).
var ErrUnavailable = errors.New("no release information")

// Latest reads the newest release.
func Latest(ctx context.Context, client *http.Client) (Release, error) {
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, URL, nil)
	if err != nil {
		return Release{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "modelkeeper-update-check")
	resp, err := client.Do(req)
	if err != nil {
		return Release{}, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Release{}, fmt.Errorf("%w: HTTP %d", ErrUnavailable, resp.StatusCode)
	}
	var r struct {
		Tag        string `json:"tag_name"`
		HTMLURL    string `json:"html_url"`
		Published  string `json:"published_at"`
		Body       string `json:"body"`
		Draft      bool   `json:"draft"`
		Prerelease bool   `json:"prerelease"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&r); err != nil {
		return Release{}, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	if r.Tag == "" || r.Draft || r.Prerelease || !strings.HasPrefix(r.HTMLURL, "https://github.com/") {
		return Release{}, ErrUnavailable
	}
	notes := r.Body
	if len([]rune(notes)) > 2000 {
		notes = string([]rune(notes)[:2000]) + "…"
	}
	return Release{Version: r.Tag, URL: r.HTMLURL, Published: r.Published, Notes: notes}, nil
}

// parse reads "v1.2.3" (also "1.2", "v1.2.3-4-gabc"); ok=false for others ("dev-...").
func parse(v string) (n [3]int, ok bool) {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexAny(v, "-+ "); i >= 0 {
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	if len(parts) == 0 || len(parts) > 3 {
		return n, false
	}
	for i, p := range parts {
		x, err := strconv.Atoi(p)
		if err != nil || x < 0 {
			return n, false
		}
		n[i] = x
	}
	return n, true
}

// Newer reports whether release is newer than current. A development build
// ("dev-…") is never told to update.
func Newer(current, release string) bool {
	c, ok1 := parse(current)
	r, ok2 := parse(release)
	if !ok1 || !ok2 {
		return false
	}
	for i := 0; i < 3; i++ {
		if r[i] != c[i] {
			return r[i] > c[i]
		}
	}
	return false
}
