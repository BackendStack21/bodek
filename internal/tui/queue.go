package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ── queued-prompt strip ─────────────────────────────────────────────────────
//
// Unfocused, the queue is a shelf chip. ^Q unfolds the strip: one row per
// queued prompt with ▲ ▼ ✕ mouse controls, keyboard focus (j/k select, h/l
// move, d delete, esc leaves), and an overflow tail once the queue outgrows
// the cap. The queue itself is bodek-local state (m.queue) — prompts are
// held client-side until sendQueued fires them on turn end.

// queueStripCap bounds the rendered rows; the rest folds into the tail line.
const queueStripCap = 8

// queueHeld reports prompts waiting that neither an approval nor the /queue
// panel currently owns — the shelf chip and ^Q latch both key off this.
func (m *Model) queueHeld() bool {
	return len(m.queue) > 0 && m.curApproval() == nil && m.panel != panelQueue
}

// queueStripVisible reports whether the unfolded strip occupies rows.
// Unfocused, the count rides the composer shelf instead.
func (m *Model) queueStripVisible() bool {
	return m.queueHeld() && m.qfocus
}

// queueStripHeight is the number of rows the strip claims above the input,
// keeping View, relayout (via inputAreaHeight), and the mouse math in
// agreement.
func (m *Model) queueStripHeight() int {
	if !m.queueStripVisible() {
		return 0
	}
	h := min(len(m.queue), queueStripCap)
	if len(m.queue) > queueStripCap {
		h++ // the overflow tail
	}
	if m.qarm >= 0 {
		h++ // the armed-delete confirm row
	}
	return h
}

// queueStripTop is the absolute screen row of the strip's first row: header,
// viewport, then the busy status line when it shows.
func (m *Model) queueStripTop() int {
	top := headerHeight + m.chromeBodyHeight()
	if m.statusLineVisible() {
		top += 2 // blank separator row + the status row (statusLine renders both)
	}
	return top
}

// queueStripView renders the strip rows (queueStripHeight is the row budget).
// Every row carries the full ▲ ▼ ✕ control set; moves that would leave the
// queue are clamped no-ops rather than hidden, so the targets stay put while
// the queue churns.
func (m *Model) queueStripView() string {
	if !m.queueStripVisible() {
		return ""
	}
	th := m.th
	window := min(len(m.queue), queueStripCap)
	controls := th.footerSep.Render(" ▲ ▼ ✕")
	cw := lipgloss.Width(controls)
	rows := make([]string, 0, window+2)
	for i := range window {
		marker, num := "  ", th.footer.Render(fmt.Sprintf("%d ", i+1))
		if m.qfocus && m.qarm == i {
			marker = th.footerDanger.Render("! ") // armed for delete
		} else if m.qfocus && m.qsel == i {
			marker = "▸ "
		}
		// collapse first: a queued prompt may span lines, but the strip's
		// row budget counts one row per prompt — never more.
		text := th.footer.Render(truncate(collapse(m.queue[i]), max(1, m.width-cw-5)))
		rows = append(rows, th.footer.Render(marker)+num+text+controls)
	}
	if tail := len(m.queue) - window; tail > 0 {
		s := fmt.Sprintf("… and %d more", tail)
		if m.qfocus && m.qsel >= window && m.qsel < len(m.queue) {
			s += " · ▸ " + truncate(collapse(m.queue[m.qsel]), max(1, m.width-lipgloss.Width(s)-2))
		}
		rows = append(rows, th.acDetail.Render(s))
	}
	if m.qarm >= 0 {
		// The ! marker alone reads as an error, not a pending confirm.
		rows = append(rows, th.footerDanger.Render("  y deletes · esc cancels"))
	}
	return strings.Join(rows, "\n")
}

// clampSel keeps a strip selection inside [0, n).
func clampSel(sel, n int) int {
	if n == 0 {
		return 0
	}
	return min(max(sel, 0), n-1)
}

