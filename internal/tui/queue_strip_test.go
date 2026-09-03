package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/BackendStack21/bodek/internal/client"
)

// Queue-strip tests: the always-visible queue panel above the input area.
// The strip renders one row per queued prompt with ▲/▼/✕ mouse controls, a
// ctrl+q keyboard focus mode (j/k select, h/l move, d delete, esc leaves),
// and collapses to zero rows when the queue is empty.

// queueStripRows splits the rendered strip into its display rows.
func queueStripRows(m *Model) []string {
	return strings.Split(m.queueStripView(), "\n")
}

// stripHeightOK asserts the strip's layout contract: the rendered row count
// matches queueStripHeight, the number relayout and the mouse math budget.
func stripHeightOK(t *testing.T, m *Model) {
	t.Helper()
	if got, want := len(queueStripRows(m)), m.queueStripHeight(); got != want {
		t.Fatalf("strip renders %d rows, height budget says %d", got, want)
	}
}

// clickQueueControl sends a left press on the given control glyph within the
// given strip row, using the same screen math the mouse dispatcher uses.
// Bubbletea reports X in terminal CELLS, so the column is the display width
// of the row prefix before the glyph — not its byte offset.
func clickQueueControl(t *testing.T, m *Model, row int, glyph rune) {
	t.Helper()
	rows := queueStripRows(m)
	if row < 0 || row >= len(rows) {
		t.Fatalf("strip row %d out of range, have %d rows", row, len(rows))
	}
	un := plain(rows[row])
	x, cell := -1, 0
	for i, r := range un {
		if r == glyph {
			x = cell
			_ = i
			break
		}
		cell += lipgloss.Width(string(r))
	}
	if x < 0 {
		t.Fatalf("strip row %d has no %q control: %q", row, string(glyph), un)
	}
	m.Update(tea.MouseMsg{
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
		X:      x,
		Y:      m.queueStripTop() + row,
	})
}

func TestQueueStripHiddenWhenEmpty(t *testing.T) {
	m := newTestModel()
	if m.queueStripVisible() {
		t.Fatal("strip must be hidden while the queue is empty")
	}
	if h := m.queueStripHeight(); h != 0 {
		t.Errorf("empty strip height = %d, want 0", h)
	}
	if v := m.View(); strings.Contains(v, "✕") {
		t.Error("empty queue must not render strip controls")
	}
	// ctrl+q with nothing queued must not latch focus onto a phantom strip.
	m.Update(key("ctrl+q"))
	if m.qfocus {
		t.Error("ctrl+q on an empty queue must not enter focus mode")
	}
}

func TestQueueStripRendersAboveInput(t *testing.T) {
	m := newTestModel()
	busyTurn(m)
	m.queue = []string{"first queued", "second queued"}
	m.refresh()

	if !m.queueStripVisible() {
		t.Fatal("strip should be visible with queued prompts")
	}
	rows := queueStripRows(m)
	if len(rows) != 2 {
		t.Fatalf("strip rows = %d, want 2 (one per queued prompt)", len(rows))
	}
	if got := plain(rows[0]); !strings.Contains(got, "first queued") {
		t.Errorf("head row = %q, want the queue head text", got)
	}
	if got := plain(rows[1]); !strings.Contains(got, "second queued") {
		t.Errorf("row 1 = %q, want the second queued prompt", got)
	}
	for _, r := range rows {
		p := plain(r)
		for _, glyph := range []rune{'▲', '▼', '✕'} {
			if !strings.ContainsRune(p, glyph) {
				t.Errorf("row %q missing %q control", p, string(glyph))
			}
		}
	}

	// Placement: directly above the input, below the busy status line. The
	// viewport starts at row 2 (header); the status line renders a blank
	// separator row plus its own row when visible.
	wantTop := 2 + m.vp.Height + 2
	if got := m.queueStripTop(); got != wantTop {
		t.Errorf("queueStripTop = %d, want %d", got, wantTop)
	}
	lines := strings.Split(plain(m.View()), "\n")
	if !strings.Contains(lines[wantTop], "first queued") {
		t.Errorf("View row %d = %q, want the strip head row", wantTop, lines[wantTop])
	}

	// The layout budget must reserve the strip's rows, or the footer drifts:
	// with the queue present, the below-viewport chrome grows by exactly the
	// strip's height.
	with := m.inputAreaHeight()
	m.queue = nil
	base := m.inputAreaHeight()
	m.queue = []string{"first queued", "second queued"}
	if with != base+m.queueStripHeight() {
		t.Errorf("inputAreaHeight = %d with strip, %d without; strip rows must be budgeted",
			with, base)
	}
}

