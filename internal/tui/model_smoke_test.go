package tui

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/BackendStack21/bodek/internal/client"
)

// ansiRe strips terminal escape sequences so tests can assert on visible text.
// (glamour inserts SGR resets between words, which would break naive substring
// checks even though the rendered text is correct.)
var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func plain(s string) string { return ansiRe.ReplaceAllString(s, "") }

// newTestModel builds a Model without a live client/TTY for rendering tests.
func newTestModel() *Model {
	ta := textarea.New()
	ta.SetHeight(3) // match New(), so inputHeight row math holds
	// Mirror New()'s composer configuration so previews and layout tests
	// render the production input box.
	ta.Prompt = " "
	ta.ShowLineNumbers = false
	m := &Model{
		th:     newTheme(),
		ta:     ta,
		sp:     spinner.New(),
		curIdx: -1,
		status: "ready",
		events: make(chan client.Event, 8),
	}
	m.resize(100, 30)
	return m
}

// TestRenderStreamingTurn drives a full turn through handleEvent and asserts
// View renders without panicking and reflects the streamed content.
func TestRenderStreamingTurn(t *testing.T) {
	m := newTestModel()

	// Simulate the user having sent a prompt.
	m.msgs = append(m.msgs,
		message{role: roleUser, content: "list the files"},
		message{role: roleAsst, streaming: true},
	)
	m.curIdx = 1
	m.busy = true
	m.runStart = time.Now()

	feed := []client.Event{
		{Type: "session", SessionID: "s1", Model: "deepseek-v4-flash"},
		{Type: "thinking", Content: "let me check the directory"},
		{Type: "tool_call", Name: "shell", Data: `{"command":"ls -la"}`},
		{Type: "tool_result", Name: "shell", Data: "main.go\nREADME.md"},
		{Type: "token", Content: "Here are "},
		{Type: "token", Content: "the files."},
		{Type: "done", SessionContextTokens: 1200, SessionOutputTokens: 340, Latency: 2.5},
	}
	for _, ev := range feed {
		m.handleEvent(ev)
	}

	if m.busy {
		t.Error("model should not be busy after done")
	}
	out := plain(m.View())
	for _, want := range []string{"odek", "shell", "files.", "deepseek-v4-flash"} {
		if !strings.Contains(out, want) {
			t.Errorf("View missing %q", want)
		}
	}
}

// TestEmptyStreamingTurnHidden verifies that while a streamed assistant turn
// has no content yet, the transcript shows neither a "thinking…" placeholder
// nor a bare odek block — the status line above the input is the only
// progress signal.
// The turn block appears atomically once the first real content arrives.
func TestEmptyStreamingTurnHidden(t *testing.T) {
	m := newTestModel()
	m.msgs = append(m.msgs,
		message{role: roleUser, content: "hi"},
		message{role: roleAsst, streaming: true},
	)
	m.curIdx = 1
	m.busy = true

	out := plain(m.conversation())
	if strings.Contains(out, "thinking…") {
		t.Errorf("transcript shows duplicated thinking placeholder:\n%s", out)
	}
	if strings.Contains(out, "⬡ odek") {
		t.Errorf("empty streaming turn renders a bare odek block:\n%s", out)
	}

	// First reasoning chunk makes the turn block visible.
	m.handleEvent(client.Event{Type: "thinking", Content: "hmm"})
	out = plain(m.conversation())
	if !strings.Contains(out, "⬡ odek") || !strings.Contains(out, "hmm") {
		t.Errorf("turn block did not appear after first content:\n%s", out)
	}
}

