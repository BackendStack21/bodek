package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// View composes the full screen: header, scrollable transcript, the busy
// status line (while a turn runs), input (or approval prompt), and footer.
func (m *Model) View() string {
	if !m.ready {
		return "\n  starting bodek…"
	}
	if m.plain {
		return m.plainView()
	}
	body := m.vp.View()
	if m.panel != panelNone {
		body = m.renderPanel(m.width, m.vp.Height)
	} else if m.popover {
		body = m.popoverView(m.width, m.vp.Height)
	}
	parts := []string{m.header(), body}
	if sl := m.statusLine(); sl != "" {
		parts = append(parts, sl)
	}
	if s := m.queueStripView(); s != "" {
		parts = append(parts, s)
	}
	parts = append(parts, m.inputArea(), m.footer())
	return strings.Join(parts, "\n")
}

// plainView composes linear mode's bottom chrome: status line, capped
// panel/popover, input, footer. The transcript itself lives in the
// terminal's scrollback, printed line-by-line as events arrive (plain.go).
func (m *Model) plainView() string {
	var parts []string
	if sl := m.statusLine(); sl != "" {
		parts = append(parts, sl)
	}
	if m.panel != panelNone {
		parts = append(parts, m.renderPanel(m.width, plainPanelMax))
	} else if m.popover {
		parts = append(parts, m.popoverView(m.width, plainPanelMax))
	}
	if s := m.queueStripView(); s != "" {
		parts = append(parts, s)
	}
	parts = append(parts, m.inputArea(), m.footer())
	return strings.Join(parts, "\n")
}

// ── header ─────────────────────────────────────────────────────────────────

func (m *Model) header() string {
	th := m.th
	// The logo gradient is width-independent, so render it once and cache it
	// (like gradRule) instead of re-interpolating every frame.
	if m.logoCache == "" {
		m.logoCache = th.logo.Render(gradient("⬡ bodek", th.grad[0], th.grad[1]))
	}
	logo := m.logoCache
	// bodek's own version rides next to the logo; bare numbers get a v prefix
	// to match the "odek vX.Y.Z" segment.
	if v := m.bodekVersion; v != "" {
		if v[0] >= '0' && v[0] <= '9' {
			v = "v" + v
		}
		logo += " " + th.headerMeta.Render(v)
	}

	modelName := m.model
	if modelName == "" {
		modelName = "default"
	}
	// The ⚡ badge marks a server streaming token/thinking deltas live (the
	// same marker the WebUI's top bar carries).
	if m.serverStream {
		modelName = "⚡ " + modelName
	}

	// The left cluster, split around the model name: its truncation budget is
	// computed against everything else once the segments are known.
	head := logo + "   "
	tail := ""
	// A subtle, persistent marker while extended thinking is enabled — the
	// same ✳ glyph the per-turn stat line uses to flag a thought turn.
	if m.thinkOn {
		tail += th.headerMeta.Render(" · ✳ think")
	}
	if m.odekVersion != "" {
		tail += th.headerMeta.Render(" · odek ") + th.headerKey.Render(m.odekVersion)
	}
	// Sandbox status, prominently colored: green ● when isolated, amber ▲
	// when the agent has host access.
	tail += th.headerMeta.Render("  ·  ") + m.sandboxBadge()
	// Session spend rides the left cluster; hidden until odek reports both
	// token prices (never show a guessed $0).
	if inPrice, outPrice := m.prices(); inPrice > 0 && outPrice > 0 {
		tail += th.headerMeta.Render("  ·  ") + th.headerKey.Render(formatUSD(costUSD(m.sessCtxTok, m.sessOutTok, inPrice, outPrice)))
	}

	status := m.statusBadge()
	// The gauge is the header's sole token metric — session totals live in
	// /stats and the per-turn stat line, so a fresh session never flashes
	// placeholder zeros up here.
	buildRight := func(gauge string) string {
		right := gauge
		if status != "" { // empty while busy — progress rides the status line
			right += "   " + status
		}
		return right
	}

	// The model name carries the slack: truncate it (with ellipsis) to what
	// the bar can hold against the most-shed right cluster, so a long model
	// ID can never push the header past headerHeight lines.
	budget := m.width - lipgloss.Width(head) - lipgloss.Width(tail) - lipgloss.Width(buildRight("")) - 1 // gap
	if budget < 4 {
		budget = 4 // keep a few chars; the clamp below covers absurd widths
	}
	left := head + th.headerKey.Render(truncate(modelName, budget)) + tail

	// Shed gauge detail under width pressure: full gauge → compact glyph+percent
	// → no gauge at all. The final gap clamp only prevents a negative pad.
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
	// Absolute guarantee: relayout and the mouse offset math assume the
	// header occupies exactly headerHeight rows, so clamp any residual
	// overflow ANSI-safely to one line (a no-op whenever the bar fits).
	if m.width > 0 && lipgloss.Width(bar) > m.width {
		bar = lipgloss.NewStyle().MaxWidth(m.width).Render(bar)
	}
	return bar + "\n" + m.rule()
}

