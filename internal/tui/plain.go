package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/BackendStack21/bodek/internal/client"
)

// Linear mode (--plain) replaces the alt-screen transcript with an
// append-only scrollback log: agent events print above a minimal input
// chrome while they arrive, and severity rides text prefixes so it survives
// without color. Screen readers, tmux copy-mode, and
// `bodek --plain < task > run.log` pipelines all read the same linear feed.

// plainPanelMax caps overlay height (management drawer, palette, approval
// panel) in linear mode: they render as bottom chrome, not full screen.
const plainPanelMax = 14

// plainPrintCmd renders one agent event into the scrollback. Nil when plain
// mode is off or the event maps to no output (streamed fragments never
// print; the reply lands whole on done).
func (m *Model) plainPrintCmd(ev client.Event) tea.Cmd {
	if !m.plain {
		return nil
	}
	lines := m.plainEventLines(ev)
	if len(lines) == 0 {
		return nil
	}
	return tea.Println(strings.Join(lines, "\n"))
}

// plainEventLines maps a wire event to its linear text lines. Kept free of
// tea types so tests assert the mapping directly. Every wire-borne string
// goes through collapse() — sanitize + whitespace flatten — because these
// lines land verbatim on the terminal.
func (m *Model) plainEventLines(ev client.Event) []string {
	switch ev.Type {
	case "thinking":
		if s := collapse(ev.Content); s != "" {
			return []string{"[think] " + plainClip(s)}
		}

	case "tool_call":
		name := collapse(ev.Name)
		if arg := collapse(ev.Data); arg != "" {
			return []string{plainClip("▸ " + name + " · " + arg)}
		}
		return []string{"▸ " + name}

	case "tool_result":
		glyph := "✓"
		if looksLikeError(ev.Data) {
			glyph = "✗"
		}
		return []string{"▪ " + collapse(ev.Name) + " " + glyph}

	case "error":
		if s := collapse(ev.Message); s != "" {
			return []string{"[error] " + plainClip(s)}
		}

	case "approval_request":
		what := collapse(ev.Risk)
		if cmd := collapse(ev.Command); cmd != "" {
			if what != "" {
				what += ": "
			}
			what += cmd
		}
		if what != "" {
			what = " · " + what
		}
		return []string{plainClip("⚠ approval" + what + " — ↑/↓ then ⏎ (Esc denies)")}

	case "skill_event":
		return []string{"· skill · " + strings.TrimSpace(collapse(ev.SubType+" "+ev.SkillName)) + eventTail(ev)}
	case "memory_event":
		return []string{"· memory · " + strings.TrimSpace(collapse(ev.SubType+" "+ev.Target)) + eventTail(ev)}
	case "agent_signal":
		return []string{"· signal · " + strings.TrimSpace(collapse(ev.SubType+" "+ev.Detail)) + eventTail(ev)}
	case "subagent_log":
		line := strings.TrimSpace(collapse(ev.SubType + " " + ev.Name))
		if d := collapse(ev.Detail); d != "" {
			line = strings.TrimSpace(line + " · " + d)
		}
		return []string{plainClip("· subagent · " + line + eventTail(ev))}

	case "done":
		var lines []string
		if reply := m.lastReply(); reply != "" {
			lines = append(lines, reply)
		}
		lines = append(lines, m.plainDoneSummary())
		return lines

	case client.EventDisconnected:
		return []string{"[error] connection lost"}
	}
	return nil
}

// plainDoneSummary builds the turn-boundary line from the telemetry the
// done handler captured (plainPrintCmd runs after handleEvent, so the last
// turnStats entry is this turn's).
func (m *Model) plainDoneSummary() string {
	s := "✓ done"
	if n := len(m.turnStats); n > 0 {
		ts := m.turnStats[n-1]
		s += fmt.Sprintf(" · %d tools · %.1fs · %d tok", ts.toolCount, ts.wall.Seconds(), ts.outTok)
	}
	return s
}

// plainClip bounds a line for the scrollback log: long tool arguments and
// errors excerpt instead of flooding the feed (the full text still lives in
// the session for the TUI and exports).
func plainClip(s string) string {
	const max = 160
	r := []byte(s)
	if len(r) <= max {
		return s
	}
	cut := max
	for cut > 0 && r[cut-1] >= 0x80 && r[cut-1] < 0xC0 {
		cut-- // never split a UTF-8 sequence
	}
	return string(r[:cut]) + "…"
}

// plainPromptLine renders a submitted prompt for the scrollback.
func plainPromptLine(text string) string {
	return "❯ " + plainClip(collapse(text))
}
