package tui

import (
	"strconv"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func mouse(m *Model, action tea.MouseAction, x, y int) tea.Cmd {
	_, cmd := m.Update(tea.MouseMsg{Action: action, Button: tea.MouseButtonLeft, X: x, Y: y})
	return cmd
}

// dragSelect presses at one screen cell, drags to another, and releases.
func dragSelect(m *Model, x0, y0, x1, y1 int) {
	mouse(m, tea.MouseActionPress, x0, y0)
	mouse(m, tea.MouseActionMotion, x1, y1)
	mouse(m, tea.MouseActionRelease, x1, y1)
}

// selText is what the active selection would copy.
func selText(m *Model) string {
	return selectedText(m.selLines(), m.sel)
}

// findCell locates needle on the rendered transcript: content line and
// display column of its first cell.
func findCell(t *testing.T, m *Model, needle string) (line, col int) {
	t.Helper()
	for i, l := range m.contentLines() {
		p := ansi.Strip(l)
		if j := strings.Index(p, needle); j >= 0 {
			return i, ansi.StringWidth(p[:j])
		}
	}
	t.Fatalf("%q is not on the transcript:\n%s", needle, plain(m.vpContent))
	return 0, 0
}

func selModel(t *testing.T, msgs ...message) *Model {
	t.Helper()
	t.Setenv("SSH_TTY", "/dev/ttys001") // OSC 52: no local clipboard helper runs
	m := newTestModel()
	m.msgs = append(m.msgs, msgs...)
	m.refresh()
	m.vp.GotoTop()
	return m
}

func TestDragSelectsAndCopiesOnRelease(t *testing.T) {
	m := selModel(t, message{role: roleUser, content: "alpha beta gamma\nsecond line here"})
	line, col := findCell(t, m, "beta")
	y := headerHeight + line

	mouse(m, tea.MouseActionPress, col, y)
	if m.sel.on {
		t.Fatal("a press alone must not select")
	}
	mouse(m, tea.MouseActionMotion, col+3, y)
	if !m.sel.on || !m.sel.dragging {
		t.Fatalf("drag did not start a selection: %+v", m.sel)
	}
	if got := selText(m); got != "beta" {
		t.Fatalf("selected %q, want beta", got)
	}
	if !strings.Contains(m.View(), selOn+"beta"+selOff) {
		t.Error("the selected run is not painted in reverse video")
	}
	cmd := mouse(m, tea.MouseActionRelease, col+3, y)
	if cmd == nil || !m.copyFlashing() {
		t.Fatal("release did not copy the selection")
	}
	if !m.sel.on || m.sel.dragging {
		t.Errorf("the copied range should stay highlighted: %+v", m.sel)
	}
}

func TestDragAcrossLinesDropsSharedMargin(t *testing.T) {
	m := selModel(t, message{role: roleAsst, content: "x",
		rendered: "  func main() {\n      run()\n  }"})
	l0, c0 := findCell(t, m, "func main")
	l1, c1 := findCell(t, m, "}")
	mouse(m, tea.MouseActionPress, c0, headerHeight+l0)
	mouse(m, tea.MouseActionMotion, c1, headerHeight+l1)
	if got, want := selText(m), "func main() {\n    run()\n}"; got != want {
		t.Fatalf("selected %q, want %q", got, want)
	}
	// Starting mid-line keeps the first row's own text and still strips
	// the margin from the rows below it.
	mouse(m, tea.MouseActionRelease, c1, headerHeight+l1)
	mouse(m, tea.MouseActionPress, c0+5, headerHeight+l0)
	mouse(m, tea.MouseActionMotion, c1, headerHeight+l1)
	if got, want := selText(m), "main() {\n    run()\n}"; got != want {
		t.Fatalf("mid-line start selected %q, want %q", got, want)
	}
}

func TestBackwardDragSelectsTheSameRange(t *testing.T) {
	m := selModel(t, message{role: roleUser, content: "alpha beta gamma"})
	line, col := findCell(t, m, "beta")
	y := headerHeight + line
	mouse(m, tea.MouseActionPress, col+3, y)
	mouse(m, tea.MouseActionMotion, col, y)
	if got := selText(m); got != "beta" {
		t.Fatalf("backward drag selected %q, want beta", got)
	}
}

func TestClickWithoutDragStillActs(t *testing.T) {
	m := selModel(t, message{role: roleAsst, content: "fold me"})
	y := headerHeight + m.turnLineIndex[0].line
	mouse(m, tea.MouseActionPress, 0, y)
	if m.msgs[0].collapsed {
		t.Fatal("the fold fired on press — a drag could never start on a head")
	}
	mouse(m, tea.MouseActionRelease, 0, y)
	if !m.msgs[0].collapsed {
		t.Fatal("click (press + release) did not fold the turn")
	}
}

func TestDragFromTurnHeadSelectsInsteadOfFolding(t *testing.T) {
	m := selModel(t, message{role: roleAsst, content: "keep me open"})
	y := headerHeight + m.turnLineIndex[0].line
	dragSelect(m, 0, y, 5, y)
	if m.msgs[0].collapsed {
		t.Fatal("a drag that started on the turn head folded it")
	}
	if !m.copyFlashing() {
		t.Fatal("the drag did not copy")
	}
}

func TestDoubleClickSelectsWordTripleSelectsLine(t *testing.T) {
	m := selModel(t, message{role: roleUser, content: `open "internal/tui/view.go", then stop`})
	line, col := findCell(t, m, "internal")
	y := headerHeight + line
	clickAt(m, col+2, y)
	mouse(m, tea.MouseActionPress, col+2, y)
	if got := selText(m); got != "internal/tui/view.go" {
		t.Fatalf("double click selected %q, want the bare path", got)
	}
	mouse(m, tea.MouseActionRelease, col+2, y)
	if !m.copyFlashing() {
		t.Fatal("double click did not copy the word")
	}
	mouse(m, tea.MouseActionPress, col+2, y)
	if got := selText(m); got != `open "internal/tui/view.go", then stop` {
		t.Fatalf("triple click selected %q, want the line", got)
	}
}

func TestSlowSecondClickIsNotADoubleClick(t *testing.T) {
	m := selModel(t, message{role: roleUser, content: "alpha beta"})
	line, col := findCell(t, m, "beta")
	y := headerHeight + line
	clickAt(m, col, y)
	m.lastClick.at = time.Now().Add(-2 * multiClickGap)
	mouse(m, tea.MouseActionPress, col, y)
	if m.sel.on {
		t.Fatal("a late second click selected a word")
	}
}

func TestRepeatClicksOnAControlStayClicks(t *testing.T) {
	m := selModel(t, message{role: roleAsst, content: "fold me"})
	y := headerHeight + m.turnLineIndex[0].line
	clickAt(m, 0, y)
	clickAt(m, 0, y)
	if m.msgs[0].collapsed {
		t.Fatal("two quick clicks on a turn head must fold and unfold, not select a word")
	}
	if m.sel.on {
		t.Fatal("a control double click left a selection")
	}
}

func TestKeyClearsSelectionAndEscOnlyClears(t *testing.T) {
	m := selModel(t, message{role: roleUser, content: "alpha beta gamma"})
	line, col := findCell(t, m, "beta")
	y := headerHeight + line
	dragSelect(m, col, y, col+3, y)
	m.busy = true
	m.Update(key("esc"))
	if m.sel.on {
		t.Fatal("esc did not clear the selection")
	}
	if m.confirm != confirmNone {
		t.Fatal("esc on a selection must not reach the cancel gate")
	}
	m.busy = false
	dragSelect(m, col, y, col+3, y)
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	if m.sel.on {
		t.Fatal("typing did not clear the selection")
	}
	if m.ta.Value() != "x" {
		t.Errorf("the clearing key was not typed: %q", m.ta.Value())
	}
}

func TestHeldSelectionDropsWhenItsLinesMove(t *testing.T) {
	m := selModel(t,
		message{role: roleAsst, content: "first reply"},
		message{role: roleUser, content: "alpha beta gamma"})
	line, col := findCell(t, m, "beta")
	y := headerHeight + line
	dragSelect(m, col, y, col+3, y)
	m.refresh()
	if !m.sel.on {
		t.Fatal("an unchanged rebuild dropped the selection")
	}
	m.toggleCollapseAt(0) // the rows under the highlight shift
	m.refresh()
	if m.sel.on {
		t.Fatal("the highlight survived on rows that moved")
	}
}

func TestDragPastViewportEdgeScrolls(t *testing.T) {
	var long []string
	for i := 0; i < 80; i++ {
		long = append(long, "row of filler text")
	}
	m := selModel(t, message{role: roleUser, content: strings.Join(long, "\n")})
	y := headerHeight + 3
	mouse(m, tea.MouseActionPress, 2, y)
	cmd := mouse(m, tea.MouseActionMotion, 4, headerHeight+m.vp.Height+2)
	if cmd == nil || !m.selScrolling {
		t.Fatal("dragging below the transcript did not arm the edge scroll")
	}
	before := m.vp.YOffset
	if next := m.handleSelScroll(selScrollMsg{seq: m.selScrollSeq}); next == nil {
		t.Fatal("the edge scroll did not re-arm while the pointer stays out")
	}
	if m.vp.YOffset <= before {
		t.Fatalf("the transcript did not scroll: %d → %d", before, m.vp.YOffset)
	}
	if _, b := m.sel.ordered(); b.line != m.vp.YOffset+m.vp.Height-1 {
		t.Errorf("the range does not reach the last visible line: %+v", b)
	}
	// Scrolling up past the top edge, and stale ticks are dropped.
	mouse(m, tea.MouseActionMotion, 4, 0)
	top := m.vp.YOffset
	m.handleSelScroll(selScrollMsg{seq: m.selScrollSeq})
	if m.vp.YOffset >= top {
		t.Fatalf("the transcript did not scroll back up: %d → %d", top, m.vp.YOffset)
	}
	if cmd := m.handleSelScroll(selScrollMsg{seq: m.selScrollSeq - 5}); cmd != nil {
		t.Error("a stale edge-scroll tick re-armed")
	}
	// Back inside: the tick lets go.
	mouse(m, tea.MouseActionMotion, 4, y)
	if cmd := m.handleSelScroll(selScrollMsg{seq: m.selScrollSeq}); cmd != nil {
		t.Error("the edge scroll kept running with the pointer inside")
	}
	mouse(m, tea.MouseActionRelease, 4, y)
	if cmd := m.handleSelScroll(selScrollMsg{seq: m.selScrollSeq}); cmd != nil {
		t.Error("the edge scroll outlived the drag")
	}
}

func TestWheelMidDragFollowsTheText(t *testing.T) {
	var long []string
	for i := 0; i < 80; i++ {
		long = append(long, "row of filler text")
	}
	m := selModel(t, message{role: roleUser, content: strings.Join(long, "\n")})
	y := headerHeight + 3
	mouse(m, tea.MouseActionPress, 2, y)
	mouse(m, tea.MouseActionMotion, 6, y+1)
	head := m.sel.head.line
	m.Update(tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonWheelDown})
	if m.sel.head.line <= head {
		t.Fatalf("the range head did not follow the wheel: %d → %d", head, m.sel.head.line)
	}
}

