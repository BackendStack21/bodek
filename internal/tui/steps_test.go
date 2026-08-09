package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/BackendStack21/bodek/internal/client"
)

func TestPadRight(t *testing.T) {
	if padRight("ab", 4) != "ab  " {
		t.Errorf("padRight short: %q", padRight("ab", 4))
	}
	if padRight("abcd", 2) != "abcd" {
		t.Errorf("padRight overflow: %q", padRight("abcd", 2))
	}
}

func TestIsSubagent(t *testing.T) {
	for _, n := range []string{"task", "delegate_task", "Subagent", "spawn_subagent"} {
		if !isSubagent(n) {
			t.Errorf("isSubagent(%q) = false, want true", n)
		}
	}
	for _, n := range []string{"shell", "read_file", "grep"} {
		if isSubagent(n) {
			t.Errorf("isSubagent(%q) = true, want false", n)
		}
	}
}

func TestLooksLikeError(t *testing.T) {
	errs := []string{
		"Error: boom", "fatal: not a git repo", "panic: nil deref",
		"Traceback (most recent call last):", "exit status 1",
		"bash: foo: command not found", "open x: no such file or directory",
	}
	for _, s := range errs {
		if !looksLikeError(s) {
			t.Errorf("looksLikeError(%q) = false, want true", s)
		}
	}
	oks := []string{"", "ok", "found 3 matches mentioning error", "PASS"}
	for _, s := range oks {
		if looksLikeError(s) {
			t.Errorf("looksLikeError(%q) = true, want false", s)
		}
	}
}

func TestResultExcerpt(t *testing.T) {
	// Blank lines are dropped; short output is returned whole.
	got := resultExcerpt("a\n\n  \nb")
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Errorf("resultExcerpt blanks: %#v", got)
	}
	// Long output is capped with a "+N more lines" footer.
	var lines []string
	for i := 0; i < 9; i++ {
		lines = append(lines, "line")
	}
	got = resultExcerpt(strings.Join(lines, "\n"))
	if len(got) != 6 { // 5 + footer
		t.Fatalf("resultExcerpt cap: %#v", got)
	}
	if !strings.Contains(got[5], "+4 more lines") {
		t.Errorf("missing overflow footer: %q", got[5])
	}
}

func TestResultPreviewCapsAndSanitizes(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 300; i++ {
		b.WriteString("x\n")
	}
	out := resultPreview(b.String() + "tail\x1b[2J")
	if strings.ContainsRune(out, '\x1b') {
		t.Error("resultPreview left an escape byte")
	}
	if n := strings.Count(out, "\n"); n > 200 {
		t.Errorf("resultPreview did not cap lines: %d", n)
	}
}

// TestSubagentLogNesting verifies a subagent_log lands under the in-flight
// sub-agent step when one exists, and falls back to a notice otherwise.
func TestSubagentLogNesting(t *testing.T) {
	m := newTestModel()
	m.msgs = append(m.msgs, message{role: roleAsst, streaming: true})
	m.curIdx = 0
	m.busy = true

	// A non-sub-agent tool: the log has nowhere to nest → notice.
	m.handleEvent(client.Event{Type: "tool_call", Name: "shell", Data: `{"command":"ls"}`})
	m.handleEvent(client.Event{Type: "subagent_log", SubType: "started", Name: "explorer"})
	if got := strings.Join(m.notices, "\n"); !strings.Contains(got, "subagent · started explorer") {
		t.Errorf("expected fallback notice, notices=%q", got)
	}

	// A sub-agent tool: subsequent logs nest under its step.
	m.handleEvent(client.Event{Type: "tool_call", Name: "delegate_task", Data: `{"task":"explore the repo"}`})
	m.handleEvent(client.Event{Type: "subagent_log", SubType: "tool_call", Name: "read", Detail: "main.go"})
	step := m.msgs[0].steps[len(m.msgs[0].steps)-1]
	if !step.subagent {
		t.Fatal("delegate step not flagged as sub-agent")
	}
	if len(step.logs) != 1 || !strings.Contains(step.logs[0], "read") {
		t.Errorf("sub-agent log not nested: %#v", step.logs)
	}
}

