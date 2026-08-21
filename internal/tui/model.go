package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"

	"github.com/BackendStack21/bodek/internal/client"
	"github.com/BackendStack21/bodek/internal/tokens"
	"github.com/BackendStack21/bodek/internal/update"
)

// role identifies who authored a conversation entry.
type role int

const (
	roleUser role = iota
	roleAsst
	roleNote
)

// step is a single tool invocation within an assistant turn.
type step struct {
	name     string
	arg      string
	result   string // sanitized tool output (multi-line); excerpted at render
	done     bool
	isErr    bool          // the result reads as a failure (tints the status glyph red)
	subagent bool          // this call delegates to a sub-agent (renders its log tree)
	logs     []string      // nested sub-agent activity, from subagent_log events
	expanded bool          // user has expanded this step to show full output/logs
	started  time.Time     // when the tool_call arrived; zero for resumed history
	dur      time.Duration // wall-clock the call took; 0 until the result lands
}

// stepRef maps a rendered transcript line to a specific step for mouse
// hit-testing.
type stepRef struct {
	msgIdx  int
	stepIdx int
	line    int
}

// turnItem is one entry in a turn's chronological timeline: either a
// reasoning block or a tool call, in arrival order.
type turnItem struct {
	thinking bool   // false = tool step
	text     string // thinking excerpt when thinking (capped per block)
	stepIdx  int    // index into msg.steps when !thinking
}

// turnStats is the telemetry of one finalized assistant turn, captured from the
// done event plus locally-tracked timing/tool activity. It powers the per-turn
// stat line and the /stats session dashboard.
type turnStats struct {
	latency    float64       // model latency reported by the server (seconds)
	wall       time.Duration // wall-clock from prompt submit to done
	ctxTok     int           // context tokens consumed this turn
	outTok     int           // output tokens produced this turn
	cacheWrite int           // provider cache writes (prompt cache stores)
	cacheRead  int           // provider cache hits
	cachedTok  int           // automatic prefix-match cache tokens
	toolCount  int           // tool invocations this turn
	toolGlyphs []string      // up to 4 deduped tool glyphs, in first-seen order
	thought    bool          // the model streamed reasoning this turn
}

// message is one entry in the transcript.
type message struct {
	role      role
	content   string // raw text/markdown
	rendered  string // cached glamour render (assistant, finalized)
	thinking  string // captured reasoning for this turn (finalized)
	steps     []step
	items     []turnItem // chronological timeline of reasoning blocks and tool calls
	streaming bool
	stats     *turnStats // finalized-turn telemetry; nil while streaming / for history
	raw       bool       // content is pre-styled; render verbatim, never re-render
}

// Options carries startup display info into the model.
type Options struct {
	Model       string
	Sandbox     bool
	CWD         string
	LogPath     string // file the spawned server's stderr is captured to, if any
	OdekVersion string // engine version for the header (empty when attached/unknown)
	Version     string // bodek's own version; drives the startup update check

	// Reconnect, when set, redials the server after the socket drops. The
	// session resumes transparently: every prompt already carries
	// session_id + auth_token, so the next send re-binds it server-side.
	Reconnect func() (*client.Client, error)
}