// ctxGauge renders the context-window usage indicator for the header right
// cluster: a pressure-tinted fill glyph, a percentage, and (when not compact)
// the used/max fraction. Usage is the last request's prompt size — the live
// window fill, derived from deltas of odek's cumulative-per-run contextTokens
// reports, so it drops again after odek trims history — never the cumulative
// session total, which only grows. Returns "" when the model's budget is
// unknown so the header silently keeps its prior shape rather than guessing.
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
	// Color carries state, dim carries magnitude: bar and percent share the
	// pressure tint, raw token counts recede. The "ctx" label anchors the
	// WebUI's documented `ctx ▓▓▓░░ 40%` idiom.
	g := m.th.headerMeta.Render("ctx ") +
		m.gaugeColor(ratio).Render(gaugeGlyph(ratio)+" "+pct)
	if !compact {
		// used via human(); max via humanCtx() for a tidy whole-k budget.
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
// gaugeGlyph renders the context gauge as a five-cell fill bar with
// eighth-block sub-cell precision — the WebUI's documented `ctx ▓▓▓░░ 40%`
// idiom, sharpened: full cells are █, the leading edge rounds to the nearest
// eighth block (▏▎▍▌▋▊▉), the remainder stays ░.
func gaugeGlyph(r float64) string {
	if r < 0 {
		r = 0
	}
	if r > 1 {
		r = 1
	}
	const cells = 5
	v := r * cells
	full := int(v)
	bar := strings.Repeat("█", full)
	if full < cells {
		// eighths rounds the leading cell's fill to the nearest eighth;
		// 0x2588 is █ and each codepoint down to 0x258F (▏) is one eighth
		// lighter. A rounding to 8 lands back on █; a rounding to 0 stays
		// ░ — the cell always renders, five cells total.
		eighths := int((v-float64(full))*8 + 0.5)
		if eighths > 0 {
			bar += string(rune(0x2590 - eighths))
		} else {
			bar += "░"
		}
		bar += strings.Repeat("░", cells-full-1)
	}
	return bar
}

// rule returns a full-width gradient hairline, cached per width.
func (m *Model) rule() string {
	w := max(m.width, 1)
	if m.gradRule == "" || m.gradRuleW != w {
		m.gradRule = gradient(strings.Repeat("─", w), m.th.grad[0], m.th.grad[1])
		m.gradRuleW = w
	}
	return m.gradRule
}

// statusBadge is the header's session-state segment. In-flight progress no
// longer lives here — it renders on the status line above the input, next to
// the user's last message — so a running turn leaves the segment empty.
func (m *Model) statusBadge() string {
	th := m.th
	switch {
	case m.disconn:
		if strings.HasPrefix(m.status, "reconnecting") {
			return th.statusBusy.Render("● reconnecting…")
		}
		return th.badgeDanger.Render("● disconnected")
	case m.curApproval() != nil:
		if n := len(m.approvals); n > 1 {
			return th.statusBusy.Render(fmt.Sprintf("⚠ approval %d/%d", 1, n))
		}
		return th.statusBusy.Render("⚠ approval required")
	case m.busy:
		return ""
	default:
		return th.statusReady.Render("● " + m.status)
	}
}

// statusLine renders the in-flight progress indicator — spinner, a
// context-aware label for the running tool (or the thinking/composing phase),
// and the elapsed timer — on its own row directly above the input box. That
// is right below the user's last message, where the eyes already are after
// submitting a prompt; the header's top-right badge was too far away. Returns
// "" when the row is hidden so it costs no height.
func (m *Model) statusLine() string {
	if !m.statusLineVisible() {
		return ""
	}
	th := m.th
	var label string
	switch {
	case m.lastTool != "":
		// Context-aware message derived from the running tool + its args.
		label = toolProgress(m.lastTool, m.lastArg)
	case m.status == "responding":
		label = "💬 composing the reply"
	default: // thinking / pre-tool
		label = "🧠 thinking"
	}
	el := ""
	if e := m.elapsed(); e != "" {
		el = th.headerMeta.Render(" · " + e)
	}
	// Held prompts ride the same row: mid-turn ⏎ queues invisibly, so the
	// count shows where the eyes already are (mirrors the footer indicator).
	q := ""
	if n := len(m.queue); n > 0 {
		q = th.acDetail.Render(fmt.Sprintf(" · %d queued", n))
	}
	// Live plan strip: rides the same row,
	// silent unless a run is active AND a plan exists — absence costs zero
	// pixels. Bounded to a short label so small terminals keep the row sane.
	strip := ""
	if s := m.planStripLabel(); s != "" {
		strip = th.acDetail.Render("   ▸ " + s)
	}
	// A blank row above separates the indicator from the transcript tail —
	// inputAreaHeight accounts for it so the layout math stays exact.
	return "\n" + th.spinner.Render(m.sp.View()) + " " + th.statusBusy.Render(label) + el + q + strip
}

// statusLineVisible reports whether the status line occupies a row, keeping
// View and inputAreaHeight in agreement. While an approval panel owns the
// input area or the socket is down, the header badge carries the state and
// the row stays hidden.
func (m *Model) statusLineVisible() bool {
	return m.busy && m.curApproval() == nil && !m.disconn
}

// ── transcript ───────────────────────────────────────────────────────────

// streamRenderInterval is the coalescing window for high-frequency streaming
// events (tokens, thinking): instead of rebuilding the viewport — which
// re-runs glamour on the streaming tail — per event, they share one rebuild.
const streamRenderInterval = 80 * time.Millisecond

// renderFlushMsg fires streamRenderInterval after the first coalesced
// streaming event; a stale seq means a newer flush superseded it.
type renderFlushMsg struct {
	seq int
}

// queueRender schedules the coalesced streaming-render flush, or returns nil
// when one is already pending (all events since then share that flush).
func (m *Model) queueRender() tea.Cmd {
	if m.renderPending {
		return nil
	}
	m.renderPending = true
	m.renderSeq++
	seq := m.renderSeq
	return tea.Tick(streamRenderInterval, func(time.Time) tea.Msg {
		return renderFlushMsg{seq: seq}
	})
}

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
		m.turnLineIndex = nil
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
	var turns []stepRef
	var msgsIdx []stepRef
	collectTurn := func(i, line int) {
		if m.msgs[i].role == roleAsst && !m.msgs[i].raw {
			turns = append(turns, stepRef{msgIdx: i, stepIdx: -1, line: line})
		}
		msgsIdx = append(msgsIdx, stepRef{msgIdx: i, stepIdx: -1, line: line})
	}
	lineOffset := 0
	if m.convCount != tail {
		blocks := make([]string, 0, tail)
		for i := 0; i < tail; i++ {
			collectTurn(i, lineOffset)
			s, r := m.renderMessage(m.msgs[i], i, lineOffset)
			blocks = append(blocks, s)
			refs = append(refs, r...)
			lineOffset += lineCount(s) + 1 // blank separator between blocks
		}
		m.convPrefix = strings.Join(blocks, "\n\n")
		m.convPrefixRefs = refs
		m.convPrefixTurn = turns
		m.convPrefixMsgs = msgsIdx
		m.convCount = tail
	} else {
		refs = append(refs, m.convPrefixRefs...)
		turns = append(turns, m.convPrefixTurn...)
		msgsIdx = append(msgsIdx, m.convPrefixMsgs...)
		if m.convPrefix != "" {
			lineOffset = lineCount(m.convPrefix) + 1
		}
	}
	blocks := make([]string, 0, len(m.msgs)-tail+2)
	if m.convPrefix != "" {
		blocks = append(blocks, m.convPrefix)
	}
	for i := tail; i < len(m.msgs); i++ {
		if emptyStreamingTurn(m.msgs[i]) {
			// Nothing to show yet: the top-bar spinner is the sole
			// progress signal — no bare odek block, no placeholder.
			continue
		}
		collectTurn(i, lineOffset)
		s, r := m.renderMessage(m.msgs[i], i, lineOffset)
		blocks = append(blocks, s)
		refs = append(refs, r...)
		lineOffset += lineCount(s) + 1
	}
	m.msgLineIndex = msgsIdx
	if len(m.notices) > 0 {
		if notes := m.renderNotices(); notes != "" {
			blocks = append(blocks, notes)
		}
	}
	m.stepLineIndex = refs
	m.turnLineIndex = turns
	return strings.Join(blocks, "\n\n")
}