func TestScreenSelectionReadsChromeAndTrimsBorders(t *testing.T) {
	m := selModel(t, message{role: roleUser, content: "hello"})
	m.ta.SetValue("draft text in the composer")
	rows := strings.Split(plain(m.compose()), "\n")
	y := -1
	for i, r := range rows {
		if strings.Contains(r, "draft text") {
			y = i
		}
	}
	if y < 0 {
		t.Fatalf("composer row not found:\n%s", strings.Join(rows, "\n"))
	}
	mouse(m, tea.MouseActionPress, 0, y)
	mouse(m, tea.MouseActionMotion, m.width-1, y)
	if !m.sel.screen {
		t.Fatal("a drag below the transcript must select in screen space")
	}
	if got := strings.TrimSpace(selText(m)); got != "draft text in the composer" {
		t.Fatalf("selected %q, want the draft without its box border", got)
	}
	if !strings.Contains(m.View(), selOn) {
		t.Error("the screen selection is not painted")
	}
	mouse(m, tea.MouseActionRelease, m.width-1, y)
	if !m.copyFlashing() {
		t.Fatal("the screen selection was not copied")
	}
	if m.sel.on {
		t.Error("a screen selection has no stable anchor and must not be held")
	}
}

func TestBlankDragCopiesNothing(t *testing.T) {
	m := selModel(t, message{role: roleUser, content: "hi"})
	y := headerHeight + m.vp.Height - 1 // padding below the short transcript
	dragSelect(m, 40, y, 60, y)
	if m.copyFlashing() || m.sel.on {
		t.Fatal("a drag over blank cells copied or left a selection")
	}
	// A double click on blank space is equally inert.
	clickAt(m, 40, y)
	clickAt(m, 40, y)
	if m.sel.on || m.copyFlashing() {
		t.Fatal("double click on blank space selected something")
	}
}

