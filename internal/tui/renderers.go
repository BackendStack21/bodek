package tui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
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
	adds, dels = countDiffLines(s)
	return adds, dels, adds > 0 || dels > 0
}

// countDiffLines tallies +/- body lines, skipping the ---/+++ file markers.
func countDiffLines(s string) (adds, dels int) {
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
	return adds, dels
}

// ── fenced diff blocks ─────────────────────────────────────────────────────

// fencedDiffBlocks extracts the contents of well-formed ` ```diff ` fences
// in s, in order. A fence is authoritative — its body is a diff even
// without a @@ hunk header — but it must close; unterminated fences are
// left to the verbatim path.
func fencedDiffBlocks(s string) []string {
	var blocks []string
	var cur []string
	in := false
	for _, ln := range strings.Split(s, "\n") {
		t := strings.TrimSpace(ln)
		if in {
			if t == "```" {
				in = false
				blocks = append(blocks, strings.Join(cur, "\n"))
				cur = nil
				continue
			}
			cur = append(cur, ln)
			continue
		}
		if t == "```diff" || strings.HasPrefix(t, "```diff ") {
			in = true
			cur = nil
		}
	}
	return blocks
}

// hasFencedDiff reports whether s embeds at least one closed ` ```diff `
// fence.
func hasFencedDiff(s string) bool {
	return len(fencedDiffBlocks(s)) > 0
}

// renderMixedDiff renders tool output that interleaves prose with fenced
// ` ```diff ` blocks: prose keeps the verbatim style, each fence unwraps
// and tints through renderDiff, and the fence markers themselves never
// appear in the output.
func renderMixedDiff(s string, width int, th theme) []string {
	w := detailWidth(width)
	var out []string
	var block []string
	in := false
	flush := func() {
		if len(block) > 0 {
			out = append(out, renderDiff(strings.Join(block, "\n"), width, th)...)
			block = nil
		}
	}
	for _, ln := range strings.Split(s, "\n") {
		t := strings.TrimSpace(ln)
		if in {
			if t == "```" {
				in = false
				flush()
				continue
			}
			block = append(block, ln)
			continue
		}
		if t == "```diff" || strings.HasPrefix(t, "```diff ") {
			in = true
			block = nil
			continue
		}
		if t == "" {
			continue
		}
		out = append(out, th.stepRes.Render(truncate(strings.TrimRight(ln, " \t"), w)))
		if len(out) >= maxDetailLines {
			return append(out, th.stepArg.Render("… output truncated"))
		}
	}
	return out
}

