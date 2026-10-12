package tui

import (
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

// Lone 8-bit CSI/OSC bytes (invalid UTF-8) and invisible tag characters
// must not slip through sanitize's fast path.
func TestSanitizeInvalidUTF8AndInvisibles(t *testing.T) {
	for _, in := range []string{"ok\x9b2J", "ok\x9d0;title\x07", "a\U000E0041b", "a\u061cb", "a\u2062b", "a\ufffab"} {
		out := sanitize(in)
		// ContainsAny would decode a raw 0x9b as U+FFFD and match the
		// (correct) replacement, so check bytes and runes separately.
		if !utf8.ValidString(out) || strings.IndexByte(out, 0x9b) >= 0 || strings.IndexByte(out, 0x9d) >= 0 ||
			strings.ContainsAny(out, "\u061c\u2062\ufffa\U000E0041\x07") {
			t.Errorf("sanitize(%q) = %q, still carries a control/invisible", in, out)
		}
	}
	if got := sanitize("plain text"); got != "plain text" {
		t.Errorf("readable text changed: %q", got)
	}
}

// Wire ids reach the terminal and filenames through shortID.
func TestShortIDAndExportNameAreSafe(t *testing.T) {
	if got := shortID("\x1b[2J\x1b[Hsess"); strings.Contains(got, "\x1b") {
		t.Fatalf("shortID kept an escape: %q", got)
	}
	if got := shortID(strings.Repeat("é", 20)); !strings.HasSuffix(got, "…") || strings.ContainsRune(got, '�') {
		t.Fatalf("shortID split a rune: %q", got)
	}
	dir := t.TempDir()
	path, err := writeExport(dir, "/../../../../tmp/x", "md", []byte("x"))
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(path) != dir {
		t.Fatalf("export escaped its directory: %s", path)
	}
}

// A sub-agent envelope's status is wire text like every other card field.
func TestAgentResultStatusSanitized(t *testing.T) {
	r := parseAgentResult(`{"status":"done\u001b]52;c;Zm9v\u0007","summary":"x"}`)
	if r == nil {
		t.Fatal("envelope not parsed")
	}
	if strings.ContainsAny(r.status, "\x1b\x07") {
		t.Fatalf("status carries escapes: %q", r.status)
	}
}

// The panel status line may quote raw server bodies.
func TestPanelMsgRenderedSanitized(t *testing.T) {
	m := newTestModel()
	m.runCommandLine("/queue")
	m.panelMsg = "promote failed: \x1b]52;c;Zm9v\x07"
	if v := m.View(); strings.Contains(v, "]52;") && strings.Contains(v, "\x1b]52") {
		t.Fatal("panel message rendered a raw OSC sequence")
	}
}
