package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func (m *Model) clearClarify() {
	m.clarify = nil
	m.clarifyBuf = ""
}

func (m *Model) handleClarifyKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.clarify == nil {
		return m, nil
	}
	switch msg.String() {
	case "enter":
		return m, m.sendClarifyAnswer()
	case "esc":
		// The card is not dismissable (odek is waiting). ESC while a turn
		// is running arms cancel — the same two-step gate as a bare composer.
		if m.busy {
			return m, m.armConfirm(confirmCancel, "the running turn")
		}
		return m, nil
	case "backspace", "delete", "ctrl+h":
		m.backspaceClarify()
		return m, nil
	case "ctrl+c":
		return m, m.armConfirm(confirmQuit, "bodek")
	case "shift+enter", "ctrl+enter", "alt+enter", "ctrl+j":
		m.appendClarify("\n")
		return m, nil
	case "pgup", "pgdown", "ctrl+u", "ctrl+d":
		var cmd tea.Cmd
		m.vp, cmd = m.vp.Update(msg)
		return m, cmd
	case "ctrl+g":
		m.vp.GotoBottom()
		return m, nil
	}
	if text := clarifyTyped(msg); text != "" {
		m.appendClarify(text)
	}
	return m, nil
}

// clarifyTyped returns text to insert from a keypress. Bubble Tea sends the
// spacebar as KeySpace (String is " " or "space"), not KeyRunes — a
// KeyRunes-only path mashed words together. Letters, punctuation, and
// paste still arrive as KeyRunes.
func clarifyTyped(msg tea.KeyMsg) string {
	if msg.Type == tea.KeySpace {
		return " "
	}
	if msg.Type == tea.KeyRunes && len(msg.Runes) > 0 && !msg.Alt {
		return string(msg.Runes)
	}
	if s := msg.String(); len([]rune(s)) == 1 {
		return s
	}
	return ""
}

func (m *Model) appendClarify(s string) {
	if s == "" {
		return
	}
	m.clarifyBuf += s
	m.relayout()
	m.refresh()
}

func (m *Model) backspaceClarify() {
	if m.clarifyBuf == "" {
		return
	}
	r := []rune(m.clarifyBuf)
	m.clarifyBuf = string(r[:len(r)-1])
	m.relayout()
	m.refresh()
}

func (m *Model) sendClarifyAnswer() tea.Cmd {
	if m.clarify == nil {
		return nil
	}
	answer := strings.TrimSpace(m.clarifyBuf)
	if answer == "" {
		return nil
	}
	id := m.clarify.ID
	m.clearClarify()
	m.setRunStatus("thinking")
	m.relayout()
	m.refresh()
	cl := m.cl
	return func() tea.Msg {
		if err := cl.SendClarify(id, answer); err != nil {
			return errMsg{err: err}
		}
		return nil
	}
}

func (m *Model) clarifyPanel() string {
	return m.th.apprBox.Width(m.cardWidth()).Render(m.clarifyBody())
}

func (m *Model) clarifyBody() string {
	th := m.th
	if m.clarify == nil {
		return ""
	}
	budget := m.cardInner()
	question := wrapCells(sanitize(m.clarify.Question), budget)
	answer := wrapCells("answer: "+sanitize(m.clarifyBuf), budget)
	if capn := m.clarifyAnswerCap(len(question)); len(answer) > capn {
		answer = answer[len(answer)-capn:]
	}
	lines := []string{th.apprHead.Render("❓ question from the agent")}
	for _, ln := range question {
		lines = append(lines, th.apprBody.Render(ln))
	}
	for _, ln := range answer {
		lines = append(lines, th.apprKey.Render(ln))
	}
	lines = append(lines, th.apprBody.Render("enter send · ⇧⏎ newline · type to answer"))
	return strings.Join(lines, "\n")
}

// wrapCells hard-wraps s to n display columns (CJK/emoji count as two).
// Unlike wrapText, this is cell-width aware so a wide glyph cannot overflow
// the card. Always returns at least one line.
func wrapCells(s string, n int) []string {
	if n < 1 {
		n = 1
	}
	if s == "" {
		return []string{""}
	}
	return strings.Split(ansi.Wrap(s, n, ""), "\n")
}

// clarifyAnswerCap is the max wrapped answer rows the card may paint. The
// buffer keeps the full text (so a long paste is not refused); only the
// tail is shown so typing stays visible and View cannot outgrow the terminal.
func (m *Model) clarifyAnswerCap(questionLines int) int {
	if questionLines < 1 {
		questionLines = 1
	}
	used := headerHeight + footerHeight + 1 // one transcript row stays
	used += m.ta.Height() + 2               // composer box
	if m.statusLineVisible() {
		used += 2
	}
	used += 2 + 1 + questionLines + 1 // card borders, head, question, hint
	room := m.height - used
	if room < 1 {
		room = 1
	}
	if room > composerMaxRows {
		room = composerMaxRows
	}
	return room
}
