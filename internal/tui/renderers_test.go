package tui

import (
	"strings"
	"testing"
)

// diffFixture is a minimal unified diff: one file, one hunk, 2 adds 1 del.
const diffFixture = `--- a/internal/tui/view.go
+++ b/internal/tui/view.go
@@ -10,7 +10,8 @@ func header() {
   base := m.width
-  old line
+  new line one
+  new line two
 }`

func TestDiffStat(t *testing.T) {
	adds, dels, ok := diffStat(diffFixture)
	if !ok || adds != 3 || dels != 2 {
		// +++/--- markers are excluded; "+  new line one", "+  new line two",
		// and the +++ file marker... markers excluded → 2 adds? No: "+ new
		// line one", "+ new line two" = 2; del "-  old line" = 1... plus the
		// --- marker excluded. Assert precisely below.
		t.Logf("diffStat = +%d −%d (ok=%v)", adds, dels, ok)
	}
	if adds != 2 || dels != 1 {
		t.Errorf("diffStat = +%d −%d, want +2 −1", adds, dels)
	}
	if _, _, ok := diffStat("plain text\nno diff here"); ok {
		t.Error("diffStat matched plain text")
	}
}

func TestRenderDiffTints(t *testing.T) {
	th := newTheme()
	lines := renderDiff(diffFixture, 80, th)
	if len(lines) == 0 {
		t.Fatal("no diff lines rendered")
	}
	joined := strings.Join(lines, "\n")
	for _, want := range []string{"new line one", "new line two", "old line", "@@ -10,7"} {
		if !strings.Contains(plain(joined), want) {
			t.Errorf("diff render missing %q:\n%s", want, plain(joined))
		}
	}
	// File markers and hunk headers render dim/steel — content presence is
	// asserted above; exact tinting depends on the terminal color profile.
}

func TestRenderNumbered(t *testing.T) {
	th := newTheme()
	lines := renderNumbered("package main\n\nfunc x() {}\n", 60, th)
	joined := plain(strings.Join(lines, "\n"))
	if !strings.Contains(joined, "1 │") || !strings.Contains(joined, "package main") {
		t.Errorf("numbered view missing line numbers:\n%s", joined)
	}
	if !strings.Contains(joined, "2 │") {
		t.Errorf("blank line should keep its number row:\n%s", joined)
	}
}

func TestRenderJSON(t *testing.T) {
	th := newTheme()
	out := renderJSON(`{"a":1,"b":[1,2]}`, 60, th)
	if out == nil {
		t.Fatal("valid JSON failed to render")
	}
	joined := plain(strings.Join(out, "\n"))
	if !strings.Contains(joined, `"a": 1`) {
		t.Errorf("JSON not indented:\n%s", joined)
	}
	if renderJSON("{not json", 60, th) != nil {
		t.Error("invalid JSON should fall back (nil)")
	}
}

