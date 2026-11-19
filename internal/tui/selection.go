package tui

import (
	"strings"
	"time"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// Mouse reporting takes the terminal's own click-drag selection away, so
// bodek provides one: a left drag selects text and copies it on release, a
// double click takes a word, a triple click a line. Every click action
// (folds, step inspection, link prompts, card copy) fires on release, and
// only when the pointer never left the press cell — a drag can start
// anywhere without triggering what sits under it.

const (
	// multiClickGap is how long a second press on the same cell still counts
	// as a double (then triple) click.
	multiClickGap = 400 * time.Millisecond
	// selScrollTick paces the transcript scroll while a drag is held past
	// the viewport edge.
	selScrollTick = 50 * time.Millisecond
	// selOn/selOff wrap a selected run. Reverse video, written raw: it is
	// not a colour, so it survives NO_COLOR and every theme keeps contrast.
	selOn  = "\x1b[0m\x1b[7m"
	selOff = "\x1b[0m"
)

// selPoint is one cell: a row (transcript content line, or screen row) and
// a display column.
type selPoint struct{ line, col int }

// selection is the active text range. Both ends are inclusive cells.
type selection struct {
	on       bool     // a range exists: being dragged, or held after its copy
	dragging bool     // the button is still down
	screen   bool     // rows are screen rows; false = transcript content lines
	anchor   selPoint // where the drag started
	head     selPoint // where it is now
	// A held transcript range remembers the text it copied: a rebuild that
	// puts anything else under it (fold, expand, resize) drops the
	// highlight instead of leaving it on unrelated text.
	text string
}

// ordered returns the range's ends in reading order.
func (s selection) ordered() (selPoint, selPoint) {
	a, b := s.anchor, s.head
	if b.line < a.line || b.line == a.line && b.col < a.col {
		a, b = b, a
	}
	return a, b
}

// span returns the selected cells [c0, c1) of one row whose text is w cells
// wide; ok is false when the row lies outside the range.
func (s selection) span(line, w int) (c0, c1 int, ok bool) {
	a, b := s.ordered()
	if line < a.line || line > b.line {
		return 0, 0, false
	}
	c1 = w
	if line == a.line {
		c0 = a.col
	}
	if line == b.line && b.col+1 < w {
		c1 = b.col + 1
	}
	if c0 > c1 {
		c0 = c1
	}
	return c0, c1, true
}

// mousePress tracks the left button between press and release.
type mousePress struct {
	down   bool
	x, y   int // press cell
	dx, dy int // latest pointer cell while dragging
	clicks int // 1 click · 2 word · 3 line
}

// clickMark remembers the previous press for multi-click detection.
type clickMark struct {
	at   time.Time
	x, y int
	n    int
}

// selScrollMsg advances a drag held past the transcript edge.
type selScrollMsg struct{ seq int }

// inTranscript reports whether a screen row shows transcript content — not
// the cockpit popover or a full-bleed drawer painted over the viewport.
func (m *Model) inTranscript(y int) bool {
	if m.popover || m.panel != panelNone && m.drawerFullBleed() {
		return false
	}
	return y >= headerHeight && y < headerHeight+m.vp.Height
}

// contentLines is the transcript as rendered rows, split once per rebuild.
func (m *Model) contentLines() []string {
	if m.vpLines == nil {
		m.vpLines = strings.Split(m.vpContent, "\n")
	}
	return m.vpLines
}

// clearSelection drops the range and any press in flight.
func (m *Model) clearSelection() {
	m.sel = selection{}
	m.press = mousePress{}
	m.selScrollSeq++ // a pending edge-scroll tick is stale now
	m.selScrolling = false
}

// reconcileSelection runs after a transcript rebuild: a held range whose
// end lines no longer match what was copied is dropped.
func (m *Model) reconcileSelection() {
	if !m.sel.on || m.sel.screen || m.sel.dragging {
		return
	}
	_, b := m.sel.ordered()
	lines := m.contentLines()
	if b.line >= len(lines) || selectedText(lines, m.sel) != m.sel.text {
		m.sel = selection{}
	}
}

// selPointAt maps a screen cell to a selection point in the active space.
// Transcript points clamp to the viewport: past its top edge the point is
// the start of the first visible line, past the bottom the end of the last.
func (m *Model) selPointAt(x, y int) selPoint {
	if m.sel.screen {
		return selPoint{line: min(max(y, 0), max(m.height-1, 0)), col: x}
	}
	row := y - headerHeight
	switch {
	case row < 0:
		row, x = 0, 0
	case row >= m.vp.Height:
		row, x = m.vp.Height-1, max(m.width, x)
	}
	line := m.vp.YOffset + row
	if n := m.vp.TotalLineCount(); line >= n {
		line = n - 1
	}
	return selPoint{line: max(line, 0), col: x}
}

// handleMouse routes every mouse event: the left button selects and
// clicks, everything else (the wheel) scrolls the transcript.
func (m *Model) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	switch {
	case msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft:
		m.mousePressed(msg)
		return m, nil
	case msg.Action == tea.MouseActionMotion && m.press.down:
		return m, m.mouseDragged(msg)
	case msg.Action == tea.MouseActionRelease && m.press.down:
		return m.mouseReleased()
	}
	var cmd tea.Cmd
	m.vp, cmd = m.vp.Update(msg)
	m.relayout()
	if m.sel.dragging && !m.sel.screen {
		// A wheel turn mid-drag moves the text under a still pointer.
		m.sel.head = m.selPointAt(m.press.dx, m.press.dy)
	}
	return m, cmd
}

