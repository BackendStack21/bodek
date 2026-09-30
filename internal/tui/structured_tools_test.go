package tui

import (
	"reflect"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

var retiredToolNames = []string{"parallel_shell", "batch_patch", "batch_read", "multi_grep", "http_batch"}

func TestRetiredToolsHaveNoTypedRendering(t *testing.T) {
	th := newTheme()
	for _, name := range retiredToolNames {
		for _, variant := range []string{name, " " + strings.ToUpper(name) + " "} {
			if toolGlyph(variant) != "✦" || isShellTool(variant) || isSearchTool(variant) || fileReadTool(variant) || touchedPath(variant, "a.go") != "" {
				t.Errorf("%q matched a supported tool family", variant)
			}
			if got := toolProgress(variant, "go test ./..."); got != "🔧 running "+variant {
				t.Errorf("retired progress = %q", got)
			}
			if got := invocationText(step{name: variant, callArgs: `{"command":"go test ./..."}`}); !strings.HasPrefix(got, "invocation · arguments\n") {
				t.Errorf("retired tool got a shell invocation: %q", got)
			}
			for _, raw := range []string{
				`{"results":[{"command":"echo x","stdout":"safe","exit_code":0,"duration_ms":5,"path":"a.go","success":true,"status":200}]}`,
				`{"results":[]}`, `{"results":["not an object"]}`, `{"results":[{"metadata":{"nested":true}}]}`, `{"results":[{"path":123}]}`,
				`{"content":"keep envelope","total_lines":1}`,
				"@@ -1 +1 @@\n-old\n+new", "```diff\n-old\n+new\n```",
				"found 3 matches", "ok  example.test/pkg  0.5s", "[2] warning",
				`{"results":[{"command":"echo \u001b]52;c;secret\u0007","stdout":"safe"}]}`,
				"plain \x1b]52;c;secret\x07", `{"results":`,
			} {
				result := toolResultPreview(variant, raw)
				if got := stepHeadSuffix(variant, "go test ./...", result, th); got != "" {
					t.Errorf("%s inferred a typed summary: %q", name, plain(got))
				}
				got := stepDetail(variant, result, 60, th)
				if want := genericDetail(result, 60, th); !reflect.DeepEqual(got, want) {
					t.Errorf("%s bypassed generic rendering: %q", name, got)
				}
				if strings.ContainsAny(plain(strings.Join(got, "\n")), "\x1b\x07") {
					t.Errorf("%s exposed terminal controls", name)
				}
				if got := scanReceipt(message{steps: []step{{name: variant, arg: "a.go", result: result}}}); got != (receipt{}) {
					t.Errorf("%s contributed a typed receipt: %+v", name, got)
				}
			}
		}
	}
}

func TestSupportedReplacementToolsKeepRendering(t *testing.T) {
	th := newTheme()
	for _, tc := range []struct{ name, arg, raw, glyph, chip, body string }{
		{"shell", "go test ./...", "ok  example.test/pkg  0.5s", "❯", "✓ tests pass", "ok"},
		{"patch", "a.go", "@@ -1 +1 @@\n-old\n+new", "✎", "  +1 −1", "+new"},
		{"read_file", "a.go", `{"content":"package main","total_lines":1}`, "◰", "", "1 │ package main"},
		{"search_files", "needle", "found 3 matches", "⌕", "3 hits", "found 3 matches"},
		{"http_request", "https://example.test", `{"status":200,"body":"response"}`, "⌖", "", `"status": 200`},
	} {
		result := toolResultPreview(tc.name, tc.raw)
		if got := toolGlyph(tc.name); got != tc.glyph {
			t.Errorf("%s glyph = %q", tc.name, got)
		}
		if got := plain(stepHeadSuffix(tc.name, tc.arg, result, th)); got != tc.chip {
			t.Errorf("%s chip = %q, want %q", tc.name, got, tc.chip)
		}
		if got := plain(strings.Join(stepDetail(tc.name, result, 80, th), "\n")); !strings.Contains(got, tc.body) {
			t.Errorf("%s detail missing %q: %s", tc.name, tc.body, got)
		}
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
