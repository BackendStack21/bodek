package tui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// ── typed tool renderers ───────────────────────────────────────────────────
//
// Tool output is not prose — it is diffs, files, JSON, and test runs. The
// step line stays one-line everywhere; what changes is the INSPECT depth:
// expanding a step picks a renderer by tool name and result shape. Every
// renderer is a pure function of (data, width) so they golden-test headlessly
// and never touch model state.
//
// Renderers own truncation (on raw text, BEFORE styling) and return fully
// styled lines — callers must never re-truncate styled output, since rune
// counting over ANSI escapes corrupts sequences.

// maxDetailLines bounds every renderer's expansion, matching the historic
// plain-text cap.
const maxDetailLines = 200

// detailWidth is the truncation column renderers use: the viewport minus the
// "  ⎿ " connector and breathing margin.
func detailWidth(width int) int {
	if width < 12 {
		return 12
	}
	return width - 6
}

// ── unified diff ────────────────────────────────────────────────────────────

// diffLooksLike reports whether s reads as a unified diff (git diff, the
// diff tool): a hunk header plus +/- body lines.
func diffLooksLike(s string) bool {
	if !strings.Contains(s, "@@") {
		return false
	}
	return strings.Contains(s, "\n+") || strings.Contains(s, "\n-")
}

// diffStat returns add/delete line counts when s parses as a unified diff.
func diffStat(s string) (adds, dels int, ok bool) {
	if !diffLooksLike(s) {
		return 0, 0, false
	}
	for _, ln := range strings.Split(s, "\n") {
		switch {
		case strings.HasPrefix(ln, "+++"), strings.HasPrefix(ln, "---"):
			continue // file markers
		case strings.HasPrefix(ln, "+"):
			adds++
		case strings.HasPrefix(ln, "-"):
			dels++
		}
	}
	return adds, dels, adds > 0 || dels > 0
}

// renderDiff tints a unified diff: + green, − red, hunk headers steel, file
// markers dim. Blank lines stay stripped like every other renderer.
func renderDiff(s string, width int, th theme) []string {
	w := detailWidth(width)
	out := make([]string, 0, 32)
	for _, ln := range strings.Split(s, "\n") {
		if strings.TrimSpace(ln) == "" {
			continue
		}
		ln = truncate(strings.TrimRight(ln, " \t"), w)
		switch {
		case strings.HasPrefix(ln, "+++"), strings.HasPrefix(ln, "---"):
			out = append(out, th.stepArg.Render(ln))
		case strings.HasPrefix(ln, "@@"):
			out = append(out, th.stepName.Render(ln))
		case strings.HasPrefix(ln, "+"):
			out = append(out, th.diffAdd.Render(ln))
		case strings.HasPrefix(ln, "-"):
			out = append(out, th.diffDel.Render(ln))
		default:
			out = append(out, th.stepRes.Render(ln))
		}
		if len(out) >= maxDetailLines {
			return append(out, th.stepArg.Render("… output truncated"))
		}
	}
	return out
}

// ── numbered file view ──────────────────────────────────────────────────────

// renderNumbered renders plain file content with line numbers — the shape
// read_file/batch_read return — so excerpts read as code, not wrapped prose.
func renderNumbered(s string, width int, th theme) []string {
	w := detailWidth(width)
	lines := strings.Split(s, "\n")
	// Trim trailing blank lines without disturbing content.
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) > maxDetailLines {
		lines = append(lines[:maxDetailLines], "… output truncated")
	}
	out := make([]string, 0, len(lines))
	numW := len(fmt.Sprintf("%d", len(lines)))
	for i, ln := range lines {
		if strings.TrimSpace(ln) == "" {
			out = append(out, th.stepTree.Render(fmt.Sprintf("  %*d │", numW, i+1)))
			continue
		}
		num := th.stepTree.Render(fmt.Sprintf("  %*d │", numW, i+1))
		body := truncate(strings.TrimRight(ln, " \t"), w-numW-5)
		out = append(out, num+" "+th.stepRes.Render(body))
	}
	return out
}

// fileReadTool reports whether a tool's result is raw file content.
func fileReadTool(name string) bool {
	n := strings.ToLower(name)
	return strings.Contains(n, "read_file") || strings.Contains(n, "batch_read")
}

// ── JSON ────────────────────────────────────────────────────────────────────

// jsonLooksLike reports whether s is a JSON object/array worth pretty-printing.
func jsonLooksLike(s string) bool {
	t := strings.TrimSpace(s)
	return strings.HasPrefix(t, "{") || strings.HasPrefix(t, "[")
}