// emptyStreamingTurn reports whether a message is an in-flight assistant
// turn without any visible content yet — no tokens, reasoning, or steps.
func emptyStreamingTurn(msg message) bool {
	if msg.role != roleAsst || !msg.streaming {
		return false
	}
	if strings.TrimSpace(msg.content) != "" || strings.TrimSpace(msg.thinking) != "" || len(msg.steps) > 0 {
		return false
	}
	for _, it := range msg.items {
		if !it.thinking || strings.TrimSpace(it.text) != "" {
			return false
		}
	}
	return true
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
		if !msg.sentAt.IsZero() {
			label += th.acDetail.Render(" · " + ago(msg.sentAt))
		}
		return label + "\n" + th.userBar.Render(msg.content), nil

	case roleNote:
		return th.sysBar.Width(m.vp.Width - 2).Render(msg.content), nil

	default: // assistant
		// The turn head carries the telemetry (WebUI parity: what a turn cost
		// reads before its content, so long sessions scan top-down). The
		// segments shed in priority order under width pressure, exactly like
		// the old foot line.
		label := th.asstLabel.Render("⬡ odek")
		if msg.stats != nil {
			limit := m.vp.Width - lipgloss.Width(label) - 4
			if s := m.joinStatSegs(m.statSegments(*msg.stats), limit); s != "" {
				label += "  " + s
			}
		}
		if msg.collapsed {
			return label + "\n" + th.statsDim.Render(m.collapseSummary(msg)), nil
		}
		// Resolve the markdown body for messages without timeline replies
		// (hand-built or resumed transcripts): finalized turns show the
		// cached glamour render.
		content := msg.content
		if !msg.streaming && msg.rendered != "" {
			content = msg.rendered
		}
		// Compose the turn body from the chronological timeline: reasoning
		// blocks, tool steps, and reply segments interleaved in arrival
		// order — each think→reply pair renders independently.
		items := msg.items
		if len(items) == 0 {
			// Messages without a timeline (hand-built) fall back to the old
			// fixed order: thinking, steps, then the reply as one card.
			if strings.TrimSpace(msg.thinking) != "" {
				items = append(items, turnItem{thinking: true, text: msg.thinking})
			}
			for i := range msg.steps {
				items = append(items, turnItem{stepIdx: i})
			}
		}
		var lines []string // turn-body rows below the label, in render order
		var refs []stepRef
		prevCard := false    // previous emitted block was an answer card
		var replied []string // reply texts emitted from the timeline
		// addBlock stacks one rendered block onto the body. Work stays
		// tightly packed under the previous work item; any block touching a
		// card is separated by a blank row so each raised card reads as its
		// own unit. Returns the row the block starts at.
		addBlock := func(block string, card bool) int {
			if len(lines) > 0 && (card || prevCard) {
				lines = append(lines, "")
			}
			start := lineOffset + 1 + len(lines)
			lines = append(lines, strings.Split(strings.TrimRight(block, "\n"), "\n")...)
			prevCard = card
			return start
		}
		for it := range items {
			if items[it].thinking {
				t := strings.TrimSpace(items[it].text)
				if t == "" {
					continue
				}
				// Accordion: the live stream renders the full block
				// (auto-follow while its turn runs); finalized turns show a
				// capped excerpt unless the user opened the block (or ^E).
				body := collapse(capThinkingText(t, maxThinkingLen))
				full := msg.streaming || items[it].open || m.expandAll
				if full {
					body = t
				}
				excerpt := th.thinkStyle.Width(max(m.vp.Width-4, 8)).Render("… " + body)
				addBlock(th.asstWork.Render(excerpt), false)
				continue
			}
			if items[it].reply {
				t := items[it].text
				if strings.TrimSpace(t) == "" {
					continue
				}
				body := t
				if !msg.streaming && items[it].rendered != "" {
					body = items[it].rendered
				}
				card, _ := m.answerCardBody(body)
				addBlock(card, true)
				replied = append(replied, t)
				continue
			}
			if items[it].stepIdx < 0 || items[it].stepIdx >= len(msg.steps) {
				continue
			}
			block, _, _ := m.renderStep(msg.steps[items[it].stepIdx], msg.streaming, msgIdx, items[it].stepIdx, 0)
			start := addBlock(th.asstWork.Render(block), false)
			refs = append(refs, stepRef{msgIdx: msgIdx, stepIdx: items[it].stepIdx, line: start})
		}
		// Residual prose: hand-built messages carry their reply only in
		// msg.content (or msg.rendered) — render it as one trailing card.
		// Live and replayed turns keep the blob in sync with the timeline
		// (appendReply), so the per-cycle cards already carry everything.
		trail := content
		if len(replied) > 0 && msg.content == strings.Join(replied, "\n\n") {
			trail = ""
		}
		if strings.TrimSpace(trail) != "" {
			card, _ := m.answerCardBody(trail)
			addBlock(card, true)
		}
		var out strings.Builder
		out.WriteString(label)
		if len(lines) > 0 {
			out.WriteString("\n")
			out.WriteString(strings.Join(lines, "\n"))
		}
		return out.String(), refs
	}
}

