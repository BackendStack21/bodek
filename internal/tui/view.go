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
	body := m.vp.View()
	if m.panel != panelNone {
		body = m.renderPanel(m.width, m.vp.Height)
	}
	parts := []string{
		m.header(),
		body,
		m.inputArea(),
		m.footer(),
	}
	return strings.Join(parts, "\n")
}

// ── header ─────────────────────────────────────────────────────────────────

func (m *Model) header() string {
	th := m.th
	// The logo gradient is width-independent, so render it once and cache it
	// (like gradRule) instead of re-interpolating every frame.
	if m.logoCache == "" {
		m.logoCache = th.logo.Render(gradient("⬡ bodek", gradFrom, gradTo))
	}
	logo := m.logoCache

	think := "off"
	if m.thinkOn {
		think = "on"
	}
	modelName := m.model
	if modelName == "" {
		modelName = "default"
	}
	// Sandbox status, prominently colored: green ● when isolated, amber ▲
	// when the agent has host access.
	sandbox := m.sandboxBadge()
	meta := th.headerMeta.Render(" · think ") + th.headerKey.Render(think)
	model := th.headerKey.Render(modelName)

	left := logo + "   " + model + th.headerMeta.Render("  ·  ") + sandbox + meta

	status := m.statusBadge()
	tokens := th.headerMeta.Render(fmt.Sprintf("∑ ⌂ %s · ⎇ %s",
		human(m.sessCtxTok), human(m.sessOutTok)))
	sep := th.headerMeta.Render("  ·  ")
	buildRight := func(gauge string) string {
		if gauge != "" {
			return gauge + sep + tokens + "   " + status
		}
		return tokens + "   " + status
	}

	// Shed gauge detail under width pressure: full gauge → compact glyph+percent
	// → no gauge at all. The final gap clamp only prevents a negative pad; the
	// remaining left/tokens/status overflow (if any) is pre-existing.
	right := buildRight(m.ctxGauge(false))
	gap := m.width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		right = buildRight(m.ctxGauge(true))
		gap = m.width - lipgloss.Width(left) - lipgloss.Width(right)
	}
	if gap < 1 {
		right = buildRight("")
		gap = m.width - lipgloss.Width(left) - lipgloss.Width(right)
	}
	if gap < 1 {
		gap = 1
	}
	bar := left + strings.Repeat(" ", gap) + right
	return bar + "\n" + m.rule()
}

// ctxGauge renders the context-window usage indicator for the header right
// cluster: a pressure-tinted fill glyph, a percentage, and (when not compact)
// the used/max fraction. Usage is the last turn's contextTokens — the live
// window fill, which drops again after odek trims history — never the
// cumulative session total, which only grows. Returns "" when the model's
// budget is unknown so the header silently keeps its prior shape rather than
// guessing.
func (m *Model) ctxGauge(compact bool) string {
	if m.maxContext <= 0 {
		return ""
	}
	ratio := float64(m.winCtxTok) / float64(m.maxContext)
	if ratio < 0 {
		ratio = 0
	}
	if ratio > 1 {
		ratio = 1
	}
	pct := fmt.Sprintf("%d%%", int(ratio*100+0.5))
	g := m.gaugeColor(ratio).Render(gaugeGlyph(ratio)) + " " + m.th.headerMeta.Render(pct)
	if !compact {
		// used via human() so it matches the adjacent "∑ ⌂ …" summary; max via
		// humanCtx() for a tidy whole-k budget.
		g += " " + m.th.headerMeta.Render(human(m.winCtxTok)+"/"+humanCtx(m.maxContext))
	}
	return g
}

// gaugeColor tints the fill glyph by context pressure: green under 75%, amber
// to 90%, red above.
func (m *Model) gaugeColor(ratio float64) lipgloss.Style {
	switch {
	case ratio >= 0.90:
		return m.th.gaugeHot
	case ratio >= 0.75:
		return m.th.gaugeWarn
	default:
		return m.th.gaugeOK
	}
}

// sandboxBadge renders the agent's isolation state with the monochrome glyph
// vocabulary (width-stable, unlike emoji): a green ● when sandboxed, an amber ▲
// when it has host access. Shared by the header and the /stats card so the two
// never drift.
func (m *Model) sandboxBadge() string {
	if m.sandbox {
		return m.th.badgeOK.Render("● sandboxed")
	}
	return m.th.badgeWarn.Render("▲ host access")
}

