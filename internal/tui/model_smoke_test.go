package tui

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/BackendStack21/bodek/internal/client"
)

// ansiRe strips terminal escape sequences so tests can assert on visible text.
// (glamour inserts SGR resets between words, which would break naive substring
// checks even though the rendered text is correct.)
var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func plain(s string) string { return ansiRe.ReplaceAllString(s, "") }

// newTestModel builds a Model without a live client/TTY for rendering tests.
func newTestModel() *Model {
	m := &Model{
		th:     newTheme(),
		ta:     textarea.New(),
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

// TestApprovalAndAutocompleteRender ensures the approval panel and the @-popup
// render at full and narrow widths without panicking.
func TestApprovalAndAutocompleteRender(t *testing.T) {
	m := newTestModel()

	m.approval = &client.Event{Type: "approval_request", Risk: "network_egress",
		Name: "shell", Command: "curl https://example.com", Description: "fetch", AllowTrust: true}
	if out := plain(m.View()); !strings.Contains(out, "approval required") {
		t.Error("approval panel not rendered")
	}
	m.approval = nil

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