// renderStepsForTest renders all steps of a message through renderStep,
// mirroring the deleted renderSteps helper.
func renderStepsForTest(m *Model, msg message, startLine, msgIdx int) (string, []stepRef) {
	var blocks []string
	var refs []stepRef
	line := startLine
	for i, s := range msg.steps {
		block, ref, n := m.renderStep(s, msg.streaming, msgIdx, i, line)
		blocks = append(blocks, block)
		refs = append(refs, ref)
		line += n
	}
	return strings.Join(blocks, "\n"), refs
}

// TestRenderStepsSubagentAndError exercises the one-line step summary: a
// sub-agent label, an error-tinted status glyph, and no result excerpt.
func TestRenderStepsSubagentAndError(t *testing.T) {
	m := newTestModel()
	msg := message{
		role:      roleAsst,
		streaming: false,
		steps: []step{
			{name: "delegate_task", arg: "explore", subagent: true, done: true,
				logs: []string{"started explorer"}, result: "done"},
			{name: "shell", arg: "go test", done: true, isErr: true,
				result: "exit status 1\nFAIL"},
		},
	}
	out, _ := renderStepsForTest(m, msg, 0, 0)
	plainOut := plain(out)
	for _, want := range []string{"sub-agent", "delegate_task", "explore", "✗", "shell", "go test", "▶"} {
		if !strings.Contains(plainOut, want) {
			t.Errorf("renderSteps missing %q in:\n%s", want, plainOut)
		}
	}
	// The compact head carries no result excerpt — details only on expand.
	if strings.Contains(plainOut, "→") || strings.Contains(plainOut, "exit status 1") {
		t.Errorf("compact head should not show a result excerpt:\n%s", plainOut)
	}
	// Nested logs are still captured but not expanded in the default render.
	if strings.Contains(plainOut, "⎿") || strings.Contains(plainOut, "explorer") {
		t.Errorf("renderSteps should not expand nested logs in one-line mode:\n%s", plainOut)
	}

	// Streaming turn: a not-done step renders the live spinner; a not-done step
	// in a finalized turn renders the pending glyph. Also drive the narrow-width
	// budget floor.
	m.vp.Width = 8
	if s, _ := renderStepsForTest(m, message{streaming: true, steps: []step{{name: "read", arg: "x"}}}, 0, 0); s == "" {
		t.Error("streaming step rendered empty")
	}
	if s, _ := renderStepsForTest(m, message{streaming: false, steps: []step{{name: "read"}}}, 0, 0); !strings.Contains(plain(s), "▸") {
		t.Errorf("pending step missing ▸ glyph: %q", plain(s))
	}
}

func TestRenderStepsExpanded(t *testing.T) {
	m := newTestModel()
	msg := message{role: roleAsst, streaming: false, steps: []step{
		{name: "shell", arg: "go test", done: true, isErr: true, expanded: true,
			result: "exit status 1\nFAIL"},
	}}
	out, refs := renderStepsForTest(m, msg, 5, 0)
	plainOut := plain(out)
	for _, want := range []string{"▼", "shell", "go test", "exit status 1", "FAIL"} {
		if !strings.Contains(plainOut, want) {
			t.Errorf("expanded step missing %q in:\n%s", want, plainOut)
		}
	}
	if strings.Contains(plainOut, "▶") {
		t.Errorf("expanded step should not show ▶ in:\n%s", plainOut)
	}
	if len(refs) != 1 || refs[0].line != 5 {
		t.Errorf("unexpected step refs: %+v", refs)
	}
}