// mousePressed records the press. A double or triple click selects its
// word or line at once; a single press waits to become a drag or a click.
func (m *Model) mousePressed(msg tea.MouseMsg) {
	m.clearSelection()
	// A second press on the same cell is a double click — except on a
	// control, where clicking twice means acting twice.
	n := 1
	if c := m.lastClick; c.x == msg.X && c.y == msg.Y && time.Since(c.at) <= multiClickGap && !m.clickActs(msg.X, msg.Y) {
		n = c.n%3 + 1
	}
	m.lastClick = clickMark{at: time.Now(), x: msg.X, y: msg.Y, n: n}
	if m.link != nil {
		// A click outside the keys dismisses the link prompt. Only a double
		// click goes on, to select the link text the first click landed on.
		m.link = nil
		if n < 2 {
			return
		}
	}
	m.press = mousePress{down: true, x: msg.X, y: msg.Y, dx: msg.X, dy: msg.Y, clicks: n}
	if n < 2 {
		return
	}
	m.sel.screen = !m.inTranscript(msg.Y) // the space selPointAt and selLines read
	p := m.selPointAt(msg.X, msg.Y)
	lines := m.selLines()
	c0, c1, ok := 0, 0, false
	// Rows below the last transcript line are padding, not that line.
	blank := !m.sel.screen && msg.Y-headerHeight+m.vp.YOffset >= m.vp.TotalLineCount()
	if p.line < len(lines) && !blank {
		text := strings.TrimRight(ansi.Strip(lines[p.line]), " ")
		if c0, c1, ok = wordSpan(text, p.col); n == 3 {
			c0, c1 = len(text)-len(strings.TrimLeft(text, " ")), ansi.StringWidth(text)
			ok = c1 > c0
		}
	}
	if !ok {
		m.sel = selection{}
		return
	}
	m.sel.on = true
	m.sel.anchor = selPoint{line: p.line, col: c0}
	m.sel.head = selPoint{line: p.line, col: c1 - 1}
	// Sealed at once: a rebuild between this press and its release (a
	// stream flush lands every 80ms) must not take it for a stale range.
	m.sel.text = selectedText(lines, m.sel)
}

// selectionVisible reports whether a range is painted on screen right now.
// One scrolled out of view must not cost the reader a keypress.
func (m *Model) selectionVisible() bool {
	if !m.sel.on || m.sel.screen {
		return m.sel.on
	}
	a, b := m.sel.ordered()
	return m.inTranscript(headerHeight) && b.line >= m.vp.YOffset && a.line < m.vp.YOffset+m.vp.Height
}

