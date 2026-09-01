package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/BackendStack21/bodek/internal/client"
)

func TestListen(t *testing.T) {
	ch := make(chan client.Event, 1)
	ch <- client.Event{Type: "hello"}
	if msg := listen(ch)(); eventMsg(msg.(eventMsg)).Type != "hello" {
		t.Errorf("listen value = %+v", msg)
	}
	closed := make(chan client.Event)
	close(closed)
	if msg := listen(closed)(); client.Event(msg.(eventMsg)).Type != client.EventDisconnected {
		t.Errorf("listen closed = %+v", msg)
	}
}

func TestGlyphsAllBranches(t *testing.T) {
	for _, n := range []string{"shell", "bash", "write_file", "patch", "read_file", "list_dir", "search_files", "web_search", "browser", "http_batch", "delegate_tasks", "memory", "vision", "transcribe", "unknown_x"} {
		if toolGlyph(n) == "" {
			t.Errorf("empty glyph for %q", n)
		}
	}
	for _, ty := range []string{"session", "skill", "file"} {
		if resourceGlyph(ty) == "" {
			t.Errorf("empty resource glyph for %q", ty)
		}
	}
}

func TestStatusBadgeStates(t *testing.T) {
	m := wired(t)
	m.runStart = time.Now()

	// Busy progress rides the status line above the input; the header badge
	// segment stays empty while a turn runs.
	m.busy, m.lastTool, m.lastArg = true, "shell", "go test"
	if !strings.Contains(plain(m.statusLine()), "tests") {
		t.Error("tool status line missing")
	}
	if plain(m.statusBadge()) != "" {
		t.Error("header badge must stay empty while busy")
	}
	m.lastTool = ""
	m.status = "responding"
	if !strings.Contains(plain(m.statusLine()), "composing") {
		t.Error("responding status line missing")
	}
	m.status = "thinking"
	if plain(m.statusLine()) == "" {
		t.Error("thinking status line empty")
	}

	// Approval arrives mid-turn: the panel owns the input area, so the badge
	// announces it and the status line yields.
	m.approvals = []client.Event{{Type: "approval_request"}}
	if !strings.Contains(plain(m.statusBadge()), "approval") {
		t.Error("approval badge missing")
	}
	if m.statusLine() != "" {
		t.Error("status line must stay hidden behind the approval panel")
	}
	m.approvals = nil
	m.busy = false
	m.disconn = true
	if !strings.Contains(plain(m.statusBadge()), "disconnected") {
		t.Error("disconnected badge missing")
	}
}

// The busy indicator renders on its own row between the transcript and the
// input box — right below the last user message — and releases the row when
// the turn ends.
func TestStatusLinePlacement(t *testing.T) {
	m := newTestModel()
	m.msgs = append(m.msgs,
		message{role: roleUser, content: "unique-marker prompt"},
		message{role: roleAsst, streaming: true},
	)
	m.curIdx = 1
	m.busy = true
	m.status = "thinking"
	m.runStart = time.Now()
	m.refresh() // rebuild the viewport, as sendPrompt would

	view := plain(m.View())
	marker := strings.Index(view, "unique-marker prompt")
	thinking := strings.Index(view, "🧠 thinking")
	if thinking < 0 {
		t.Fatal("busy view missing the thinking status line")
	}
	if marker < 0 || thinking < marker {
		t.Error("status line must render below the last user message")
	}
	if !strings.HasPrefix(m.statusLine(), "\n") {
		t.Error("status line must lead with a blank separator row")
	}
	if strings.Contains(plain(m.header()), "thinking") {
		t.Error("header must not carry the busy indicator anymore")
	}

	// The indicator claims two rows of layout — a blank separator above it,
	// then the status row itself; idle releases both, and a pending approval
	// (which owns the input area) never shows it.
	if got, want := m.inputAreaHeight(), inputHeight+2; got != want {
		t.Errorf("busy inputAreaHeight = %d, want %d", got, want)
	}
	m.busy = false
	if m.statusLine() != "" {
		t.Error("idle model must not render a status line")
	}
	if got := m.inputAreaHeight(); got != inputHeight {
		t.Errorf("idle inputAreaHeight = %d, want %d", got, inputHeight)
	}
	m.busy = true
	m.approvals = []client.Event{{Type: "approval_request"}}
	if m.statusLineVisible() {
		t.Error("status line must stay hidden while an approval is pending")
	}
}

func TestNotReadyView(t *testing.T) {
	m := &Model{th: newTheme(), curIdx: -1}
	if !strings.Contains(m.View(), "starting") {
		t.Error("not-ready view should say starting")
	}
	m.refresh() // no-op when not ready — must not panic
}

func TestAddNoteRingBuffer(t *testing.T) {
	m := wired(t)
	for i := 0; i < 10; i++ {
		m.addNote("note")
	}
	if len(m.notices) > 6 {
		t.Errorf("notices not capped: %d", len(m.notices))
	}
}