func TestTestSummary(t *testing.T) {
	passes := []struct{ name, out, want string }{
		{"go verbose", "=== RUN TestY\n--- PASS: TestY\nok  \texample.com/pkg\t0.5s", "✓ tests pass"},
		{"go ok cached", "ok  \texample.com/pkg\t(cached)", "✓ tests pass"},
		{"go ok only", "ok  \texample.com/pkg\t1.2s\nok  \texample.com/other\t0.3s", "✓ tests pass"},
		{"go coverage", "ok  \texample.com/pkg\t0.5s\tcoverage: 82.3% of statements", "✓ tests pass · 82.3% cov"},
		{"go coverage averaged", "ok  \ta\t0.5s\tcoverage: 82.0% of statements\n" +
			"ok  \tb\t0.3s\tcoverage: 84.0% of statements", "✓ tests pass · 83% cov"},
		{"go skips", "=== RUN TestY\n--- PASS: TestY\n--- SKIP: TestS (0.00s)\nok  \texample.com/pkg\t0.5s", "✓ tests pass · 1 skipped"},
		{"pytest", "=================================== test session starts ==\n5 passed in 0.12s", "✓ 5 passed"},
		// "1 warning" in the summary line must not pollute the chip.
		{"pytest skips", "5 passed, 2 skipped, 1 warning in 0.12s", "✓ 5 passed · 2 skipped"},
		// Suite-level lines ("Test Suites"/"Test Files") never inflate the
		// test count.
		{"jest", "Test Suites: 1 passed, 1 total\nTests:       3 passed, 3 total", "✓ 3 passed"},
		{"vitest", "Test Files  2 passed (2)\n     Tests  5 passed | 3 skipped (8)", "✓ 5 passed · 3 skipped"},
		{"cargo", "running 5 tests\ntest a ... ok\n" +
			"test result: ok. 5 passed; 0 failed; 2 ignored; 0 measured", "✓ 5 passed · 2 skipped"},
		{"tap", "1..2\nok 1 - adds numbers\nok 2 - trims input", "✓ tests pass"},
	}
	for _, tc := range passes {
		if s, ok := testSummary(tc.out); !ok || s != tc.want {
			t.Errorf("%s: summary = %q, %v; want %q", tc.name, s, ok, tc.want)
		}
	}
	fails := []struct{ name, out, want string }{
		{"go", "=== RUN   TestX\n--- FAIL: TestX (0.00s)\nFAIL\nexit status 1", "✗ 1 failing (TestX)"},
		// FAILED lines plus the pytest summary line must not double count.
		{"pytest", "FAILED tests/x_test.py::test_a\n1 failed, 3 passed in 0.1s", "✗ 1 failing"},
		{"pytest quiet", ".F.\n1 failed, 2 passed in 0.3s", "✗ 1 failing"},
		{"cargo", "test result: FAILED. 0 passed; 2 failed; 0 ignored; 0 measured", "✗ 2 failing"},
		{"jest", "✕ renders header (5 ms)\nTests: 1 failed, 2 passed, 3 total", "✗ 1 failing"},
	}
	for _, tc := range fails {
		if s, ok := testSummary(tc.out); !ok || s != tc.want {
			t.Errorf("%s: summary = %q, %v; want %q", tc.name, s, ok, tc.want)
		}
	}
	// Ordinary output that used to trip the loose substring matcher must
	// stay silent: no bare "ok" lines, no prose "passed", no "testsuite",
	// and coverage numbers without a pass signal are not a verdict.
	noise := []string{
		"Build passed",
		"All validation checks passed",
		"Deployment passed all gates",
		"ok 127 packages found",
		"listing testsuite\ntestsuite/  fixtures/",
		"everything is ok",
		"0 packets passed the filter",
		"coverage: 82.3% of statements",
		"just some output\nnothing testy",
		"",
	}
	for _, s := range noise {
		if got, ok := testSummary(s); ok {
			t.Errorf("testSummary(%q) = %q; want no match", s, got)
		}
	}
}

// TestStepDetailDispatch verifies shape → renderer selection and the
// verbatim fallback.
func TestStepDetailDispatch(t *testing.T) {
	th := newTheme()
	// Diff result → diff renderer (hunk header survives).
	if out := stepDetail("diff", diffFixture, 80, th); len(out) == 0 ||
		!strings.Contains(plain(strings.Join(out, "\n")), "@@ -10,7") {
		t.Errorf("diff tool did not use the diff renderer")
	}
	// Shell output containing a diff also picks it up.
	if out := stepDetail("shell", "git diff\n"+diffFixture, 80, th); len(out) == 0 ||
		!strings.Contains(plain(strings.Join(out, "\n")), "new line one") {
		t.Errorf("shell git-diff output did not use the diff renderer")
	}
	// read_file → numbered lines.
	if out := stepDetail("read_file", "package main\n", 80, th); len(out) == 0 ||
		!strings.Contains(plain(strings.Join(out, "\n")), "1 │") {
		t.Errorf("read_file did not use the numbered renderer")
	}
	// JSON → indented.
	if out := stepDetail("json_query", `{"k":1}`, 80, th); len(out) == 0 ||
		!strings.Contains(plain(strings.Join(out, "\n")), `"k": 1`) {
		t.Errorf("json_query did not use the JSON renderer")
	}
	// Everything else → verbatim lines.
	if out := stepDetail("web_search", "result one\nresult two", 80, th); len(out) != 2 {
		t.Errorf("fallback renderer lines = %d", len(out))
	}
}

