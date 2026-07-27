// Package update queries GitHub for the latest bodek release. It is shared by
// the `bodek upgrade` command and the TUI's startup update hint.
package update

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

// LatestURL is the GitHub API endpoint for the latest bodek release.
const LatestURL = "https://api.github.com/repos/BackendStack21/bodek/releases/latest"

// release is the subset of the GitHub releases API we consume.
type release struct {
	TagName string `json:"tag_name"`
	Assets  []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
	} `json:"assets"`
}

// LatestRelease fetches the latest published release and returns its tag
// (e.g. "v0.0.10") plus a name → download URL map of its assets.
func LatestRelease(ctx context.Context, client *http.Client, apiURL string) (tag string, assets map[string]string, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return "", nil, fmt.Errorf("build latest-release request: %w", err)
	}
	req.Header.Set("User-Agent", "bodek-updater")
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := client.Do(req)
	if err != nil {
		return "", nil, fmt.Errorf("query latest release: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", nil, fmt.Errorf("query latest release: unexpected status %s", resp.Status)
	}
	var rel release
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return "", nil, fmt.Errorf("decode latest release: %w", err)
	}
	if rel.TagName == "" {
		return "", nil, fmt.Errorf("latest release has no tag_name")
	}
	assets = make(map[string]string, len(rel.Assets))
	for _, a := range rel.Assets {
		assets[a.Name] = a.URL
	}
	return rel.TagName, assets, nil
}

// Newer reports whether latest is a higher version than current. Both may
// carry a "v" prefix; comparison is numeric over up to 3 dot-separated
// components, with missing components treated as 0. Anything unparsable —
// including a dev build's "dev" or an empty string — reports false, so a
// failed or skipped check never nags.
func Newer(latest, current string) bool {
	l, lok := parseSemver(latest)
	c, cok := parseSemver(current)
	if !lok || !cok {
		return false
	}
	for i := range l {
		if l[i] != c[i] {
			return l[i] > c[i]
		}
	}
	return false
}

// parseSemver splits an optional-"v"-prefixed version into its numeric
// components, zero-padding to 3. Non-numeric components fail the parse.
func parseSemver(v string) ([3]int, bool) {
	var out [3]int
	parts := strings.Split(strings.TrimPrefix(strings.TrimSpace(v), "v"), ".")
	if len(parts) > 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return out, false
		}
		out[i] = n
	}
	return out, true
}
