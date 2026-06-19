package tui

import (
	"strings"
	"testing"

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

// TestRenderStepsSubagentAndError exercises the enriched step rendering: a
// sub-agent label, a nested log tree, and an error-tinted result.
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
	out := plain(m.renderSteps(msg))
	for _, want := range []string{"sub-agent", "⎿", "explorer", "✗", "exit status 1"} {
		if !strings.Contains(out, want) {
			t.Errorf("renderSteps missing %q in:\n%s", want, out)
		}
	}

	// Streaming turn: a not-done step renders the live spinner; a not-done step
	// in a finalized turn renders the pending glyph. Also drive the narrow-width
	// budget floor.
	m.vp.Width = 8
	if s := m.renderSteps(message{streaming: true, steps: []step{{name: "read", arg: "x"}}}); s == "" {
		t.Error("streaming step rendered empty")
	}
	if s := m.renderSteps(message{streaming: false, steps: []step{{name: "read"}}}); !strings.Contains(plain(s), "▸") {
		t.Errorf("pending step missing ▸ glyph: %q", plain(s))
	}
}