// TestStepHeadSuffix verifies the step-line chips: diffstat, test verdict,
// and the arg-gated family (git, lint, warnings, HTTP) plus search hits.
func TestStepHeadSuffix(t *testing.T) {
	th := newTheme()
	chips := []struct{ name, tool, arg, result, want string }{
		{"diffstat", "diff", "", diffFixture, "  +2 −1"},
		{"go pass", "shell", "go test ./...", "--- PASS: TestY\nok  \tpkg\t0.5s", "✓ tests pass"},
		{"ordinary", "shell", "ls", "ordinary output", ""},
		{"read_file no verdict", "read_file", "out.txt", "ok  \tpkg\t0.5s", ""},
		{"prose pass", "shell", "./deploy.sh", "Build passed\nAll checks passed", ""},
		{"commit", "shell", `git commit -m "fix: stuff"`,
			"[main a1b2c3def9] fix: stuff\n 1 file changed, 2 insertions(+)", "⎇ a1b2c3d fix: stuff"},
		{"commit root", "shell", "git commit -m init", "[main (root-commit) 1234567] init", "⎇ 1234567 init"},
		{"commit needs git arg", "shell", "echo hi", "[main a1b2c3d] x", ""},
		{"commit subject capped", "shell", "git commit -m x",
			"[main abcdef1234] fix: 12345678901234567890123", "⎇ abcdef1 fix: 12345678901234567890…"},
		{"push", "shell", "git push origin main",
			"To github.com:me/x.git\n   7c0a0dc..8cefa19  main -> main", "↑ main"},
		{"push force", "shell", "git push --force origin main",
			"+   7c0a0dc...8cefa19  main -> main", "↑ main"},
		{"push new branch", "shell", "git push -u origin feat/x",
			"* [new branch]      feat/x -> feat/x", "↑ feat/x"},
		{"push up to date", "shell", "git push", "Everything up-to-date", "↑ up to date"},
		{"push needs git arg", "shell", "echo pushing", "   7c0a0dc..8cefa19  main -> main", ""},
		{"lint clean", "shell", "golangci-lint run", "0 issues.", "✓ lint clean"},
		{"lint issues", "shell", "make lint", "2 issues.", "2 issues"},
		{"lint ruff", "shell", "ruff check .", "All checks passed!", "✓ lint clean"},
		{"lint needs lint arg", "shell", "go build ./...", "0 issues.", ""},
		{"warnings emitted", "shell", "cargo build", "warning: unused variable\nwarning: 2 warnings emitted", "⚠ 2 warnings"},
		{"warnings generated", "shell", "make", "lib.c:3:5: warning: unused var\n3 warnings generated.", "⚠ 3 warnings"},
		{"no zero-warning chip", "shell", "cargo build", "warning: 0 warnings emitted", ""},
		{"http ok", "shell", "curl -s https://api.example.com/health", "HTTP/2 200", "● 200"},
		{"http err", "shell", "curl http://x.dev/api", "HTTP/1.1 404 Not Found", "● 404"},
		{"http 3xx amber", "shell", "curl -L http://x.dev", "HTTP/1.1 302 Found", "● 302"},
		{"http redirect chain", "shell", "curl -L http://x.dev", "HTTP/1.1 301 Moved\nHTTP/2 200", "● 200"},
		{"http wget style", "shell", "wget -qO- http://x.dev",
			"--2026-08-31 12:00:00--  http://x.dev/\nHTTP request sent, awaiting response... 200 OK", "● 200"},
		{"http needs client arg", "shell", "cat headers.txt", "HTTP/2 200", ""},
		{"go pass colored", "shell", "go test ./...", "[32mok  \texample.com/pkg[0m  [33m0.5s[0m", "✓ tests pass"},
		{"pytest colored pass", "shell", "pytest -q", "[32m5 passed[0m in [32m0.12s[0m", "✓ 5 passed"},
		{"pytest colored fail", "shell", "pytest -q", "[31mFAILED tests/x.py::test_a[0m\n[31m1 failed[0m, 3 passed in 0.1s", "1 failing"},
		{"pytest default pass", "shell", "pytest",
			"============================================================ 5 passed in 0.12s ==========================" +
				"==================", "✓ 5 passed"},
		{"pytest default fail", "shell", "pytest",
			"================================= FAILURES ==========================\n======= 1 failed, 2 passed in 0.3s ========" +
				"====", "1 failing"},
		{"golangci colon issues", "shell", "golangci-lint run", "2 issues:\n- x.go:1:1: boom", "2 issues"},
		{"prose counts stay silent", "shell", "./validate.sh", "10 files failed validation, 5 passed", ""},
		{"prose counts stay silent 2", "shell", "./validate.sh", "5 passed, 10 failed validation", ""},
		{"lint word gate", "shell", "git commit -m fix-lint", "0 issues.", ""},
		{"warnings prose anchored", "shell", "grep -rn TODO .", "42 warnings generated during the scan", ""},
		{"warnings singular", "shell", "cargo build", "warning: 1 warning emitted", "⚠ 1 warning"},
		{"eslint issues red", "shell", "eslint .", "✖ 2 problems (2 errors, 0 warnings)", "2 issues"},
		{"eslint zero", "shell", "eslint .", "✖ 0 problems", "✓ lint clean"},
		{"jest duplicated summaries", "shell", "npm test",
			"Test Suites: 1 passed, 1 total\nTests: 3 passed, 3 total\nTests: 3 passed, 3 total", "✓ 3 passed"},
		{"jest suites only", "shell", "npm test", "Test Suites: 1 passed, 1 total", "✓ 1 passed"},
		{"hits only on search tools", "read_file", "novel.txt", "found 5 matches in the manuscript", ""},
		{"hits", "grep", "needle", "found 3 matches mentioning error", "3 hits"},
		{"hits not on shell", "shell", "grep -rn needle .", "found 3 matches mentioning error", ""},
		{"test beats git", "shell", "git commit -m x", "--- PASS: TestY\nok  \tpkg\t0.5s", "✓ tests pass"},
	}
	for _, tc := range chips {
		if got := plain(stepHeadSuffix(tc.tool, tc.arg, tc.result, th)); got != tc.want {
			t.Errorf("%s: chip = %q, want %q", tc.name, got, tc.want)
		}
	}
	// Severity styling: lint issues and 5xx run red, warnings run amber.
	if got := stepHeadSuffix("shell", "make lint", "2 issues.", th); got != th.stepErr.Render("2 issues") {
		t.Errorf("lint issues style = %q", plain(got))
	}
	if got := stepHeadSuffix("shell", "cargo build", "warning: 2 warnings emitted", th); got != th.badgeWarn.Render("⚠ 2 warnings") {
		t.Errorf("warnings style = %q", plain(got))
	}
	if got := stepHeadSuffix("shell", "curl http://x", "HTTP/1.1 500", th); got != th.stepErr.Render("● 500") {
		t.Errorf("http 5xx style = %q", plain(got))
	}
}

// TestStepExpansionTypedRenderers drives renderStep end to end: the step line
// gains its chip, and expansion shows the typed body.
func TestStepExpansionTypedRenderers(t *testing.T) {
	m := newTestModel()
	m.msgs = append(m.msgs, message{role: roleAsst})
	s := step{name: "patch", arg: "auth.go", result: diffFixture, done: true, expanded: true}
	block, ref, n := m.renderStep(s, false, 0, 0, 0)
	out := plain(block)
	if !strings.Contains(out, "+2 −1") {
		t.Errorf("step line missing diffstat:\n%s", out)
	}
	if !strings.Contains(out, "new line one") {
		t.Errorf("expanded step missing diff body:\n%s", out)
	}
	if n != lineCount(block) {
		t.Errorf("line count = %d, want %d", n, lineCount(block))
	}
	if ref.line != 0 {
		t.Errorf("stepRef line = %d", ref.line)
	}
}
