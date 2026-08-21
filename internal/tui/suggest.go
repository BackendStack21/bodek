package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// ── skill suggestions (skill_prompt_response) ───────────────────────────────
//
// When the learn loop proposes a skill (skill_event "suggested"), the card
// sits above the composer as a PASSIVE affordance: it never captures ⏎ or
// esc — a suggestion arriving mid-conversation must not block sending — and
// answers ride alt-chords. The protocol reply is an acknowledgment; skill
// auto-save governs real persistence server-side.

// handleSuggestKeys answers a pending skill suggestion when one shows.
func (m *Model) handleSuggestKeys(s string) (tea.Model, tea.Cmd, bool) {
	if m.skillSuggest == nil {
		return m, nil, false
	}
	switch s {
	case "alt+s":
		return m, m.answerSuggestion("save"), true
	case "alt+x":
		return m, m.answerSuggestion("skip"), true
	}
	return m, nil, false
}

func (m *Model) answerSuggestion(action string) tea.Cmd {
	name := m.skillSuggest.SkillName
	m.skillSuggest = nil
	m.relayout()
	m.refresh()
	cl := m.cl
	verb := "skipped"
	if action == "save" {
		verb = "saved"
	}
	note := m.transientNoteCmd("skill " + name + ": " + verb + " (auto-save governs persistence)")
	return tea.Batch(note, func() tea.Msg {
		if err := cl.SendSkillPromptResponse(action, name); err != nil {
			return errMsg{err}
		}
		return nil
	})
}

// suggestionCard renders the pending suggestion above the composer. Height
// must match suggestionCardHeight for the layout math.
func (m *Model) suggestionCard() string {
	th := m.th
	if m.skillSuggest == nil {
		return ""
	}
	name := m.skillSuggest.SkillName
	if name == "" {
		name = "(unnamed)"
	}
	heur := strings.TrimSpace(m.skillSuggest.Detail)
	title := th.acTitle.Render("✦ skill suggested") + th.acItem.Render("  "+name)
	rows := []string{title}
	if heur != "" {
		rows = append(rows, th.acDetail.Render("  "+truncate(heur, max(m.width-8, 12))))
	}
	rows = append(rows, th.footerKey.Render("alt+s")+th.footer.Render(" save · ")+
		th.footerKey.Render("alt+x")+th.footer.Render(" skip")+th.acDim.Render("  — auto-save governs persistence"))
	return th.acBox.Width(m.width - 2).Render(strings.Join(rows, "\n"))
}

// suggestionCardHeight is the card's rendered height (border + title + hint,
// + heuristic line when present).
func (m *Model) suggestionCardHeight() int {
	if m.skillSuggest == nil {
		return 0
	}
	h := 5
	if strings.TrimSpace(m.skillSuggest.Detail) != "" {
		h++
	}
	return h
}
