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

	m.busy, m.lastTool, m.lastArg = true, "shell", "go test"
	if !strings.Contains(plain(m.statusBadge()), "tests") {
		t.Error("tool status badge missing")
	}
	m.lastTool = ""
	m.status = "responding"
	if !strings.Contains(plain(m.statusBadge()), "composing") {
		t.Error("responding badge missing")
	}
	m.status = "thinking"
	if plain(m.statusBadge()) == "" {
		t.Error("thinking badge empty")
	}
	m.busy = false
	m.approval = &client.Event{Type: "approval_request"}
	if !strings.Contains(plain(m.statusBadge()), "approval") {
		t.Error("approval badge missing")
	}
	m.approval = nil
	m.disconn = true
	if !strings.Contains(plain(m.statusBadge()), "disconnected") {
		t.Error("disconnected badge missing")
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
	if m.submit() != nil {
		t.Error("submit while disconnected should be nil")
	}
}

func TestTinyHelpers(t *testing.T) {
	if orDash("") != "—" || orDash("x") != "x" {
		t.Error("orDash")
	}
	if max(1, 2) != 2 || max(5, 3) != 5 {
		t.Error("max")
	}
	if pad("ab", 4) != "ab  " || pad("abcd", 2) != "abcd" {
		t.Errorf("pad: %q / %q", pad("ab", 4), pad("abcd", 2))
	}
	if human(0) != "0" {
		t.Error("human zero")
	}
}

func TestQuitKeys(t *testing.T) {
	m := wired(t)
	m.Update(key("ctrl+c"))
	if !m.quitting {
		t.Error("ctrl+c should set quitting")
	}
}