// TestApprovalAndAutocompleteRender ensures the approval panel and the @-popup
// render at full and narrow widths without panicking.
func TestApprovalAndAutocompleteRender(t *testing.T) {
	m := newTestModel()

	m.approvals = []client.Event{{Type: "approval_request", Risk: "network_egress",
		Name: "shell", Command: "curl https://example.com", Description: "fetch", AllowTrust: true}}
	if out := plain(m.View()); !strings.Contains(out, "approval required") {
		t.Error("approval panel not rendered")
	}
	m.approvals = nil

	m.ac = autocomplete{open: true, query: "cli", items: []client.Resource{
		{ID: "@internal/client/client.go", Type: "file", Label: "internal/client/client.go", Detail: "5.5 KB"},
	}}
	m.relayout()
	if out := plain(m.View()); !strings.Contains(out, "client.go") {
		t.Error("autocomplete popup not rendered")
	}

	// Narrow terminal must not panic.
	m.resize(24, 12)
	_ = m.View()
}

// TestWindowSizeMsg exercises the resize path via Update.
func TestWindowSizeMsg(t *testing.T) {
	m := newTestModel()
	_, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	if !m.ready {
		t.Error("model not ready after WindowSizeMsg")
	}
}

// TestApprovalPanelLayoutStable verifies the screen does not grow a row when
// the approval panel (taller than the textarea it replaces) opens or closes.
func TestApprovalPanelLayoutStable(t *testing.T) {
	m := newTestModel()
	height := func() int { return strings.Count(m.View(), "\n") + 1 }
	base := height()

	// The panel (selectable options + a description) is taller than the
	// textarea it replaces; relayout must shrink the viewport to match so the
	// footer stays put.
	m.handleEvent(client.Event{Type: "approval_request", Risk: "shell_exec",
		Name: "shell", Command: "rm x", Description: "delete files", AllowTrust: true})
	if got := height(); got != base {
		t.Errorf("view height changed when approval opened: %d → %d rows", base, got)
	}
	m.answer("approve")
	if got := height(); got != base {
		t.Errorf("view height changed when approval closed: %d → %d rows", base, got)
	}

	// Very narrow terminal: truncate budgets go ≤ 0 — must not panic.
	m.resize(6, 12)
	m.handleEvent(client.Event{Type: "approval_request", Name: "shell", Command: "rm x"})
	_ = m.View()
}

// TestHeaderNeverExceedsHeight verifies a long model name truncates instead
// of wrapping the header past headerHeight rows, at any width — relayout and
// the mouse offset math assume the header occupies exactly that many rows.
func TestHeaderNeverExceedsHeight(t *testing.T) {
	m := newTestModel()
	m.model = strings.Repeat("very-long-model-id-", 10)
	for _, w := range []int{10, 24, 40, 72, 100} {
		m.resize(w, 24)
		h := m.header()
		if got := strings.Count(h, "\n") + 1; got != headerHeight {
			t.Errorf("width %d: header = %d lines, want %d", w, got, headerHeight)
		}
		if bar, _, _ := strings.Cut(h, "\n"); lipgloss.Width(bar) > w {
			t.Errorf("width %d: header bar is %d columns wide", w, lipgloss.Width(bar))
		}
	}
	// Last loop width (100): the model name must carry an ellipsis.
	bar, _, _ := strings.Cut(m.header(), "\n")
	if !strings.Contains(plain(bar), "…") {
		t.Errorf("long model name should truncate with an ellipsis: %q", plain(bar))
	}
}

// TestRenderStepTinyWidths verifies step rendering degrades without panics at
// absurd widths, and that truncated detail lines actually fit the viewport.
func TestRenderStepTinyWidths(t *testing.T) {
	m := newTestModel()
	s := step{name: "shell", arg: strings.Repeat("x", 100), done: true,
		expanded: true, result: strings.Repeat("y", 200)}
	for _, w := range []int{4, 8, 12, 24} {
		m.resize(w, 20)
		out, _, _ := m.renderStep(s, false, 0, 0, 0) // must not panic
		if w < 8 {
			continue // below the tree connector itself, only the panic matters
		}
		for _, ln := range strings.Split(plain(out), "\n")[1:] { // detail lines
			if n := lipgloss.Width(ln); n > w {
				t.Errorf("width %d: detail line overflows (%d cols): %q", w, n, ln)
			}
		}
	}
}