// Model is the Bubble Tea model for bodek.
type Model struct {
	cl     *client.Client
	events <-chan client.Event
	opts   Options
	th     theme
	tokens *tokens.Store

	width, height int
	ready         bool

	vp   viewport.Model
	ta   textarea.Model
	sp   spinner.Model
	glam *glamour.TermRenderer

	msgs     []message
	curIdx   int // index of the streaming assistant message, -1 when idle
	busy     bool
	runStart time.Time
	lastTool string
	lastArg  string

	approval     *client.Event // pending approval, nil when none
	apprSel      int           // highlighted option in the approval panel
	apprExpanded bool          // tab: show the full command/description text
	apprTyped    string        // friction mode: the literal word being typed ("approve")
	ac           autocomplete  // @-reference completion state
	queue        []string      // prompts typed mid-turn, sent when the turn ends

	history   []string // submitted prompts, newest last (recalled with ↑)
	histNav   bool     // true while ^P/^N is walking the history
	histIdx   int      // index into history while navigating
	histDraft string   // input stashed while navigating history

	model     string
	sandbox   bool
	sessionID string
	authToken string // session-scoped token (for cancel / resume)
	pendModel string // model to apply on the next prompt
	thinkOn   bool
	expandAll bool // Ctrl+E: render every step's full output/logs

	odekVersion  string // engine version shown in the header ("" hides it)
	bodekVersion string // bodek's own version, for the startup update check

	panel    panelMode
	sessions []client.Session
	models   []client.ModelInfo
	panelSel int
	panelMsg string // status/error line inside a panel

	// Sessions panel state: server-side search plus paged "load more".
	sessQuery   string        // applied search text (server-side substring match)
	sessHasMore bool          // last page was full — more may follow
	panelEdit   panelEditMode // text-entry submode while a panel is open
	panelDraft  string        // the text being edited (search query / rename)

	profiles []client.Profile // built-in model catalog (picker + context gauge)

	sessCtxTok int
	sessOutTok int
	winCtxTok  int // live context-window fill: last request's prompt size
	runCtxCum  int // last cumulative run contextTokens seen (odek reports per-run
	// cumulative prompt tokens, so the window fill is the delta between reports)
	lastLatency float64

	// WS protocol v2 server snapshot (server_info on connect, refreshed by
	// every pong) plus the heartbeat that measures it.
	serverStream bool          // server streams token_delta/thinking_delta live
	rtt          time.Duration // last ping round-trip (0 = never measured)
	pingSentAt   time.Time     // when the outstanding heartbeat left
	srvUptime    time.Duration // server uptime at the latest snapshot
	srvConns     int64         // live WS connections at the latest snapshot
	cancelAck    bool          // cancelled seen — the run's trailing error event is expected noise

	attachments []client.Attachment // files staged for the next prompt (/attach)

	limits       client.Limits     // server budget limits + token prices (zero prices → cost hidden)
	serverModel  string            // /api/limits model — effective_prices apply to it
	effectivePrx client.ModelPrice // server-resolved prices for serverModel
	maxContext   int               // active model's context window (0 = unknown → gauge hidden)
	turnStats    []turnStats       // per-turn telemetry retained for the /stats dashboard
	toolTotal    int               // cumulative tool calls this session
	sessionStart time.Time         // first-prompt timestamp, for session wall-clock

	status    string
	notices   []string
	noticeExp []time.Time // parallel to notices; zero = sticky, else expires at
	noticeSeq int         // bumped on each transient notice, to invalidate stale timers
	disconn   bool
	quitting  bool

	gradRule  string // cached full-width gradient rule
	gradRuleW int
	logoCache string // cached gradient logo (width-independent)

	convPrefix     string    // cached rendering of the finalized transcript prefix
	convPrefixRefs []stepRef // step header line index for the cached prefix
	convCount      int       // messages the prefix covers (-1 = invalidated)
	stepLineIndex  []stepRef // full transcript step index for mouse hit-testing

	renderPending bool // a coalesced streaming render is scheduled
	renderSeq     int  // bumped per scheduled flush, to drop stale ticks
}

// New builds the initial model.
func New(cl *client.Client, opts Options) *Model {
	th := newTheme()

	ta := textarea.New()
	ta.Placeholder = "Ask odek to build, fix, explore… (⏎ send · ^J newline · ↑ scroll · ^P history)"
	ta.Prompt = th.asstLabel.Render("┃ ")
	ta.ShowLineNumbers = false
	ta.CharLimit = 0
	ta.SetHeight(3)
	ta.FocusedStyle.CursorLine = th.taCursorLine
	ta.Focus()

	sp := spinner.New()
	// A smooth braille spinner reads as fluid motion at small size.
	sp.Spinner = spinner.Spinner{
		Frames: []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"},
		FPS:    time.Second / 12,
	}
	sp.Style = th.spinner

	return &Model{
		cl:           cl,
		events:       cl.Events,
		opts:         opts,
		th:           th,
		tokens:       tokens.Open(),
		ta:           ta,
		sp:           sp,
		curIdx:       -1,
		model:        opts.Model,
		sandbox:      opts.Sandbox,
		thinkOn:      false,
		status:       "ready",
		odekVersion:  opts.OdekVersion,
		bodekVersion: opts.Version,
	}
}

func (m *Model) Init() tea.Cmd {
	return tea.Batch(textarea.Blink, m.sp.Tick, listen(m.events),
		m.fetchModels(), m.fetchProfiles(), m.fetchLimits(), m.checkUpdate(),
		m.armHeartbeat())
}

