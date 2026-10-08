package tui

import (
	"fmt"
	"strings"
	"unicode/utf8"

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
// print; completed reply segments print at the next turn boundary).
func (m *Model) plainPrintCmd(ev client.Event) tea.Cmd {
	if !m.plain {
		return nil
	}
	return plainLinesCmd(m.plainEventLines(ev))
}

// plainLinesCmd emits one ordered batch of scrollback lines.
func plainLinesCmd(lines []string) tea.Cmd {
	if len(lines) == 0 {
		return nil
	}
	return tea.Println(strings.Join(lines, "\n"))
}

// plainEventLines maps a wire event to its linear text lines. Kept free of
// tea types so tests assert the mapping directly. Every wire-borne string
// goes through sanitize(); event labels also flatten whitespace. Reply
// segments keep their formatting and print before the next reasoning/tool
// boundary, with only the remaining text emitted on completion or failure.
func (m *Model) plainEventLines(ev client.Event) []string {
	if ev.Type == "answer_superseded" {
		// The draft already reached (or is about to reach) scrollback, which
		// cannot be unprinted: print what is left of it, then say plainly
		// that the next answer replaces it.
		out := m.plainReplyLines()
		if d := strings.TrimSpace(m.plainDraft); d != "" {
			out = append(out, d)
		}
		m.plainDraft = ""
		return append(out, "[draft revised · "+draftReasonLabel(collapse(ev.Reason))+"] the next answer replaces the text above")
	}
	var reply []string
	switch ev.Type {
	case "thinking", "thinking_delta", "tool_call", "approval_request", "clarify_request", "done", "error", client.EventDisconnected:
		reply = m.plainReplyLines()
	}
	return append(reply, m.plainStatusLines(ev)...)
}

// plainReplyLines drains only new prose from the latest assistant card.
// Keeping the cursor on the message prevents a new empty turn from replaying
// an earlier answer and lets a failed turn retain its partial output.
func (m *Model) plainReplyLines() []string {
	for i := len(m.msgs) - 1; i >= 0; i-- {
		msg := &m.msgs[i]
		if msg.role != roleAsst || msg.raw {
			continue
		}
		if msg.plainPrinted >= len(msg.content) {
			return nil
		}
		text := sanitize(msg.content[msg.plainPrinted:])
		msg.plainPrinted = len(msg.content)
		if strings.TrimSpace(text) == "" {
			return nil
		}
		return []string{strings.TrimPrefix(text, "\n\n")}
	}
	return nil
}

func (m *Model) plainStatusLines(ev client.Event) []string {
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
		return []string{plainClip("⚠ approval — a approve · d deny (empty draft); Alt+A/Alt+D always" + what)}

	case "skill_event", "memory_event", "agent_signal":
		// Engine bookkeeping: no reachable action, and plain scrollback has
		// no drawer tabs to consult — silence beats noise.
		return nil
	case "subagent_log":
		line := strings.TrimSpace(collapse(ev.SubType + " " + ev.Name))
		if d := collapse(ev.Detail); d != "" {
			line = strings.TrimSpace(line + " · " + d)
		}
		return []string{plainClip("· subagent · " + line + eventTail(ev))}

	case "session":
		if ev.SystemInitiated {
			return []string{"[wake] background job finished — agent turning"}
		}

	case "done":
		return []string{m.plainDoneSummary()}

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
	for i := len(m.msgs) - 1; i >= 0; i-- {
		if m.msgs[i].role == roleAsst {
			if m.msgs[i].unverified {
				s = "✗ unverified"
			}
			break
		}
	}
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
	for cut > 0 && !utf8.RuneStart(r[cut]) {
		cut-- // never split a UTF-8 sequence
	}
	return string(r[:cut]) + "…"
}

// plainPromptLine renders a submitted prompt for the scrollback.
func plainPromptLine(text string) string {
	return "❯ " + plainClip(collapse(text))
}
