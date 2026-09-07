package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// Canonical thinking levels — same contract as odek serve.
var thinkingLevels = []string{"disabled", "low", "medium", "high"}

func normalizeThinking(s string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "":
		return "", true
	case "disabled", "low", "medium", "high":
		return strings.ToLower(strings.TrimSpace(s)), true
	case "mid":
		return "medium", true
	case "enabled", "on", "true", "1":
		return "medium", true
	case "max":
		return "high", true
	case "off", "false", "0":
		return "disabled", true
	case "inherit", "default":
		return "", true
	default:
		return "", false
	}
}

func cycleThinking(cur string) string {
	if cur == "" || cur == "disabled" {
		return "low"
	}
	for i, l := range thinkingLevels {
		if l == cur {
			return thinkingLevels[(i+1)%len(thinkingLevels)]
		}
	}
	return "low"
}

func thinkingHeaderLabel(level string) string {
	switch level {
	case "low":
		return "low"
	case "medium":
		return "mid"
	case "high":
		return "high"
	default:
		return ""
	}
}

func thinkingWire(level string) string {
	if level == "" {
		return ""
	}
	return level
}

func seedStartupThinking(raw string) string {
	canon, ok := normalizeThinking(raw)
	if !ok {
		return ""
	}
	return canon
}

type thinkingSeedMsg struct {
	level string
}

func (m *Model) fetchThinkingSeed() tea.Cmd {
	if m.thinking != "" {
		return nil
	}
	cl := m.cl
	if cl == nil {
		return nil
	}
	return func() tea.Msg {
		cfg, err := cl.ConfigView()
		if err != nil || cfg == nil {
			return nil
		}
		raw, _ := cfg["thinking"].(string)
		canon, ok := normalizeThinking(raw)
		if !ok || canon == "" {
			return nil
		}
		return thinkingSeedMsg{level: canon}
	}
}

func (m *Model) applyThinkingSeed(msg thinkingSeedMsg) {
	if m.thinking != "" || msg.level == "" {
		return
	}
	m.thinking = msg.level
}

func (m *Model) setThinking(level string) tea.Cmd {
	canon, ok := normalizeThinking(level)
	if !ok {
		return m.transientNoteCmd("thinking: want disabled, low, medium, high (or inherit)")
	}
	m.thinking = canon
	label := canon
	if label == "" {
		label = "inherit"
	}
	if m.opts.OnThinkingChange != nil {
		if err := m.opts.OnThinkingChange(canon); err != nil {
			m.refresh()
			return m.transientNoteCmd("thinking " + label + " (save failed)")
		}
	}
	m.refresh()
	return m.transientNoteCmd("thinking " + label)
}

func (m *Model) cycleThinkingLevel() tea.Cmd {
	return m.setThinking(cycleThinking(m.thinking))
}