func TestQueueStripDeleteViaMouse(t *testing.T) {
	m := newTestModel()
	busyTurn(m)
	m.queue = []string{"alpha", "beta", "gamma"}
	m.refresh()

	clickQueueControl(t, m, 1, '✕') // arms (two-step delete)
	clickQueueControl(t, m, 1, '✕') // confirms
	if got := strings.Join(m.queue, ","); got != "alpha,gamma" {
		t.Errorf("queue = %q, want alpha,gamma (beta deleted)", got)
	}
	if rows := queueStripRows(m); len(rows) != 2 {
		t.Errorf("strip rows = %d, want 2 after delete", len(rows))
	}

	// The head row can be deleted too.
	clickQueueControl(t, m, 0, '✕')
	clickQueueControl(t, m, 0, '✕')
	if got := strings.Join(m.queue, ","); got != "gamma" {
		t.Errorf("queue = %q, want gamma", got)
	}
}

func TestQueueStripReorderViaMouse(t *testing.T) {
	m := newTestModel()
	busyTurn(m)
	m.queue = []string{"alpha", "beta", "gamma"}
	m.refresh()

	// Move the tail up one: alpha,gamma,beta.
	clickQueueControl(t, m, 2, '▲')
	if got := strings.Join(m.queue, ","); got != "alpha,gamma,beta" {
		t.Errorf("after ▲ on row 2: %q, want alpha,gamma,beta", got)
	}
	// Move the head down one: gamma,alpha,beta.
	clickQueueControl(t, m, 0, '▼')
	if got := strings.Join(m.queue, ","); got != "gamma,alpha,beta" {
		t.Errorf("after ▼ on row 0: %q, want gamma,alpha,beta", got)
	}
	// Edge clamps: ▲ on the head and ▼ on the tail are no-ops.
	clickQueueControl(t, m, 0, '▲')
	clickQueueControl(t, m, 2, '▼')
	if got := strings.Join(m.queue, ","); got != "gamma,alpha,beta" {
		t.Errorf("edge clamps moved the queue: %q", got)
	}
}

func TestQueueStripKeyboardFocus(t *testing.T) {
	m := newTestModel()
	m.ta.Focus()
	busyTurn(m)
	m.queue = []string{"alpha", "beta"}
	m.refresh()

	// While unfocused, typing still reaches the composer.
	m.Update(key("x"))
	if m.ta.Value() != "x" {
		t.Fatalf("strip visible but unfocused: typing must reach the input, got %q", m.ta.Value())
	}
	m.ta.Reset()

	m.Update(key("ctrl+q"))
	if !m.qfocus {
		t.Fatal("ctrl+q should enter queue focus mode")
	}
	if m.qsel != 0 {
		t.Errorf("qsel = %d, want 0 (head selected on entry)", m.qsel)
	}

	// j/k select; letters are captured by the strip, not the composer.
	m.Update(key("j"))
	if m.qsel != 1 {
		t.Errorf("qsel = %d, want 1 after j", m.qsel)
	}
	m.Update(key("x"))
	if m.ta.Value() != "" {
		t.Errorf("typing in focus mode leaked to the input: %q", m.ta.Value())
	}

	// h moves the selected item toward the head, l back toward the tail.
	m.Update(key("h"))
	if got := strings.Join(m.queue, ","); got != "beta,alpha" {
		t.Errorf("after h: %q, want beta,alpha", got)
	}
	m.Update(key("l"))
	if got := strings.Join(m.queue, ","); got != "alpha,beta" {
		t.Errorf("after l: %q, want alpha,beta", got)
	}

	// d arms, second d deletes (two-step, like every destructive action).
	m.Update(key("d"))
	if len(m.queue) != 2 {
		t.Errorf("first d deleted without confirm: %v", m.queue)
	}
	m.Update(key("d"))
	if got := strings.Join(m.queue, ","); got != "alpha" {
		t.Errorf("after d d: %q, want alpha (beta deleted)", got)
	}
	if m.qsel != 0 {
		t.Errorf("qsel = %d, want clamped 0 after delete", m.qsel)
	}

	// esc leaves focus mode; typing reaches the composer again.
	m.Update(key("esc"))
	if m.qfocus {
		t.Fatal("esc should leave queue focus mode")
	}
	m.Update(key("y"))
	if m.ta.Value() != "y" {
		t.Errorf("typing after esc must reach the input, got %q", m.ta.Value())
	}

	// ctrl+c still reaches the gate while focused — the strip never traps
	// the exit; a second ^C confirms it.
	m.Update(key("ctrl+q"))
	m.Update(key("ctrl+c"))
	if m.confirm != confirmQuit || m.quitting {
		t.Fatalf("ctrl+c must still arm the gate from queue focus: confirm=%v quitting=%v", m.confirm, m.quitting)
	}
	m.Update(key("ctrl+c"))
	if !m.quitting {
		t.Error("second ctrl+c must quit from queue focus mode")
	}
}

