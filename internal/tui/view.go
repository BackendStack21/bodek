package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// View composes the full screen: header, scrollable transcript, input (or
// approval prompt), and footer.
func (m *Model) View() string {
	if !m.ready {
		return "\n  starting bodek…"
	}
	parts := []string{
		m.header(),
		m.vp.View(),
		m.inputArea(),
		m.footer(),
	}
	return strings.Join(parts, "\n")
}

// ── header ─────────────────────────────────────────────────────────────────

func (m *Model) header() string {
	th := m.th
	logo := th.logo.Render(gradient("⬡ bodek", gradFrom, gradTo))

	sandbox := "off"
	if m.sandbox {
		sandbox = "on"
	}
	think := "off"
	if m.thinkOn {
		think = "on"
	}
	modelName := m.model
	if modelName == "" {
		modelName = "default"
	}
	meta := th.headerMeta.Render(fmt.Sprintf("%s · sandbox %s · think %s",
		th.headerKey.Render(modelName), sandbox, think))

	left := logo + th.headerMeta.Render("   "+meta)

	status := m.statusBadge()
	tokens := th.headerMeta.Render(fmt.Sprintf("∑ ⌂ %s · ⎇ %s",
		human(m.sessCtxTok), human(m.sessOutTok)))
	right := tokens + "   " + status

	gap := m.width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		gap = 1
	}
	bar := left + strings.Repeat(" ", gap) + right
	rule := th.rule.Render(strings.Repeat("─", max(m.width, 1)))
	return bar + "\n" + rule
}

func (m *Model) statusBadge() string {
	th := m.th
	switch {
	case m.disconn:
		return lipgloss.NewStyle().Foreground(colRed).Render("● disconnected")
	case m.approval != nil:
		return th.statusBusy.Render("● approval required")
	case m.busy:
		return th.statusBusy.Render(m.sp.View() + " " + m.status)
	default:
		return th.statusReady.Render("● " + m.status)
	}
}

// ── transcript ───────────────────────────────────────────────────────────

// refresh rebuilds the viewport content and scrolls to the latest output.
func (m *Model) refresh() {
	if !m.ready {
		return
	}
	m.vp.SetContent(m.conversation())
	m.vp.GotoBottom()
}

func (m *Model) conversation() string {
	if len(m.msgs) == 0 {
		return welcome(m.th, m.vp.Width)
	}
	blocks := make([]string, 0, len(m.msgs)+1)
	for i := range m.msgs {
		blocks = append(blocks, m.renderMessage(m.msgs[i]))
	}
	// Live reasoning for the in-flight turn, shown dimly under the steps.
	if m.busy && m.thinking.Len() > 0 {
		think := m.th.thinkStyle.Render("… " + collapse(m.thinking.String()))
		blocks = append(blocks, lipgloss.NewStyle().Width(m.vp.Width).Render(think))
	}
	if len(m.notices) > 0 {
		blocks = append(blocks, m.renderNotices())
	}
	return strings.Join(blocks, "\n\n")
}

func (m *Model) renderMessage(msg message) string {
	th := m.th
	switch msg.role {
	case roleUser:
		label := th.userLabel.Render("❯ you")
		body := th.userBar.Width(m.vp.Width - 2).Render(msg.content)
		return label + "\n" + body

	case roleNote:
		return th.sysBar.Width(m.vp.Width - 2).Render(msg.content)

	default: // assistant
		label := th.asstLabel.Render("⬡ odek")
		var b strings.Builder
		if steps := m.renderSteps(msg); steps != "" {
			b.WriteString(steps)
			if strings.TrimSpace(msg.content) != "" {
				b.WriteString("\n")
			}
		}
		content := msg.content
		if !msg.streaming && msg.rendered != "" {
			content = msg.rendered
		}
		if strings.TrimSpace(content) == "" && msg.streaming {
			content = th.thinkStyle.Render(m.sp.View() + " thinking…")
		}
		b.WriteString(content)
		body := th.asstBar.Width(m.vp.Width - 2).Render(strings.TrimRight(b.String(), "\n"))
		return label + "\n" + body
	}
}

func (m *Model) renderSteps(msg message) string {
	if len(msg.steps) == 0 {
		return ""
	}
	th := m.th
	lines := make([]string, 0, len(msg.steps))
	for _, s := range msg.steps {
		var icon string
		switch {
		case s.done:
			icon = th.stepDone.Render("✓")
		case msg.streaming:
			icon = m.sp.View()
		default:
			icon = th.stepRun.Render("▸")
		}
		line := icon + " " + th.stepName.Render(s.name)
		if s.arg != "" {
			line += th.stepArg.Render("  " + s.arg)
		}
		lines = append(lines, line)
		if s.done && s.result != "" {
			lines = append(lines, th.stepRes.Render("    ↳ "+s.result))
		}
	}
	return strings.Join(lines, "\n")
}

func (m *Model) renderNotices() string {
	th := m.th
	lines := make([]string, len(m.notices))
	for i, n := range m.notices {
		lines[i] = th.noticeStyle.Render("· " + n)
	}
	return strings.Join(lines, "\n")
}

// ── input / approval area ──────────────────────────────────────────────────

func (m *Model) inputArea() string {
	if m.approval != nil {
		return m.approvalPanel()
	}
	return m.th.inputBox.Width(m.width - 2).Render(m.ta.View())
}

func (m *Model) approvalPanel() string {
	th := m.th
	a := m.approval
	head := th.apprHead.Render(fmt.Sprintf("⚠ approval required · risk: %s", orDash(a.Risk)))

	target := a.Command
	if a.Name != "" {
		target = a.Name + ": " + target
	}
	cmd := th.apprBody.Render(truncate(collapse(target), m.width-8))

	desc := ""
	if a.Description != "" {
		desc = th.noticeStyle.Render(truncate(collapse(a.Description), m.width-8)) + "\n"
	}

	keys := th.apprKey.Render("[a]") + th.apprBody.Render(" approve   ") +
		th.apprKey.Render("[d]") + th.apprBody.Render(" deny")
	if a.AllowTrust {
		keys += th.apprKey.Render("   [t]") + th.apprBody.Render(" trust class")
	}

	body := head + "\n" + cmd + "\n" + desc + keys
	return th.apprBox.Width(m.width - 2).Render(body)
}

// ── footer ─────────────────────────────────────────────────────────────────

func (m *Model) footer() string {
	th := m.th
	if m.approval != nil {
		return th.footer.Render("  answer the approval prompt to continue")
	}
	if m.disconn {
		return th.footer.Render("  connection closed · press ^C to quit")
	}
	sep := th.footerSep.Render("  ·  ")
	keys := []string{
		th.footerKey.Render("⏎") + th.footer.Render(" send"),
		th.footerKey.Render("^J") + th.footer.Render(" newline"),
		th.footerKey.Render("^T") + th.footer.Render(" thinking"),
		th.footerKey.Render("^L") + th.footer.Render(" clear"),
		th.footerKey.Render("^C") + th.footer.Render(" quit"),
	}
	left := "  " + strings.Join(keys, sep)

	right := ""
	if m.lastLatency > 0 {
		right = th.footer.Render(fmt.Sprintf("%.1fs  ", m.lastLatency))
	}
	gap := m.width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		gap = 1
	}
	return left + strings.Repeat(" ", gap) + right
}

// ── small helpers ──────────────────────────────────────────────────────────

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

// human formats a token count compactly (e.g. 1234 → "1.2k").
func human(n int) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%.1fk", float64(n)/1_000)
	default:
		return fmt.Sprintf("%d", n)
	}
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
