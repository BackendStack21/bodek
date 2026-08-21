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
	goFail := "=== RUN   TestX\n--- FAIL: TestX (0.00s)\nFAIL\nexit status 1"
	if s, ok := testSummary(goFail); !ok || s != "✗ 1 failing (TestX)" {
		t.Errorf("go fail summary = %q, %v", s, ok)
	}
	goOK := "=== RUN TestY\n--- PASS: TestY\nok  \texample.com/pkg\t0.5s"
	if s, ok := testSummary(goOK); !ok || s != "✓ tests pass" {
		t.Errorf("go pass summary = %q, %v", s, ok)
	}
	pyFail := "FAILED tests/x_test.py::test_a\n1 failed, 3 passed in 0.1s"
	if s, ok := testSummary(pyFail); !ok || !strings.Contains(s, "1 failing") {
		t.Errorf("pytest summary = %q, %v", s, ok)
	}
	if _, ok := testSummary("just some output\nnothing testy"); ok {
		t.Error("plain output matched a test summary")
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

// TestStepHeadSuffix verifies the step-line chips: diffstat and test verdict.
func TestStepHeadSuffix(t *testing.T) {
	th := newTheme()
	if got := plain(stepHeadSuffix("diff", diffFixture, th)); got != "  +2 −1" {
		t.Errorf("diffstat chip = %q", got)
	}
	if got := plain(stepHeadSuffix("shell", "--- PASS: TestY\nok  \tpkg", th)); got != "  ✓ tests pass" {
		t.Errorf("tests chip = %q", got)
	}
	if got := stepHeadSuffix("shell", "ordinary output", th); got != "" {
		t.Errorf("unexpected chip: %q", got)
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
