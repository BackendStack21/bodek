package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// The cockpit popover (h, or /server) is the single place to read server,
// link, budget, and session state — the consolidation the redesign promises:
// am I connected, on what model, how full is my context, what has this cost,
// and what are the caps. Everything renders from state the heartbeat,
// /api/limits, and the turn stream already keep live.

// handlePopoverKey drives the cockpit overlay: it never blocks the run (the
// transcript keeps streaming underneath), scroll keys page the card, and
// esc/h/q close it.
func (m *Model) handlePopoverKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		m.quitting = true
		return m, tea.Quit
	case "esc", "h", "q":
		m.popover = false
		m.refresh()
		return m, nil
	case "up", "ctrl+p", "k":
		m.vp.LineUp(1)
	case "down", "ctrl+n", "j":
		m.vp.LineDown(1)
	case "pgup", "ctrl+u":
		m.vp.HalfViewUp()
	case "pgdown", "ctrl+d":
		m.vp.HalfViewDown()
	case "ctrl+g":
		m.vp.GotoBottom()
	}
	m.refresh()
	return m, nil
}

// popoverView renders the cockpit card sized to the transcript area.
func (m *Model) popoverView(w, h int) string {
	th := m.th
	var b strings.Builder
	b.WriteString(th.acTitle.Render("⬡ cockpit"))
	b.WriteString("\n" + th.rule.Render(strings.Repeat("─", max(w-6, 8))))

	b.WriteString("\n" + m.cockpitServerSection())
	b.WriteString("\n" + m.cockpitBudgetSection())
	b.WriteString("\n\n" + m.statsCardBody())

	return th.acBox.Width(w - 2).Height(h - 2).Render(b.String())
}

// cockpitServerSection is the server/link card: identity and liveness from
// the server_info/pong snapshot plus the heartbeat round-trip.
func (m *Model) cockpitServerSection() string {
	rows := [][2]string{
		{"server", orDash(prefixVersion("odek ", m.odekVersion))},
		{"model", orDash(m.model)},
		{"stream", boolDash(m.serverStream, "⚡ live deltas", "buffered")},
		{"sandbox", boolDash(m.sandbox, "isolated", "host access")},
	}
	if m.srvUptime > 0 {
		rows = append(rows, [2]string{"uptime", formatDuration(m.srvUptime)})
	}
	if m.srvConns > 0 {
		rows = append(rows, [2]string{"ws conns", fmt.Sprintf("%d", m.srvConns)})
	}
	if m.rtt > 0 {
		rows = append(rows, [2]string{"rtt", formatStepDur(m.rtt)})
	}
	if id := shortID(m.sessionID); id != "" {
		rows = append(rows, [2]string{"session", id})
	}
	return m.cockpitRows("server", rows)
}

// cockpitBudgetSection is the budget card: the server's configured execution
// caps and this session's spend against them.
func (m *Model) cockpitBudgetSection() string {
	l := m.limits
	inPrice, outPrice := m.prices()
	rows := [][2]string{}
	if l.MaxRuntimeSeconds > 0 {
		rows = append(rows, [2]string{"runtime cap", fmt.Sprintf("%ds", l.MaxRuntimeSeconds)})
	}
	if l.MaxToolCalls > 0 {
		rows = append(rows, [2]string{"tool calls", fmt.Sprintf("%d", l.MaxToolCalls)})
	}
	if l.MaxCostUSD > 0 {
		spend := formatUSD(costUSD(m.sessCtxTok, m.sessOutTok, inPrice, outPrice))
		rows = append(rows, [2]string{"cost cap", fmt.Sprintf("%s of %s", spend, formatUSD(l.MaxCostUSD))})
	}
	if inPrice > 0 && outPrice > 0 {
		rows = append(rows, [2]string{"prices", fmt.Sprintf("$%.2f in · $%.2f out /M", inPrice, outPrice)})
	}
	if len(rows) == 0 {
		return m.cockpitRows("budget", [][2]string{{"caps", "none configured"}})
	}
	return m.cockpitRows("budget", rows)
}

// cockpitRows renders one titled section as aligned label→value rows.
func (m *Model) cockpitRows(title string, rows [][2]string) string {
	th := m.th
	gutter := 0
	for _, r := range rows {
		if w := lipgloss.Width(r[0]); w > gutter {
			gutter = w
		}
	}
	var b strings.Builder
	b.WriteString(th.statsLabel.Render(title))
	for _, r := range rows {
		pad := strings.Repeat(" ", max(gutter-lipgloss.Width(r[0]), 0)+1)
		b.WriteString("\n  " + th.statsDim.Render(r[0]) + pad + th.statsValue.Render(r[1]))
	}
	return b.String()
}

// prefixVersion prepends a label when v is non-empty.
func prefixVersion(label, v string) string {
	if v == "" {
		return ""
	}
	return label + v
}

// boolDash renders a yes/no value with distinct labels per state.
func boolDash(v bool, yes, no string) string {
	if v {
		return yes
	}
	return no
}
