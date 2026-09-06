// Package update queries GitHub for the latest bodek release. It is shared by
// the `bodek upgrade` command and the TUI's startup update hint.
package update

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
)

const (
	// LatestURL is the GitHub API endpoint for the latest bodek release.
	LatestURL = "https://api.github.com/repos/BackendStack21/bodek/releases/latest"
	// PageLatestURL is the HTML latest-release redirect. GitHub's anonymous
	// API quota (and occasional IP blocks) return 403 on LatestURL; this
	// path is not rate-limited the same way.
	PageLatestURL = "https://github.com/BackendStack21/bodek/releases/latest"

	userAgent  = "bodek-updater"
	apiVersion = "2022-11-28"
)

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
// A 401/403/429 against the real API falls back to the HTML latest page.
func LatestRelease(ctx context.Context, client *http.Client, apiURL string) (tag string, assets map[string]string, err error) {
	page := ""
	if apiURL == LatestURL {
		page = PageLatestURL
	}
	return fetchLatest(ctx, client, apiURL, page)
}

func fetchLatest(ctx context.Context, client *http.Client, apiURL, pageURL string) (string, map[string]string, error) {
	tag, assets, err := queryAPI(ctx, client, apiURL)
	if err == nil {
		return tag, assets, nil
	}
	if pageURL == "" || !statusRetryable(err) {
		return "", nil, err
	}
	tag, assets, ferr := queryLatestPage(ctx, client, pageURL)
	if ferr != nil {
		return "", nil, fmt.Errorf("%w; html fallback: %v", err, ferr)
	}
	return tag, assets, nil
}

func queryAPI(ctx context.Context, client *http.Client, apiURL string) (string, map[string]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return "", nil, fmt.Errorf("build latest-release request: %w", err)
	}
	applyGitHubHeaders(req)
	resp, err := client.Do(req)
	if err != nil {
		return "", nil, fmt.Errorf("query latest release: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", nil, statusError(resp.StatusCode, resp.Status)
	}
	var rel release
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return "", nil, fmt.Errorf("decode latest release: %w", err)
	}
	if rel.TagName == "" {
		return "", nil, fmt.Errorf("latest release has no tag_name")
	}
	assets := make(map[string]string, len(rel.Assets))
	for _, a := range rel.Assets {
		assets[a.Name] = a.URL
	}
	return rel.TagName, assets, nil
}

func queryLatestPage(ctx context.Context, client *http.Client, pageURL string) (string, map[string]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pageURL, nil)
	if err != nil {
		return "", nil, fmt.Errorf("build latest-page request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := client.Do(req)
	if err != nil {
		return "", nil, fmt.Errorf("query latest page: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	final := resp.Request.URL
	if loc := resp.Header.Get("Location"); loc != "" && (resp.StatusCode == http.StatusFound ||
		resp.StatusCode == http.StatusMovedPermanently || resp.StatusCode == http.StatusTemporaryRedirect ||
		resp.StatusCode == http.StatusPermanentRedirect) {
		if u, perr := resp.Request.URL.Parse(loc); perr == nil {
			final = u
		}
	}
	tag, ok := tagFromURL(final)
	if !ok {
		return "", nil, fmt.Errorf("latest page did not redirect to a release tag (%s)", resp.Status)
	}
	return tag, conventionalAssets(tag), nil
}

func tagFromURL(u *url.URL) (string, bool) {
	if u == nil {
		return "", false
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	for i := 0; i+1 < len(parts); i++ {
		if parts[i] == "releases" && parts[i+1] == "tag" && i+2 < len(parts) && parts[i+2] != "" {
			return parts[i+2], true
		}
	}
	return "", false
}

func conventionalAssets(tag string) map[string]string {
	bare := strings.TrimPrefix(tag, "v")
	base := "https://github.com/BackendStack21/bodek/releases/download/" + tag + "/"
	assets := map[string]string{"checksums.txt": base + "checksums.txt"}
	for _, goos := range []string{"darwin", "linux", "windows"} {
		for _, arch := range []string{"amd64", "arm64"} {
			ext := "tar.gz"
			if goos == "windows" {
				ext = "zip"
			}
			name := fmt.Sprintf("bodek_%s_%s_%s.%s", bare, goos, arch, ext)
			assets[name] = base + name
		}
	}
	return assets
}

func applyGitHubHeaders(req *http.Request) {
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", apiVersion)
	if tok := githubToken(); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
}

func githubToken() string {
	if t := os.Getenv("GITHUB_TOKEN"); t != "" {
		return t
	}
	return os.Getenv("GH_TOKEN")
}

func statusError(code int, status string) error {
	if code == http.StatusUnauthorized || code == http.StatusForbidden || code == http.StatusTooManyRequests {
		return fmt.Errorf("query latest release: unexpected status %s (GitHub rate limit or anonymous block — set GITHUB_TOKEN)", status)
	}
	return fmt.Errorf("query latest release: unexpected status %s", status)
}

func statusRetryable(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "401") || strings.Contains(s, "403") || strings.Contains(s, "429")
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