// pingEvery is the application-level heartbeat cadence (spec: 25s). The
// server answers inline — even mid-run — so the ping doubles as a liveness
// probe and the pong refreshes the server snapshot (RTT, uptime, conns).
const pingEvery = 25 * time.Second

// heartbeatMsg re-arms the heartbeat loop.
type heartbeatMsg struct{}

// armHeartbeat schedules the next ping. The send time is stamped in Update
// when the tick fires (model state must only mutate on the update goroutine);
// only the socket write itself runs async — the client serializes writes.
func (m *Model) armHeartbeat() tea.Cmd {
	return tea.Tick(pingEvery, func(time.Time) tea.Msg {
		return heartbeatMsg{}
	})
}

func (m *Model) handleHeartbeat() tea.Cmd {
	m.pingSentAt = time.Now()
	cl := m.cl
	if cl == nil {
		return m.armHeartbeat()
	}
	send := func() tea.Msg {
		_ = cl.Ping()
		return nil
	}
	return tea.Batch(send, m.armHeartbeat())
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		return m, m.resize(msg.Width, msg.Height)

	case tea.KeyMsg:
		return m.handleKey(msg)

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.sp, cmd = m.sp.Update(msg)
		if m.busy || m.ac.loading {
			m.refresh()
		}
		return m, cmd

	case heartbeatMsg:
		return m, m.handleHeartbeat()

	case renderFlushMsg:
		if msg.seq == m.renderSeq && m.renderPending {
			m.renderPending = false
			m.refresh()
		}
		return m, nil

	case errMsg:
		m.busy = false
		m.status = "error"
		m.addNote("error: " + msg.err.Error())
		// Close out the turn sendPrompt opened — otherwise the transcript
		// keeps a phantom streaming assistant message with no reply. Same
		// inline styling as a server-side error event.
		if i := m.cur(); i >= 0 && m.msgs[i].content == "" {
			m.msgs[i].content = "**Error:** " + msg.err.Error()
		}
		m.finalize()
		m.relayout() // the busy status line releases its row
		m.refresh()
		return m, m.sendQueued()

	case acResultMsg:
		if msg.seq != m.ac.seq || m.ac.mode != acRef {
			return m, nil // stale response, or popup switched to command mode
		}
		m.ac.loading = false
		m.ac.items = msg.items
		if m.ac.sel >= len(m.ac.items) {
			m.ac.sel = 0
		}
		m.relayout()
		m.refresh()
		return m, nil

	case sessionsMsg:
		m.handleSessionsMsg(msg)
		m.refresh()
		return m, nil

	case sessionsPageMsg:
		m.handleSessionsPageMsg(msg)
		m.refresh()
		return m, nil

	case modelsMsg:
		m.handleModelsMsg(msg)
		m.refresh()
		return m, nil

	case pickerMsg:
		// The picker's combined fetch: reuse the per-list handlers so panel
		// state and the context gauge update exactly as on separate fetches.
		m.handleModelsMsg(modelsMsg{items: msg.models, err: msg.err})
		m.handleProfilesMsg(profilesMsg{items: msg.profiles})
		if m.panel == panelModels {
			m.refresh()
		}
		return m, nil

	case profilesMsg:
		m.handleProfilesMsg(msg)
		return m, nil

	case sessionUpdatedMsg:
		return m, m.handleSessionUpdated(msg)

	case sessionRenamedMsg:
		return m, m.handleSessionRenamed(msg)

	case sessionExportedMsg:
		return m, m.handleSessionExported(msg)

	case sessionSwitchMsg:
		return m, m.handleSessionSwitch(msg)

	case limitsMsg:
		m.handleLimitsMsg(msg)
		return m, nil

	case sessionDetailMsg:
		return m, m.handleSessionDetail(msg)

	case sessionDeletedMsg:
		return m, m.handleSessionDeleted(msg)

	case cancelDoneMsg:
		if msg.err != nil {
			m.addNote("cancel failed: " + msg.err.Error())
			m.refresh()
		} else {
			// The API accepted the abort; the turn's done event settles the
			// status shortly after. Acknowledge the keypress in the meantime.
			cmd := m.transientNoteCmd("cancelled")
			m.refresh()
			return m, cmd
		}
		return m, nil

	case updateCheckMsg:
		// Silent on error or when already current: the hint only ever nags
		// once, at startup, when a newer release is confirmed.
		if msg.err == nil && update.Newer(msg.latest, m.bodekVersion) {
			m.addNote(fmt.Sprintf("⬆ bodek %s available — run `bodek upgrade`", msg.latest))
			m.refresh()
		}
		return m, nil

	case eventMsg:
		return m.handleEvent(client.Event(msg))

	case reconnectMsg:
		return m.handleReconnect(msg)

	case noticeExpireMsg:
		m.pruneNotices(time.Now())
		m.refresh()
		return m, nil

	case tea.MouseMsg:
		if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft && m.panel == panelNone && !m.ac.open {
			// Viewport content begins below the header (2 rows).
			top := 2
			if msg.Y >= top && msg.Y < top+m.vp.Height {
				line := msg.Y - top + m.vp.YOffset
				if msgIdx, stepIdx, ok := m.stepAtLine(line); ok {
					m.toggleStep(msgIdx, stepIdx)
					m.refresh()
					return m, nil
				}
			}
		}
		var cmd tea.Cmd
		m.vp, cmd = m.vp.Update(msg)
		return m, cmd
	}

	// Forward anything else to the focused input.
	var cmd tea.Cmd
	m.ta, cmd = m.ta.Update(msg)
	return m, cmd
}