func TestQueueStripDrainStepsDown(t *testing.T) {
	m := newTestModel()
	busyTurn(m)
	m.queue = []string{"next up", "after that"}
	m.refresh()

	// Turn end: the head pops and fires (sendQueued pops synchronously when
	// the done event is handled; the returned cmd is not executed — the test
	// model has no client).
	_, cmd := m.handleEvent(client.Event{Type: "done", Latency: 1})
	if cmd == nil {
		t.Fatal("done should drain the queued prompt")
	}
	if got := strings.Join(m.queue, ","); got != "after that" {
		t.Fatalf("queue after drain = %q, want [after that]", got)
	}
	if rows := queueStripRows(m); len(rows) != 1 {
		t.Errorf("strip rows after drain = %d, want 1", len(rows))
	}
}

func TestQueueStripOverflowCap(t *testing.T) {
	m := newTestModel()
	busyTurn(m)
	m.queue = []string{
		"one", "two", "three", "four", "five",
		"six", "seven", "eight", "nine", "ten",
	}
	m.refresh()

	rows := queueStripRows(m)
	if len(rows) != queueStripCap+1 {
		t.Fatalf("rows = %d, want %d + 1 overflow tail", len(rows), queueStripCap)
	}
	if got := plain(rows[queueStripCap]); !strings.Contains(got, "and 2 more") {
		t.Errorf("overflow tail = %q, want an \"and 2 more\" hint", got)
	}

	// Keyboard selection reaches past the visible cap.
	m.Update(key("ctrl+q"))
	for i := 0; i < 9; i++ {
		m.Update(key("j"))
	}
	if m.qsel != 9 {
		t.Fatalf("qsel = %d, want 9", m.qsel)
	}
	rows = queueStripRows(m) // re-render: the tail now names the hidden selection
	if got := plain(rows[queueStripCap]); !strings.Contains(got, "ten") {
		t.Errorf("overflow tail = %q, want the selected hidden row (ten)", got)
	}
	m.Update(key("d"))
	m.Update(key("d"))
	if len(m.queue) != 9 || m.queue[8] != "nine" {
		t.Errorf("queue after delete = %v, want ten removed", m.queue)
	}
}

func TestQueueStripPlainParity(t *testing.T) {
	m := newTestModel()
	m.plain = true
	m.queue = []string{"linear mode queued"}
	m.refresh()

	if v := m.plainView(); !strings.Contains(plain(v), "linear mode queued") {
		t.Errorf("plainView missing the queue strip: %q", plain(v))
	}
}

// TestQueueStripClickUsesCellColumns verifies the ▲▼✕ hit zones in terminal
// CELL columns — the unit bubbletea reports. Byte-offset hit tests drift 2
// cells per multibyte glyph, making ▼ move up and ✕ move down instead of
// delete; both ASCII and multibyte prompt text must hit their own control.
func TestQueueStripClickUsesCellColumns(t *testing.T) {
	for _, text := range []string{"plain ascii prompt", "café ☕ unicode prompt"} {
		m := newTestModel()
		busyTurn(m)
		m.queue = []string{text, "second queued"}
		m.refresh()

		// ▼ on the head row swaps the two entries.
		clickQueueControl(t, m, 0, '▼')
		if len(m.queue) != 2 || m.queue[0] != "second queued" {
			t.Errorf("[%s] ▼ click: queue = %v, want the head moved down", text, m.queue)
			continue
		}
		m.refresh()

		// ✕ on the head row deletes it (two-step: arm, then confirm).
		clickQueueControl(t, m, 0, '✕')
		clickQueueControl(t, m, 0, '✕')
		if len(m.queue) != 1 || m.queue[0] != text {
			t.Errorf("[%s] ✕ click: queue = %v, want %q deleted", text, m.queue, "second queued")
			continue
		}
		m.refresh()

		// ▲ on the last row is a clamped no-op, not a crash or misfire.
		clickQueueControl(t, m, 0, '▲')
		if len(m.queue) != 1 || m.queue[0] != text {
			t.Errorf("[%s] ▲ click at the top: queue = %v, want unchanged", text, m.queue)
		}
		m.refresh()
	}
}