// gaugeGlyph mirrors the gaugeColor bands (0.75 / 0.90) so fill and hue tell
// the same story: open while comfortable, half when warm, full when hot.
func gaugeGlyph(r float64) string {
	switch {
	case r >= 0.90:
		return "●"
	case r >= 0.75:
		return "◐"
	default:
		return "○"
	}
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

func (m *Model) statusBadge() string {
	th := m.th
	switch {
	case m.disconn:
		return th.badgeDanger.Render("● disconnected")
	case m.approval != nil:
		return th.statusBusy.Render("⚠ approval required")
	case m.busy:
		var label string
		switch {
		case m.lastTool != "":
			// Context-aware message derived from the running tool + its args.
			label = toolProgress(m.lastTool, m.lastArg)
		case m.status == "responding":
			label = "💬 composing the reply"
		default: // thinking / pre-tool: cycle phrases so a pause feels alive.
			idx := int(time.Since(m.runStart)/(1500*time.Millisecond)) % len(thinkingPhrases)
			label = thinkingPhrases[idx]
		}
		el := ""
		if e := m.elapsed(); e != "" {
			el = th.headerMeta.Render(" · " + e)
		}
		return th.spinner.Render(m.sp.View()) + " " + th.statusBusy.Render(label) + el
	default:
		return th.statusReady.Render("● " + m.status)
	}
}

// ── transcript ───────────────────────────────────────────────────────────

// refresh rebuilds the viewport content and scrolls to the latest output only
// when the reader is already at the bottom — a run in progress must not yank
// them away from scrollback they are reading.
func (m *Model) refresh() {
	if !m.ready {
		return
	}
	stick := m.vp.AtBottom()
	m.vp.SetContent(m.conversation())
	if stick {
		m.vp.GotoBottom()
	}
}

func (m *Model) conversation() string {
	if len(m.msgs) == 0 {
		m.stepLineIndex = nil
		return welcome(m.th, m.vp.Width, m.opts.CWD)
	}
	// Everything before the in-flight streaming message is stable, so cache its
	// rendering (convPrefix) and re-render only the tail — a spinner tick would
	// otherwise re-style every finalized message each frame. The cache is
	// invalidated when the message list is replaced wholesale (clear, resume)
	// or re-rendered (resize).
	tail := len(m.msgs)
	if i := m.cur(); i >= 0 {
		tail = i
	}
	var refs []stepRef
	lineOffset := 0
	if m.convCount != tail {
		blocks := make([]string, 0, tail)
		for i := 0; i < tail; i++ {
			s, r := m.renderMessage(m.msgs[i], i, lineOffset)
			blocks = append(blocks, s)
			refs = append(refs, r...)
			lineOffset += lineCount(s) + 1 // blank separator between blocks
		}
		m.convPrefix = strings.Join(blocks, "\n\n")
		m.convPrefixRefs = refs
		m.convCount = tail
	} else {
		refs = append(refs, m.convPrefixRefs...)
		if m.convPrefix != "" {
			lineOffset = lineCount(m.convPrefix) + 1
		}
	}
	blocks := make([]string, 0, len(m.msgs)-tail+2)
	if m.convPrefix != "" {
		blocks = append(blocks, m.convPrefix)
	}
	for i := tail; i < len(m.msgs); i++ {
		s, r := m.renderMessage(m.msgs[i], i, lineOffset)
		blocks = append(blocks, s)
		refs = append(refs, r...)
		lineOffset += lineCount(s) + 1
	}
	if len(m.notices) > 0 {
		if notes := m.renderNotices(); notes != "" {
			blocks = append(blocks, notes)
		}
	}
	m.stepLineIndex = refs
	return strings.Join(blocks, "\n\n")
}

func (m *Model) renderMessage(msg message, msgIdx, lineOffset int) (string, []stepRef) {
	th := m.th
	// Pre-styled cards (e.g. /stats) render verbatim — no label, no bar, and
	// never re-rendered through glamour.
	if msg.raw {
		return msg.content, nil
	}
	switch msg.role {
	case roleUser:
		label := th.userLabel.Render("❯ you")
		body := th.userBar.Width(m.vp.Width - 2).Render(msg.content)
		return label + "\n" + body, nil

	case roleNote:
		return th.sysBar.Width(m.vp.Width - 2).Render(msg.content), nil

	default: // assistant
		label := th.asstLabel.Render("⬡ odek")
		// Resolve the markdown body (finalized or streaming) first.
		content := msg.content
		if !msg.streaming && msg.rendered != "" {
			content = msg.rendered
		}
		if strings.TrimSpace(content) == "" && msg.streaming {
			content = th.thinkStyle.Render(m.sp.View() + " thinking…")
		}
		// Compose the turn body from the chronological timeline: reasoning
		// blocks and tool steps interleaved in arrival order.
		items := msg.items
		if len(items) == 0 {
			// Messages without a timeline (hand-built or resumed transcripts)
			// fall back to the old fixed order: thinking, then steps.
			if strings.TrimSpace(msg.thinking) != "" {
				items = append(items, turnItem{thinking: true, text: msg.thinking})
			}
			for i := range msg.steps {
				items = append(items, turnItem{stepIdx: i})
			}
		}
		var b strings.Builder
		var refs []stepRef
		line := lineOffset + 1 // body starts one line below the label
		for _, it := range items {
			if it.thinking {
				t := strings.TrimSpace(it.text)
				if t == "" {
					continue
				}
				excerpt := th.thinkStyle.Width(max(m.vp.Width-4, 8)).Render("… " + collapse(t))
				if b.Len() > 0 {
					b.WriteString("\n")
				}
				b.WriteString(excerpt)
				line += lineCount(excerpt)
				continue
			}
			if it.stepIdx < 0 || it.stepIdx >= len(msg.steps) {
				continue
			}
			block, ref, n := m.renderStep(msg.steps[it.stepIdx], msg.streaming, msgIdx, it.stepIdx, line)
			if b.Len() > 0 {
				b.WriteString("\n")
			}
			b.WriteString(block)
			refs = append(refs, ref)
			line += n
		}
		if strings.TrimSpace(content) != "" {
			if b.Len() > 0 {
				b.WriteString("\n")
			}
			b.WriteString(content)
		}
		body := th.asstBar.Width(m.vp.Width - 2).Render(strings.TrimRight(b.String(), "\n"))
		out := label + "\n" + body
		if msg.stats != nil {
			if line := m.statLine(*msg.stats); line != "" {
				out += "\n" + line
			}
		}
		return out, refs
	}
}

// lineCount returns the number of newline-terminated lines in a rendered block.
func lineCount(s string) int {
	if s == "" {
		return 0
	}
	if strings.HasSuffix(s, "\n") {
		return strings.Count(s, "\n")
	}
	return strings.Count(s, "\n") + 1
}

// statLine renders the compact telemetry row shown beneath a finalized
// assistant turn. Glyphs carry the hue; values and separators recede in faint.
// Segments self-suppress when empty and drop in priority order (tools, then
// thinking, then wall-clock); the result is then hard-clamped to the viewport
// width so the row never wraps, even when the essentials alone overflow a very
// narrow terminal.
func (m *Model) statLine(ts turnStats) string {
	th := m.th
	type seg struct {
		text string
		drop int // higher = dropped sooner under width pressure
	}
	var segs []seg
	add := func(text string, drop int) {
		if text != "" {
			segs = append(segs, seg{text, drop})
		}
	}

	// latency — always present
	add(th.statTime.Render("⚡")+th.statLine.Render(" "+fmt.Sprintf("%.1fs", ts.latency)), 0)
	// wall-clock — only when it diverges meaningfully from model latency
	if ts.wall > 0 && absSec(ts.wall.Seconds()-ts.latency) > 0.3 {
		add(th.statTime.Render("⊙")+th.statLine.Render(" "+formatDuration(ts.wall)), 3)
	}
	// context + output tokens — always present
	add(th.statCtx.Render("⌂")+th.statLine.Render(" "+human(ts.ctxTok)), 0)
	add(th.statCtx.Render("⎇")+th.statLine.Render(" "+human(ts.outTok)), 0)
	// tools — count plus the deduped glyph cluster
	if ts.toolCount > 0 {
		tools := th.statTool.Render("⚒") + th.statLine.Render(" "+fmt.Sprintf("%d", ts.toolCount))
		if len(ts.toolGlyphs) > 0 {
			tools += th.statGlyph.Render(" " + strings.Join(ts.toolGlyphs, ""))
		}
		add(tools, 1)
	}
	// thinking marker — no value
	if ts.thought {
		add(th.statThink.Render("✳"), 2)
	}

	sep := th.statSep.Render(" · ")
	render := func(keep []seg) string {
		parts := make([]string, len(keep))
		for i, s := range keep {
			parts[i] = s.text
		}
		return "  " + strings.Join(parts, sep)
	}

	limit := m.vp.Width - 2
	line := render(segs)
	// Under width pressure, drop droppable segments in priority order: tools
	// (1), then thinking (2), then wall-clock (3). drop==0 segments always stay.
	for _, maxDrop := range []int{1, 2, 3} {
		if lipgloss.Width(line) <= limit {
			break
		}
		kept := segs[:0:0]
		for _, s := range segs {
			if s.drop == 0 || s.drop > maxDrop {
				kept = append(kept, s)
			}
		}
		line = render(kept)
	}
	// Final guarantee: the non-droppable essentials can still exceed a tiny
	// viewport, so clamp ANSI-safely (a no-op when the line already fits).
	if limit > 0 && lipgloss.Width(line) > limit {
		line = lipgloss.NewStyle().MaxWidth(limit).Render(line)
	}
	return line
}

// absSec returns the absolute value of a float (seconds delta).
func absSec(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// renderStep renders one tool step as a one-line summary (expand chevron,
// status icon, tool glyph, name, arg, and — once done — its response time),
// plus its full output/logs when expanded. It returns the rendered block, the
// stepRef for mouse hit-testing (pointing at startLine), and the block's line
// count.
func (m *Model) renderStep(s step, streaming bool, msgIdx, stepIdx, startLine int) (string, stepRef, int) {
	th := m.th
	budget := m.vp.Width - 10
	if budget < 14 {
		budget = 14
	}
	detailBudget := m.vp.Width - 8
	if detailBudget < 16 {
		detailBudget = 16
	}
	// A step shows its details when toggled individually or via the global
	// Ctrl+E toggle.
	expanded := s.expanded || m.expandAll
	// Status glyph: a spinner while the call runs, then ✓ / ✗ once it lands.
	var icon string
	switch {
	case !s.done && streaming:
		icon = th.spinner.Render(m.sp.View())
	case s.done && s.isErr:
		icon = th.stepErr.Render("✗")
	case s.done:
		icon = th.stepDone.Render("✓")
	default:
		icon = th.stepRun.Render("▸")
	}
	var chevron string
	if s.done {
		if expanded {
			chevron = th.stepTree.Render("▼")
		} else {
			chevron = th.stepTree.Render("▶")
		}
	} else {
		chevron = th.stepTree.Render(" ")
	}
	head := chevron + " " + icon + " " + th.toolIcon.Render(toolGlyph(s.name)) + " " + th.stepName.Render(s.name)
	if s.subagent {
		head += th.stepArg.Render(" · sub-agent")
	}
	if s.arg != "" {
		head += th.stepArg.Render("  " + truncate(s.arg, budget))
	}
	// Response time appears only once the call lands — while running, the
	// spinner already conveys that.
	if s.done && s.dur > 0 {
		head += th.stepArg.Render("  " + formatStepDur(s.dur))
	}
	lines := []string{head}
	if expanded {
		details := append([]string{}, s.logs...)
		for _, ln := range strings.Split(s.result, "\n") {
			if c := collapse(ln); c != "" {
				details = append(details, c)
			}
		}
		if len(details) > 200 {
			details = details[:200]
			details = append(details, "… output truncated")
		}
		for i, d := range details {
			conn := "    "
			if i == 0 {
				conn = "  ⎿ "
			}
			lines = append(lines, th.stepTree.Render(conn)+th.stepRes.Render(truncate(d, detailBudget)))
		}
	}
	return strings.Join(lines, "\n"), stepRef{msgIdx: msgIdx, stepIdx: stepIdx, line: startLine}, len(lines)
}

// resultExcerpt turns sanitized tool output into a compact, blank-stripped
// preview: up to maxResultLines meaningful lines, with a "+N more lines"
// footer when the output runs longer.
func resultExcerpt(result string) []string {
	const maxResultLines = 5
	var out []string
	for _, ln := range strings.Split(result, "\n") {
		if c := collapse(ln); c != "" {
			out = append(out, c)
		}
	}
	if len(out) <= maxResultLines {
		return out
	}
	trimmed := append([]string{}, out[:maxResultLines]...)
	return append(trimmed, fmt.Sprintf("… +%d more lines", len(out)-maxResultLines))
}

func (m *Model) renderNotices() string {
	th := m.th
	now := time.Now()
	lines := make([]string, 0, len(m.notices))
	for i, n := range m.notices {
		if exp := m.noticeExp[i]; !exp.IsZero() && !now.Before(exp) {
			continue // expired transient, pending the next sweep
		}
		lines = append(lines, th.noticeStyle.Render("· "+n))
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

	label, hint := "@ attach file", "  ↑↓ select · ⇥ insert · esc cancel"
	if m.ac.mode == acCmd {
		label, hint = "commands", "  ↑↓ select · ⇥ complete · ⏎ run · esc cancel"
	}
	title := th.acTitle.Render(label)
	if lipgloss.Width(label)+lipgloss.Width(hint) <= innerW {
		title += th.acDim.Render(hint)
	}

	var rows []string
	switch {
	case m.ac.loading && len(m.ac.items) == 0:
		rows = append(rows, th.acDim.Render(m.sp.View()+" searching…"))
	case len(m.ac.items) == 0:
		noun := "files"
		if m.ac.mode == acCmd {
			noun = "commands"
		}
		rows = append(rows, th.acDim.Render("no matching "+noun))
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
	if a.IsOperation {
		head += th.apprBody.Render(" · ") + th.opChip.Render("⚙ operation")
	}
	if a.Untrusted {
		head += th.apprBody.Render(" · ") + th.untrustedTag.Render("⚠ untrusted")
	}

	target := a.Command
	if a.Name != "" {
		target = a.Name + ": " + target
	}
	cmd := th.apprBody.Render(truncate(collapse(target), m.width-8))

	desc := ""
	if a.Description != "" {
		desc = th.noticeStyle.Render(truncate(collapse(a.Description), m.width-8)) + "\n"
	}

	keys := th.apprKey.Render("a") + th.apprBody.Render(" approve   ") +
		th.apprKey.Render("d") + th.apprBody.Render(" deny")
	if a.AllowTrust {
		keys += th.apprBody.Render("   ") + th.apprKey.Render("t") + th.apprBody.Render(" trust class")
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
	if m.panel == panelSessions {
		return m.panelFooter(
			th.footer.Render("↑↓ select"),
			th.footer.Render("⏎ resume"),
			th.footerDanger.Render("d delete"), // destructive — tinted to telegraph it
			th.footer.Render("esc close"),
		)
	}
	if m.panel == panelModels {
		return m.panelFooter(
			th.footer.Render("↑↓ select"),
			th.footer.Render("⏎ use"),
			th.footer.Render("esc close"),
		)
	}
	// The status bar carries no static key cheatsheet (the welcome splash and
	// /help cover that) — only the live run state: a cancel hint while busy on
	// the left, and latency / scroll position on the right.
	left := ""
	if m.busy {
		left = "  " + th.footerKey.Render("esc") + th.footer.Render(" cancel")
	}

	var segs []string
	if m.lastLatency > 0 {
		seg := th.statTime.Render("⚡") + th.footer.Render(fmt.Sprintf(" %.1fs", m.lastLatency))
		if n := len(m.turnStats); n > 0 {
			seg += th.footerSep.Render(" · ") + th.statCtx.Render("⎇") +
				th.footer.Render(" "+human(m.turnStats[n-1].outTok))
		}
		segs = append(segs, seg)
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

// panelFooter joins pre-styled hint segments for an open panel (pre-styled so
// destructive hints can carry the danger tint).
func (m *Model) panelFooter(hints ...string) string {
	return "  " + strings.Join(hints, m.th.footerSep.Render("  ·  "))
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
	case n >= 999_500: // promote to M once one-decimal k rounding would reach 1000.0k
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%.1fk", float64(n)/1_000)
	default:
		return fmt.Sprintf("%d", n)
	}
}

// humanCtx formats a context-window count to whole-k with no trailing ".0", so
// the gauge fraction stays tidy and aligned (e.g. 48200 → "48k", 128000 →
// "128k", 1_500_000 → "1.5M").
func humanCtx(n int) string {
	switch {
	case n >= 999_500: // promote to M once whole-k rounding would reach 1000k
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%dk", (n+500)/1_000)
	default:
		return fmt.Sprintf("%d", n)
	}
}
