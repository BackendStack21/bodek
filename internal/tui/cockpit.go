package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/BackendStack21/bodek/internal/client"
)

// The cockpit popover (/server, the palette, or a header click) is the single
// place to read server,
// link, budget, and session state — the consolidation the redesign promises:
// am I connected, on what model, how full is my context, what has this cost,
// and what are the caps. Everything renders from state the heartbeat,
// /api/limits, and the turn stream already keep live.

// openCockpit shows the popover and fires a one-shot live fetch of the
// authoritative /api/health and /api/usage snapshots — the heartbeat-derived
// fields render immediately; these fill in a moment later.
func (m *Model) openCockpit() tea.Cmd {
	m.popover = true
	m.popScroll = 0
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
		m.popScroll = max(m.popScroll-1, 0)
	case "down", "ctrl+n", "j":
		m.popScroll++ // clamped against the card height when drawn
	case "pgup", "ctrl+u":
		m.popScroll = max(m.popScroll-popoverPage(m.vp.Height), 0)
	case "pgdown", "ctrl+d":
		m.popScroll += popoverPage(m.vp.Height)
	case "ctrl+g":
		m.popScroll = 1 << 20
	}
	m.refresh()
	return m, nil
}

// popoverView renders the cockpit card sized to the transcript area. The card
// hugs its content; h is only a maximum, past which the body scrolls.
func (m *Model) popoverView(w, h int) string {
	th := m.th
	var b strings.Builder
	b.WriteString(th.acTitle.Render("⬡ cockpit"))
	// Same framed-card contract as the composer: Width(term-2) leaves
	// term-4 of text — the rule fills it so the right edge stays flush.
	b.WriteString("\n" + th.rule.Render(strings.Repeat("─", boxInner(w))))

	// Every label/value section shares one label column so values line up
	// across the server, budget, and lifetime cards.
	sections := []cockpitSection{
		{"server", m.cockpitServerRows()},
		{"budget", m.cockpitBudgetRows()},
	}
	if m.usageSnap != nil {
		sections = append(sections, cockpitSection{"lifetime", m.cockpitLifetimeRows()})
	}
	gutter := cockpitGutter(sections)
	for _, s := range sections {
		b.WriteString("\n" + m.cockpitRows(s.title, s.rows, gutter))
	}
	// The session section renders un-boxed — no nested card inside a card.
	b.WriteString("\n\n" + m.sessionBlock(true, gutter+cockpitValueOffset))

	// Window the card to the transcript area (the box height is only a
	// maximum): the hint row stays pinned, the rest scrolls.
	lines := strings.Split(b.String(), "\n")
	visible := max(h-4, 1)
	m.popScroll = min(max(m.popScroll, 0), max(len(lines)-visible, 0))
	if len(lines) > visible {
		lines = lines[m.popScroll : m.popScroll+visible]
	}
	lines = append(lines, "", th.acDetail.Render("r refresh · esc close"))

	return th.acBox.Width(boxWidth(w)).MaxHeight(h).Render(strings.Join(lines, "\n"))
}

// popoverPage is the half-page step for the cockpit card.
func popoverPage(h int) int { return max((h-4)/2, 1) }

// cockpitSection is one titled label→value card inside the cockpit.
type cockpitSection struct {
	title string
	rows  [][2]string
}

// cockpitValueOffset is the distance from a section's label column to its
// value column: the two-space indent plus the gutter space.
const cockpitValueOffset = 3

// cockpitServerRows is the server/link card: identity and liveness from the
// server_info/pong snapshot plus the heartbeat round-trip.
func (m *Model) cockpitServerRows() [][2]string {
	// The engine version row lives in the session block below (⬢ engine) —
	// rendering it here too duplicated it inside the same cockpit.
	rows := [][2]string{
		{"model", orDash(m.model)},
		{"stream", boolDash(m.serverStream, "» live deltas", "buffered")},
		{"sandbox", boolDash(m.sandbox, "isolated", "host access")},
	}
	if m.srvUptime > 0 {
		rows = append(rows, [2]string{"uptime", formatDuration(m.srvUptime)})
	}
	if m.srvConns > 0 {
		rows = append(rows, [2]string{"connections", fmt.Sprintf("%d", m.srvConns)})
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
	return rows
}

// cockpitBudgetRows is the budget card: the server's configured execution
// caps and this session's spend against them.
func (m *Model) cockpitBudgetRows() [][2]string {
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
		return [][2]string{{"caps", "none configured"}}
	}
	return rows
}

// cockpitLifetimeRows is the server-lifetime card from /api/usage.
func (m *Model) cockpitLifetimeRows() [][2]string {
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
	if u.PlansCreated > 0 || u.PlansUpdated > 0 || u.PlansBlocked > 0 {
		rows = append(rows, [2]string{"plans", fmt.Sprintf("%d created · %d updated · %d blocked",
			u.PlansCreated, u.PlansUpdated, u.PlansBlocked)})
	}
	if u.RunsActive > 0 {
		rows = append(rows, [2]string{"active runs", fmt.Sprintf("%d", u.RunsActive)})
	}
	return rows
}

// cockpitGutter is the widest label across every section: the shared label
// column that keeps values aligned from card to card.
func cockpitGutter(sections []cockpitSection) int {
	gutter := 0
	for _, s := range sections {
		for _, r := range s.rows {
			gutter = max(gutter, lipgloss.Width(r[0]))
		}
	}
	return gutter
}

// cockpitRows renders one titled section as label→value rows, padding every
// label to the shared gutter.
func (m *Model) cockpitRows(title string, rows [][2]string, gutter int) string {
	th := m.th
	var b strings.Builder
	b.WriteString(th.statsLabel.Render(title))
	for _, r := range rows {
		pad := strings.Repeat(" ", max(gutter-lipgloss.Width(r[0]), 0)+1)
		b.WriteString("\n  " + th.statsDim.Render(r[0]) + pad + th.statsValue.Render(r[1]))
	}
	return b.String()
}

// boolDash renders a yes/no value with distinct labels per state.
func boolDash(v bool, yes, no string) string {
	if v {
		return yes
	}
	return no
}