func TestSelectionIgnoresPopoverAndPanelRows(t *testing.T) {
	m := selModel(t, message{role: roleAsst, content: "fold me"})
	m.popover = true
	clickAt(m, 0, headerHeight+m.turnLineIndex[0].line)
	if m.msgs[0].collapsed {
		t.Fatal("a click on the cockpit popover folded the turn hidden under it")
	}
	if m.inTranscript(headerHeight) {
		t.Fatal("popover rows are not transcript rows")
	}
}

func TestSelectedTextPrimitives(t *testing.T) {
	// Styles never leak into the copy, and trailing padding is dropped.
	lines := []string{"\x1b[31m  red text\x1b[0m     ", "", "  tail"}
	s := selection{on: true, anchor: selPoint{0, 0}, head: selPoint{2, 99}}
	if got, want := selectedText(lines, s), "red text\n\ntail"; got != want {
		t.Fatalf("selectedText = %q, want %q", got, want)
	}
	// Wide characters select by cell.
	c0, c1, ok := wordSpan("go 日本語 ok", 5)
	if !ok || c0 != 3 || c1 != 9 {
		t.Fatalf("wordSpan over wide runes = %d,%d,%v", c0, c1, ok)
	}
	if _, _, ok := wordSpan("a  b", 2); ok {
		t.Error("wordSpan matched blank space")
	}
	if _, _, ok := wordSpan("abc", 9); ok {
		t.Error("wordSpan matched past the end")
	}
	// A highlight past the row's end pads rather than panics, and the line
	// break inside a range is marked on blank rows.
	if got := highlightCells("ab", 4, 6); !strings.Contains(got, "ab  "+selOn+"  "+selOff) {
		t.Errorf("highlight past the end = %q", got)
	}
	// The viewport pads its rows, so the mark fits; it never widens one.
	painted := paintSelection("one\n   \ntwo", selection{on: true, anchor: selPoint{0, 1}, head: selPoint{2, 0}}, 0)
	if want := "o" + selOn + "ne" + selOff + "\n" + selOn + " " + selOff + "  \n" + selOn + "t" + selOff + "wo"; painted != want {
		t.Errorf("paintSelection = %q, want %q", painted, want)
	}
	if got := paintSelection("one\n\ntwo", selection{on: true, anchor: selPoint{0, 1}, head: selPoint{2, 0}}, 0); strings.Split(got, "\n")[1] != "" {
		t.Errorf("the line-break mark widened an unpadded row: %q", got)
	}
	if got := trimFrame("│  boxed text  │"); got != "   boxed text" {
		t.Errorf("trimFrame = %q", got)
	}
}

