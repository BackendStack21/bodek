package update

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNewer(t *testing.T) {
	cases := []struct {
		latest, current string
		want            bool
	}{
		{"v0.0.12", "0.0.11", true},
		{"0.0.12", "v0.0.11", true},
		{"v0.0.12", "0.0.12", false}, // equal
		{"v0.0.9", "0.0.10", false},  // older latest
		{"v1.0.0", "0.9.9", true},    // major bump
		{"v0.1.0", "0.0.99", true},   // minor bump
		{"v0.0.12", "dev", false},    // dev build never nags
		{"v0.0.12", "", false},       // unstamped build
		{"dev", "0.0.11", false},     // unparsable latest
		{"v0.1", "0.0.9", true},      // two-component latest
		{"v0.0.12", "0.0", true},     // two-component current pads to 0
		{"v0.0.1.2", "0.0.1", false}, // four components: unparsable
		{"v0.0.x", "0.0.1", false},   // non-numeric component
	}
	for _, tc := range cases {
		if got := Newer(tc.latest, tc.current); got != tc.want {
			t.Errorf("Newer(%q, %q) = %v, want %v", tc.latest, tc.current, got, tc.want)
		}
	}
}

func TestLatestRelease(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("User-Agent"); got != "bodek-updater" {
			t.Errorf("expected User-Agent bodek-updater, got %q", got)
		}
		if got := r.Header.Get("Accept"); got != "application/vnd.github+json" {
			t.Errorf("expected Accept application/vnd.github+json, got %q", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"tag_name": "v9.9.9",
			"assets": []map[string]string{
				{"name": "bodek_9.9.9_linux_amd64.tar.gz", "browser_download_url": "https://example.test/dl"},
			},
		})
	}))
	t.Cleanup(srv.Close)

	tag, assets, err := LatestRelease(context.Background(), srv.Client(), srv.URL)
	if err != nil {
		t.Fatalf("LatestRelease: %v", err)
	}
	if tag != "v9.9.9" {
		t.Errorf("tag = %q, want %q", tag, "v9.9.9")
	}
	if assets["bodek_9.9.9_linux_amd64.tar.gz"] != "https://example.test/dl" {
		t.Errorf("assets = %v", assets)
	}
}

func TestLatestReleaseHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	t.Cleanup(srv.Close)

	_, _, err := LatestRelease(context.Background(), srv.Client(), srv.URL)
	if err == nil || !strings.Contains(err.Error(), "unexpected status") {
		t.Fatalf("expected status error, got %v", err)
	}
}

func TestLatestReleaseMissingTag(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"assets": []any{}})
	}))
	t.Cleanup(srv.Close)

	_, _, err := LatestRelease(context.Background(), srv.Client(), srv.URL)
	if err == nil || !strings.Contains(err.Error(), "no tag_name") {
		t.Fatalf("expected missing-tag error, got %v", err)
	}
}

func TestLatestReleaseBadJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("not json"))
	}))
	t.Cleanup(srv.Close)

	_, _, err := LatestRelease(context.Background(), srv.Client(), srv.URL)
	if err == nil || !strings.Contains(err.Error(), "decode") {
		t.Fatalf("expected decode error, got %v", err)
	}
}

func TestLatestReleaseUnreachable(t *testing.T) {
	if _, _, err := LatestRelease(context.Background(), &http.Client{}, "http://127.0.0.1:1"); err == nil {
		t.Fatal("expected query error to unreachable host")
	}
}