func TestTransientNoticeExpires(t *testing.T) {
	m := wired(t)
	m.addNote("alert tier")
	m.addTransientNote("skill · loaded")
	if cmd := m.noticeSweep(); cmd == nil {
		t.Fatal("pending notices should arm the expiry sweep")
	}
	// A sweep landing at the info TTL drops the transient trace but keeps
	// the alert — and re-arms the sweep for the alert's own expiry.
	m.noticeExp[1] = time.Now().Add(-time.Second)
	if _, cmd := m.Update(noticeExpireMsg{}); cmd == nil {
		t.Error("expiry sweep should reschedule while an alert is pending")
	}
	if got := strings.Join(m.notices, "\n"); got != "alert tier" {
		t.Errorf("notices after expiry = %q", got)
	}
}

func TestRenderNoticesHidesExpired(t *testing.T) {
	m := wired(t)
	m.addTransientNote("skill · loaded")
	m.noticeExp[0] = time.Now().Add(-time.Second)
	if got := m.renderNotices(); got != "" {
		t.Errorf("expired notice still rendered: %q", got)
	}
}

func TestArgPreviewFallbacks(t *testing.T) {
	if got := argPreview(`{"foo":"bar"}`); got != "bar" {
		t.Errorf("argPreview value-join = %q", got)
	}
	if got := argPreview(`{"n":123}`); got != "" {
		t.Errorf("argPreview non-string = %q", got)
	}
}

func TestRenderFallbacks(t *testing.T) {
	m := wired(t)
	if m.render("") != "" {
		t.Error("render empty should be empty")
	}
	m.glam = nil
	if m.render("# hi") != "# hi" {
		t.Error("render without glamour should pass through")
	}
}

func TestAcceptCompletionNoRef(t *testing.T) {
	m := wired(t)
	m.ac.items = []client.Resource{{ID: "@x", Label: "x"}}
	m.ac.open = true
	m.ta.SetValue("no at sign here")
	m.acceptCompletion() // refStart fails → just closes
	if m.ac.open {
		t.Error("accept should close the popup")
	}
	// Empty items → closes.
	m.ac.items = nil
	m.ac.open = true
	m.acceptCompletion()
	if m.ac.open {
		t.Error("accept with no items should close")
	}
}

func TestPanelLenAndKeys(t *testing.T) {
	m := wired(t)
	if m.panelLen() != 0 {
		t.Error("panelLen with no panel should be 0")
	}
	// 'd' on the models panel is a no-op (delete only applies to sessions).
	m.panel = panelModels
	m.models = []client.ModelInfo{{ID: "a"}}
	m.Update(key("d"))
	// vim-style nav + q to close.
	m.Update(key("j"))
	m.Update(key("k"))
	m.Update(key("q"))
	if m.panel != panelNone {
		t.Error("q should close panel")
	}
}

func TestSubmitGuards(t *testing.T) {
	m := wired(t)
	m.busy = true
	if m.submit() != nil {
		t.Error("submit while busy should be nil")
	}
	m.busy = false
	m.ta.SetValue("   ")
	if m.submit() != nil {
		t.Error("submit with blank text should be nil")
	}
	m.disconn = true
	m.ta.SetValue("hi")
	if cmd := m.submit(); cmd == nil {
		t.Error("submit while disconnected should arm the warning's expiry sweep")
	}
	if m.ta.Value() != "hi" {
		t.Error("submit while disconnected must keep the draft")
	}
	if len(m.notices) == 0 {
		t.Error("submit while disconnected should explain why nothing was sent")
	}
}

// TestWrapText covers the approval panel's hard-wrap helper: degenerate
// widths, empty and blank lines, exact fits, and unbreakable words.
func TestWrapText(t *testing.T) {
	cases := []struct {
		in   string
		n    int
		want []string
	}{
		{"", 5, []string{""}},                             // empty input still claims its row
		{"hello", 5, []string{"hello"}},                   // exact width
		{"hello", 0, []string{"h", "e", "l", "l", "o"}},   // width floors at 1
		{"abcdefghij", 4, []string{"abcd", "efgh", "ij"}}, // unbreakable word chunks
		{"a\n\nb", 10, []string{"a", "", "b"}},            // blank lines survive
	}
	for _, tc := range cases {
		if got := wrapText(tc.in, tc.n); strings.Join(got, "\n") != strings.Join(tc.want, "\n") {
			t.Errorf("wrapText(%q, %d) = %q, want %q", tc.in, tc.n, got, tc.want)
		}
	}
}

func TestTinyHelpers(t *testing.T) {
	if orDash("") != "—" || orDash("x") != "x" {
		t.Error("orDash")
	}
	if max(1, 2) != 2 || max(5, 3) != 5 {
		t.Error("max")
	}
	if padLeft("ab", 4) != "  ab" || padLeft("abcd", 2) != "abcd" {
		t.Errorf("padLeft: %q / %q", padLeft("ab", 4), padLeft("abcd", 2))
	}
	if human(0) != "0" {
		t.Error("human zero")
	}
}

func TestQuitKeys(t *testing.T) {
	m := wired(t)
	m.Update(key("ctrl+c"))
	if m.confirm != confirmQuit || m.quitting {
		t.Fatalf("ctrl+c should arm the gate: confirm=%v quitting=%v", m.confirm, m.quitting)
	}
	m.Update(key("ctrl+c"))
	if !m.quitting {
		t.Error("second ctrl+c should set quitting")
	}
}