func TestMotionEdgeCases(t *testing.T) {
	m := selModel(t, message{role: roleUser, content: "alpha beta gamma"})
	line, col := findCell(t, m, "beta")
	y := headerHeight + line
	// Jitter inside the press cell is still a click, not a drag.
	mouse(m, tea.MouseActionPress, col, y)
	mouse(m, tea.MouseActionMotion, col, y)
	if m.sel.on {
		t.Fatal("motion inside the press cell started a selection")
	}
	mouse(m, tea.MouseActionRelease, col, y)
	// A drag after a double click keeps the word: it is already selected.
	mouse(m, tea.MouseActionPress, col, y)
	mouse(m, tea.MouseActionMotion, col+8, y)
	if got := selText(m); got != "beta" {
		t.Fatalf("dragging after a double click changed the word: %q", got)
	}
	// The edge-scroll tick delivers its generation.
	m.selScrolling = false
	if msg, ok := m.armSelScroll()().(selScrollMsg); !ok || msg.seq != m.selScrollSeq {
		t.Fatalf("edge-scroll tick = %#v", msg)
	}
	if m.armSelScroll() != nil {
		t.Error("a second edge-scroll tick was armed while one is pending")
	}
}

func TestClicksUnderAPanelDoNothing(t *testing.T) {
	m := selModel(t, message{role: roleAsst, content: "fold me"})
	y := headerHeight + m.turnLineIndex[0].line
	m.panel = panelStats
	clickAt(m, 0, y)
	clickAt(m, 0, y)
	if m.msgs[0].collapsed {
		t.Fatal("a click with a drawer open folded a turn")
	}
	if m.clickActs(0, 0) {
		t.Error("nothing is a control while a drawer holds the screen")
	}
	m.panel = panelNone
	if !m.clickActs(0, 0) {
		t.Error("the cockpit header is a control")
	}
	if m.clickActs(0, m.height-1) {
		t.Error("the footer is not a control")
	}
	// Two quick header clicks toggle the cockpit twice.
	clickAt(m, 3, 0)
	clickAt(m, 3, 0)
	if m.popover || m.sel.on {
		t.Fatalf("header double click: popover=%v sel=%v", m.popover, m.sel.on)
	}
}

