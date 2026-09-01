package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/BackendStack21/bodek/internal/client"
)

// The cockpit popover (h, or /server) is the single place to read server,
// link, budget, and session state — the consolidation the redesign promises:
// am I connected, on what model, how full is my context, what has this cost,
// and what are the caps. Everything renders from state the heartbeat,
// /api/limits, and the turn stream already keep live.

// openCockpit shows the popover and fires a one-shot live fetch of the
// authoritative /api/health and /api/usage snapshots — the heartbeat-derived
// fields render immediately; these fill in a moment later.
func (m *Model) openCockpit() tea.Cmd {
	m.popover = true
	m.refresh()
	return m.cockpitFetch()
}

// cockpitFetch is the live health+usage fetch: fired on open and again on
// r while the popover stays up.
func (m *Model) cockpitFetch() tea.Cmd {
	cl := m.cl
	if cl == nil {
		return nil
	}
	return func() tea.Msg {
		msg := cockpitMsg{}
		msg.health, _ = cl.Health()
		msg.usage, _ = cl.Usage()
		return msg
	}
}

// cockpitMsg carries the popover's live fetch (either half may fail soft).
type cockpitMsg struct {
	health client.Health
	usage  client.Usage
}

func (m *Model) handleCockpitMsg(msg cockpitMsg) tea.Cmd {
	h, u := msg.health, msg.usage
	m.healthSnap, m.usageSnap = &h, &u
	if m.popover {
		m.refresh()
	}
	return nil
}

// handlePopoverKey drives the cockpit overlay: it never blocks the run (the
// transcript keeps streaming underneath), scroll keys page the card,
// r re-fires the live fetch, and esc/h/q close it.
func (m *Model) handlePopoverKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		return m, m.armConfirm(confirmQuit, "bodek")
	case "esc", "h", "q":
		m.popover = false
		m.refresh()
		return m, nil
	case "r":
		return m, m.cockpitFetch()
	case "up", "ctrl+p", "k":
		m.vp.ScrollUp(1)
	case "down", "ctrl+n", "j":
		m.vp.ScrollDown(1)
	case "pgup", "ctrl+u":
		m.vp.HalfPageUp()
	case "pgdown", "ctrl+d":
		m.vp.HalfPageDown()
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
	// acBox adds border(2) + padding(2): Width(w-2) leaves w-4 of text —
	// the rule fills it exactly so the card's right edge stays flush.
	b.WriteString("\n" + th.rule.Render(strings.Repeat("─", max(w-4, 8))))

	b.WriteString("\n" + m.cockpitServerSection())
	b.WriteString("\n" + m.cockpitBudgetSection())
	if m.usageSnap != nil {
		b.WriteString("\n" + m.cockpitLifetimeSection())
	}
	// The session section renders un-boxed — no nested card inside a card.
	b.WriteString("\n\n" + m.statsBody())
	b.WriteString("\n\n" + th.acDetail.Render("r refresh · esc close"))

	return th.acBox.Width(w - 2).Height(h - 2).Render(b.String())
}

// cockpitServerSection is the server/link card: identity and liveness from
// the server_info/pong snapshot plus the heartbeat round-trip.
func (m *Model) cockpitServerSection() string {
	rows := [][2]string{
		{"version", orDash(prefixVersion("odek ", m.odekVersion))},
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
	if m.healthSnap != nil && !m.healthSnap.StartedAt.IsZero() {
		rows = append(rows, [2]string{"since", m.healthSnap.StartedAt.Format("Jan 2 15:04")})
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
		spend := formatUSD(costUSD(m.sessCtxTok, m.sessOutTok, inPrice, outPrice) + m.subCostTotal())
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

// cockpitLifetimeSection is the server-lifetime card from /api/usage.
func (m *Model) cockpitLifetimeSection() string {
	u := m.usageSnap
	rows := [][2]string{
		{"prompts", fmt.Sprintf("%d started · %d completed", u.PromptsStarted, u.PromptsCompleted)},
		{"tokens", fmt.Sprintf("⇥%s ↦%s", human(int(u.TokensIn)), human(int(u.TokensOut)))},
	}
	if u.PricesConfigured {
		rows = append(rows, [2]string{"lifetime cost", formatUSD(u.EstimatedCostUSD)})
	} else {
		rows = append(rows, [2]string{"lifetime cost", "unavailable (no prices)"})
	}
	if u.RunsActive > 0 {
		rows = append(rows, [2]string{"active runs", fmt.Sprintf("%d", u.RunsActive)})
	}
	return m.cockpitRows("lifetime", rows)
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