// TestQueueStripHidesControlsWithoutMouse verifies the glyphs disappear when
// mouse tracking is off — dead pixels lie. The strip keeps its rows, adds a
// one-row ^Q hint (the key legend once focus latches), and still collapses
// to zero rows on an empty queue.
func TestQueueStripHidesControlsWithoutMouse(t *testing.T) {
	m := newTestModel()
	m.mouse = false
	busyTurn(m)
	m.queue = []string{"held one", "held two"}
	m.refresh()

	if !m.queueStripVisible() {
		t.Fatal("strip must stay visible without mouse — the queue is real")
	}
	if v := m.queueStripView(); strings.ContainsAny(plain(v), "▲▼✕") {
		t.Errorf("controls must be hidden without --mouse: %q", plain(v))
	}
	if v := plain(m.queueStripView()); !strings.Contains(v, "^q") {
		t.Errorf("missing the ^Q hint row: %q", v)
	}
	// Hint row costs a row: two queue rows + the hint.
	if h := m.queueStripHeight(); h != 3 {
		t.Errorf("strip height = %d, want 3 (2 rows + hint)", h)
	}

	// Focused: the hint becomes the key legend.
	m.qfocus, m.qsel = true, 0
	m.refresh()
	if v := plain(m.queueStripView()); !strings.Contains(v, "delete") {
		t.Errorf("focused hint should show the key legend: %q", v)
	}

	// Clicks on the hint row are inert: no delete, no move, no focus change.
	m.qfocus = false
	before := append([]string(nil), m.queue...)
	m.Update(tea.MouseMsg{
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
		X:      3,
		Y:      m.queueStripTop() + 2, // the hint row
	})
	if !strings.EqualFold(strings.Join(m.queue, "|"), strings.Join(before, "|")) {
		t.Errorf("hint-row click mutated the queue: %v → %v", before, m.queue)
	}
}

// TestQueueStripOneLinePerItem pins the one-line rule: multi-line queued
// prompts collapse to a single strip row, never overflow the row budget.
func TestQueueStripOneLinePerItem(t *testing.T) {
	m := newTestModel()
	busyTurn(m)
	m.queue = []string{"alpha\nbeta\ngamma", "plain prompt"}
	m.refresh()

	stripHeightOK(t, m)
	if rows := queueStripRows(m); len(rows) != 2 {
		t.Fatalf("strip rows = %d, want 2 (one per queued prompt)", len(rows))
	}
	if v := plain(strings.Join(queueStripRows(m), "\n")); !strings.Contains(v, "alpha beta gamma") {
		t.Errorf("multi-line prompt must collapse to one line, got %q", v)
	}
	stripHeightOK(t, m)
}

// TestQueueStripTailPreviewOneLine applies the same rule to the overflow
// tail's selected-item preview.
func TestQueueStripTailPreviewOneLine(t *testing.T) {
	m := newTestModel()
	busyTurn(m)
	for i := 0; i < queueStripCap; i++ {
		m.queue = append(m.queue, "filler")
	}
	m.queue = append(m.queue, "overflow\nspans\nlines")
	m.qfocus = true
	m.qsel = len(m.queue) - 1 // force the tail preview to show this item
	m.refresh()

	stripHeightOK(t, m)
	tail := queueStripRows(m)[queueStripCap]
	if v := plain(tail); !strings.Contains(v, "overflow spans lines") {
		t.Errorf("tail preview must collapse newlines, got %q", v)
	}
}

// TestHelpCardCoversQueueStrip verifies the in-app F1 card teaches the
// queue strip — the README is not in the terminal.
func TestHelpCardCoversQueueStrip(t *testing.T) {
	m := newTestModel()
	m.showHelp()
	if len(m.msgs) == 0 {
		t.Fatal("showHelp appended nothing")
	}
	card := plain(m.msgs[len(m.msgs)-1].content)
	if !strings.Contains(card, "^Q") || !strings.Contains(strings.ToLower(card), "queue") {
		t.Errorf("F1 card missing the queue-strip binding: %q", card)
	}
}