// mouseDragged starts or extends the range once the pointer leaves the
// press cell.
func (m *Model) mouseDragged(msg tea.MouseMsg) tea.Cmd {
	if m.press.clicks > 1 {
		return nil // the word or line is already selected
	}
	if !m.sel.dragging {
		if msg.X == m.press.x && msg.Y == m.press.y {
			return nil
		}
		m.sel = selection{on: true, dragging: true, screen: !m.inTranscript(m.press.y)}
		m.lastClick = clickMark{} // a drag is not the first half of a double click
		m.sel.anchor = m.selPointAt(m.press.x, m.press.y)
	}
	m.press.dx, m.press.dy = msg.X, msg.Y
	m.sel.head = m.selPointAt(msg.X, msg.Y)
	if !m.sel.screen && !m.inTranscript(msg.Y) {
		return m.armSelScroll()
	}
	return nil
}

// mouseReleased ends the gesture: a drag or multi-click copies its range,
// a plain click fires whatever sits under the press cell.
func (m *Model) mouseReleased() (tea.Model, tea.Cmd) {
	p := m.press
	m.press = mousePress{}
	if !m.sel.on {
		if p.clicks > 1 {
			return m, nil // double click on blank space
		}
		return m.mouseClick(p.x, p.y)
	}
	m.sel.dragging = false
	lines := m.selLines()
	text := selectedText(lines, m.sel)
	_, b := m.sel.ordered()
	switch {
	case strings.TrimSpace(text) == "":
		m.sel = selection{}
		return m, nil
	case m.sel.screen || b.line >= len(lines):
		// Screen rows have no stable anchor once the frame repaints.
		m.sel = selection{}
	default:
		m.sel.text = text
	}
	return m, m.copyText(text)
}

// mouseClick is a left click (press and release on one cell) at a screen
// position.
func (m *Model) mouseClick(x, y int) (tea.Model, tea.Cmd) {
	if m.panel != panelNone || m.ac.open {
		return m, nil
	}
	// Clicking the cockpit (the header's top rows) toggles the
	// server/budget/session popover — WebUI health-popover parity.
	if y >= 0 && y < headerHeight {
		if !m.popover {
			cmd := m.openCockpit()
			m.refresh()
			return m, cmd
		}
		m.popover = false
		m.refresh()
		return m, nil
	}
	// The queue strip owns the rows between the status line and the
	// input: its controls act on their row.
	if m.queueStripClick(y, x) {
		m.refresh()
		return m, nil
	}
	if !m.inTranscript(y) {
		return m, nil
	}
	line := y - headerHeight + m.vp.YOffset
	// Turn heads fold; chips and step heads toggle; a link asks before it
	// opens; answer cards copy the reply (final or partial).
	if msgIdx, ok := m.turnAtLine(line); ok {
		m.toggleCollapseAt(msgIdx)
		m.refresh()
		return m, nil
	}
	if msgIdx, stepIdx, agentIdx, ok := m.chipAt(line, x); ok {
		m.selectAgentChip(msgIdx, stepIdx, agentIdx)
		m.refresh()
		return m, nil
	}
	if msgIdx, stepIdx, ok := m.stepAtLine(line); ok {
		m.openInspectStep(msgIdx, stepIdx)
		return m, nil
	}
	if u, unsure := m.linkAt(line, x); u != "" {
		m.openLinkPrompt(u, unsure)
		return m, nil
	}
	// Answer cards (and collapsed summaries) copy the turn's reply — final
	// or the partial stream so far.
	if msgIdx, ok := m.replyAtLine(line); ok {
		return m, m.copyReplyAt(msgIdx)
	}
	return m, nil
}