// diffStatOf counts a result's diff activity across both shapes: fenced
// ` ```diff ` blocks (which win outright) and a whole-result unified diff.
func diffStatOf(s string) (adds, dels int, ok bool) {
	if blocks := fencedDiffBlocks(s); len(blocks) > 0 {
		for _, b := range blocks {
			a, d := countDiffLines(b)
			adds += a
			dels += d
		}
		return adds, dels, adds > 0 || dels > 0
	}
	return diffStat(s)
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

// Structured pass/fail patterns — only real runner output matches. Loose
// words ("Build passed", a stray "ok" progress line, the word "testsuite")
// must never produce a verdict chip.
var (
	// go test: "ok  \tpkg\t0.5s" / "ok  \tpkg\t(cached)"
	goPassRe = regexp.MustCompile(`^ok[ \t]+\S+[ \t]+(?:\d+(?:\.\d+)?s|\(cached\))`)
	// TAP: "ok 1 - description"
	tapPassRe = regexp.MustCompile(`^ok[ \t]+\d+[ \t]+-`)
	// Counted verdicts: pytest "1 failed, 4 passed in 0.1s", jest
	// "Tests: 3 passed, 3 total", vitest "Tests  5 passed (5)", cargo
	// "0 passed; 2 ignored".
	countedRe = regexp.MustCompile(`\b(\d+) (passed|failed|skipped|ignored)\b`)
	// go -cover: "coverage: 82.3% of statements", also trailing on ok lines.
	coverageRe = regexp.MustCompile(`(\d+(?:\.\d+)?)% of statements`)
	// Counted verdicts only fire on summary-shaped lines — bare "N passed"
	// in prose ("10 files failed validation, 5 passed") must not produce
	// verdicts. Jest ("Tests:"), vitest ("Tests "), cargo ("test result:"),
	// or a pytest-style duration tail ("in 0.12s").
	countSummaryRe = regexp.MustCompile(`^tests?[ :]|^test result:|in \d+(?:\.\d+)?s?(?: =+)?$`)
	// sanitize() strips ESC bytes but leaves SGR residue ("[32m") that glues
	// digits and defeats line anchors; chips match on stripped text.
	sgrResidueRe = regexp.MustCompile(`(?:\x1b)?\[[0-9;]+m`)
)

// Arg-gated chip patterns: they fire only when the command words say the
// step actually ran the tool, so output that merely mentions a hash or an
// HTTP status never grows a chip.
var (
	commitLineRe     = regexp.MustCompile(`^\[(.+?)\][ \t]*(.*)$`)
	commitHashRe     = regexp.MustCompile(`([0-9a-f]{7,40})\z`)
	gitPushRe        = regexp.MustCompile(`[0-9a-f]{7,40}\.{2,3}[0-9a-f]{7,40}[ \t]+(\S+)[ \t]+->`)
	gitNewRefRe      = regexp.MustCompile(`\[(?:new branch|new tag)\][ \t]+(\S+)[ \t]+->`)
	lintIssuesRe     = regexp.MustCompile(`^(\d+) issues?\.?:?$`)
	eslintProblemsRe = regexp.MustCompile(`✖ (\d+) problems`)
	// Build / vet verdict patterns: compiler errors (go/rust/tsc shapes) and
	// failed exits mark a failed build; vet diagnostics are file:line: col.
	// Success rides the silent-output convention instead of a pattern.
	// '# pkg' headers only count when a file:line diagnostic follows — a
	// markdown heading alone must not paint 'build failed'.
	buildFailRe     = regexp.MustCompile(`(?m)^(?:#\s+\S[^\n]*\n[^\n]*:\d+:\d+: |error\[E\d+\]|ERROR:|exit status \d+)|(?:^|\n)[^\n]*:\d+:\d+: [^\n]*\berror\b|(?:^|\n)[^\n]*\(\d+,\d+\): error TS`)
	vetFailRe       = regexp.MustCompile(`(?m)^[^\n]*\.go:\d+:\d+: `)
	warnEmittedRe   = regexp.MustCompile(`^warning: (\d+) warnings? emitted\.?$`)
	warnGeneratedRe = regexp.MustCompile(`^(\d+) warnings? generated\.?$`)
	httpStatusRe    = regexp.MustCompile(`(?i)^HTTP/[\d.]+ (\d{3})`)
	wgetStatusRe    = regexp.MustCompile(`awaiting response\.\.\.?[ \t]?(\d{3})`)
	searchHitsRe    = regexp.MustCompile(`found (\d+) matches`)
	planHeaderRe    = regexp.MustCompile(`^\[Current plan:\s*v(\d+)\s+—\s+(\d+)/(\d+) done,\s+(\d+) blocked\.`)
	planCompleteRe  = regexp.MustCompile(`^\[Current plan:\s*v(\d+)\s+—\s+all\s+(\d+)\s+steps?\s+complete\.`)
	planStepRe      = regexp.MustCompile(`^(\S+)\s+\[([^\]]+)\]\s*(.*)$`)
)

// structuredToolItem is the small, display-oriented subset shared by odek's
// batch result envelopes. Keep this deliberately narrower than the wire
// schema: unknown fields and nested values stay on the generic safe path.
type structuredToolItem struct {
	label       string
	command     string
	stdout      string
	stderr      string
	content     string
	diff        string
	error       string
	status      int
	contentLen  int64
	totalLines  int
	durationMS  int64
	exitCode    int
	hasStatus   bool
	hasExitCode bool
	hasSuccess  bool
	success     bool
}

// structuredTool reports the built-in tools whose results are arrays of
// independent work items. The name gate is intentional: arbitrary JSON from
// an MCP tool must continue through the normal renderer without guesswork.
func structuredTool(name string) bool {
	n := strings.ToLower(strings.TrimSpace(name))
	return n == "parallel_shell" || n == "http_batch" ||
		n == "batch_read" || n == "batch_patch"
}

func rawString(m map[string]json.RawMessage, key string) string {
	v, ok := m[key]
	if !ok || string(v) == "null" {
		return ""
	}
	var s string
	if json.Unmarshal(v, &s) != nil {
		return ""
	}
	return sanitize(foldUntrustedWrappers(s))
}

func rawInt64(m map[string]json.RawMessage, key string) (int64, bool) {
	v, ok := m[key]
	if !ok || string(v) == "null" {
		return 0, false
	}
	var n int64
	if json.Unmarshal(v, &n) != nil {
		return 0, false
	}
	return n, true
}

func rawBool(m map[string]json.RawMessage, key string) (bool, bool) {
	v, ok := m[key]
	if !ok || string(v) == "null" {
		return false, false
	}
	var b bool
	if json.Unmarshal(v, &b) != nil {
		return false, false
	}
	return b, true
}

// structuredJSONItems decodes only an object with a non-empty results array
// of known scalar fields. A malformed or foreign envelope returns ok=false,
// preserving the fail-safe generic JSON renderer.
func structuredJSONItems(name, data string) ([]structuredToolItem, bool) {
	if !structuredTool(name) {
		return nil, false
	}
	var env map[string]json.RawMessage
	if err := json.Unmarshal([]byte(strings.TrimSpace(sanitize(data))), &env); err != nil {
		return nil, false
	}
	raw, ok := env["results"]
	if !ok {
		return nil, false
	}
	var rows []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &rows); err != nil || len(rows) == 0 {
		return nil, false
	}
	items := make([]structuredToolItem, 0, len(rows))
	for _, row := range rows {
		item := structuredToolItem{
			label:   rawString(row, "path"),
			command: rawString(row, "command"),
			stdout:  rawString(row, "stdout"),
			stderr:  rawString(row, "stderr"),
			content: rawString(row, "content"),
			diff:    rawString(row, "diff"),
			error:   rawString(row, "error"),
		}
		if item.label == "" {
			item.label = rawString(row, "url")
		}
		if n, ok := rawInt64(row, "status"); ok {
			item.status, item.hasStatus = int(n), true
		}
		if n, ok := rawInt64(row, "exit_code"); ok {
			item.exitCode, item.hasExitCode = int(n), true
		}
		if n, ok := rawInt64(row, "content_length"); ok {
			item.contentLen = n
		}
		if n, ok := rawInt64(row, "total_lines"); ok {
			item.totalLines = int(n)
		}
		if n, ok := rawInt64(row, "duration_ms"); ok {
			item.durationMS = n
		}
		if b, ok := rawBool(row, "success"); ok {
			item.success, item.hasSuccess = b, true
		}
		// Require at least one field this renderer understands. In particular,
		// do not turn {"results":[{"metadata":{...}}]} into an empty card.
		known := item.label != "" || item.command != "" || item.stdout != "" ||
			item.stderr != "" || item.content != "" || item.diff != "" || item.error != "" ||
			item.hasStatus || item.hasExitCode || item.hasSuccess || item.totalLines > 0 || item.contentLen > 0
		if !known {
			return nil, false
		}
		items = append(items, item)
	}
	return items, true
}