func (m *Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Approval mode captures the keyboard until answered; only the transcript
	// scroll keys pass through to the viewport.
	if m.approval != nil {
		return m.handleApprovalKey(msg)
	}

	// A full-area panel (sessions / models) captures the keyboard while open.
	if m.panel != panelNone {
		return m.handlePanelKey(msg)
	}

	// The @-reference popup captures navigation keys while open.
	if m.ac.open {
		return m.handleACKey(msg)
	}

	// A dead connection offers a manual retry on r — only with an empty
	// input, so a drafted prompt is never disturbed.
	if m.disconn && m.opts.Reconnect != nil && msg.String() == "r" && m.ta.Value() == "" {
		m.status = "reconnecting"
		m.addNote("retrying connection…")
		m.refresh()
		return m, m.scheduleReconnect(0)
	}

	switch msg.String() {
	case "ctrl+c":
		m.quitting = true
		return m, tea.Quit
	case "esc":
		if m.busy {
			return m, m.cancelRun()
		}
		return m, nil
	case "ctrl+r":
		return m, m.openSessions()
	case "ctrl+o":
		return m, m.openModels()
	case "enter":
		return m, m.submit()
	case "ctrl+j":
		// Insert a newline into the textarea.
		var cmd tea.Cmd
		m.ta, cmd = m.ta.Update(tea.KeyMsg{Type: tea.KeyEnter})
		return m, tea.Batch(cmd, m.syncAC())
	case "ctrl+t":
		m.thinkOn = !m.thinkOn
		state := "off"
		if m.thinkOn {
			state = "on"
		}
		cmd := m.transientNoteCmd("thinking " + state)
		m.refresh()
		return m, cmd
	case "ctrl+l":
		if !m.busy {
			m.clearConversation()
		}
		return m, nil
	case "ctrl+e":
		// Global details toggle: every step (including in-flight ones) shows
		// its full output/logs; the per-step mouse toggle still layers on top.
		m.expandAll = !m.expandAll
		state := "off"
		if m.expandAll {
			state = "on"
		}
		cmd := m.transientNoteCmd("tool details " + state)
		m.convCount = -1 // re-render the cached transcript prefix too
		m.refresh()
		return m, cmd
	case "ctrl+p":
		// Prompt-history recall lives on this dedicated readline-style
		// binding so bare ↑/↓ are free to scroll the transcript — the far
		// more frequent intent while the input holds focus.
		if m.ta.Line() == 0 && (m.histNav || m.ta.Value() == "") {
			m.historyPrev()
			return m, nil
		}
	case "ctrl+n":
		if m.ta.Line() == m.ta.LineCount()-1 && m.histNav {
			m.historyNext()
			return m, nil
		}
	case "up", "down":
		// At the input's edge lines, scroll the transcript; inside a
		// multi-line input the textarea moves the cursor instead.
		if msg.String() == "up" && m.ta.Line() == 0 {
			var cmd tea.Cmd
			m.vp, cmd = m.vp.Update(msg)
			return m, cmd
		}
		if msg.String() == "down" && m.ta.Line() == m.ta.LineCount()-1 {
			var cmd tea.Cmd
			m.vp, cmd = m.vp.Update(msg)
			return m, cmd
		}
	case "ctrl+g":
		// Jump to the latest output. A ctrl binding, so typing a capital G
		// (even as the first character of a prompt) is never hijacked.
		m.vp.GotoBottom()
		return m, nil
	case "end":
		// End doubles as jump-to-latest — only with an empty input, so its
		// cursor-movement meaning inside a draft keeps working.
		if m.ta.Value() == "" {
			m.vp.GotoBottom()
			return m, nil
		}
	case "pgup", "pgdown", "ctrl+u", "ctrl+d":
		var cmd tea.Cmd
		m.vp, cmd = m.vp.Update(msg)
		return m, cmd
	}

	// Normal typing — update the input, then re-evaluate @-completion.
	var cmd tea.Cmd
	m.ta, cmd = m.ta.Update(msg)
	return m, tea.Batch(cmd, m.syncAC())
}

