package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/BackendStack21/bodek/internal/client"
)

// command is a slash command typed in the input as "/name [args]".
type command struct {
	name string
	desc string
	run  func(m *Model, args string) tea.Cmd
}

// slashCommands is the registry. Keeping it a function keeps the closures
// simple and avoids package-init ordering concerns.
func slashCommands() []command {
	return []command{
		{"help", "show available commands", func(m *Model, _ string) tea.Cmd {
			m.showHelp()
			return nil
		}},
		{"clear", "clear the conversation", func(m *Model, _ string) tea.Cmd {
			m.msgs = nil
			m.curIdx = -1
			m.refresh()
			return nil
		}},
		{"stats", "session metrics & context gauge", func(m *Model, _ string) tea.Cmd {
			m.showStats()
			return nil
		}},
		{"sessions", "browse & resume saved sessions", func(m *Model, _ string) tea.Cmd {
			return m.openSessions()
		}},
		{"model", "switch model — /model [name]", func(m *Model, args string) tea.Cmd {
			if args != "" {
				m.pendModel = args
				m.model = args
				m.resolveMaxContext()
				m.addNote("model set to " + args + " (applies next turn)")
				m.refresh()
				return nil
			}
			return m.openModels()
		}},
		{"thinking", "extended thinking — /thinking [on|off]", func(m *Model, args string) tea.Cmd {
			switch strings.ToLower(args) {
			case "on", "enabled", "true":
				m.thinkOn = true
			case "off", "disabled", "false":
				m.thinkOn = false
			default:
				m.thinkOn = !m.thinkOn
			}
			state := "off"
			if m.thinkOn {
				state = "on"
			}
			m.addNote("thinking " + state)
			m.refresh()
			return nil
		}},
		{"cancel", "cancel the running turn", func(m *Model, _ string) tea.Cmd {
			return m.cancelRun()
		}},
		{"quit", "exit bodek", func(m *Model, _ string) tea.Cmd {
			m.quitting = true
			return tea.Quit
		}},
	}
}

// commandPrefix reports the command token when the input is a line-initial
// slash command still being typed (a leading "/" with no whitespace yet).
func commandPrefix(s string) (string, bool) {
	if !strings.HasPrefix(s, "/") {
		return "", false
	}
	body := s[1:]
	if strings.ContainsAny(body, " \t\n") {
		return "", false
	}
	return body, true
}

// runCommandLine parses and dispatches a full "/name args" line.
func (m *Model) runCommandLine(text string) tea.Cmd {
	body := strings.TrimPrefix(text, "/")
	name, args := body, ""
	if i := strings.IndexAny(body, " \t"); i >= 0 {
		name, args = body[:i], strings.TrimSpace(body[i+1:])
	}
	return m.runCommand(name, args)
}

// runCommand finds and executes a command by name, resetting the input.
func (m *Model) runCommand(name, args string) tea.Cmd {
	m.ta.Reset()
	m.closeAC()
	for _, c := range slashCommands() {
		if c.name == name {
			return c.run(m, args)
		}
	}
	m.addNote("unknown command: /" + name + " — try /help")
	m.refresh()
	return nil
}

// runSelectedCommand executes the command highlighted in the popup.
func (m *Model) runSelectedCommand() tea.Cmd {
	if len(m.ac.items) == 0 {
		m.closeAC()
		return nil
	}
	name := strings.TrimPrefix(m.ac.items[m.ac.sel].ID, "/")
	return m.runCommand(name, "")
}

// openCmdAC populates the popup with commands matching the typed prefix.
func (m *Model) openCmdAC(query string) {
	var items []client.Resource
	for _, c := range slashCommands() {
		if strings.HasPrefix(c.name, query) {
			items = append(items, client.Resource{
				ID: "/" + c.name, Type: "command", Label: "/" + c.name, Detail: c.desc,
			})
		}
	}
	m.ac.open = true
	m.ac.loading = false
	m.ac.mode = acCmd
	m.ac.query = query
	m.ac.items = items
	m.ac.seq++ // invalidate any in-flight @-search result
	if m.ac.sel >= len(items) {
		m.ac.sel = 0
	}
	m.relayout()
	m.refresh()
}

// showHelp appends a rendered help card listing commands and key bindings.
func (m *Model) showHelp() {
	var b strings.Builder
	b.WriteString("### Commands\n\n")
	for _, c := range slashCommands() {
		b.WriteString(fmt.Sprintf("- `/%s` — %s\n", c.name, c.desc))
	}
	b.WriteString("\n### Keys\n\n")
	b.WriteString("`@` attach files/sessions · `^R` sessions · `^O` model · " +
		"`^T` thinking · `^J` newline · `Esc` cancel · `^L` clear · `^C` quit\n")
	content := b.String()
	m.msgs = append(m.msgs, message{role: roleAsst, content: content, rendered: m.render(content)})
	m.refresh()
}