const (
	structuredDetailCap    = 64 * 1024
	structuredDetailRows   = 256
	structuredDetailString = 2048
)

type structuredDisplayMeta struct {
	totalItems       int
	displayTruncated bool
	bodiesOmitted    bool
}

func structuredDisplayMetadata(data string, fallbackItems int) structuredDisplayMeta {
	meta := structuredDisplayMeta{totalItems: fallbackItems}
	var env struct {
		TotalItems       int  `json:"total_items"`
		DisplayTruncated bool `json:"display_truncated"`
		BodiesOmitted    bool `json:"bodies_omitted"`
	}
	if json.Unmarshal([]byte(strings.TrimSpace(sanitize(data))), &env) == nil {
		if env.TotalItems > meta.totalItems {
			meta.totalItems = env.TotalItems
		}
		meta.displayTruncated = env.DisplayTruncated && meta.totalItems > fallbackItems
		meta.bodiesOmitted = env.BodiesOmitted
	}
	return meta
}

// boundedStructuredDetail keeps the small, display-oriented metadata needed
// by typed batch renderers. It is deliberately re-encoded after parsing:
// truncating the raw JSON could leave an invalid document, while encoding
// sanitized bounded fields always leaves a parser-safe payload. Unknown or
// malformed shapes fall back to the ordinary normalized display text.
func boundedStructuredDetail(name, raw string) string {
	if !structuredTool(name) {
		return ""
	}
	items, ok := structuredJSONItems(name, raw)
	if !ok {
		return boundedStructuredFallback(raw)
	}
	totalItems := len(items)
	if len(items) > structuredDetailRows {
		items = items[:structuredDetailRows]
	}
	displayTruncated := len(items) < totalItems

	// Re-encode through maps so the renderer sees the same field names it
	// understands on the wire (path/url, command, stdout, and so on).
	build := func(limit int, bodies bool) string {
		rows := make([]map[string]any, 0, len(items))
		for _, item := range items {
			row := make(map[string]any, 12)
			if item.label != "" {
				if strings.EqualFold(name, "http_batch") {
					row["url"] = boundedStructuredString(item.label, min(limit, 512))
				} else {
					row["path"] = boundedStructuredString(item.label, min(limit, 512))
				}
			}
			if item.command != "" {
				row["command"] = boundedStructuredString(item.command, limit)
			}
			if bodies {
				if item.stdout != "" {
					row["stdout"] = boundedStructuredString(item.stdout, limit)
				}
				if item.stderr != "" {
					row["stderr"] = boundedStructuredString(item.stderr, limit)
				}
				if item.content != "" {
					row["content"] = boundedStructuredString(item.content, limit)
				}
				if item.diff != "" {
					row["diff"] = boundedStructuredString(item.diff, limit)
				}
				if item.error != "" {
					row["error"] = boundedStructuredString(item.error, limit)
				}
			}
			if item.hasStatus {
				row["status"] = item.status
			}
			if item.hasExitCode {
				row["exit_code"] = item.exitCode
			}
			if item.hasSuccess {
				row["success"] = item.success
			}
			if item.contentLen != 0 {
				row["content_length"] = item.contentLen
			}
			if item.totalLines != 0 {
				row["total_lines"] = item.totalLines
			}
			if item.durationMS != 0 {
				row["duration_ms"] = item.durationMS
			}
			rows = append(rows, row)
		}
		envelope := map[string]any{"results": rows}
		if displayTruncated {
			envelope["display_truncated"] = true
			envelope["total_items"] = totalItems
		}
		if !bodies {
			envelope["bodies_omitted"] = true
		}
		encoded, err := json.Marshal(envelope)
		if err != nil || len(encoded) > structuredDetailCap {
			return ""
		}
		return string(encoded)
	}

	for limit := structuredDetailString; limit >= 128; limit /= 2 {
		if encoded := build(limit, true); encoded != "" {
			return encoded
		}
	}
	// Labels, status, and exit metadata are more useful than an unbounded body
	// when a result contains hundreds of large outputs.
	if encoded := build(256, false); encoded != "" {
		return encoded
	}
	return boundedStructuredFallback(raw)
}