func TestChipClickFocusesAgent(t *testing.T) {
	m := selModel(t, message{role: roleAsst, streaming: true,
		steps: []step{{name: "delegate_tasks", subagent: true, agents: []*agentCard{
			{taskID: "t1", idx: 0, phase: "active", status: "running"},
			{taskID: "t2", idx: 1, phase: "active", status: "running"},
		}}},
		items: []turnItem{{stepIdx: 0}},
	})
	var chip *stepRef
	for i := range m.stepLineIndex {
		if r := &m.stepLineIndex[i]; r.x1 > r.x0 {
			chip = r
			break
		}
	}
	if chip == nil {
		t.Fatalf("no chip hit box indexed: %+v", m.stepLineIndex)
	}
	y := headerHeight + chip.line - m.vp.YOffset
	if !m.clickActs(chip.x0, y) {
		t.Fatal("a chip is a control")
	}
	clickAt(m, chip.x0, y)
	if m.msgs[0].steps[0].agentSel == 0 {
		t.Fatal("click on a chip did not focus its agent")
	}
}

func TestSelectionPrimitiveEdges(t *testing.T) {
	// A range that ends at column 0 of a blank row paints nothing there.
	s := selection{on: true, anchor: selPoint{0, 0}, head: selPoint{1, 0}}
	if got := paintSelection("ab\n", s, 0); got != selOn+"ab"+selOff+"\n" {
		t.Errorf("paintSelection = %q", got)
	}
	// Combining marks ride their base cell.
	c0, c1, ok := wordSpan("café au", 3)
	if !ok || c0 != 0 || c1 != 4 {
		t.Errorf("wordSpan with a combining mark = %d,%d,%v", c0, c1, ok)
	}
}

// TestDragGestureFromTerminalReports replays the bytes a terminal sends for
// press → drag → release through the real input path (reassembler, Bubble
// Tea's parser, the key filter) in both mouse encodings, and checks the
// decoded events drive a selection: SGR reports the released button, legacy
// X10 reports none, and both must end the drag.
func TestDragGestureFromTerminalReports(t *testing.T) {
	x10 := func(button, x, y int) string {
		return "\x1b[M" + string([]byte{byte(32 + button), byte(33 + x), byte(33 + y)})
	}
	sgr := func(button, x, y int, final string) string {
		return "\x1b[<" + strconv.Itoa(button) + ";" + strconv.Itoa(x+1) + ";" + strconv.Itoa(y+1) + final
	}
	for name, script := range map[string]func(x0, x1, y int) [][]byte{
		"sgr": func(x0, x1, y int) [][]byte {
			return [][]byte{[]byte(sgr(0, x0, y, "M")), []byte(sgr(32, x1, y, "M")), []byte(sgr(0, x1, y, "m"))}
		},
		"x10": func(x0, x1, y int) [][]byte {
			return [][]byte{[]byte(x10(0, x0, y)), []byte(x10(32, x1, y)), []byte(x10(3, x1, y))}
		},
	} {
		t.Run(name, func(t *testing.T) {
			m := selModel(t, message{role: roleUser, content: "alpha beta gamma"})
			line, col := findCell(t, m, "beta")
			y := headerHeight + line
			events := mouseMsgs(runProgram(t, script(col, col+3, y), 3, 2*time.Second))
			if len(events) != 3 {
				t.Fatalf("decoded %d mouse events, want press, motion, release: %v", len(events), events)
			}
			for i, ev := range events {
				m.Update(ev)
				if i == 1 {
					if got := selText(m); got != "beta" {
						t.Fatalf("mid-drag selection = %q, want beta", got)
					}
				}
			}
			if m.sel.dragging || m.press.down {
				t.Fatalf("the release did not end the gesture: %+v %+v", m.sel, m.press)
			}
			if !m.copyFlashing() {
				t.Fatal("the gesture did not copy")
			}
		})
	}
}