// ── actions ──────────────────────────────────────────────────────────────

// clearConversation wipes the transcript and the session-scoped telemetry, so
// /stats, the header gauge, and the footer start fresh after a clear instead
// of reporting turns, tools, tokens, and age from before it (mirrors the
// reset done when resuming a session).
func (m *Model) clearConversation() {
	m.msgs = nil
	m.curIdx = -1
	m.convCount = -1 // transcript replaced — drop the cached prefix
	m.convPrefixRefs = nil
	m.stepLineIndex = nil
	m.turnStats = nil
	m.toolTotal = 0
	m.sessionStart = time.Time{}
	m.sessCtxTok = 0
	m.sessOutTok = 0
	m.winCtxTok = 0
	m.runCtxCum = 0
	m.lastLatency = 0
	m.refresh()
}

// ── helpers ────────────────────────────────────────────────────────────────

// resolveMaxContext sets m.maxContext from the active model's advertised
// context window: an exact /api/models match wins; otherwise the first
// built-in profile whose id prefixes the model id (profiles are prefix
// entries by contract). No match leaves 0, which hides the gauge rather
// than guessing.
func (m *Model) resolveMaxContext() {
	m.maxContext = 0
	for _, md := range m.models {
		if md.ID == m.model {
			m.maxContext = md.MaxContext
			return
		}
	}
	for _, p := range m.profiles {
		if p.MaxContext > 0 && strings.HasPrefix(m.model, p.ID) {
			m.maxContext = p.MaxContext
			return
		}
	}
}

// fetchModels loads the advertised model list at startup so the context-window
// gauge knows the active model's budget without the picker ever being opened.
func (m *Model) fetchModels() tea.Cmd {
	cl := m.cl
	return func() tea.Msg {
		items, err := cl.Models()
		return modelsMsg{items: items, err: err}
	}
}

// fetchProfiles loads the built-in model catalog (id prefixes + context
// windows) — picker entries beyond the configured model, and the fallback
// source for the header gauge after a model switch.
func (m *Model) fetchProfiles() tea.Cmd {
	cl := m.cl
	return func() tea.Msg {
		items, err := cl.Profiles()
		return profilesMsg{items: items, err: err}
	}
}

// prices returns the effective per-million token prices for the active
// model: the server's effective_prices when it matches, otherwise the
// client-side resolution twin. Zero prices mean "cost unavailable".
func (m *Model) prices() (inPerMillion, outPerMillion float64) {
	return client.LimitsResponse{
		Model:           m.serverModel,
		Limits:          m.limits,
		EffectivePrices: m.effectivePrx,
	}.PricesFor(m.model)
}

// fetchLimits loads the server's budget limits and token prices at startup so
// turn footers and /stats can render cost. Prices resolve per active model at
// render time, so model switches need no refetch.
func (m *Model) fetchLimits() tea.Cmd {
	cl := m.cl
	return func() tea.Msg {
		resp, err := cl.Limits()
		return limitsMsg{resp: resp, err: err}
	}
}

// render runs content through glamour; falls back to raw text on error.
func (m *Model) render(content string) string {
	if m.glam == nil || strings.TrimSpace(content) == "" {
		return content
	}
	out, err := m.glam.Render(content)
	if err != nil {
		return content
	}
	return strings.TrimRight(out, "\n")
}