func boundedStructuredFallback(raw string) string {
	s := resultPreview(raw)
	if len(s) <= structuredDetailCap {
		return s
	}
	// This fallback is plain display text, so a rune-safe cap is preferable to
	// slicing a JSON document and leaving the typed parser with broken syntax.
	cut := structuredDetailCap - len("…")
	for cut > 0 && cut < len(s) && (s[cut]&0xc0) == 0x80 {
		cut--
	}
	return s[:cut] + "…"
}

func structuredResultFailed(name, raw string) bool {
	items, ok := structuredJSONItems(name, raw)
	if !ok {
		return false
	}
	for _, item := range items {
		if structuredItemFailed(item) {
			return true
		}
	}
	return false
}

func boundedStructuredString(s string, max int) string {
	s = sanitize(foldUntrustedWrappers(s))
	return truncate(s, max)
}

// stepDetailResult selects the structured bounded payload when one was kept
// during ingestion, while preserving compatibility with hand-built and old
// history steps that only have the normalized result.
func stepDetailResult(s step) string {
	if s.detailResult != "" {
		return s.detailResult
	}
	return s.result
}

func structuredItems(name, data string) ([]structuredToolItem, bool) {
	// Structured grouping is authoritative only when it comes from the
	// supported JSON envelope. Normalized legacy text may contain arbitrary
	// bracketed lines such as "[2] warning"; treating those as item headers
	// invents counts and can hide the real failure shape.
	return structuredJSONItems(name, data)
}

func structuredItemFailed(it structuredToolItem) bool {
	return (it.hasExitCode && it.exitCode != 0) ||
		(it.hasStatus && (it.status < 200 || it.status >= 400)) ||
		(it.hasSuccess && !it.success) || it.error != ""
}

func structuredHeadSuffix(name, result string, th theme) string {
	items, ok := structuredItems(name, result)
	if !ok || len(items) == 0 {
		return ""
	}
	failed := 0
	confirmed := 0
	for _, it := range items {
		if structuredItemFailed(it) {
			failed++
		}
		if (it.hasExitCode && it.exitCode == 0) || (it.hasSuccess && it.success) || (it.hasStatus && it.status >= 200 && it.status < 400) {
			confirmed++
		}
	}
	n := len(items)
	label := "items"
	switch strings.ToLower(name) {
	case "parallel_shell":
		label = "commands"
	case "batch_read":
		label = "files"
	case "batch_patch":
		label = "patches"
	case "http_batch":
		label = "urls"
	}
	meta := structuredDisplayMetadata(result, n)
	count := fmt.Sprintf("%d %s", n, label)
	if meta.displayTruncated {
		count = fmt.Sprintf("%d/%d %s shown", n, meta.totalItems, label)
	}
	if failed > 0 {
		return th.stepErr.Render(fmt.Sprintf("%s · %d failed", count, failed))
	}
	// Normalized bodies can lose success metadata; count them without
	// claiming a verified successful execution.
	if confirmed != n {
		return th.stepRes.Render(count)
	}
	if meta.displayTruncated {
		return th.stepRes.Render(count)
	}
	if strings.ToLower(name) == "batch_patch" {
		return th.stepDone.Render(fmt.Sprintf("✓ %d patched", n))
	}
	return th.stepDone.Render(fmt.Sprintf("✓ %d %s", n, label))
}

func planSnapshotLines(result string, width int, th theme) []string {
	s := sanitize(foldUntrustedWrappers(result))
	lines := strings.Split(s, "\n")
	var out []string
	matched := false
	for _, ln := range lines {
		t := strings.TrimSpace(ln)
		if t == "" {
			continue
		}
		if m := planHeaderRe.FindStringSubmatch(t); m != nil {
			matched = true
			text := fmt.Sprintf("plan v%s · %s/%s done · %s blocked", m[1], m[2], m[3], m[4])
			out = append(out, th.stepName.Render(truncate(text, detailWidth(width))))
			continue
		}
		if m := planCompleteRe.FindStringSubmatch(t); m != nil {
			matched = true
			text := fmt.Sprintf("plan v%s · %s/%s done · 0 blocked", m[1], m[2], m[2])
			out = append(out, th.stepName.Render(truncate(text, detailWidth(width))))
			continue
		}
		if m := planStepRe.FindStringSubmatch(t); m != nil {
			matched = true
			status := strings.ToLower(sanitize(m[2]))
			glyph := "○"
			switch status {
			case "done", "complete", "completed":
				glyph = "✓"
			case "in_progress", "running":
				glyph = "▸"
			case "blocked":
				glyph = "!"
			}
			row := glyph + " " + sanitize(m[1])
			if title := collapse(m[3]); title != "" {
				row += "  " + title
			}
			out = append(out, th.stepRes.Render(truncate(row, detailWidth(width))))
			if len(out) >= maxDetailLines {
				return append(out, th.stepArg.Render("… output truncated"))
			}
		}
	}
	if !matched {
		return nil
	}
	return out
}

