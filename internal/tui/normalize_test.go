package tui

import (
	"strings"
	"testing"
)

// read_file-style JSON envelope: Go's encoding/json HTML-escapes <, >, & in
// the wrapped content, so the raw result exposes < untrusted_content_…
// noise. resultPreview must decode the envelope and fold the wrapper.
func TestResultPreviewJSONEnvelope(t *testing.T) {
	raw := `{"content":"<untrusted_content_dd96e237d3be7447 source=\"/path/to/file.go\">\n217|\n218|\tta := textarea.New()\n</untrusted_content_dd96e237d3be7447>","total_lines":300}`
	got := resultPreview(raw)
	if strings.Contains(got, `<`) || strings.Contains(got, "untrusted_content_") {
		t.Errorf("escaped wrapper still visible:\n%s", got)
	}
	if !strings.Contains(got, "⚠ untrusted: /path/to/file.go") {
		t.Errorf("missing source badge:\n%s", got)
	}
	if !strings.Contains(got, "218|\tta := textarea.New()") {
		t.Errorf("body line numbers not intact:\n%s", got)
	}
	if !strings.Contains(got, "total_lines: 300") {
		t.Errorf("missing scalar metadata footer:\n%s", got)
	}
}

// Live (non-JSON) tool results carry the wrapper literally; it folds into a
// badge line plus the body.
func TestResultPreviewLiteralWrapper(t *testing.T) {
	raw := "<untrusted_content_ab12cd34 source=\"/etc/hosts\">\n127.0.0.1 localhost\n</untrusted_content_ab12cd34>"
	got := resultPreview(raw)
	if strings.Contains(got, "untrusted_content_") {
		t.Errorf("wrapper tags still visible:\n%s", got)
	}
	if !strings.Contains(got, "⚠ untrusted: /etc/hosts") {
		t.Errorf("missing source badge:\n%s", got)
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