// renderJSON pretty-prints compact JSON with indentation; keys stay plain and
// the structure reads as a tree. Invalid JSON falls back to nil.
func renderJSON(s string, width int, th theme) []string {
	var buf bytes.Buffer
	if err := json.Indent(&buf, []byte(strings.TrimSpace(s)), "", "  "); err != nil {
		return nil
	}
	w := detailWidth(width)
	out := make([]string, 0, 16)
	for _, ln := range strings.Split(buf.String(), "\n") {
		if strings.TrimSpace(ln) == "" {
			continue
		}
		out = append(out, th.stepRes.Render(truncate(ln, w)))
		if len(out) >= maxDetailLines {
			return append(out, th.stepArg.Render("… output truncated"))
		}
	}
	return out
}

// ── test-run summary ────────────────────────────────────────────────────────

// testSummary extracts a compact pass/fail summary from test-runner output
// (go test / pytest / jest). ok=false when nothing recognizable.
func testSummary(s string) (summary string, ok bool) {
	lower := strings.ToLower(s)
	fails := 0
	var failNames []string
	for _, ln := range strings.Split(s, "\n") {
		// go test: "--- FAIL: TestX"
		if name, found := cutPrefixTrim(ln, "--- FAIL: "); found {
			fails++
			if fields := strings.Fields(name); len(fields) > 0 && len(failNames) < 3 {
				failNames = append(failNames, fields[0])
			}
			continue
		}
		// pytest: "FAILED tests/test_x.py::test_y"
		if strings.Contains(ln, "FAILED ") {
			fails++
			continue
		}
		// jest: "✕ test name" / "● test name"
		if strings.HasPrefix(strings.TrimSpace(ln), "✕") {
			fails++
		}
	}
	if fails == 0 {
		// A clean run: go test "ok  \tpkg", pytest "N passed".
		if strings.Contains(lower, "\nok ") || strings.Contains(lower, " passed") ||
			strings.Contains(lower, "testsuite") {
			return "✓ tests pass", true
		}
		return "", false
	}
	summary = fmt.Sprintf("✗ %d failing", fails)
	if len(failNames) > 0 {
		summary += " (" + strings.Join(failNames, ", ") + ")"
	}
	return summary, true
}

// cutPrefixTrim reports whether s starts with prefix, returning the remainder
// trimmed.
func cutPrefixTrim(s, prefix string) (string, bool) {
	if strings.HasPrefix(s, prefix) {
		return strings.TrimSpace(strings.TrimPrefix(s, prefix)), true
	}
	return "", false
}

// ── dispatch ────────────────────────────────────────────────────────────────

// stepDetail renders a step's expanded body with the typed renderer its
// output shape selects: diffs tint, files get line numbers, JSON indents,
// test runs summarize, everything else falls back to verbatim lines. The
// returned lines are fully styled and truncated — append them verbatim.
func stepDetail(name, result string, width int, th theme) []string {
	switch {
	case diffLooksLike(result):
		if out := renderDiff(result, width, th); len(out) > 0 {
			return out
		}
	case fileReadTool(name):
		return renderNumbered(result, width, th)
	case jsonLooksLike(result):
		if out := renderJSON(result, width, th); len(out) > 0 {
			return out
		}
	}
	// Fallback: the historic verbatim rendering (styled here so the caller
	// appends every line verbatim).
	var out []string
	for _, ln := range strings.Split(result, "\n") {
		if strings.TrimSpace(ln) != "" {
			out = append(out, th.stepRes.Render(truncate(ln, detailWidth(width))))
		}
		if len(out) >= maxDetailLines {
			return append(out, th.stepArg.Render("… output truncated"))
		}
	}
	return out
}

// stepHeadSuffix renders the typed chip a step line gains from its result:
// a diffstat for diffs, a pass/fail summary for test runs.
func stepHeadSuffix(name, result string, th theme) string {
	if adds, dels, ok := diffStat(result); ok {
		return th.diffAdd.Render(fmt.Sprintf("  +%d", adds)) +
			th.diffDel.Render(fmt.Sprintf(" −%d", dels))
	}
	if s, ok := testSummary(result); ok {
		if strings.HasPrefix(s, "✗") {
			// The step's status icon already flags the failure — the chip
			// names what failed, without a second ✗.
			return th.stepErr.Render(strings.TrimPrefix(s, "✗ "))
		}
		return th.stepDone.Render(s)
	}
	return ""
}