// queueDeleteAt removes the i-th queued prompt, clamping the focus-mode
// selection into the new range and leaving focus once the queue runs dry.
func (m *Model) queueDeleteAt(i int) {
	if i < 0 || i >= len(m.queue) {
		return
	}
	m.qarm = -1 // the armed row is gone — never leave a stale confirm
	m.queue = append(m.queue[:i], m.queue[i+1:]...)
	m.qsel = clampSel(m.qsel, len(m.queue))
	if m.panel == panelQueue {
		m.panelSel = clampSel(m.panelSel, len(m.queue))
	}
	if len(m.queue) == 0 {
		m.qfocus = false
	}
	m.persistLocal()
	m.refresh()
}

// queueMove swaps the i-th prompt with its neighbor delta rows away, keeping
// the selection on the moved item. Out-of-range moves are no-ops.
func (m *Model) queueMove(i, delta int) {
	j := i + delta
	if i < 0 || i >= len(m.queue) || j < 0 || j >= len(m.queue) {
		return
	}
	m.queue[i], m.queue[j] = m.queue[j], m.queue[i]
	if m.qsel == i {
		m.qsel = j
	}
	if m.panel == panelQueue && m.panelSel == i {
		m.panelSel = j // the panel selection rides the moved item too
	}
	m.qarm = -1 // the armed row moved — disarm rather than mis-aim the confirm
	m.refresh()
}