func planHeadSuffix(result string, th theme) string {
	for _, ln := range strings.Split(sanitize(foldUntrustedWrappers(result)), "\n") {
		if m := planHeaderRe.FindStringSubmatch(strings.TrimSpace(ln)); m != nil {
			return th.stepRes.Render(fmt.Sprintf("v%s · %s/%s done · %s blocked", m[1], m[2], m[3], m[4]))
		}
		if m := planCompleteRe.FindStringSubmatch(strings.TrimSpace(ln)); m != nil {
			return th.stepRes.Render(fmt.Sprintf("v%s · %s/%s done · 0 blocked", m[1], m[2], m[2]))
		}
	}
	return ""
}

func structuredItemHead(name string, item structuredToolItem, index int) (string, bool) {
	label := item.label
	if label == "" {
		label = fmt.Sprintf("item %d", index+1)
	}
	switch strings.ToLower(name) {
	case "parallel_shell":
		if item.hasExitCode && item.exitCode != 0 {
			return fmt.Sprintf("%s · exit %d", label, item.exitCode), true
		}
		return label, structuredItemFailed(item)
	case "batch_patch":
		if item.hasSuccess && !item.success || item.error != "" {
			return "✗ " + label, true
		}
		if item.hasSuccess {
			return "✓ " + label, false
		}
		return label, false
	case "batch_read":
		return label, structuredItemFailed(item)
	case "http_batch":
		if item.hasStatus {
			return fmt.Sprintf("%s · %d", label, item.status), structuredItemFailed(item)
		}
		return label, structuredItemFailed(item)
	default:
		return label, structuredItemFailed(item)
	}
}

func appendStructuredLine(out *[]string, line string, width int, style lipgloss.Style) bool {
	if len(*out) >= maxDetailLines {
		return false
	}
	*out = append(*out, style.Render(truncate(strings.TrimRight(sanitize(line), " \t"), detailWidth(width))))
	return true
}

// structuredDetailLines renders one result row per batch item. It is used
// only after a deliberate expand, and therefore may show command/path/URL
// labels that are deliberately absent from the calm one-line preview.
func structuredDetailLines(name, result string, width int, th theme) []string {
	items, ok := structuredItems(name, result)
	if !ok || len(items) == 0 {
		return nil
	}
	meta := structuredDisplayMetadata(result, len(items))
	out := make([]string, 0, min(len(items)*3, maxDetailLines))
	if meta.displayTruncated {
		omitted := meta.totalItems - len(items)
		if omitted > 0 {
			appendStructuredLine(&out, fmt.Sprintf("… %d more items omitted", omitted), width, th.stepArg)
		}
	}
	if meta.bodiesOmitted {
		appendStructuredLine(&out, "… item output omitted to stay within the detail limit", width, th.stepArg)
	}
	for i, item := range items {
		head, failed := structuredItemHead(name, item, i)
		style := th.stepName
		if failed {
			style = th.stepErr
		}
		if !appendStructuredLine(&out, head, width, style) {
			break
		}
		if item.command != "" {
			if !appendStructuredLine(&out, "$ "+item.command, width, th.stepArg) {
				break
			}
		}

		appendBody := func(body string, bodyStyle lipgloss.Style) bool {
			body = sanitize(foldUntrustedWrappers(body))
			for _, ln := range strings.Split(body, "\n") {
				if strings.TrimSpace(ln) == "" {
					continue
				}
				if !appendStructuredLine(&out, "  "+ln, width, bodyStyle) {
					return false
				}
			}
			return true
		}

		if item.diff != "" {
			diff := sanitize(foldUntrustedWrappers(item.diff))
			var body []string
			switch {
			case hasFencedDiff(diff):
				body = renderMixedDiff(diff, width, th)
			case diffLooksLike(diff):
				body = renderDiff(diff, width, th)
			default:
				body = []string{th.stepRes.Render(truncate(diff, detailWidth(width)))}
			}
			for _, ln := range body {
				if len(out) >= maxDetailLines {
					break
				}
				out = append(out, ln)
			}
		}
		if item.content != "" && !appendBody(item.content, th.stepRes) {
			break
		}
		if item.stdout != "" && !appendBody(item.stdout, th.stepRes) {
			break
		}
		if item.stderr != "" && !appendBody("stderr: "+item.stderr, th.stepErr) {
			break
		}
		if item.error != "" && item.stderr == "" && !appendBody(item.error, th.stepErr) {
			break
		}
		if strings.ToLower(name) == "parallel_shell" && item.durationMS > 0 {
			if !appendStructuredLine(&out, fmt.Sprintf("  %dms", item.durationMS), width, th.stepArg) {
				break
			}
		}
		if strings.ToLower(name) == "http_batch" && item.contentLen > 0 {
			if !appendStructuredLine(&out, fmt.Sprintf("  %d bytes", item.contentLen), width, th.stepArg) {
				break
			}
		}
		if strings.ToLower(name) == "batch_read" && item.totalLines > 0 {
			if !appendStructuredLine(&out, fmt.Sprintf("  %d total lines", item.totalLines), width, th.stepArg) {
				break
			}
		}
	}
	if len(out) >= maxDetailLines {
		out = append(out[:maxDetailLines-1], th.stepArg.Render("… output truncated"))
	}
	return out
}

