package tui

import (
	"os"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/BackendStack21/bodek/internal/client"
)

// TestDumpPreview renders every surface to files for visual inspection
// (BODEK_PREVIEW=/dir). Not a assertion test — a design tool.
func TestDumpPreview(t *testing.T) {
	dir := os.Getenv("BODEK_PREVIEW")
	if dir == "" {
		t.Skip("set BODEK_PREVIEW=<dir> to dump UI previews")
	}
	// The test harness runs without a TTY, so lipgloss's detected profile is
	// ASCII and no color would render. Previews exist to judge color — force
	// TrueColor for the lifetime of this test (plain()-based assertions in
	// other tests are unaffected).
	lipgloss.SetColorProfile(termenv.TrueColor)
	write := func(name, s string) {
		_ = os.WriteFile(dir+"/"+name, []byte(s), 0o644)
	}
	// glamour's renderer sniffs the terminal on construction (model resize)
	// and resets the global profile — re-assert TrueColor before each render
	// so previews judge real colors.
	forceColor := func() { lipgloss.SetColorProfile(termenv.TrueColor) }
	_ = forceColor

	// Conversation: two turns with reasoning, steps, answers, telemetry.
	m := newTestModel()
	m.odekVersion = "1.24.0"
	m.model = "glm-5.3"
	m.serverStream = true
	m.sessionID = "20260821-abcdef"
	m.sessCtxTok = 12140
	m.winCtxTok, m.maxContext = 420_000, 1_000_000
	m.rtt = 34 * time.Millisecond
	m.lastLatency = 8.2

	m.msgs = append(m.msgs,
		message{role: roleUser, content: "fix the login bug in the auth module", sentAt: time.Now().Add(-4 * time.Minute)},
		message{role: roleAsst,
			items: []turnItem{
				{thinking: true, text: "The user wants the login bug fixed. Let me find the auth module and reproduce the failure before touching anything."},
				{stepIdx: 0}, {stepIdx: 1},
			},
			steps: []step{
				{name: "search_files", arg: `{"pattern":"auth"}`, done: true, dur: 143 * time.Millisecond, result: "auth/login.go\nauth/session.go"},
				{name: "patch", arg: "auth/login.go", done: true, dur: 312 * time.Millisecond, result: "--- a/auth/login.go\n+++ b/auth/login.go\n@@ -88,3 +88,4 @@\n func login() {\n-\tcookie := stale()\n+\tcookie := fresh()\n+\tvalidate(cookie)\n }"},
			},
			content: "The bug was a **stale session cookie** on line 88. I've replaced the lookup and added validation.",
			stats:   &turnStats{latency: 8.2, wall: 9 * time.Second, ctxTok: 9100, outTok: 1180, cacheWrite: 800, cacheRead: 40, toolCount: 2, toolGlyphs: []string{"⌕", "✎"}, thought: true}},
		message{role: roleUser, content: "run the tests", sentAt: time.Now().Add(-1 * time.Minute)},
		message{role: roleAsst,
			steps:   []step{{name: "shell", arg: "go test ./auth/...", done: true, isErr: true, dur: 2400 * time.Millisecond, result: "--- FAIL: TestLogin (0.00s)\nFAIL\nexit status 1"}},
			content: "One test still fails — `TestLogin` mocks the old cookie. Updating the mock next.",
			stats:   &turnStats{latency: 3.1, ctxTok: 6400, outTok: 210, toolCount: 1, toolGlyphs: []string{"❯"}}},
	)
	m.refresh()
	forceColor()
	write("conversation.txt", m.View())
	m.expandAll = true
	m.convCount = -1
	m.refresh()
	forceColor()
	write("conversation-expanded.txt", m.View())
	m.expandAll = false

	// Welcome / first run.
	w := newTestModel()
	w.opts.CWD = "/Users/kyberneees/Work/github/21no.de/bodek"
	w.refresh()
	forceColor()
	write("welcome.txt", w.View())

	// Approval panel (friction + normal).
	a := newTestModel()
	a.busy = true
	a.handleEvent(client.Event{Type: "approval_request", ID: "apr-1", Risk: "shell_exec",
		Name: "shell", Command: "rm -rf build/ dist/ && make clean", Description: "clean build artifacts", AllowTrust: true})
	a.refresh()
	forceColor()
	write("approval.txt", a.View())

	// Cockpit.
	m.popover = true
	m.refresh()
	forceColor()
	write("cockpit.txt", m.View())
	m.popover = false

	// Sessions drawer.
	m.sessions = []client.Session{{ID: "20260821-abcdef", Task: "fix the login bug in the auth module", Turns: 4, UpdatedAt: time.Now().Add(-4 * time.Minute), Pinned: true, InputTokens: 9100, OutputTokens: 1180}}
	m.panel = panelSessions
	m.refresh()
	forceColor()
	write("drawer-sessions.txt", m.View())
}
