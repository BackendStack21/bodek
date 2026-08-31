package tui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strconv"
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
	warnEmittedRe    = regexp.MustCompile(`^warning: (\d+) warnings? emitted\.?$`)
	warnGeneratedRe  = regexp.MustCompile(`^(\d+) warnings? generated\.?$`)
	httpStatusRe     = regexp.MustCompile(`(?i)^HTTP/[\d.]+ (\d{3})`)
	wgetStatusRe     = regexp.MustCompile(`awaiting response\.\.\.?[ \t]?(\d{3})`)
	searchHitsRe     = regexp.MustCompile(`found (\d+) matches`)
)

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
// At most one chip per step, in that precedence.
func stepHeadSuffix(name, arg, result string, th theme) string {
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
	if s, ok := testSummary(result); ok {
		if strings.HasPrefix(s, "✗") {
			// The step's status icon already flags the failure — the chip
			// names what failed, without a second ✗.
			return th.stepErr.Render(strings.TrimPrefix(s, "✗ "))
		}
		return th.stepDone.Render(s)
	}
	for _, chip := range []string{
		gitChip(arg, result, th),
		lintChip(arg, result, th),
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

// lintChip reports the linter outcome: "✓ lint clean" or a red issue
// count. Gated on linter-sounding commands, so ruff's "All checks passed"
// cannot leak into arbitrary output.
func lintChip(arg, result string, th theme) string {
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
				return th.stepDone.Render("✓ lint clean")
			}
			return th.stepErr.Render(m[1] + " issues")
		}
		if strings.HasPrefix(t, "All checks passed") {
			return th.stepDone.Render("✓ lint clean")
		}
		if m := eslintProblemsRe.FindStringSubmatch(t); m != nil {
			if n, _ := strconv.Atoi(m[1]); n == 0 {
				return th.stepDone.Render("✓ lint clean")
			}
			return th.stepErr.Render(m[1] + " issues")
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
