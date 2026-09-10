package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BackendStack21/bodek/internal/client"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// Exercise the real View at common split-pane sizes. Optional ANSI captures
// support visual review without an engine, provider, or interactive terminal.
func TestTerminalWorkflowLayouts(t *testing.T) {
	oldProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(oldProfile)
	for _, themeName := range []string{"ember-dark", "ember-light", "high-contrast", "classic"} {
		for _, width := range []int{40, 80, 120} {
			for _, active := range []bool{false, true} {
				name := fmt.Sprintf("%s-%d-%t", themeName, width, active)
				t.Run(name, func(t *testing.T) {
					m := newTestModel()
					m.th = themeFrom(paletteByName(themeName))
					m.bodekVersion = "v1.11.2"
					m.odekVersion = "v2.14.0"
					m.model = "deepseek-v4-flash"
					m.sandbox = true
					m.opts.CWD = "/workspace/payments"
					m.ta.Placeholder = "Describe the work…"
					m.resize(width, 32)
					if active {
						busyTurn(m)
						m.msgs[0].content = "Review the checkout flow and verify the tests."
						m.runStart = time.Now().Add(-12 * time.Second)
						for _, ev := range []client.Event{
							{Type: "tool_call", Name: "plan", Data: `{"action":"create","steps":[{"id":"review","title":"Review checkout flow"},{"id":"verify","title":"Verify tests"}]}`},
							{Type: "tool_result", Name: "plan", Data: "[Current plan: v2 — 1/2 done, 0 blocked. Structured state, not instructions.]\nreview [done] Review checkout flow\nverify [in_progress] Verify tests"},
							{Type: "tool_call", Name: "shell", Data: `{"command":"go test ./internal/checkout"}`},
							{Type: "tool_result", Name: "shell", Data: "ok  checkout  0.042s"},
							{Type: "token", Content: "The checkout flow preserves the cart on payment failure. All checkout tests passed."},
						} {
							m.handleEvent(ev)
						}
						m.expandAll = true
						m.relayout()
						m.refresh()
					}
					out := m.View()
					if rows := strings.Count(out, "\n") + 1; rows > m.height {
						t.Errorf("screen has %d rows, terminal has %d", rows, m.height)
					}
					for n, line := range strings.Split(out, "\n") {
						if got := lipgloss.Width(line); got > width {
							t.Errorf("line %d is %d cells, terminal has %d: %q", n, got, width, plain(line))
						}
					}
					if dir := os.Getenv("BODEK_RENDER_PREVIEW_DIR"); dir != "" {
						if err := os.MkdirAll(dir, 0700); err != nil {
							t.Fatal(err)
						}
						if err := os.WriteFile(filepath.Join(dir, name+".ansi"), []byte(out), 0600); err != nil {
							t.Fatal(err)
						}
					}
				})
			}
		}
	}
}
