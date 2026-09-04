package tui

import (
	"strings"
	"testing"
)

// read_file-style JSON envelope: Go's encoding/json HTML-escapes <, >, & in
// the wrapped content, so the raw result exposes \u003c untrusted_content_…
// noise. resultPreview must decode the envelope and fold the wrapper away
// entirely — the body renders bare, with scalar metadata as a footer.
func TestResultPreviewJSONEnvelope(t *testing.T) {
	raw := `{"content":"<untrusted_content_dd96e237d3be7447 source=\"/path/to/file.go\">\n217|\n218|\tta := textarea.New()\n</untrusted_content_dd96e237d3be7447>","total_lines":300}`
	got := resultPreview(raw)
	if strings.Contains(got, `<`) || strings.Contains(got, "untrusted_content_") || strings.Contains(got, "⚠") {
		t.Errorf("wrapper or badge noise still visible:\n%s", got)
	}
	if !strings.Contains(got, "218|\tta := textarea.New()") { // tabs survive sanitize (copy fidelity)
		t.Errorf("body line numbers not intact:\n%s", got)
	}
	if !strings.Contains(got, "total_lines: 300") {
		t.Errorf("missing scalar metadata footer:\n%s", got)
	}
}

// Live (non-JSON) tool results carry the wrapper literally; it folds away,
// leaving only the body.
func TestResultPreviewLiteralWrapper(t *testing.T) {
	raw := "<untrusted_content_ab12cd34 source=\"/etc/hosts\">\n127.0.0.1 localhost\n</untrusted_content_ab12cd34>"
	got := resultPreview(raw)
	if strings.Contains(got, "untrusted_content_") || strings.Contains(got, "⚠") {
		t.Errorf("wrapper tags or badge still visible:\n%s", got)
	}
	if !strings.Contains(got, "127.0.0.1 localhost") {
		t.Errorf("body lost:\n%s", got)
	}
}

// Plain, non-JSON tool output renders byte-identical.
func TestResultPreviewPlainUnchanged(t *testing.T) {
	for _, s := range []string{
		"",
		"plain output",
		"exit status 1\nFAIL TestX",
		"line with <angle> & \"quotes\" kept as-is",
		`{"matches":[{"path":"a.go"}]}`,    // JSON without a string content field
		`{"content":"x","nested":{"a":1}}`, // non-scalar metadata: not a known envelope
	} {
		if got := resultPreview(s); got != s {
			t.Errorf("resultPreview(%q) = %q, want unchanged", s, got)
		}
	}
}

// A malformed envelope (truncated JSON) renders raw, never lossy.
func TestResultPreviewMalformedEnvelope(t *testing.T) {
	raw := `{"content":"<untrusted_content_ab`
	if got := resultPreview(raw); got != raw {
		t.Errorf("malformed envelope rewritten: got %q", got)
	}
}

// Parallel tools (parallel_shell) return a top-level results array of per-
// call objects. resultPreview extracts each item's display body — stdout,
// stderr, non-zero exit codes — and drops the JSON noise (command echoes,
// index, duration). Wrappers inside stdout fold away entirely.
func TestResultPreviewParallelResults(t *testing.T) {
	raw := `{"results":[{"index":0,"command":"gofmt -l .","description":"check formatting","stdout":"\u003cuntrusted_content_abc123 source=\"parallel_shell:0:stdout\"\u003e\nREADME.md\n\u003c/untrusted_content_abc123\u003e","stderr":"","exit_code":0,"duration_ms":12},{"index":1,"command":"go vet ./...","description":"vet","stdout":"","stderr":"vets hate this","exit_code":1,"duration_ms":300}]}`
	got := resultPreview(raw)
	for _, want := range []string{"[1] README.md", "[2] exit status 1", "stderr: vets hate this"} {
		if !strings.Contains(got, want) {
			t.Errorf("parallel results missing %q in:\n%s", want, got)
		}
	}
	for _, banned := range []string{"{", "}", "\"command\"", "gofmt -l", "go vet ./...", "index", "duration_ms", "description", "untrusted", "⚠"} {
		if strings.Contains(got, banned) {
			t.Errorf("parallel results leak %q:\n%s", banned, got)
		}
	}
}

// A single-item results array renders the body bare — no index label.
func TestResultPreviewSingleResult(t *testing.T) {
	raw := `{"results":[{"index":0,"command":"ls","stdout":"main.go\nutil.go","stderr":"","exit_code":0,"duration_ms":5}]}`
	got := resultPreview(raw)
	if !strings.Contains(got, "main.go") || !strings.Contains(got, "util.go") {
		t.Errorf("stdout body lost:\n%s", got)
	}
	if strings.Contains(got, "[1]") || strings.Contains(got, "ls") || strings.Contains(got, "index") {
		t.Errorf("single result carries labels or arg echoes:\n%s", got)
	}
}

// Non-stdout result shapes (delegate_tasks headlines) still extract.
func TestResultPreviewResultsHeadline(t *testing.T) {
	raw := `{"results":[{"headline":"built 3 sub-agents","artifacts":[{"id":"a1","path":"x.go","bytes":10}],"cost_usd":0.5}]}`
	got := resultPreview(raw)
	if !strings.Contains(got, "built 3 sub-agents") {
		t.Errorf("headline body lost:\n%s", got)
	}
	if strings.Contains(got, "artifacts") || strings.Contains(got, "cost_usd") {
		t.Errorf("nested metadata leaked:\n%s", got)
	}
}

// A non-zero exit_code anywhere in a parallel envelope flags the step as
// failed — even when the extracted display text looks innocuous.
func TestHasFailedExit(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want bool
	}{
		{"{\"results\":[{\"exit_code\":0}]}", false},
		{"{\"results\":[{\"stdout\":\"all good\"}]}", false},
		{"{\"results\":[{\"exit_code\":1,\"stdout\":\"hmm\"}]}", true},
		{"{\"results\":[{\"exit_code\":0},{\"exit_code\":2}]}", true},
		{"plain output", false},
		{"{\"other\":1}", false},
	} {
		if got := hasFailedExit(tc.raw); got != tc.want {
			t.Errorf("hasFailedExit(%q) = %v, want %v", tc.raw, got, tc.want)
		}
	}
}

// Unknown results shapes are never rewritten — the fail-safe holds.
func TestResultPreviewResultsFailSafe(t *testing.T) {
	for _, s := range []string{
		`{"results":[]}`,
		`{"results":["plain string"]}`,
		`{"results":[{"weird":{"a":1}}]}`,
		`{"results":[{"other":"only unknown scalars"}]}`,
	} {
		if got := resultPreview(s); got != s {
			t.Errorf("resultPreview(%q) = %q, want unchanged", s, got)
		}
	}
}