func (m *Model) resize(w, h int) tea.Cmd {
	m.width, m.height = w, h

	if !m.ready {
		m.vp = viewport.New(w, 3)
		m.ready = true
	}
	m.ta.SetWidth(w - 4)
	m.gradRule = ""  // invalidate cached rule for the new width
	m.convCount = -1 // and the cached transcript prefix (bars are width-dependent)
	m.relayout()

	wrap := w - 6
	if wrap < 20 {
		wrap = 20
	}
	if r, err := glamour.NewTermRenderer(
		glamour.WithStandardStyle("dark"),
		glamour.WithWordWrap(wrap),
	); err == nil {
		m.glam = r
		// Re-render finalized assistant messages at the new width. Pre-styled
		// cards (raw) are point-in-time snapshots and must never go through
		// glamour, which would mangle their embedded ANSI.
		for i := range m.msgs {
			if m.msgs[i].role == roleAsst && !m.msgs[i].streaming && !m.msgs[i].raw {
				m.msgs[i].rendered = m.render(m.msgs[i].content)
			}
		}
	}
	m.refresh()
	return nil
}

// relayout recomputes the viewport height from the current chrome, accounting
// for the approval panel or the @-reference popup when either is open.
func (m *Model) relayout() {
	if !m.ready {
		return
	}
	vpH := m.height - headerHeight - footerHeight - m.inputAreaHeight()
	if vpH < 3 {
		vpH = 3
	}
	m.vp.Width = m.width
	m.vp.Height = vpH
}

// inputAreaHeight is the number of rows below the transcript viewport — the
// input area plus the busy status line when it shows — so the viewport
// shrinks by exactly the right amount and the footer never moves.
func (m *Model) inputAreaHeight() int {
	if m.approval != nil {
		return lineCount(m.approvalBody()) + 2 // panel border
	}
	h := inputHeight
	if m.statusLineVisible() {
		h += 2 // busy status line + blank separator row above the input box
	}
	if m.ac.open {
		h += m.ac.height()
	}
	return h
}

// elapsed formats the current run's wall-clock time in whole seconds (the live
// badge re-renders with every spinner tick, so tenths would flicker). The
// finalized per-turn stat line keeps tenths via formatDuration.
func (m *Model) elapsed() string {
	if m.runStart.IsZero() {
		return ""
	}
	d := time.Since(m.runStart)
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
}

// sanitize strips terminal control sequences from untrusted content before it
// is rendered. Agent output — streamed tokens, tool results, file contents,
// resumed transcripts — is attacker-influenced; raw C0 control bytes (notably
// ESC, 0x1b) could drive ANSI/OSC escapes that move the cursor, clear the
// screen, or exfiltrate via OSC 52. We keep newlines and tabs and drop every
// other control byte (and DEL), which defangs escape sequences by removing
// their introducer while leaving readable text intact.
func sanitize(s string) string {
	if !strings.ContainsFunc(s, isControl) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if !isControl(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// isControl reports whether r is a control character we strip from untrusted
// text (C0 controls and DEL, except newline and tab).
func isControl(r rune) bool {
	if r == '\n' || r == '\t' {
		return false
	}
	return r < 0x20 || r == 0x7f
}

// formatDuration renders a short, friendly elapsed time.
func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
}

// formatStepDur renders a tool call's response time compactly: milliseconds
// under a second, tenths of a second under a minute, then minutes+seconds.
func formatStepDur(d time.Duration) string {
	switch {
	case d < time.Second:
		return fmt.Sprintf("%dms", d.Milliseconds())
	case d < time.Minute:
		return fmt.Sprintf("%.1fs", d.Seconds())
	default:
		return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
	}
}

func truncate(s string, n int) string {
	if n < 1 {
		return "" // no room even for the ellipsis (very narrow terminal)
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// toggleStep flips the expanded state of a step and invalidates the transcript
// prefix cache so the re-render picks it up.
func (m *Model) toggleStep(msgIdx, stepIdx int) {
	if msgIdx < 0 || msgIdx >= len(m.msgs) {
		return
	}
	steps := m.msgs[msgIdx].steps
	if stepIdx < 0 || stepIdx >= len(steps) {
		return
	}
	steps[stepIdx].expanded = !steps[stepIdx].expanded
	m.convCount = -1
}

// stepAtLine maps a viewport content line to a step for mouse hit-testing,
// matching only the step's own header line (the chevron row) — a click on
// detail lines or prose below a step must not toggle it.
func (m *Model) stepAtLine(line int) (msgIdx, stepIdx int, ok bool) {
	for i := range m.stepLineIndex {
		if m.stepLineIndex[i].line > line {
			break
		}
		if m.stepLineIndex[i].line == line {
			return m.stepLineIndex[i].msgIdx, m.stepLineIndex[i].stepIdx, true
		}
	}
	return
}