// unstyle drops SGR escape sequences so glyph columns can be located on the
// unstyled row — styling never changes cell positions.
func unstyle(s string) string {
	if !strings.Contains(s, "\x1b[") {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		if s[i] == '\x1b' && i+1 < len(s) && s[i+1] == '[' {
			if j := strings.IndexByte(s[i+2:], 'm'); j >= 0 {
				i += j + 3
				continue
			}
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

// queueStripClick handles a left press inside the strip: the trailing
// controls act on their row, a click on the row body selects it (entering
// focus mode so h/l/d operate immediately). Reports whether the click landed
// on the strip at all.
func (m *Model) queueStripClick(y, x int) bool {
	if !m.queueStripVisible() {
		return false
	}
	rel := y - m.queueStripTop()
	if rel < 0 || rel >= m.queueStripHeight() {
		return false
	}
	rowsCount := min(len(m.queue), queueStripCap)
	if len(m.queue) > queueStripCap {
		rowsCount++ // the overflow tail
	}
	if rel >= rowsCount {
		return true // the overflow tail / the armed-confirm row: no controls
	}
	// Control targets are located on the unstyled row. Bubbletea reports X
	// in terminal CELLS, so glyph columns are display widths — byte offsets
	// drift 2 cells per multibyte rune and misfire the neighboring control.
	row := unstyle(strings.Split(m.queueStripView(), "\n")[rel])
	upC, downC, delC := -1, -1, -1
	cell := 0
	for _, r := range row {
		switch r {
		case '▲':
			if upC < 0 {
				upC = cell
			}
		case '▼':
			if downC < 0 {
				downC = cell
			}
		case '✕':
			if delC < 0 {
				delC = cell
			}
		}
		cell += lipgloss.Width(string(r))
	}
	// Check right-to-left: the controls trail the row, so a hit test claims
	// the rightmost control whose column the click reached.
	if delC >= 0 && x >= delC {
		if m.qarm == rel { // second ✕ on the same row confirms
			m.queueDeleteAt(rel)
			return true
		}
		m.qarm = rel
		m.refresh()
		return true
	}
	if downC >= 0 && x >= downC {
		m.queueMove(rel, 1)
		return true
	}
	if upC >= 0 && x >= upC {
		m.queueMove(rel, -1)
		return true
	}
	m.qfocus = true
	m.qsel = rel
	m.refresh()
	return true
}

// queueStripKey routes keys while the strip owns keyboard focus. Bare letters
// other than d are deliberately unbound: the composer keeps them the moment
// focus leaves, and only the explicit chord ctrl+q re-enters.
func (m *Model) queueStripKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if len(m.queue) == 0 {
		// The queue drained out from under focus (turn-end pop, cancel
		// hand-back): drop focus instead of trapping the keyboard.
		m.qfocus = false
		return m.Update(msg)
	}
	switch msg.String() {
	case "ctrl+c":
		return m, m.armConfirm(confirmQuit, "bodek")
	case "esc", "enter", "ctrl+q":
		m.qfocus = false
		m.qarm = -1
	case "up", "k":
		m.qsel = clampSel(m.qsel-1, len(m.queue))
		m.qarm = -1
	case "down", "j":
		m.qsel = clampSel(m.qsel+1, len(m.queue))
		m.qarm = -1
	case "left", "h":
		m.queueMove(m.qsel, -1)
	case "right", "l":
		m.queueMove(m.qsel, 1)
	case "d":
		// Two-step delete, like every destructive action here: the first
		// press arms the row (marked ! on the strip), y — or a second d —
		// confirms; anything else disarms.
		if m.qarm == m.qsel {
			m.qarm = -1
			m.queueDeleteAt(m.qsel)
			break
		}
		m.qarm = m.qsel
	case "y", "Y":
		// The confirm verb matches every other gate in the app.
		if m.qarm == m.qsel {
			m.qarm = -1
			m.queueDeleteAt(m.qsel)
		}
	}
	m.refresh()
	return m, nil
}

// ── /queue management panel ──────────────────────────────────────────────
//
// The full-area counterpart of the strip: same queue state, room to breathe
// — arrow selection, ←/→ priority moves, a gated delete, and ⏎ to send the
// selected prompt ahead of the queue when idle. Index 0 ships first.

// openQueue shows the queue manager. The strip stands down while the panel
// is open — one queue surface at a time.
func (m *Model) openQueue() tea.Cmd {
	m.panel = panelQueue
	m.panelSel = 0
	m.panelMsg = ""
	m.panelEdit = panelEditNone
	m.panelDetail = false // a detail view never survives the tab change
	m.detailScroll = 0
	m.qfocus = false // the panel replaces the strip's keyboard mode
	m.qarm = -1
	m.relayout()
	m.refresh()
	return nil
}

// queuePanelRows renders one row per queued prompt in send order. Every
// prompt collapses to a single line — the panel manages, it does not preview.
func (m *Model) queuePanelRows(w int) []string {
	th := m.th
	rows := make([]string, 0, len(m.queue))
	for i, q := range m.queue {
		num := th.footer.Render(fmt.Sprintf("%d. ", i+1))
		lab := truncate(collapse(q), max(1, w-2-lipgloss.Width(num)))
		prefix, body := "  ", th.acItem.Render(lab)
		if i == m.panelSel {
			prefix, body = "› ", th.acSel.Render(lab)
		}
		rows = append(rows, prefix+num+body)
	}
	if len(rows) == 0 {
		rows = append(rows, th.acDim.Render("queue is empty — ⏎ mid-turn queues a prompt here"))
	}
	return rows
}

// queueSendSelected fires the selected prompt now. Mid-turn it refuses and
// names the escape hatch: ← raises the prompt's priority instead.
func (m *Model) queueSendSelected() tea.Cmd {
	if m.panelSel >= len(m.queue) {
		return nil
	}
	if m.disconn {
		return m.transientNoteCmd("disconnected — reconnect before sending")
	}
	if m.busy {
		return m.transientNoteCmd("a turn is running — this sends when it ends · ← raises its priority")
	}
	i := m.panelSel
	text := m.queue[i]
	m.queue = append(m.queue[:i], m.queue[i+1:]...)
	m.qsel = clampSel(m.qsel, len(m.queue))
	m.panelSel = clampSel(m.panelSel, len(m.queue))
	m.qarm = -1
	if len(m.queue) == 0 {
		m.qfocus = false
	}
	m.closePanel() // the turn is the point — the transcript gets its screen back
	return m.sendPrompt(text)
}
