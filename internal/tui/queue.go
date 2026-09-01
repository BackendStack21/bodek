package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ── queued-prompt strip ─────────────────────────────────────────────────────
//
// The strip is the always-visible queue panel above the input area: one row
// per queued prompt with ▲ ▼ ✕ mouse controls, a ctrl+q keyboard focus mode
// (j/k select, h/l move, d delete, esc leaves), and an overflow tail once the
// queue outgrows the cap. The queue itself is bodek-local state (m.queue) —
// prompts are held client-side until sendQueued fires them on turn end.

// queueStripCap bounds the rendered rows; the rest folds into the tail line.
const queueStripCap = 8

// queueStripVisible reports whether the strip occupies rows: it needs queued
// prompts and stays out of the way while an approval owns the input area.
func (m *Model) queueStripVisible() bool {
	return len(m.queue) > 0 && m.curApproval() == nil
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
	if !m.mouse {
		h++ // the ^Q hint row replaces the ▲▼✕ controls
	}
	return h
}

// queueStripTop is the absolute screen row of the strip's first row: header,
// viewport, then the busy status line when it shows.
func (m *Model) queueStripTop() int {
	top := headerHeight + m.vp.Height
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
	controls := ""
	if m.mouse {
		// Glyphs only where the terminal tracks the mouse — without
		// tracking they are dead pixels, so mouseless runs get the ^Q
		// hint row below instead.
		controls = th.footerSep.Render(" ▲ ▼ ✕")
	}
	cw := lipgloss.Width(controls)
	rows := make([]string, 0, window+2)
	for i := range window {
		marker, num := "  ", th.footer.Render(fmt.Sprintf("%d ", i+1))
		if m.qfocus && m.qsel == i {
			marker = "▸ "
		}
		text := th.footer.Render(truncate(m.queue[i], max(1, m.width-cw-5)))
		rows = append(rows, th.footer.Render(marker)+num+text+controls)
	}
	if tail := len(m.queue) - window; tail > 0 {
		s := fmt.Sprintf("… and %d more", tail)
		if m.qfocus && m.qsel >= window && m.qsel < len(m.queue) {
			s += " · ▸ " + truncate(m.queue[m.qsel], max(1, m.width-lipgloss.Width(s)-2))
		}
		rows = append(rows, th.acDetail.Render(s))
	}
	if !m.mouse {
		if m.qfocus {
			rows = append(rows, th.acDetail.Render("  ↑↓ select · ←→ move · d delete · esc done"))
		} else {
			rows = append(rows, th.acDetail.Render("  ^q to manage the queue"))
		}
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
	m.queue = append(m.queue[:i], m.queue[i+1:]...)
	m.qsel = clampSel(m.qsel, len(m.queue))
	if len(m.queue) == 0 {
		m.qfocus = false
	}
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
		return true // the overflow tail / the mouseless hint row: no controls
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
		m.queueDeleteAt(rel)
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
	case "up", "k":
		m.qsel = clampSel(m.qsel-1, len(m.queue))
	case "down", "j":
		m.qsel = clampSel(m.qsel+1, len(m.queue))
	case "left", "h":
		m.queueMove(m.qsel, -1)
	case "right", "l":
		m.queueMove(m.qsel, 1)
	case "d":
		m.queueDeleteAt(m.qsel)
	}
	m.refresh()
	return m, nil
}