// answerCardBody styles one reply segment as its raised card — the
// deliverable of a think→reply cycle, visually distinct from the dimmed,
// indented work around it. Glamour wraps at vp-6, so the card's padding
// still fits; high-contrast skips the surface entirely. Returns the styled
// card and its line count.
func (m *Model) answerCardBody(body string) (string, int) {
	if m.th.answerCard.GetBackground() == nil {
		return body, lineCount(body)
	}
	// Glamour resets styling after each span; without re-asserting the
	// surface after every reset, the text would sit on the terminal's own
	// background instead of the card.
	card := m.th.answerCard.Width(m.vp.Width - 2)
	styled := card.Render(weaveSurface(body, surfaceSGR(m.th.answerCard)))
	return styled, lineCount(styled)
}

// collapseSummary describes what a folded turn card hides, so the collapsed
// form stays informative: steps, reasoning, and a reply preview.
func (m *Model) collapseSummary(msg message) string {
	parts := []string{"⋯ collapsed"}
	if n := len(msg.steps); n > 0 {
		parts = append(parts, fmt.Sprintf("%d tool steps", n))
	}
	if strings.TrimSpace(msg.thinking) != "" {
		parts = append(parts, "reasoning")
	}
	if c := strings.TrimSpace(msg.content); c != "" {
		parts = append(parts, "reply: "+truncate(collapse(c), 60))
	}
	return strings.Join(parts, " · ")
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
// statSeg is one telemetry segment of a turn head; drop orders which segments
// shed first under width pressure (higher goes sooner).
type statSeg struct {
	text string
	drop int
}

// statSegments builds the per-turn telemetry: latency, wall-clock, tokens,
// tools, cache activity, cost, thinking marker. Segments self-suppress when
// empty.
func (m *Model) statSegments(ts turnStats) []statSeg {
	th := m.th
	var segs []statSeg
	add := func(text string, drop int) {
		if text != "" {
			segs = append(segs, statSeg{text, drop})
		}
	}

	// latency — always present
	add(th.statTime.Render("⚡")+th.statLine.Render(" "+fmt.Sprintf("%.1fs", ts.latency)), 0)
	// wall-clock lives on the /stats card — the head stays quiet
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
	// provider cache activity as one total — the breakdown lives on the
	// stats card; the head reports scale, not bookkeeping.
	if total := ts.cacheWrite + ts.cacheRead + ts.cachedTok; total > 0 {
		add(th.statCtx.Render("⛁")+th.statLine.Render(" "+human(total)), 2)
	}
	// turn cost — only when odek has token prices configured (both must be
	// non-zero, mirroring odek's own cost-enforcement gate)
	if inPrice, outPrice := m.prices(); inPrice > 0 && outPrice > 0 {
		add(th.statCtx.Render("$")+th.statLine.Render(" "+formatUSD(costUSD(ts.ctxTok, ts.outTok, inPrice, outPrice))), 2)
	}
	// thinking marker — no value
	if ts.thought {
		add(th.statThink.Render("✳"), 2)
	}
	return segs
}

// joinStatSegs assembles segments with separators, dropping droppable
// segments in priority order until the line fits limit columns, then
// clamping. Pure layout helper shared by the turn head and any statline use.
func (m *Model) joinStatSegs(segs []statSeg, limit int) string {
	th := m.th
	sep := th.statSep.Render(" · ")
	render := func(keep []statSeg) string {
		parts := make([]string, len(keep))
		for i, s := range keep {
			parts[i] = s.text
		}
		return strings.Join(parts, sep)
	}

	line := render(segs)
	// Under width pressure, drop droppable segments in priority order: tools
	// (1), then cost/cache/thinking (2), then wall-clock (3). drop==0 stays.
	for _, maxDrop := range []int{1, 2, 3} {
		if limit > 0 && lipgloss.Width(line) <= limit {
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

// statLine renders the telemetry row for a turn — the pre-head foot line,
// kept as the turn head's segment source and for callers that want the row
// alone.
func (m *Model) statLine(ts turnStats) string {
	return m.joinStatSegs(m.statSegments(ts), m.vp.Width-2)
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
	// Floors stay at a few chars so truncation genuinely shrinks with the
	// viewport instead of overflowing tiny widths (truncate handles the rest).
	detailBudget := max(m.vp.Width-8, 4)
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
	// Chevron: the expand affordance — shown on running steps too, since they
	// toggle just like finished ones.
	chevron := th.stepTree.Render("▶")
	if expanded {
		chevron = th.stepTree.Render("▼")
	}
	left := chevron + " " + icon + " " + th.toolIcon.Render(toolGlyph(s.name)) + " " + th.stepName.Render(s.name)
	if s.subagent {
		left += th.stepArg.Render(" · sub-agent")
		if r := agentRollup(&s); r != "" {
			left += th.stepArg.Render(" · " + r)
		}
	}
	// Right rail: the typed chip (diffstat / test verdict), right-aligned.
	// Tool execution time is internal telemetry — recorded on the step but
	// deliberately never rendered.
	right := ""
	if s.done {
		if chip := stepHeadSuffix(s.name, s.result, th); chip != "" {
			right = chip
		}
	}
	// The left side yields to the right rail, then the pair pads to the
	// full step width; ANSI-safe width math throughout.
	rightW := lipgloss.Width(right)
	budget := max(m.vp.Width-4-rightW-2, 4)
	if s.arg != "" {
		left += th.stepArg.Render("  " + truncate(s.arg, budget-lipgloss.Width(chevron+" "+icon+" "+toolGlyph(s.name)+" "+s.name)-2))
	}
	gap := max(m.vp.Width-4-lipgloss.Width(left)-rightW, 1)
	head := left + strings.Repeat(" ", gap) + right
	lines := []string{head}
	if expanded {
		// Sub-agent logs are plain text — style and truncate here. Tool
		// output goes through the typed renderers, which return fully
		// styled, already-truncated lines (truncating styled text would
		// corrupt ANSI sequences) — append those verbatim.
		var details []string
		for _, a := range s.agents {
			line := agentCardLine(a)
			if a.failed() {
				details = append(details, th.stepErr.Render(truncate(line, detailBudget)))
				continue
			}
			details = append(details, th.stepRes.Render(truncate(line, detailBudget)))
		}
		for _, lg := range s.logs {
			if strings.TrimSpace(lg) != "" {
				details = append(details, th.stepRes.Render(truncate(lg, detailBudget)))
			}
		}
		if s.resultCard != nil {
			details = append(details, agentResultLines(m, s.resultCard, detailBudget)...)
		} else {
			details = append(details, stepDetail(s.name, s.result, m.vp.Width, th)...)
		}
		if len(details) > 200 {
			details = details[:200]
			details = append(details, th.stepArg.Render("… output truncated"))
		}
		for i, d := range details {
			conn := "    "
			if i == 0 {
				conn = "  ⎿ "
			}
			// MaxWidth clamps ANSI-safely — styled renderer output must never
			// be rune-truncated (that would cut escape sequences mid-way).
			lines = append(lines, th.stepTree.Render(conn)+
				lipgloss.NewStyle().MaxWidth(detailBudget).Render(d))
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
	if m.curApproval() != nil {
		return m.approvalPanel()
	}
	box := m.th.inputBox.Width(m.width - 2).Render(m.ta.View())
	if m.find.open {
		return m.findBar() + "\n" + box
	}
	if m.pal.open {
		return m.palPopup() + "\n" + box
	}
	if m.ac.open {
		return m.acPopup() + "\n" + box
	}
	if card := m.suggestionCard(); card != "" {
		return card + "\n" + box
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
	return m.th.apprBox.Width(m.width - 2).Render(m.approvalBody())
}

// approvalBody builds the panel's inner content: head, the command (one
// collapsed line, or the full wrapped text once expanded via tab), the
// optional description, the selectable options, and the key hints.
// inputAreaHeight counts these lines, so every line must be pre-wrapped to
// fit the box — lipgloss would otherwise reflow them and break layout math.
func (m *Model) approvalBody() string {
	th := m.th
	a := m.curApproval()
	if a == nil {
		return ""
	}
	head := th.apprHead.Render(fmt.Sprintf("⚠ approval required · risk: %s", orDash(a.Risk)))
	if n := len(m.approvals); n > 1 {
		head += th.apprBody.Render(fmt.Sprintf(" · 1 of %d queued", n))
	}
	if a.IsOperation {
		head += th.apprBody.Render(" · ") + th.opChip.Render("⚙ operation")
	}
	if a.Untrusted {
		head += th.apprBody.Render(" · ") + th.untrustedTag.Render("⚠ untrusted")
	}
	if secs := m.apprSecondsLeft(); secs > 0 {
		label := fmt.Sprintf("expires in %ds", secs)
		head += th.apprBody.Render(" · ")
		if secs <= approvalUrgentSecs {
			head += th.apprUrgent.Render(label)
		} else {
			head += th.apprBody.Render(label)
		}
	}

	target := a.Command
	if a.Name != "" {
		target = a.Name + ": " + target
	}

	budget := m.width - 8
	lines := []string{head}
	if m.apprExpanded {
		for _, ln := range wrapText(sanitize(target), budget) {
			lines = append(lines, th.apprBody.Render(ln))
		}
		if a.Description != "" {
			for _, ln := range wrapText(sanitize(a.Description), budget) {
				lines = append(lines, th.noticeStyle.Render(ln))
			}
		}
	} else {
		lines = append(lines, th.apprBody.Render(truncate(collapse(target), budget)))
		if a.Description != "" {
			lines = append(lines, th.noticeStyle.Render(truncate(collapse(a.Description), budget)))
		}
	}

	if !a.Friction {
		for i, o := range m.approvalOptions() {
			prefix, label := "  ", th.apprBody.Render(o.label)
			if i == m.apprSel {
				prefix, label = th.apprKey.Render("› "), th.apprKey.Render(o.label)
			}
			lines = append(lines, prefix+label)
		}
	} else {
		// Friction gate: no selection shortcut — the typed confirmation line
		// replaces the options and carries its own key hints.
		// inputAreaHeight measures this method, so the line count must match
		// exactly.
		lines = append(lines, m.frictionHint())
		keys := th.apprKey.Render("abc") + th.apprBody.Render(" type the word   ") +
			th.apprKey.Render("⏎") + th.apprBody.Render(" confirm   ") +
			th.apprKey.Render("tab") + th.apprBody.Render(" expand   ") +
			th.apprKey.Render("esc") + th.apprBody.Render(" deny")
		return strings.Join(append(lines, keys), "\n")
	}

	keys := th.apprKey.Render("↑↓") + th.apprBody.Render(" select   ") +
		th.apprKey.Render("⏎") + th.apprBody.Render(" confirm   ") +
		th.apprKey.Render("tab") + th.apprBody.Render(" expand   ") +
		th.apprKey.Render("esc") + th.apprBody.Render(" deny")
	return strings.Join(append(lines, keys), "\n")
}

// ── footer ─────────────────────────────────────────────────────────────────

func (m *Model) footer() string {
	th := m.th
	if a := m.curApproval(); a != nil {
		if a.Friction {
			return th.footer.Render("  type the word approve + ⏎ · esc denies")
		}
		hints := th.footerKey.Render("A") + th.footer.Render("pprove · ") +
			th.footerKey.Render("D") + th.footer.Render("eny")
		if a.AllowTrust {
			hints += " · " + th.footerKey.Render("T") + th.footer.Render("rust")
		}
		if n := len(m.approvals); n > 1 {
			hints += th.footerSep.Render(" · ") + th.footer.Render(fmt.Sprintf("%d more queued", n-1))
		}
		return "  " + hints
	}
	if m.disconn {
		hints := []string{th.footer.Render("connection closed")}
		// Retry rides ⏎ on an empty input — a character key would hijack
		// typing, and a preserved draft must keep typing normally.
		if m.opts.Reconnect != nil && m.ta.Value() == "" {
			hints = append(hints, th.footerKey.Render("⏎")+th.footer.Render(" retry"))
		}
		hints = append(hints, th.footer.Render("^C to quit"))
		return "  " + strings.Join(hints, th.footerSep.Render(" · "))
	}
	if m.panel == panelSessions {
		if m.panelEdit == panelEditSearch {
			return m.panelFooter(
				th.footer.Render("type to search (server-side)"),
				th.footerKey.Render("⏎")+th.footer.Render(" apply"),
				th.footerKey.Render("esc")+th.footer.Render(" keep current"),
			)
		}
		if m.panelEdit == panelEditRename {
			return m.panelFooter(
				th.footer.Render("type the new label"),
				th.footerKey.Render("⏎")+th.footer.Render(" rename"),
				th.footerKey.Render("esc")+th.footer.Render(" cancel"),
			)
		}
		if m.confirm == confirmSessionDelete {
			return m.panelFooter(
				th.footerDanger.Render("delete this session?"),
				th.footerKey.Render("y")+th.footerDanger.Render(" delete"),
				th.footer.Render("any other key cancels"),
			)
		}
		hints := []string{
			th.footer.Render("↑↓ select"),
			th.footer.Render("⏎ resume"),
			th.footerKey.Render("/") + th.footer.Render(" search"),
			th.footerKey.Render("p") + th.footer.Render(" pin"),
			th.footerKey.Render("r") + th.footer.Render(" rename"),
			th.footerKey.Render("e") + th.footer.Render(" export md"),
			th.footerKey.Render("E") + th.footer.Render(" json"),
			th.footerDanger.Render("d delete → y confirm"), // two-step by design
		}
		if m.sessHasMore {
			hints = append(hints, th.footerKey.Render("n")+th.footer.Render(" more"))
		}
		hints = append(hints, th.footer.Render("esc close"))
		return m.panelFooter(hints...)
	}
	if m.panel == panelModels {
		return m.panelFooter(
			th.footer.Render("↑↓ select"),
			th.footer.Render("⏎ use"),
			th.footer.Render("esc close"),
		)
	}
	if m.panel == panelRuns {
		return m.panelFooter(
			th.footer.Render("↑↓ select · ]/[ tabs"),
			th.footerKey.Render("A")+th.footer.Render("pprove · "),
			th.footerKey.Render("D")+th.footer.Render("eny · "),
			th.footerKey.Render("T")+th.footer.Render("rust"),
			th.footerKey.Render("c")+th.footer.Render(" cancel"),
			th.footerKey.Render("e")+th.footer.Render(" events"),
			th.footerKey.Render("p")+th.footer.Render(" approvals"),
			th.footerKey.Render("r")+th.footer.Render(" refresh"),
			th.footer.Render("esc close"),
		)
	}
	if m.panel == panelEvents {
		filter := "all"
		if m.evRunFilter != "" {
			filter = "run " + shortID(m.evRunFilter)
		} else if m.evSessionFilter {
			filter = "this session"
		}
		return m.panelFooter(
			th.footerKey.Render("f")+th.footer.Render(" filter: "+filter+" · ]/[ tabs"),
			th.footerKey.Render("x")+th.footer.Render(" clear filter"),
			th.footerKey.Render("r")+th.footer.Render(" refresh"),
			th.footer.Render("esc close"),
		)
	}
	if m.panel == panelMemory {
		if m.confirm == confirmFactDelete {
			return m.panelFooter(
				th.footerDanger.Render("delete this fact?"),
				th.footerKey.Render("y")+th.footerDanger.Render(" delete"),
				th.footer.Render("any other key cancels"),
			)
		}
		if m.panelDetail {
			return m.panelFooter(
				th.footer.Render("↑↓ scroll"),
				th.footerKey.Render("p")+th.footer.Render(" promote episode"),
				th.footer.Render("esc back"),
			)
		}
		return m.panelFooter(
			th.footer.Render("⏎ detail · "),
			th.footerKey.Render("a")+th.footer.Render(" add user · "),
			th.footerKey.Render("A")+th.footer.Render(" add env"),
			th.footerKey.Render("d")+th.footer.Render(" delete fact → y confirm"),
			th.footerKey.Render("p")+th.footer.Render(" promote episode"),
			th.footerKey.Render("c")+th.footer.Render(" consolidate user · "),
			th.footerKey.Render("E")+th.footer.Render(" env"),
			th.footer.Render("]/[ tabs · esc close"),
		)
	}
	if m.panel == panelSkills {
		if m.panelDetail {
			return m.panelFooter(
				th.footer.Render("↑↓ scroll"),
				th.footerKey.Render("p")+th.footer.Render(" promote · "),
				th.footerKey.Render("P")+th.footer.Render(" force-promote"),
				th.footer.Render("esc back"),
			)
		}
		return m.panelFooter(
			th.footer.Render("↑↓ select · ⏎ detail · ]/[ tabs"),
			th.footerKey.Render("p")+th.footer.Render(" promote"),
			th.footerKey.Render("P")+th.footer.Render(" force-promote"),
			th.footer.Render("esc close"),
		)
	}
	if m.panel == panelTools {
		if m.panelDetail {
			return m.panelFooter(th.footer.Render("↑↓ scroll · esc back"))
		}
		return m.panelFooter(
			th.footer.Render("↑↓ browse · ⏎ detail · ]/[ tabs · esc close"),
		)
	}
	if m.panel == panelConfig {
		if m.panelEdit == panelEditShutdown {
			return m.panelFooter(
				th.footerDanger.Render("this stops odek serve"),
				th.footerKey.Render("shutdown")+th.footer.Render(" + ⏎"),
				th.footerKey.Render("esc")+th.footer.Render(" cancels"),
			)
		}
		if m.panelDetail {
			return m.panelFooter(th.footer.Render("↑↓ scroll · esc back"))
		}
		return m.panelFooter(
			th.footer.Render("↑↓ browse · ⏎ detail · ]/[ tabs"),
			th.footerKey.Render("d")+th.footer.Render(" kick connection"),
			th.footerKey.Render("S")+th.footerDanger.Render(" shutdown server"),
			th.footerKey.Render("r")+th.footer.Render(" refresh"),
			th.footer.Render("esc close"),
		)
	}
	// The conversation-clear gate rides the composer footer — it is armed
	// outside any panel and must be visible where ^L was pressed.
	if m.confirm == confirmClear || m.confirm == confirmCancel {
		headline := "clear the conversation?"
		action := "clear"
		if m.confirm == confirmCancel {
			headline = "cancel the running turn?"
			action = "cancel"
		}
		return m.panelFooter(
			th.footerDanger.Render(headline),
			th.footerKey.Render("y")+th.footerDanger.Render(" "+action),
			th.footer.Render("any other key cancels"),
		)
	}
	// The status bar carries no static key cheatsheet (the welcome splash and
	// /help cover that) — only the live run state: a cancel hint while busy on
	// the left, and latency / scroll position on the right.
	left := ""
	if m.busy {
		left = "  " + th.footerKey.Render("esc") + th.footer.Render(" cancel")
		if n := len(m.queue); n > 0 {
			left += th.footerSep.Render(" · ") + th.scroll.Render(fmt.Sprintf("▸ %d queued", n))
		}
	}
	// Persistent expandAll indicator — while the global toggle holds every
	// step open, per-step toggles look dead unless the chrome says why.
	if m.expandAll {
		ind := th.footerKey.Render("▼") + th.footer.Render(" details")
		if left == "" {
			left = "  " + ind
		} else {
			left += th.footerSep.Render(" · ") + ind
		}
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
		seg := ""
		if m.busy {
			seg = th.scroll.Render("↓ new output") + th.footerSep.Render(" · ")
		}
		seg += th.footerKey.Render("PgUp") + th.footer.Render(" more") +
			th.footerSep.Render(" · ") +
			th.footerKey.Render("^G") + th.footer.Render(" latest") +
			th.footerSep.Render(" · ") +
			th.scroll.Render(fmt.Sprintf("↕ %d%%", int(m.vp.ScrollPercent()*100)))
		segs = append(segs, seg)
	}
	// The persistent teaching pair: help and the palette, always one chord away.
	segs = append(segs, th.footerKey.Render("F1")+th.footer.Render(" help · ")+
		th.footerKey.Render("^K")+th.footer.Render(" everything"))
	right := strings.Join(segs, th.footerSep.Render("  ·  ")) + "  "
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

// wrapText hard-wraps s to n columns by runes, keeping existing line breaks.
// It always returns at least one line, so an empty input still claims its row.
func wrapText(s string, n int) []string {
	if n < 1 {
		n = 1
	}
	var out []string
	for _, ln := range strings.Split(s, "\n") {
		r := []rune(ln)
		if len(r) == 0 {
			out = append(out, "")
			continue
		}
		for len(r) > n {
			out = append(out, string(r[:n]))
			r = r[n:]
		}
		out = append(out, string(r))
	}
	return out
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

// human formats a token count compactly (e.g. 1234 → "1.2k").
func human(n int) string {
	trim := func(s string) string {
		// 420.0k reads as noise — whole values drop the decimal.
		return strings.Replace(strings.Replace(s, ".0k", "k", 1), ".0M", "M", 1)
	}
	switch {
	case n >= 999_500: // promote to M once one-decimal k rounding would reach 1000.0k
		return trim(fmt.Sprintf("%.1fM", float64(n)/1_000_000))
	case n >= 1_000:
		return trim(fmt.Sprintf("%.1fk", float64(n)/1_000))
	default:
		return fmt.Sprintf("%d", n)
	}
}

// costUSD estimates the dollar cost of inTok input and outTok output tokens
// at per-million prices.
func costUSD(inTok, outTok int, inPrice, outPrice float64) float64 {
	return (float64(inTok)*inPrice + float64(outTok)*outPrice) / 1e6
}

// formatUSD renders a dollar amount compactly: cents at $1+, up to four
// decimals below (trailing zeros trimmed, two-decimal floor) so small
// per-turn costs stay visible instead of rounding to $0.00.
func formatUSD(v float64) string {
	if v >= 1 {
		return fmt.Sprintf("$%.2f", v)
	}
	if v <= 0 {
		return "$0"
	}
	s := strings.TrimRight(fmt.Sprintf("%.4f", v), "0")
	if i := strings.IndexByte(s, '.'); len(s)-i < 3 {
		s += strings.Repeat("0", 3-(len(s)-i))
	}
	return "$" + s
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