func TestFormatStepDur(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{320 * time.Millisecond, "320ms"},
		{999 * time.Millisecond, "999ms"},
		{1200 * time.Millisecond, "1.2s"},
		{59 * time.Second, "59.0s"},
		{65 * time.Second, "1m05s"},
	}
	for _, c := range cases {
		if got := formatStepDur(c.d); got != c.want {
			t.Errorf("formatStepDur(%v) = %q, want %q", c.d, got, c.want)
		}
	}
}

// TestStepDuration drives a tool call through handleEvent and checks the step
// head: the response time appears once done, and no result excerpt shows.
func TestStepDuration(t *testing.T) {
	m := newTestModel()
	m.msgs = append(m.msgs, message{role: roleAsst, streaming: true})
	m.curIdx = 0
	m.busy = true

	m.handleEvent(client.Event{Type: "tool_call", Name: "shell", Data: `{"command":"go test"}`})
	m.handleEvent(client.Event{Type: "tool_result", Name: "shell", Data: "ok"})
	st := m.msgs[0].steps[0]
	if !st.done || st.started.IsZero() || st.dur <= 0 {
		t.Fatalf("tool_result should stamp the step duration: %+v", st)
	}

	// A done step head shows the duration (fixture-set, for exact rendering).
	msg := message{role: roleAsst, steps: []step{
		{name: "shell", arg: "go test", done: true, result: "exit status 1", dur: 320 * time.Millisecond},
	}}
	out, _ := renderStepsForTest(m, msg, 0, 0)
	plainOut := plain(out)
	if !strings.Contains(plainOut, "320ms") {
		t.Errorf("done head missing duration: %q", plainOut)
	}
	if strings.Contains(plainOut, "→") || strings.Contains(plainOut, "exit status 1") {
		t.Errorf("compact head should not show a result excerpt: %q", plainOut)
	}

	// A running step shows no duration even if one was recorded.
	msg = message{role: roleAsst, streaming: true, steps: []step{
		{name: "shell", arg: "go test", dur: 320 * time.Millisecond},
	}}
	if out, _ := renderStepsForTest(m, msg, 0, 0); strings.Contains(plain(out), "320ms") {
		t.Errorf("running step should not show a duration: %q", plain(out))
	}
}

func TestToggleStep(t *testing.T) {
	m := newTestModel()
	m.msgs = append(m.msgs, message{role: roleAsst, steps: []step{
		{name: "shell", arg: "go test", done: true, result: "exit status 1\nFAIL"},
	}})
	m.toggleStep(0, 0)
	if !m.msgs[0].steps[0].expanded {
		t.Error("toggleStep did not expand the step")
	}
	if m.convCount != -1 {
		t.Error("toggleStep did not invalidate the transcript cache")
	}
	out := plain(m.conversation())
	if !strings.Contains(out, "▼") || !strings.Contains(out, "FAIL") {
		t.Errorf("expanded step not rendered:\n%s", out)
	}
}

// TestToggleStepGuards verifies out-of-range indices are safe no-ops.
func TestToggleStepGuards(t *testing.T) {
	m := newTestModel()
	m.msgs = append(m.msgs, message{role: roleAsst, steps: []step{{name: "read", done: true}}})
	for _, idx := range [][2]int{{-1, 0}, {1, 0}, {5, 0}, {0, -1}, {0, 1}} {
		m.toggleStep(idx[0], idx[1]) // must not panic
	}
	if m.msgs[0].steps[0].expanded {
		t.Error("out-of-range toggleStep should not touch any step")
	}
}