// clickActs reports whether a click at a screen cell operates a control
// (cockpit header, queue strip, turn head, step row, agent chip) rather
// than landing on text.
func (m *Model) clickActs(x, y int) bool {
	if m.panel != panelNone || m.ac.open {
		return false
	}
	if y < headerHeight {
		return true
	}
	if top := m.queueStripTop(); m.queueStripVisible() && y >= top && y < top+m.queueStripHeight() {
		return true
	}
	if !m.inTranscript(y) {
		return false
	}
	line := y - headerHeight + m.vp.YOffset
	if _, ok := m.turnAtLine(line); ok {
		return true
	}
	if _, _, _, ok := m.chipAt(line, x); ok {
		return true
	}
	_, _, ok := m.stepAtLine(line)
	return ok
}

// armSelScroll starts the edge-scroll tick unless one is already pending.
func (m *Model) armSelScroll() tea.Cmd {
	if m.selScrolling {
		return nil
	}
	m.selScrolling = true
	m.selScrollSeq++
	seq := m.selScrollSeq
	return tea.Tick(selScrollTick, func(time.Time) tea.Msg { return selScrollMsg{seq: seq} })
}

// handleSelScroll scrolls the transcript one step toward a drag held past
// its edge and keeps ticking while the pointer stays out there.
func (m *Model) handleSelScroll(msg selScrollMsg) tea.Cmd {
	if msg.seq != m.selScrollSeq {
		return nil
	}
	m.selScrolling = false
	if !m.sel.dragging || m.sel.screen {
		return nil
	}
	was := m.vp.YOffset
	switch over := m.press.dy - headerHeight; {
	case over < 0:
		m.vp.ScrollUp(min(1-over/2, 4))
	case over >= m.vp.Height:
		m.vp.ScrollDown(min(1+(over-m.vp.Height)/2, 4))
	}
	if m.vp.YOffset == was {
		// The pointer came back inside, or the transcript hit its end: stop
		// ticking. The next motion past the edge re-arms it, so a release
		// the terminal never reported cannot leave a 20Hz loop behind.
		return nil
	}
	m.sel.head = m.selPointAt(m.press.dx, m.press.dy)
	m.relayout()
	return m.armSelScroll()
}

// selLines returns the rows the active selection space indexes.
func (m *Model) selLines() []string {
	if m.sel.screen {
		return strings.Split(m.compose(), "\n")
	}
	return m.contentLines()
}

// transcriptView is the viewport with a transcript selection painted on.
func (m *Model) transcriptView() string {
	body := m.vp.View()
	if !m.sel.on || m.sel.screen {
		return body
	}
	return paintSelection(body, m.sel, m.vp.YOffset)
}

// screenSelection paints a screen-space selection onto the composed frame.
func (m *Model) screenSelection(frame string) string {
	if !m.sel.on || !m.sel.screen {
		return frame
	}
	return paintSelection(frame, m.sel, 0)
}

// paintSelection highlights the range on a block of rows whose first row
// is line `first` in the selection's space.
func paintSelection(block string, s selection, first int) string {
	_, b := s.ordered()
	rows := strings.Split(block, "\n")
	for i, row := range rows {
		line := first + i
		w := ansi.StringWidth(strings.TrimRight(ansi.Strip(row), " "))
		c0, c1, ok := s.span(line, w)
		if !ok {
			continue
		}
		if c1 <= c0 {
			if line == b.line || c0 >= ansi.StringWidth(row) {
				continue
			}
			c1 = c0 + 1 // one cell marks the line break the range carries
		}
		c0, c1 = snapCells(row, c0, c1)
		rows[i] = highlightCells(row, c0, c1)
	}
	return strings.Join(rows, "\n")
}

// snapCells widens [c0, c1) to whole glyphs: an edge that falls inside a
// two-cell character takes the character, so the paint and the copy agree
// and a row never changes width.
func snapCells(row string, c0, c1 int) (int, int) {
	w := ansi.StringWidth(row)
	if c0 < w {
		c0 = min(c0, ansi.StringWidth(ansi.Cut(row, 0, c0)))
	}
	if c1 < w && ansi.StringWidth(ansi.Cut(row, 0, c1)) < c1 {
		c1 = max(c1, ansi.StringWidth(ansi.Cut(row, 0, c1+1)))
	}
	return c0, c1
}