// showStats appends a session dashboard card: context-window usage, token and
// tool totals, latency, thinking ratio, session age, and model/sandbox. It is
// built as pre-styled lines (raw) so its colors and column alignment are exact
// and survive width changes untouched.
func (m *Model) showStats() {
	th := m.th
	// boxW is the total rendered width (incl. border). lipgloss .Width(w) makes
	// the text content w-2 wide (padding) and adds 2 for the border, so passing
	// boxW-2 yields a box exactly boxW wide with a boxW-4 content column — which
	// the divider and rows must match exactly to keep the right edge flush.
	boxW := max(min(m.vp.Width-2, 60), 28)
	innerW := boxW - 4

	// A labelled row: an accent-tinted glyph, a muted word, then the value.
	type row struct {
		glyph string
		style lipgloss.Style
		label string
		value string
	}
	var rows []row

	if len(m.turnStats) > 0 {
		var sumLat, peakLat float64
		thinkN := 0
		for _, t := range m.turnStats {
			sumLat += t.latency
			if t.latency > peakLat {
				peakLat = t.latency
			}
			if t.thought {
				thinkN++
			}
		}
		mean := sumLat / float64(len(m.turnStats))

		ctxVal := th.statsValue.Render(human(m.sessCtxTok))
		if m.maxContext > 0 {
			ratio := float64(m.sessCtxTok) / float64(m.maxContext)
			if ratio > 1 {
				ratio = 1
			}
			ctxVal = th.statsValue.Render(human(m.sessCtxTok)+"/"+humanCtx(m.maxContext)) +
				"  " + m.gaugeColor(ratio).Render(gaugeGlyph(ratio)) +
				" " + th.statsDim.Render(fmt.Sprintf("%d%%", int(ratio*100+0.5)))
		}

		latVal := th.statsValue.Render(fmt.Sprintf("%.1fs", mean))
		if peakLat > mean+0.05 {
			latVal += th.statsDim.Render(fmt.Sprintf("  · slowest %.1fs", peakLat))
		}

		think := "off"
		if m.thinkOn {
			think = "on"
		}
		// Budget the (sanitized) model id so the " · think …" suffix and the
		// label gutter still fit the content column without wrapping.
		suffix := " · think " + think
		modelBudget := innerW - 11 - lipgloss.Width(suffix)
		if modelBudget < 8 {
			modelBudget = 8
		}
		modelID := truncate(collapse(orDash(m.model)), modelBudget)

		rows = []row{
			{"⌂", th.statCtx, "context", ctxVal},
			{"⎇", th.statCtx, "output", th.statsValue.Render(human(m.sessOutTok))},
			{"↻", th.statsLabel, "turns", th.statsValue.Render(fmt.Sprintf("%d", len(m.turnStats)))},
			{"⚒", th.statTool, "tools", th.statsValue.Render(fmt.Sprintf("%d", m.toolTotal))},
			{"⚡", th.statTime, "latency", latVal},
			{"✳", th.statThink, "thinking", th.statsValue.Render(fmt.Sprintf("%d of %d turns", thinkN, len(m.turnStats)))},
			{"◷", th.statsLabel, "active", th.statsValue.Render(formatDuration(time.Since(m.sessionStart)))},
			{"⬡", th.statThink, "model", th.statsValue.Render(modelID) + th.statsDim.Render(" · think "+think)},
		}
	}

	// Align values into a column just past the widest label.
	gutter := 0
	for _, r := range rows {
		if w := lipgloss.Width(r.glyph + " " + r.label); w > gutter {
			gutter = w
		}
	}
	gutter++ // one space before the value column

	var b strings.Builder
	title := th.acTitle.Render("⬡ session")
	if id := shortID(m.sessionID); id != "" {
		title += " " + th.statsDim.Render(id)
	}
	b.WriteString(title)
	b.WriteString("\n" + th.rule.Render(strings.Repeat("─", innerW)))

	if len(rows) == 0 {
		b.WriteString("\n" + th.statsDim.Render("no turns yet — ask odek something"))
	} else {
		for _, r := range rows {
			styled := r.style.Render(r.glyph) + " " + th.statsLabel.Render(r.label)
			pad := gutter - lipgloss.Width(r.glyph+" "+r.label)
			if pad < 1 {
				pad = 1
			}
			b.WriteString("\n" + styled + strings.Repeat(" ", pad) + r.value)
		}
		idline := m.sandboxBadge()
		if !m.sessionStart.IsZero() {
			idline += th.statsDim.Render(" · started " + ago(m.sessionStart))
		}
		b.WriteString("\n" + th.rule.Render(strings.Repeat("─", innerW)))
		b.WriteString("\n" + idline)
	}

	card := th.acBox.Width(boxW - 2).Render(b.String())
	m.msgs = append(m.msgs, message{role: roleAsst, content: card, rendered: card, raw: true})
	m.refresh()
}