// A stream flush rebuilds the transcript every 80ms: one landing between a
// double click's press and release must not cost the copy.
func TestDoubleClickSurvivesARebuildBeforeRelease(t *testing.T) {
	m := selModel(t, message{role: roleUser, content: "alpha beta gamma"})
	line, col := findCell(t, m, "beta")
	y := headerHeight + line
	clickAt(m, col, y)
	mouse(m, tea.MouseActionPress, col, y)
	m.refresh()
	if got := selText(m); got != "beta" {
		t.Fatalf("a rebuild dropped the double-click selection: %q", got)
	}
	m.copyFlashUntil = time.Time{}
	if cmd := mouse(m, tea.MouseActionRelease, col, y); cmd == nil || !m.copyFlashing() {
		t.Fatal("the word was not copied on release")
	}
}

// A highlight scrolled out of view is not an overlay: ESC must not be
// spent dismissing something nobody can see.
func TestEscPassesAnOffScreenSelection(t *testing.T) {
	var long []string
	for i := 0; i < 120; i++ {
		long = append(long, "row of filler text")
	}
	m := selModel(t, message{role: roleUser, content: strings.Join(long, "\n")})
	dragSelect(m, 2, headerHeight+3, 9, headerHeight+3)
	if !m.selectionVisible() {
		t.Fatal("the fresh selection should be on screen")
	}
	m.vp.GotoBottom()
	if m.selectionVisible() {
		t.Fatal("the selection scrolled away but still reads as visible")
	}
	m.busy = true
	m.Update(key("esc"))
	if m.sel.on {
		t.Error("the stale selection was not dropped")
	}
	if m.confirm != confirmCancel {
		t.Fatal("ESC was swallowed by a selection that is not on screen")
	}
}

// An edge of the range inside a two-cell glyph takes the whole glyph, in
// the paint and in the copy alike, and the row keeps its width.
func TestSelectionSnapsToWideGlyphs(t *testing.T) {
	const row = "你好世界abc"
	for c0 := 0; c0 < 11; c0++ {
		for c1 := c0 + 1; c1 <= 11; c1++ {
			a, b := snapCells(row, c0, c1)
			if got := ansi.StringWidth(highlightCells(row, a, b)); got != 11 {
				t.Fatalf("cells %d..%d: painted row is %d cells wide, want 11", c0, c1, got)
			}
		}
	}
	s := selection{on: true, anchor: selPoint{0, 1}, head: selPoint{0, 4}}
	if got := selectedText([]string{row}, s); got != "你好世" {
		t.Errorf("copy over half glyphs = %q, want every glyph the range touches", got)
	}
	if got := paintSelection(row, s, 0); !strings.Contains(got, selOn+"你好世"+selOff) {
		t.Errorf("paint and copy disagree: %q", got)
	}
}

// At the end of the transcript there is nothing left to scroll: the tick
// stops instead of spinning until the next key.
func TestEdgeScrollStopsAtTheTranscriptEnd(t *testing.T) {
	var long []string
	for i := 0; i < 80; i++ {
		long = append(long, "row of filler text")
	}
	m := selModel(t, message{role: roleUser, content: strings.Join(long, "\n")})
	m.vp.GotoBottom()
	mouse(m, tea.MouseActionPress, 2, headerHeight+3)
	mouse(m, tea.MouseActionMotion, 4, headerHeight+m.vp.Height+2)
	if cmd := m.handleSelScroll(selScrollMsg{seq: m.selScrollSeq}); cmd != nil {
		t.Fatal("the edge scroll kept ticking with nowhere to go")
	}
	if m.selScrolling {
		t.Error("the tick is still marked pending")
	}
}
