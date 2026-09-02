package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// findState is the transcript search bar. alt+f opens it; typed runes filter
// matches live across message text, reasoning/reply segments, and tool steps;
// ⏎ jumps to the next match, N to the previous; esc closes. While open the
// bar captures the keyboard — printable keys filter, they never type into
// the composer.
type findState struct {
	open    bool
	query   []rune
	matches []int // message indices holding a match, transcript order
	sel     int   // cursor into matches; ⏎ jumps to matches[sel]
}

// openFind shows the find bar and reserves its row.
func (m *Model) openFind() {
	m.find = findState{open: true}
	m.relayout()
	m.refresh()
}

// closeFind hides the find bar and drops the query.
func (m *Model) closeFind() {
	if !m.find.open {
		return
	}
	m.find = findState{}
	m.relayout()
	m.refresh()
}

// handleFindKey routes the keyboard while the find bar is open.
func (m *Model) handleFindKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		return m, m.armConfirm(confirmQuit, "bodek")
	case "esc", "alt+f":
		m.closeFind()
		return m, nil
	case "enter":
		m.findGoto(1)
		return m, nil
	case "N":
		m.findGoto(-1)
		return m, nil
	case "n":
		// vim/less reflex: lowercase is next — never query text while open.
		m.findGoto(1)
		return m, nil
	case "backspace":
		if n := len(m.find.query); n > 0 {
			m.find.query = m.find.query[:n-1]
			m.findRescan()
			m.refresh()
		}
		return m, nil
	case "ctrl+l":
		// The clear-confirm chord stays live while searching: arming hands
		// the keyboard to the confirm gate — y fires (the wipe also resets
		// and closes this bar), any other key disarms back into the query.
		if !m.busy {
			return m, m.armConfirm(confirmClear, "the conversation")
		}
		return m, nil
	}
	if msg.Type == tea.KeyRunes && len(msg.Runes) > 0 {
		m.find.query = append(m.find.query, msg.Runes...)
		m.findRescan()
		m.refresh()
	}
	// Scrolling stays live while searching — the same passthrough the
	// approval panel grants, so the bar is never a scroll dead-end.
	switch msg.String() {
	case "up", "down", "pgup", "pgdown", "ctrl+u", "ctrl+d":
		var cmd tea.Cmd
		m.vp, cmd = m.vp.Update(msg)
		return m, cmd
	case "ctrl+g":
		m.vp.GotoBottom()
		return m, nil
	}
	return m, nil // swallow everything else while the bar is open
}

// findRescan recomputes matches for the query over the whole transcript.
func (m *Model) findRescan() {
	q := strings.ToLower(string(m.find.query))
	m.find.matches = nil
	m.find.sel = 0
	if q == "" {
		return
	}
	for i := range m.msgs {
		if findMsgMatch(m.msgs[i], q) {
			m.find.matches = append(m.find.matches, i)
		}
	}
}

// findMsgMatch reports whether a message's visible text contains q. Raw cards
// (help/stats — snapshot blobs with embedded ANSI) are skipped: their bytes
// are not user prose.
func findMsgMatch(msg message, q string) bool {
	if msg.raw {
		return false
	}
	if strings.Contains(strings.ToLower(msg.content), q) {
		return true
	}
	for _, it := range msg.items {
		if (it.thinking || it.reply) && strings.Contains(strings.ToLower(it.text), q) {
			return true
		}
	}
	for _, s := range msg.steps {
		if strings.Contains(strings.ToLower(s.name+" "+s.arg+" "+s.result), q) {
			return true
		}
		for _, l := range s.logs {
			if strings.Contains(strings.ToLower(l), q) {
				return true
			}
		}
	}
	return false
}

// findGoto jumps the viewport to the match at the cursor and then advances
// the cursor by dir (+1 next, -1 previous, wrapping), parking one line of
// context above the target block.
func (m *Model) findGoto(dir int) {
	n := len(m.find.matches)
	if n == 0 {
		return
	}
	line := m.msgLine(m.find.matches[m.find.sel])
	if idx := m.find.matches[m.find.sel]; idx >= 0 && idx < len(m.msgs) && m.msgs[idx].role == roleAsst {
		m.focusIdx = idx // the jumped-to reply becomes the alt+y copy target
	}
	m.find.sel = ((m.find.sel+dir)%n + n) % n
	if line > 0 {
		line-- // land with one line of context above the block
	}
	m.vp.SetYOffset(line)
	m.refresh()
}

// msgLine maps a message index to its first content line, 0 when unknown.
func (m *Model) msgLine(idx int) int {
	for _, r := range m.msgLineIndex {
		if r.msgIdx == idx {
			return r.line
		}
	}
	return 0
}

// findBar renders the one-row search strip above the input box.
func (m *Model) findBar() string {
	th := m.th
	q := string(m.find.query)
	if w := m.width - 52; w > 0 && lipgloss.Width(q) > w {
		q = truncate(q, w)
	}
	count := "type to search the transcript"
	if len(m.find.query) > 0 {
		if n := len(m.find.matches); n == 0 {
			count = th.footerDanger.Render("no matches")
		} else {
			count = plural(n, "match", "matches")
		}
	}
	return " " + th.footerKey.Render("find") +
		th.footer.Render(" '"+q+"' · "+count+" · ⏎/n next · N prev · ↑↓ scroll · esc close")
}