// TestKeyCtrlETogglesToolDetails verifies Ctrl+E flips the global details
// toggle: every step across messages — cached prefix and streaming tail alike —
// reveals its detail lines, and a second press collapses them again.
func TestKeyCtrlETogglesToolDetails(t *testing.T) {
	m := newTestModel()
	m.ta.Focus()
	m.msgs = append(m.msgs,
		message{role: roleAsst, steps: []step{{name: "read", done: true, result: "file contents"}}},
		message{role: roleAsst, streaming: true, steps: []step{{name: "shell", done: true, result: "build ok"}}},
	)
	m.curIdx = 1
	m.busy = true

	// Collapsed by default: no detail lines anywhere.
	out := plain(m.conversation())
	if strings.Contains(out, "file contents") || strings.Contains(out, "build ok") {
		t.Fatalf("details should be hidden by default:\n%s", out)
	}

	// Ctrl+E expands every step in both messages, even while typing.
	m.ta.SetValue("hello")
	m.Update(key("ctrl+e"))
	if !m.expandAll {
		t.Fatal("Ctrl+E should enable expandAll")
	}
	out = plain(m.conversation())
	if !strings.Contains(out, "file contents") || !strings.Contains(out, "build ok") {
		t.Errorf("Ctrl+E should reveal details for all steps:\n%s", out)
	}
	if got := m.ta.Value(); got != "hello" {
		t.Errorf("Ctrl+E should not alter the input, got %q", got)
	}

	// A per-step toggle layers on top of the global one.
	m.toggleStep(0, 0)

	// A second Ctrl+E collapses everything except individually toggled steps.
	m.Update(key("ctrl+e"))
	if m.expandAll {
		t.Fatal("second Ctrl+E should disable expandAll")
	}
	out = plain(m.conversation())
	if strings.Contains(out, "build ok") {
		t.Errorf("second Ctrl+E should hide details:\n%s", out)
	}
	if !strings.Contains(out, "file contents") {
		t.Errorf("individually expanded step should stay open:\n%s", out)
	}
}

// TestCtrlEIndicator verifies the chrome flags the global details toggle: a
// transient note acknowledges the keypress and the footer carries a
// persistent indicator while expandAll holds every step open.
func TestCtrlEIndicator(t *testing.T) {
	m := newTestModel()
	m.Update(key("ctrl+e"))
	if !m.expandAll {
		t.Fatal("^E did not enable expandAll")
	}
	found := false
	for _, n := range m.notices {
		if strings.Contains(n, "tool details on") {
			found = true
		}
	}
	if !found {
		t.Errorf("^E posted no acknowledgement note: %v", m.notices)
	}
	if !strings.Contains(plain(m.footer()), "details") {
		t.Error("footer shows no expandAll indicator while enabled")
	}

	m.Update(key("ctrl+e"))
	if strings.Contains(plain(m.footer()), "details") {
		t.Error("footer indicator should clear when expandAll is disabled")
	}

	// Busy + expandAll: the indicator rides alongside the cancel hint.
	m.busy = true
	m.Update(key("ctrl+e"))
	if foot := plain(m.footer()); !strings.Contains(foot, "cancel") || !strings.Contains(foot, "details") {
		t.Errorf("busy footer should carry both hints: %q", foot)
	}
}

// TestExpandedOutputCap verifies huge tool output is capped with a
// truncation footer instead of flooding the transcript.
func TestExpandedOutputCap(t *testing.T) {
	m := newTestModel()
	m.msgs = append(m.msgs, message{role: roleAsst, steps: []step{
		{name: "shell", done: true, result: strings.Repeat("line\n", 250)},
	}})
	m.toggleStep(0, 0)
	if out := plain(m.conversation()); !strings.Contains(out, "… output truncated") {
		t.Errorf("expanded output should be capped:\n%s", out)
	}
}

func TestKeyETypesLetter(t *testing.T) {
	m := newTestModel()
	m.ta.Focus()

	// The plain 'e' key must always type the letter, even at the start.
	m.Update(key("e"))
	if got := m.ta.Value(); got != "e" {
		t.Errorf("pressing 'e' should type the letter, got %q", got)
	}
	m.Update(key("e"))
	if got := m.ta.Value(); got != "ee" {
		t.Errorf("pressing 'e' again should append the letter, got %q", got)
	}
}