// testSummary extracts a compact pass/fail summary from test-runner output
// (go test / pytest / jest / vitest / cargo / TAP). ok=false when nothing
// recognizable — only structured runner patterns produce a verdict. The
// chip carries counts when the runner provides them ("✓ 5 passed · 2
// skipped") and the go -cover figure when present ("· 82.3% cov").
func testSummary(s string) (summary string, ok bool) {
	lineFails, countedFails, passes, suitePasses, skips := 0, 0, 0, 0, 0
	counted := false
	covSum, covN := 0.0, 0
	var failNames []string
	seen := map[string]bool{}
	count := func(ln string, suite bool) {
		// Doubled summaries (jest reruns) must not inflate counts — but cargo
		// prints an identical "test result:" line per package; those are
		// real repeats.
		if seen[ln] && !strings.HasPrefix(ln, "test result:") {
			return
		}
		seen[ln] = true
		if !suite && !countSummaryRe.MatchString(ln) {
			return
		}
		for _, m := range countedRe.FindAllStringSubmatch(ln, -1) {
			n, _ := strconv.Atoi(m[1])
			switch m[2] {
			case "failed":
				countedFails += n
			case "passed":
				if suite {
					suitePasses += n
				} else {
					passes += n
					counted = true
				}
			default: // skipped, ignored
				skips += n
			}
		}
	}
	for _, ln := range strings.Split(s, "\n") {
		trimmed := strings.TrimSpace(ln)
		// go -cover figure — an addition to an existing pass verdict, never
		// one on its own.
		if m := coverageRe.FindStringSubmatch(trimmed); m != nil {
			if v, err := strconv.ParseFloat(m[1], 64); err == nil {
				covSum += v
				covN++
			}
		}
		// go test: "--- FAIL: TestX" (indented for subtests).
		if name, found := cutPrefixTrim(trimmed, "--- FAIL: "); found {
			lineFails++
			if fields := strings.Fields(name); len(fields) > 0 && len(failNames) < 3 {
				failNames = append(failNames, fields[0])
			}
			continue
		}
		// go test verbose: "--- PASS: TestY" / "--- SKIP: TestZ".
		if strings.HasPrefix(trimmed, "--- PASS: ") {
			passes++
			continue
		}
		if strings.HasPrefix(trimmed, "--- SKIP: ") {
			skips++
			continue
		}
		// pytest: "FAILED tests/test_x.py::test_y".
		if strings.HasPrefix(trimmed, "FAILED ") {
			lineFails++
			continue
		}
		// jest: "✕ test name".
		if strings.HasPrefix(trimmed, "✕") {
			lineFails++
			continue
		}
		lower := strings.ToLower(trimmed)
		// Suite-level lines ("Test Suites:" / "Test Files") must not inflate
		// the test count when the Tests line is present too.
		suite := strings.HasPrefix(lower, "test files") || strings.HasPrefix(lower, "test suites")
		// cargo: "test result: ok. 5 passed; 0 failed; ..." — the counts
		// carry the verdict; no blanket increment on the ok prefix.
		if strings.HasPrefix(lower, "test result:") {
			count(lower, false)
			continue
		}
		// go test package verdict, TAP ok, or a counted summary line.
		if goPassRe.MatchString(trimmed) || tapPassRe.MatchString(trimmed) {
			passes++
			continue
		}
		count(lower, suite)
	}
	// Runner summaries restate what the per-test lines already said — take
	// the larger count instead of adding both.
	fails := lineFails
	if countedFails > fails {
		fails = countedFails
	}
	if fails == 0 {
		if passes == 0 && suitePasses > 0 {
			passes, counted = suitePasses, true
		}
		if passes == 0 && skips == 0 {
			return "", false
		}
		chip := "✓ tests pass"
		if counted {
			chip = fmt.Sprintf("✓ %d passed", passes)
		}
		if skips > 0 {
			chip += fmt.Sprintf(" · %d skipped", skips)
		}
		if covN > 0 {
			avg := math.Round(covSum/float64(covN)*10) / 10
			chip += " · " + strconv.FormatFloat(avg, 'f', -1, 64) + "% cov"
		}
		return chip, true
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
	if strings.EqualFold(strings.TrimSpace(name), "plan") {
		if out := planSnapshotLines(result, width, th); len(out) > 0 {
			return out
		}
	}
	if out := structuredDetailLines(name, result, width, th); len(out) > 0 {
		return out
	}
	switch {
	case hasFencedDiff(result):
		// Fences first: prose stays verbatim, only the fenced content tints.
		if out := renderMixedDiff(result, width, th); len(out) > 0 {
			return out
		}
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
// a diffstat for diffs; a pass/fail summary for test runs; arg-gated git,
// lint, warning, and HTTP hints for shell steps; a hit count for searches.
// At most one chip per step, in that precedence. isErr carries the step's
// raw failure state: a failed step never paints a success-flavored chip —
// success is only claimed when the result's own metadata says so.
// stepHeadSuffix is the isErr=false convenience for callers without raw
// failure state; the chip logic lives in stepHeadSuffixFor.
func stepHeadSuffix(name, arg, result string, th theme) string {
	return stepHeadSuffixFor(name, arg, result, false, th)
}

func stepHeadSuffixFor(name, arg, result string, isErr bool, th theme) string {
	if strings.EqualFold(strings.TrimSpace(name), "plan") {
		if chip := planHeadSuffix(result, th); chip != "" {
			return chip
		}
	}
	if chip := structuredHeadSuffix(name, result, th); chip != "" {
		return chip
	}
	if adds, dels, ok := diffStatOf(result); ok {
		return th.diffAdd.Render(fmt.Sprintf("  +%d", adds)) +
			th.diffDel.Render(fmt.Sprintf(" −%d", dels))
	}
	result = sgrResidueRe.ReplaceAllString(result, "")
	if name != "shell" {
		// Non-shell steps get exactly one chip: a search hit count. Test
		// verdicts stay shell-only — a read_file returning runner text is a
		// file read, not a test run.
		if isSearchTool(name) {
			return hitsChip(result, th)
		}
		return ""
	}
	if raceFlagged(arg) && strings.Contains(result, "WARNING: DATA RACE") {
		return th.stepErr.Render("race detected")
	}
	if s, ok := testSummary(result); ok && (!isErr || strings.HasPrefix(s, "✗")) {
		// isErr suppresses pass verdicts: a failed step never claims a pass.
		if strings.HasPrefix(s, "✗") {
			// The step's status icon already flags the failure — the chip
			// names what failed, without a second ✗.
			return th.stepErr.Render(strings.TrimPrefix(s, "✗ "))
		}
		if raceFlagged(arg) {
			s += " · race"
		}
		return th.stepDone.Render(s)
	}
	for _, chip := range []string{
		buildChip(arg, result, isErr, th),
		vetChip(arg, result, isErr, th),
		gitChip(arg, result, th),
		lintChip(arg, result, isErr, th),
		warnChip(result, th),
		httpChip(arg, result, th),
	} {
		if chip != "" {
			return chip
		}
	}
	return ""
}

// isSearchTool reports whether a tool's hits deserve the hit-count chip —
// same substring matching style as toolGlyph.
func isSearchTool(name string) bool {
	n := strings.ToLower(name)
	for _, k := range []string{"grep", "search", "glob", "find"} {
		if strings.Contains(n, k) {
			return true
		}
	}
	return false
}

// shellWords flattens a shell step's command into words so chip gates can
// check what the step actually ran (multiline scripts included).
func shellWords(arg string) []string {
	return strings.Fields(strings.ReplaceAll(arg, "\n", " "))
}

func hasWord(words []string, want string) bool {
	for _, w := range words {
		if w == want {
			return true
		}
	}
	return false
}

// gitChip decorates git commit/push steps with their outcome — the short
// hash plus subject for commits, the branch for pushes. Requires the git
// verb in the command words, so output that merely mentions a hash stays
// chip-free.
func gitChip(arg, result string, th theme) string {
	words := shellWords(arg)
	if !hasWord(words, "git") {
		return ""
	}
	if hasWord(words, "commit") {
		for _, ln := range strings.Split(result, "\n") {
			m := commitLineRe.FindStringSubmatch(strings.TrimSpace(ln))
			if m == nil {
				continue
			}
			hash := commitHashRe.FindString(strings.TrimSpace(m[1]))
			if hash == "" {
				continue
			}
			if len(hash) > 7 {
				hash = hash[:7]
			}
			chip := "⎇ " + hash
			if subj := strings.TrimSpace(m[2]); subj != "" {
				chip += " " + truncate(subj, 26)
			}
			return th.stepDone.Render(chip)
		}
	}
	if hasWord(words, "push") {
		branch := ""
		for _, ln := range strings.Split(result, "\n") {
			if m := gitPushRe.FindStringSubmatch(ln); m != nil {
				branch = m[1]
			}
			if m := gitNewRefRe.FindStringSubmatch(ln); m != nil {
				branch = m[1]
			}
		}
		if branch != "" {
			return th.stepDone.Render("↑ " + branch)
		}
		if strings.Contains(result, "Everything up-to-date") {
			return th.stepDone.Render("↑ up to date")
		}
	}
	return ""
}

// raceFlagged reports whether the command asked for race detection
// (go test -race and friends).
func raceFlagged(arg string) bool {
	for _, w := range shellWords(arg) {
		if w == "-race" || w == "--race" || strings.HasPrefix(w, "-race=") {
			return true
		}
	}
	return false
}

// buildGate reports whether the command's job is compiling code. Build
// systems (make/cargo/gradle/mvn) qualify unless the run names another
// concern (lint/test/vet/check); toolchain verbs (go build, npm run build,
// tsc) qualify on the verb. git never does — a commit message may quote
// the word "build".
func buildGate(arg string) bool {
	words := shellWords(arg)
	if hasWord(words, "git") {
		return false
	}
	for _, w := range words {
		switch w {
		case "lint", "test", "vet", "check":
			return false
		}
	}
	for i, w := range words {
		switch w {
		case "make", "cmake", "cargo", "gradle", "mvn":
			return true
		case "build", "tsc", "rustc", "gcc", "clang":
			if i == 0 || words[0] != "git" {
				return true
			}
		}
	}
	return false
}

// silentBuildOutput reports output that the compile-success convention
// produces: nothing at all, or the normalized no-output placeholder.
func silentBuildOutput(result string) bool {
	t := strings.TrimSpace(result)
	return t == "" || t == "(no output)"
}

// buildChip reports the build verdict: a neutral 'built' for a compile gate
// with the silent success convention (silent output carries no exit
// metadata, so success is never claimed — no ✓), 'build failed' on
// recognized compiler errors or a failed exit. A step already marked failed
// yields no success-flavored chip at all. Unrecognized non-silent output
// yields no chip.
func buildChip(arg, result string, isErr bool, th theme) string {
	if !buildGate(arg) {
		return ""
	}
	if buildFailRe.MatchString(result) {
		return th.stepErr.Render("build failed")
	}
	if silentBuildOutput(result) && !isErr {
		return th.statsDim.Render("built")
	}
	return ""
}

// vetChip reports the go vet verdict with the same silent-success rule as
// buildChip: vet prints nothing when clean and file:line diagnostics when
// not. Gated on the vet verb (go vet ./...).
func vetChip(arg, result string, isErr bool, th theme) string {
	words := shellWords(arg)
	vet := false
	for i, w := range words {
		if w == "vet" {
			// vet heads the command (vet ./...) or rides its toolchain (go
			// vet ./...) — never a bare argument of another verb.
			vet = i == 0 || words[i-1] == "go"
			break
		}
	}
	if !vet {
		return ""
	}
	if vetFailRe.MatchString(result) {
		return th.stepErr.Render("vet failed")
	}
	if silentBuildOutput(result) && !isErr {
		// Same neutral convention as buildChip: no ✓ without exit metadata.
		return th.statsDim.Render("vet")
	}
	return ""
}

// lintChip reports the linter outcome: "✓ lint" or a red issue count. Gated on linter-sounding commands, so ruff's "All checks passed"
// cannot leak into arbitrary output.
func lintChip(arg, result string, isErr bool, th theme) string {
	words := shellWords(arg)
	linters := []string{"lint", "golangci-lint", "ruff", "eslint", "clippy"}
	gate := false
	for _, l := range linters {
		if hasWord(words, l) {
			gate = true
			break
		}
	}
	if !gate {
		// Word match, not substring: "git commit -m fix-lint" is not a lint
		// run.
		return ""
	}
	for _, ln := range strings.Split(result, "\n") {
		t := strings.TrimSpace(ln)
		if m := lintIssuesRe.FindStringSubmatch(t); m != nil {
			if n, _ := strconv.Atoi(m[1]); n == 0 {
				if isErr {
					return "" // a failed step never paints a ✓ verdict
				}
				return th.stepDone.Render("✓ lint")
			}
			return th.stepErr.Render("lint " + m[1])
		}
		if strings.HasPrefix(t, "All checks passed") {
			if isErr {
				return ""
			}
			return th.stepDone.Render("✓ lint")
		}
		if m := eslintProblemsRe.FindStringSubmatch(t); m != nil {
			if n, _ := strconv.Atoi(m[1]); n == 0 {
				if isErr {
					return ""
				}
				return th.stepDone.Render("✓ lint")
			}
			return th.stepErr.Render("lint " + m[1])
		}
	}
	return ""
}

// warnChip surfaces compiler warning summaries ("warning: 2 warnings
// emitted", "3 warnings generated") as an amber chip. Only summary lines
// count — individual warning lines are chatter.
func warnChip(result string, th theme) string {
	n := 0
	for _, ln := range strings.Split(result, "\n") {
		t := strings.TrimSpace(ln)
		if m := warnEmittedRe.FindStringSubmatch(t); m != nil {
			n, _ = strconv.Atoi(m[1])
			continue
		}
		if m := warnGeneratedRe.FindStringSubmatch(t); m != nil {
			n, _ = strconv.Atoi(m[1])
		}
	}
	if n > 0 {
		chip := "warning"
		if n > 1 {
			chip = "warnings"
		}
		return th.badgeWarn.Render(fmt.Sprintf("⚠ %d %s", n, chip))
	}
	return ""
}

// httpChip colors the final HTTP status of a client step: green 2xx, amber
// 3xx, red otherwise. Gated on the client heading the command, so a cat of
// saved headers never gets one.
func httpChip(arg, result string, th theme) string {
	head := strings.Fields(strings.SplitN(arg, "\n", 2)[0])
	if len(head) == 0 {
		return ""
	}
	switch head[0] {
	case "curl", "wget", "http", "https":
	default:
		return ""
	}
	code := ""
	for _, ln := range strings.Split(result, "\n") {
		if m := httpStatusRe.FindStringSubmatch(ln); m != nil {
			code = m[1]
		}
		if m := wgetStatusRe.FindStringSubmatch(ln); m != nil {
			code = m[1]
		}
	}
	if code == "" {
		return ""
	}
	switch code[0] {
	case '2':
		return th.stepDone.Render("● " + code)
	case '3':
		return th.badgeWarn.Render("● " + code)
	default:
		return th.stepErr.Render("● " + code)
	}
}

// hitsChip summarizes search steps: odek's "found N matches" envelope
// becomes a neutral hit count.
func hitsChip(result string, th theme) string {
	for _, ln := range strings.Split(result, "\n") {
		if m := searchHitsRe.FindStringSubmatch(ln); m != nil {
			return th.stepRes.Render(m[1] + " hits")
		}
	}
	return ""
}
