package tui

import (
	"fmt"
	"strings"
	"time"

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
	meta := th.headerMeta.Render(" · sandbox "+sandbox+" · think ") +
		th.headerKey.Render(think)
	model := th.headerKey.Render(modelName)

	left := logo + "   " + model + meta

	status := m.statusBadge()
	tokens := th.headerMeta.Render(fmt.Sprintf("∑ ⌂ %s · ⎇ %s",
		human(m.sessCtxTok), human(m.sessOutTok)))
	right := tokens + "   " + status

	gap := m.width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		gap = 1
	}
	bar := left + strings.Repeat(" ", gap) + right
	return bar + "\n" + m.rule()
}

// rule returns a full-width gradient hairline, cached per width.
func (m *Model) rule() string {
	w := max(m.width, 1)
	if m.gradRule == "" || m.gradRuleW != w {
		m.gradRule = gradient(strings.Repeat("─", w), gradFrom, gradTo)
		m.gradRuleW = w
	}
	return m.gradRule
}

// engagingVerbs cycle while the model is reasoning, so the UI feels alive.
var engagingVerbs = []string{
	"thinking", "reasoning it through", "connecting the dots",
	"consulting the model", "weighing the options", "planning the approach",
}

func (m *Model) statusBadge() string {
	th := m.th
	switch {
	case m.disconn:
		return lipgloss.NewStyle().Foreground(colRed).Render("● disconnected")
	case m.approval != nil:
		return th.statusBusy.Render("⚠ approval required")
	case m.busy:
		label := m.status
		switch {
		case m.lastTool != "":
			label = th.toolIcon.Render(toolGlyph(m.lastTool)) + " " +
				th.statusBusy.Render(m.lastTool)
		case label == "thinking", label == "":
			// Cycle engaging verbs roughly every ~1.8s of the run.
			idx := int(time.Since(m.runStart)/(1800*time.Millisecond)) % len(engagingVerbs)
			label = th.statusBusy.Render(engagingVerbs[idx])
		default:
			label = th.statusBusy.Render(label)
		}
		el := ""
		if e := m.elapsed(); e != "" {
			el = th.headerMeta.Render(" · " + e)
		}
		return th.spinner.Render(m.sp.View()) + " " + label + el
	default:
		return th.statusReady.Render("● " + m.status)
	}
}

// ── transcript ───────────────────────────────────────────────────────────

// refresh rebuilds the viewport content and scrolls to the latest output.
// While busy it follows the stream; when idle it preserves the reader's
// position unless they were already at the bottom.
func (m *Model) refresh() {
	if !m.ready {
		return
	}
	stick := m.busy || m.vp.AtBottom()
	m.vp.SetContent(m.conversation())
	if stick {
		m.vp.GotoBottom()
	}
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
			icon = th.spinner.Render(m.sp.View())
		default:
			icon = th.stepRun.Render("▸")
		}
		glyph := th.toolIcon.Render(toolGlyph(s.name))
		line := icon + " " + glyph + " " + th.stepName.Render(s.name)
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
	box := m.th.inputBox.Width(m.width - 2).Render(m.ta.View())
	if m.ac.open {
		return m.acPopup() + "\n" + box
	}
	return box
}

// acPopup renders the @-reference completion box. Its height must match
// autocomplete.height() so the layout math stays exact.
func (m *Model) acPopup() string {
	th := m.th
	// Inner content width inside the box (border + padding = 4 columns).
	innerW := m.width - 6
	if innerW < 12 {
		innerW = 12
	}

	title := th.acTitle.Render("@ reference")
	if hint := "  ↑↓ select · ⇥ insert · esc cancel"; 11+lipgloss.Width(hint) <= innerW {
		title += th.acDim.Render(hint)
	}

	var rows []string
	switch {
	case m.ac.loading && len(m.ac.items) == 0:
		rows = append(rows, th.acDim.Render(m.sp.View()+" searching…"))
	case len(m.ac.items) == 0:
		rows = append(rows, th.acDim.Render("no matches for @"+m.ac.query))
	default:
		for i, it := range m.ac.items {
			// Truncate in plain text first so styled rows never wrap.
			budget := innerW - 4 // prefix(2) + icon(1) + space(1)
			lab := truncate(it.Label, budget)
			rest := budget - lipgloss.Width(lab)
			det := ""
			if it.Detail != "" && rest > 6 {
				det = th.acDetail.Render(truncate("  "+it.Detail, rest))
			}
			icon := th.acIcon.Render(resourceGlyph(it.Type))
			prefix, lbl := "  ", th.acItem.Render(lab)
			if i == m.ac.sel {
				prefix, lbl = th.acSel.Render("› "), th.acSel.Render(lab)
			}
			rows = append(rows, prefix+icon+" "+lbl+det)
		}
	}
	body := title + "\n" + strings.Join(rows, "\n")
	return th.acBox.Width(m.width - 2).Render(body)
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

	var segs []string
	if m.lastLatency > 0 {
		segs = append(segs, th.footer.Render(fmt.Sprintf("⚡ %.1fs", m.lastLatency)))
	}
	if !m.vp.AtBottom() {
		segs = append(segs, th.scroll.Render(fmt.Sprintf("↕ %d%%", int(m.vp.ScrollPercent()*100))))
	}
	right := ""
	if len(segs) > 0 {
		right = strings.Join(segs, th.footerSep.Render("  ·  ")) + "  "
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