func TestStepLineIndex(t *testing.T) {
	m := newTestModel()
	m.msgs = append(m.msgs,
		message{role: roleAsst, steps: []step{{name: "read", done: true, result: "ok"}}},
		message{role: roleAsst, steps: []step{{name: "shell", done: true, result: "done"}}},
	)
	_ = m.conversation()
	if len(m.stepLineIndex) != 2 {
		t.Fatalf("expected 2 step refs, got %+v", m.stepLineIndex)
	}
	if m.stepLineIndex[0].line >= m.stepLineIndex[1].line {
		t.Errorf("step refs not in ascending order: %+v", m.stepLineIndex)
	}
	msgIdx, stepIdx, ok := m.stepAtLine(m.stepLineIndex[0].line)
	if !ok || msgIdx != 0 || stepIdx != 0 {
		t.Errorf("stepAtLine first header: got %d,%d,%v", msgIdx, stepIdx, ok)
	}
	msgIdx, stepIdx, ok = m.stepAtLine(m.stepLineIndex[1].line)
	if !ok || msgIdx != 1 || stepIdx != 0 {
		t.Errorf("stepAtLine second header: got %d,%d,%v", msgIdx, stepIdx, ok)
	}
	// A line between the two headers maps to nothing — only exact header
	// lines hit.
	mid := (m.stepLineIndex[0].line + m.stepLineIndex[1].line) / 2
	if _, _, ok := m.stepAtLine(mid); ok {
		t.Errorf("stepAtLine between headers should not match (line %d)", mid)
	}
}

// TestStepClickHitTesting verifies only a click on a step's own header line
// toggles it — clicks on prose below the step no longer reach it.
func TestStepClickHitTesting(t *testing.T) {
	m := newTestModel()
	m.msgs = append(m.msgs, message{role: roleAsst,
		content: "line a\nline b\nline c\nline d\nline e",
		steps:   []step{{name: "read", done: true, result: "ok"}},
	})
	_ = m.conversation()
	if len(m.stepLineIndex) != 1 {
		t.Fatalf("expected 1 step ref, got %+v", m.stepLineIndex)
	}
	click := func(line int) {
		// Viewport content begins below the header (2 rows); see Update.
		m.Update(tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonLeft, Y: 2 + line})
	}

	// Prose below the header: no toggle.
	click(m.stepLineIndex[0].line + 3)
	if m.msgs[0].steps[0].expanded {
		t.Error("click below the step header should not toggle it")
	}

	// The header line itself toggles.
	click(m.stepLineIndex[0].line)
	if !m.msgs[0].steps[0].expanded {
		t.Error("click on the step header did not toggle it")
	}
}

// TestExpandedOutputPreservesWhitespace verifies the expanded detail view
// keeps tool output verbatim — indentation and internal spacing intact — so
// diffs, JSON, and code stay aligned when expanded.
func TestExpandedOutputPreservesWhitespace(t *testing.T) {
	m := newTestModel()
	m.msgs = append(m.msgs, message{role: roleAsst, steps: []step{
		{name: "shell", arg: "git diff", done: true,
			result: "line one\n    indented := code\n\tvar x = 1  +  2"},
	}})
	m.toggleStep(0, 0)
	out := plain(m.conversation())
	// Leading spaces and internal spacing survive verbatim; the tab renders
	// expanded (the chrome reflows tabs), which keeps alignment intact.
	for _, want := range []string{"    indented := code", "var x = 1  +  2"} {
		if !strings.Contains(out, want) {
			t.Errorf("expanded output lost whitespace %q in:\n%s", want, out)
		}
	}
}

// TestRunningStepChevron verifies an in-flight step advertises the same
// expand affordance as a finished one — toggleStep works on it too.
func TestRunningStepChevron(t *testing.T) {
	m := newTestModel()
	out, _, _ := m.renderStep(step{name: "shell", arg: "go build"}, true, 0, 0, 0)
	if !strings.Contains(plain(out), "▶") {
		t.Errorf("running step should show a collapsed chevron:\n%s", plain(out))
	}
}
