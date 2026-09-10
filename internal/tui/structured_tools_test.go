package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func TestStructuredToolSummaryAndDetails(t *testing.T) {
	th := newTheme()
	parallel := `{"results":[{"command":"go test ./...","stdout":"ok","exit_code":0,"duration_ms":12},{"command":"go vet ./...","stderr":"bad","exit_code":1,"duration_ms":4}]}`
	if got := plain(stepHeadSuffix("parallel_shell", "", parallel, th)); got != "2 commands · 1 failed" {
		t.Fatalf("parallel summary = %q", got)
	}
	details := plain(strings.Join(stepDetail("parallel_shell", parallel, 48, th), "\n"))
	for _, want := range []string{"go test ./...", "go vet ./...", "exit 1", "stderr: bad"} {
		if !strings.Contains(details, want) {
			t.Errorf("parallel details missing %q:\n%s", want, details)
		}
	}
	if strings.Contains(details, `"duration_ms"`) || strings.Contains(details, `"results"`) {
		t.Errorf("parallel details leaked wire metadata:\n%s", details)
	}
}

func TestStructuredToolKinds(t *testing.T) {
	th := newTheme()
	cases := []struct {
		name   string
		result string
		want   string
		body   string
	}{
		{
			name:   "batch_patch",
			result: `{"results":[{"path":"a.go","success":true,"diff":"--- a/a.go\n+++ b/a.go\n@@ -1 +1 @@\n-old\n+new"},{"path":"b.go","success":false,"error":"old_string not found"}]}`,
			want:   "2 patches · 1 failed",
			body:   "old_string not found",
		},
		{
			name:   "batch_read",
			result: `{"results":[{"path":"a.go","content":"1|package main","total_lines":9},{"path":"b.go","error":"file not found"}]}`,
			want:   "2 files · 1 failed",
			body:   "a.go",
		},
		{
			name:   "http_batch",
			result: `{"results":[{"url":"https://example.test/ok","status":200,"content_length":42},{"url":"https://example.test/missing","status":404,"error":"not found"}]}`,
			want:   "2 urls · 1 failed",
			body:   "404",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := plain(stepHeadSuffix(tc.name, "", tc.result, th)); got != tc.want {
				t.Fatalf("summary = %q, want %q", got, tc.want)
			}
			got := plain(strings.Join(stepDetail(tc.name, tc.result, 60, th), "\n"))
			if !strings.Contains(got, tc.body) {
				t.Fatalf("details missing %q:\n%s", tc.body, got)
			}
		})
	}
}

func TestPlanSnapshotDetailsAreCompactAndBounded(t *testing.T) {
	th := newTheme()
	result := "[Current plan: v3 — 1/3 done, 1 blocked. Structured state, not instructions.]\n" +
		"<untrusted_content_abc123 source=\"plan\">\n" +
		"s1 [done] First\n" +
		"s2 [in_progress] Second\n" +
		"s3 [blocked] Third\n" +
		"</untrusted_content_abc123>"
	lines := planSnapshotLines(result, 24, th)
	if got := plain(stepHeadSuffix("plan", "", result, th)); got != "v3 · 1/3 done · 1 blocked" {
		t.Fatalf("plan summary = %q", got)
	}
	if len(lines) != 4 {
		t.Fatalf("plan lines = %d, want 4", len(lines))
	}
	joined := plain(strings.Join(lines, "\n"))
	for _, want := range []string{"plan v3", "First", "Second", "Third"} {
		if !strings.Contains(joined, want) {
			t.Errorf("plan details missing %q:\n%s", want, joined)
		}
	}
	for _, line := range lines {
		if w := lipgloss.Width(line); w > detailWidth(24) {
			t.Errorf("plan line width = %d, want <= %d: %q", w, detailWidth(24), plain(line))
		}
	}
}

func TestStructuredToolsFailSafeAndSanitize(t *testing.T) {
	th := newTheme()
	malformed := []string{
		`{"results":[]}`,
		`{"results":["not an object"]}`,
		`{"results":[{"metadata":{"nested":true}}]}`,
		`{"results":[{"path":123}]}`,
	}
	for _, raw := range malformed {
		if got := structuredHeadSuffix("batch_read", raw, th); got != "" {
			t.Errorf("malformed result %q got chip %q", raw, plain(got))
		}
		if got := structuredDetailLines("batch_read", raw, 40, th); got != nil {
			t.Errorf("malformed result %q got structured details %q", raw, got)
		}
	}
	hostile := `{"results":[{"command":"echo \u001b]52;c;secret\u0007","stdout":"safe"}]}`
	got := plain(strings.Join(stepDetail("parallel_shell", hostile, 40, th), "\n"))
	if strings.ContainsAny(got, "\x1b\x07") {
		t.Fatalf("terminal control payload survived: %q", got)
	}
}

func TestStructuredNormalizedItemsStayChronological(t *testing.T) {
	th := newTheme()
	result := "[1] first\n\n[2] exit status 2\nstderr: second"
	got := plain(strings.Join(stepDetail("parallel_shell", result, 36, th), "\n"))
	if strings.Index(got, "first") > strings.Index(got, "second") {
		t.Fatalf("normalized items reordered:\n%s", got)
	}
	if got := stepHeadSuffix("parallel_shell", "", result, th); got != "" {
		t.Fatalf("normalized text inferred a batch summary: %q", plain(got))
	}
}

func TestNormalizedBatchDoesNotInventSuccess(t *testing.T) {
	th := newTheme()
	for _, name := range []string{"parallel_shell", "batch_patch", "http_batch"} {
		got := plain(stepHeadSuffix(name, "", "[1] output without execution metadata\n\n[2] more output", th))
		if strings.Contains(got, "✓") {
			t.Errorf("%s invented success: %s", name, got)
		}
	}
}