// highlightCells repaints cells [c0, c1) of a styled row in reverse video.
// The run is stripped first: an embedded reset would end the highlight.
func highlightCells(row string, c0, c1 int) string {
	left := ansi.Cut(row, 0, c0)
	if pad := c0 - ansi.StringWidth(left); pad > 0 {
		left += strings.Repeat(" ", pad)
	}
	mid := ansi.Strip(ansi.Cut(row, c0, c1))
	if pad := c1 - c0 - ansi.StringWidth(mid); pad > 0 {
		mid += strings.Repeat(" ", pad)
	}
	out := left + selOn + mid + selOff
	if w := ansi.StringWidth(row); c1 < w {
		out += ansi.Cut(row, c1, w) // carries the styles active at c1
	}
	return out
}

// selectedText extracts the range as plain text: escapes stripped, trailing
// padding dropped, and — across several rows — the shared left margin
// removed so a block pastes flush left with its relative indent intact.
func selectedText(lines []string, s selection) string {
	a, b := s.ordered()
	var rows []string
	for i := max(a.line, 0); i <= b.line && i < len(lines); i++ {
		text := strings.TrimRight(ansi.Strip(lines[i]), " ")
		row := ""
		if c0, c1, _ := s.span(i, ansi.StringWidth(text)); c1 > c0 {
			c0, c1 = snapCells(text, c0, c1)
			row = strings.TrimRight(ansi.Cut(text, c0, c1), " ")
		}
		if s.screen {
			row = trimFrame(row)
		}
		rows = append(rows, row)
	}
	if len(rows) < 2 {
		return strings.Join(rows, "\n")
	}
	indent := func(i int) int {
		n := len(rows[i]) - len(strings.TrimLeft(rows[i], " "))
		if i == 0 {
			n += a.col // the first row starts mid-line
		}
		return n
	}
	margin := -1
	for i := range rows {
		if rows[i] == "" {
			continue
		}
		if n := indent(i); margin < 0 || n < margin {
			margin = n
		}
	}
	for i := range rows {
		cut := margin
		if i == 0 {
			cut -= a.col
		}
		if cut > 0 && cut <= len(rows[i]) {
			rows[i] = rows[i][cut:]
		}
	}
	return strings.Join(rows, "\n")
}

// trimFrame drops the card border a screen row was cut through, keeping
// the text's column so the shared margin still lines up.
func trimFrame(row string) string {
	const edge = "│"
	row = strings.TrimRight(strings.TrimSuffix(row, edge), " ")
	if body := strings.TrimLeft(row, " "); strings.HasPrefix(body, edge) {
		row = strings.Repeat(" ", len(row)-len(body)+1) + body[len(edge):]
	}
	return row
}

// wordSpan returns the cells [c0, c1) of the word under col — a run of
// non-blank cells with wrapping quotes, brackets, and sentence punctuation
// shaved off — or ok=false on blank space.
func wordSpan(text string, col int) (c0, c1 int, ok bool) {
	type cell struct {
		r      rune
		c0, c1 int
	}
	var cells []cell
	at, hit := 0, -1
	for _, r := range text {
		w := ansi.StringWidth(string(r))
		if w == 0 {
			continue // combining mark: rides the previous cell
		}
		if col >= at && col < at+w {
			hit = len(cells)
		}
		cells = append(cells, cell{r: r, c0: at, c1: at + w})
		at += w
	}
	if hit < 0 || unicode.IsSpace(cells[hit].r) {
		return 0, 0, false
	}
	lo, hi := hit, hit
	for lo > 0 && !unicode.IsSpace(cells[lo-1].r) {
		lo--
	}
	for hi < len(cells)-1 && !unicode.IsSpace(cells[hi+1].r) {
		hi++
	}
	for lo < hi && strings.ContainsRune("\"'`([{<“‘│", cells[lo].r) {
		lo++
	}
	for hi > lo && strings.ContainsRune("\"'`)]}>,;:.!?”’│…", cells[hi].r) {
		hi--
	}
	return cells[lo].c0, cells[hi].c1, true
}
