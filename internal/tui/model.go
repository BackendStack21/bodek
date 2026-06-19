package tui

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"

	"github.com/BackendStack21/bodek/internal/client"
	"github.com/BackendStack21/bodek/internal/tokens"
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
	isErr    bool     // the result reads as a failure (tints the status glyph red)
	subagent bool     // this call delegates to a sub-agent (renders its log tree)
	logs     []string // nested sub-agent activity, from subagent_log events
}

// turnStats is the telemetry of one finalized assistant turn, captured from the
// done event plus locally-tracked timing/tool activity. It powers the per-turn
// stat line and the /stats session dashboard.
type turnStats struct {
	latency    float64       // model latency reported by the server (seconds)
	wall       time.Duration // wall-clock from prompt submit to done
	ctxTok     int           // context tokens consumed this turn
	outTok     int           // output tokens produced this turn
	toolCount  int           // tool invocations this turn
	toolGlyphs []string      // up to 4 deduped tool glyphs, in first-seen order
	thought    bool          // the model streamed reasoning this turn
}

// message is one entry in the transcript.
type message struct {
	role      role
	content   string // raw text/markdown
	rendered  string // cached glamour render (assistant, finalized)
	steps     []step
	streaming bool
	stats     *turnStats // finalized-turn telemetry; nil while streaming / for history
	raw       bool       // content is pre-styled; render verbatim, never re-render
}

// Options carries startup display info into the model.
type Options struct {
	Model   string
	Sandbox bool
	CWD     string
	LogPath string // file the spawned server's stderr is captured to, if any
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
	thinking strings.Builder
	runStart time.Time
	lastTool string
	lastArg  string

	approval *client.Event // pending approval, nil when none
	ac       autocomplete  // @-reference completion state

	model     string
	sandbox   bool
	sessionID string
	authToken string // session-scoped token (for cancel / resume)
	pendModel string // model to apply on the next prompt
	thinkOn   bool

	panel    panelMode
	sessions []client.Session
	models   []client.ModelInfo
	panelSel int
	panelMsg string // status/error line inside a panel

	sessCtxTok  int
	sessOutTok  int
	lastLatency float64

	maxContext   int         // active model's context window (0 = unknown → gauge hidden)
	turnStats    []turnStats // per-turn telemetry retained for the /stats dashboard
	toolTotal    int         // cumulative tool calls this session
	sessionStart time.Time   // first-prompt timestamp, for session wall-clock

	status   string
	notices  []string
	disconn  bool
	quitting bool

	gradRule  string // cached full-width gradient rule
	gradRuleW int
}

// acMode selects what the completion popup is completing.
type acMode int

const (
	acRef acMode = iota // @-references (files/sessions), searched server-side
	acCmd               // slash commands, filtered locally
)

// autocomplete holds the completion popup state (shared by @ and / modes).
type autocomplete struct {
	open    bool
	loading bool
	mode    acMode
	query   string
	items   []client.Resource
	sel     int
	seq     int // request sequence, to drop stale responses
}

// rows is the number of list rows the popup renders.
func (a autocomplete) rows() int {
	if len(a.items) == 0 {
		return 1 // "searching…" / "no matches"
	}
	return len(a.items)
}

// height is the total rendered height of the popup (border + title + rows).
func (a autocomplete) height() int {
	return a.rows() + 3
}

// acResultMsg carries the result of an async resource search.
type acResultMsg struct {
	seq   int
	items []client.Resource
}

// New builds the initial model.
func New(cl *client.Client, opts Options) *Model {
	th := newTheme()

	ta := textarea.New()
	ta.Placeholder = "Ask odek to build, fix, explore… (⏎ send · ^J newline)"
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
		cl:      cl,
		events:  cl.Events,
		opts:    opts,
		th:      th,
		tokens:  tokens.Open(),
		ta:      ta,
		sp:      sp,
		curIdx:  -1,
		model:   opts.Model,
		sandbox: opts.Sandbox,
		thinkOn: false,
		status:  "ready",
	}
}

func (m *Model) Init() tea.Cmd {
	return tea.Batch(textarea.Blink, m.sp.Tick, listen(m.events), m.fetchModels())
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

	case errMsg:
		m.busy = false
		m.status = "error"
		m.addNote("error: " + msg.err.Error())
		m.refresh()
		return m, nil

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

	case modelsMsg:
		m.handleModelsMsg(msg)
		m.refresh()
		return m, nil

	case sessionDetailMsg:
		m.handleSessionDetail(msg)
		return m, nil

	case sessionDeletedMsg:
		return m, m.handleSessionDeleted(msg)

	case cancelDoneMsg:
		if msg.err != nil {
			m.addNote("cancel failed: " + msg.err.Error())
			m.refresh()
		}
		return m, nil

	case eventMsg:
		return m.handleEvent(client.Event(msg))

	case tea.MouseMsg:
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
	// Approval mode captures the keyboard until answered.
	if m.approval != nil {
		switch msg.String() {
		case "a", "y":
			return m, m.answer("approve")
		case "d", "n":
			return m, m.answer("deny")
		case "t":
			if m.approval.AllowTrust {
				return m, m.answer("trust")
			}
		case "ctrl+c":
			m.quitting = true
			return m, tea.Quit
		}
		return m, nil
	}

	// A full-area panel (sessions / models) captures the keyboard while open.
	if m.panel != panelNone {
		return m.handlePanelKey(msg)
	}

	// The @-reference popup captures navigation keys while open.
	if m.ac.open {
		switch msg.String() {
		case "up", "ctrl+p":
			if m.ac.sel > 0 {
				m.ac.sel--
				m.refresh()
			}
			return m, nil
		case "down", "ctrl+n":
			if m.ac.sel < len(m.ac.items)-1 {
				m.ac.sel++
				m.refresh()
			}
			return m, nil
		case "tab":
			m.acceptCompletion()
			return m, nil
		case "enter":
			// A fully-typed command executes; a reference is inserted.
			if m.ac.mode == acCmd {
				return m, m.runSelectedCommand()
			}
			m.acceptCompletion()
			return m, nil
		case "esc":
			m.closeAC()
			return m, nil
		}
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
		return m, nil
	case "ctrl+l":
		if !m.busy {
			m.msgs = nil
			m.refresh()
		}
		return m, nil
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

func (m *Model) handleEvent(ev client.Event) (tea.Model, tea.Cmd) {
	switch ev.Type {
	case "session":
		m.sessionID = ev.SessionID
		if ev.AuthToken != "" {
			m.authToken = ev.AuthToken
			m.tokens.Set(ev.SessionID, ev.AuthToken)
		}
		if ev.Model != "" {
			m.model = ev.Model
			m.resolveMaxContext()
		}
		m.sandbox = ev.Sandbox

	case "thinking":
		m.thinking.WriteString(sanitize(ev.Content))
		m.status = "thinking"

	case "token":
		if i := m.cur(); i >= 0 {
			m.msgs[i].content += sanitize(ev.Content)
			m.msgs[i].streaming = true
		}
		m.status = "responding"

	case "tool_call":
		arg := argPreview(ev.Data)
		if i := m.cur(); i >= 0 {
			m.msgs[i].steps = append(m.msgs[i].steps,
				step{name: ev.Name, arg: arg, subagent: isSubagent(ev.Name)})
		}
		m.lastTool = ev.Name
		m.lastArg = arg
		m.status = "running " + ev.Name

	case "tool_result":
		if i := m.cur(); i >= 0 {
			steps := m.msgs[i].steps
			for j := len(steps) - 1; j >= 0; j-- {
				if steps[j].name == ev.Name && !steps[j].done {
					steps[j].done = true
					steps[j].result = resultPreview(ev.Data)
					steps[j].isErr = looksLikeError(steps[j].result)
					break
				}
			}
		}
		m.lastTool = ""
		m.lastArg = ""

	case "done":
		// Capture per-turn telemetry from the live message BEFORE finalize()
		// resets m.thinking and clears curIdx.
		if i := m.cur(); i >= 0 {
			wall := time.Duration(0)
			if !m.runStart.IsZero() {
				wall = time.Since(m.runStart)
			}
			ts := turnStats{
				latency:    ev.Latency,
				wall:       wall,
				ctxTok:     ev.ContextTokens,
				outTok:     ev.OutputTokens,
				toolCount:  len(m.msgs[i].steps),
				toolGlyphs: stepGlyphs(m.msgs[i].steps),
				thought:    m.thinking.Len() > 0,
			}
			m.msgs[i].stats = &ts
			m.turnStats = append(m.turnStats, ts)
			m.toolTotal += ts.toolCount
		}
		m.finalize()
		m.busy = false
		m.lastTool = ""
		m.lastArg = ""
		m.status = "ready"
		m.sessCtxTok = ev.SessionContextTokens
		m.sessOutTok = ev.SessionOutputTokens
		m.lastLatency = ev.Latency

	case "error":
		if i := m.cur(); i >= 0 && m.msgs[i].content == "" {
			m.msgs[i].content = "**Error:** " + ev.Message
		} else {
			m.addNote("error: " + ev.Message)
		}
		m.finalize()
		m.busy = false
		m.lastTool = ""
		m.lastArg = ""
		m.status = "error"

	case "approval_request":
		e := ev
		m.approval = &e
		m.status = "approval required"

	case "skill_event":
		m.addNote("skill · " + strings.TrimSpace(ev.SubType+" "+ev.SkillName) + eventTail(ev))
	case "memory_event":
		m.addNote("memory · " + strings.TrimSpace(ev.SubType+" "+ev.Target) + eventTail(ev))
	case "agent_signal":
		m.addNote("signal · " + strings.TrimSpace(ev.SubType+" "+ev.Detail) + eventTail(ev))
	case "subagent_log":
		line := strings.TrimSpace(ev.SubType + " " + ev.Name)
		if d := collapse(ev.Detail); d != "" {
			line = strings.TrimSpace(line + " · " + d)
		}
		line += eventTail(ev)
		// Nest the log under the in-flight sub-agent step when there is one;
		// otherwise (resumed turn, idle, or an unwrapped log) keep it as a notice.
		if i := m.cur(); i >= 0 && m.attachSubLog(i, line) {
			break
		}
		m.addNote("subagent · " + line)

	case client.EventDisconnected:
		m.disconn = true
		m.busy = false
		m.status = "disconnected"
		m.addNote("disconnected from odek serve")
		if m.opts.LogPath != "" {
			m.addNote("server log · " + m.opts.LogPath)
		}
		m.refresh()
		return m, nil
	}

	m.refresh()
	return m, listen(m.events)
}

// ── actions ──────────────────────────────────────────────────────────────

func (m *Model) submit() tea.Cmd {
	text := strings.TrimSpace(m.ta.Value())
	if text == "" {
		return nil
	}
	// Slash commands run locally and are allowed even mid-turn (e.g. /cancel).
	if strings.HasPrefix(text, "/") {
		return m.runCommandLine(text)
	}
	if m.busy || m.disconn {
		return nil
	}
	m.msgs = append(m.msgs, message{role: roleUser, content: text})
	m.msgs = append(m.msgs, message{role: roleAsst, streaming: true})
	m.curIdx = len(m.msgs) - 1
	m.ta.Reset()
	m.closeAC()
	m.busy = true
	m.status = "thinking"
	m.runStart = time.Now()
	if m.sessionStart.IsZero() {
		m.sessionStart = m.runStart
	}
	m.thinking.Reset()
	m.refresh()

	thinking := ""
	if m.thinkOn {
		thinking = "enabled"
	}
	opts := client.PromptOpts{
		Thinking:  thinking,
		Model:     m.pendModel,
		SessionID: m.sessionID,
		AuthToken: m.authToken,
	}
	m.pendModel = "" // applied
	cl := m.cl
	return func() tea.Msg {
		if err := cl.SendPrompt(text, opts); err != nil {
			return errMsg{err}
		}
		return nil
	}
}

func (m *Model) answer(action string) tea.Cmd {
	id := m.approval.ID
	m.approval = nil
	m.status = "thinking"
	m.refresh()
	cl := m.cl
	return func() tea.Msg {
		if err := cl.SendApproval(id, action); err != nil {
			return errMsg{err}
		}
		return nil
	}
}

// ── helpers ────────────────────────────────────────────────────────────────

// resolveMaxContext sets m.maxContext from the active model's advertised
// context window, or 0 when the model list is unknown or has no match (which
// hides the header gauge rather than guessing).
func (m *Model) resolveMaxContext() {
	m.maxContext = 0
	for _, md := range m.models {
		if md.ID == m.model {
			m.maxContext = md.MaxContext
			return
		}
	}
}

// stepGlyphs returns up to 4 deduped tool glyphs for a turn's steps, in
// first-seen order, for the per-turn stat line.
func stepGlyphs(steps []step) []string {
	const max = 4
	seen := make(map[string]bool, len(steps))
	out := make([]string, 0, max)
	for _, s := range steps {
		g := toolGlyph(s.name)
		if seen[g] {
			continue
		}
		seen[g] = true
		out = append(out, g)
		if len(out) == max {
			break
		}
	}
	return out
}

// eventTail renders the optional ×count / #task-index suffix shared by the
// engine-event notices (skill / memory / signal / subagent).
func eventTail(ev client.Event) string {
	s := ""
	if ev.Count > 0 {
		s += fmt.Sprintf(" ×%d", ev.Count)
	}
	if ev.TaskIdx > 0 {
		s += fmt.Sprintf(" #%d", ev.TaskIdx)
	}
	return s
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

// cur returns the index of the active streaming assistant message, or -1.
func (m *Model) cur() int {
	if m.curIdx >= 0 && m.curIdx < len(m.msgs) {
		return m.curIdx
	}
	return -1
}

// finalize closes out the streaming assistant message, rendering its markdown.
func (m *Model) finalize() {
	if i := m.cur(); i >= 0 {
		m.msgs[i].streaming = false
		m.msgs[i].rendered = m.render(m.msgs[i].content)
	}
	m.curIdx = -1
	m.thinking.Reset()
}

func (m *Model) addNote(s string) {
	m.notices = append(m.notices, sanitize(s))
	if len(m.notices) > 6 {
		m.notices = m.notices[len(m.notices)-6:]
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
	m.gradRule = "" // invalidate cached rule for the new width
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
// for the @-reference popup when it is open.
func (m *Model) relayout() {
	if !m.ready {
		return
	}
	inputH := inputHeight
	if m.ac.open {
		inputH += m.ac.height()
	}
	vpH := m.height - headerHeight - footerHeight - inputH
	if vpH < 3 {
		vpH = 3
	}
	m.vp.Width = m.width
	m.vp.Height = vpH
}

// ── @-reference autocomplete ────────────────────────────────────────────────

// refRe matches a trailing @-reference token at the end of the input.
var refRe = regexp.MustCompile(`(^|\s)@([^\s@]*)$`)

// activeRef returns the query of the trailing @-token, if the cursor is in one.
func activeRef(s string) (string, bool) {
	mm := refRe.FindStringSubmatch(s)
	if mm == nil {
		return "", false
	}
	return mm[2], true
}

// refStart returns the byte index of the '@' that begins the trailing token.
func refStart(s string) (int, bool) {
	loc := refRe.FindStringSubmatchIndex(s)
	if loc == nil {
		return 0, false
	}
	return loc[4] - 1, true // group 2 start, minus the '@'
}

// syncAC re-evaluates the input and drives the completion popup — slash
// commands (filtered locally) or @-references (searched server-side).
func (m *Model) syncAC() tea.Cmd {
	val := m.ta.Value()

	// Line-initial slash command completion.
	if name, ok := commandPrefix(val); ok {
		if m.ac.open && m.ac.mode == acCmd && m.ac.query == name {
			return nil
		}
		m.openCmdAC(name)
		return nil
	}

	q, ok := activeRef(val)
	if !ok {
		if m.ac.open {
			m.closeAC()
		}
		return nil
	}
	if m.ac.open && m.ac.mode == acRef && q == m.ac.query {
		return nil // nothing changed
	}
	m.ac.open = true
	m.ac.loading = true
	m.ac.mode = acRef
	m.ac.query = q
	m.ac.sel = 0
	m.ac.seq++
	seq := m.ac.seq
	m.relayout()
	m.refresh()

	cl := m.cl
	return func() tea.Msg {
		// @ is for file attachments only; sessions are reached via /sessions
		// (or ^R). Over-fetch, then keep just files.
		items, err := cl.Resources(q, 12)
		if err != nil {
			return acResultMsg{seq: seq, items: nil}
		}
		files := make([]client.Resource, 0, len(items))
		for _, it := range items {
			if it.Type == "file" {
				files = append(files, it)
			}
		}
		if len(files) > 6 {
			files = files[:6]
		}
		return acResultMsg{seq: seq, items: files}
	}
}

// acceptCompletion inserts the highlighted item into the input.
func (m *Model) acceptCompletion() {
	if len(m.ac.items) == 0 {
		m.closeAC()
		return
	}
	item := m.ac.items[m.ac.sel]
	if m.ac.mode == acCmd {
		m.ta.SetValue(item.ID + " ")
		m.ta.CursorEnd()
		m.closeAC()
		return
	}
	val := m.ta.Value()
	if idx, ok := refStart(val); ok {
		m.ta.SetValue(val[:idx] + item.ID + " ")
		m.ta.CursorEnd()
	}
	m.closeAC()
}

// closeAC dismisses the completion popup and restores the layout.
func (m *Model) closeAC() {
	if !m.ac.open && m.ac.items == nil {
		return
	}
	m.ac = autocomplete{seq: m.ac.seq}
	m.relayout()
	m.refresh()
}

// elapsed formats the current run's wall-clock time, e.g. "3.2s".
func (m *Model) elapsed() string {
	if m.runStart.IsZero() {
		return ""
	}
	return formatDuration(time.Since(m.runStart))
}

// argPreview extracts a short, human-friendly summary from a tool's JSON args.
func argPreview(data string) string {
	data = strings.TrimSpace(data)
	if data == "" {
		return ""
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(data), &m); err != nil {
		return truncate(collapse(data), 72)
	}
	for _, key := range []string{
		"command", "cmd", "path", "file", "pattern", "query", "url",
		"prompt", "task", "description", "instruction",
	} {
		if v, ok := m[key]; ok {
			if s, ok := v.(string); ok && s != "" {
				return truncate(collapse(s), 72)
			}
		}
	}
	parts := make([]string, 0, len(m))
	for _, v := range m {
		if s, ok := v.(string); ok && s != "" {
			parts = append(parts, s)
		}
	}
	return truncate(collapse(strings.Join(parts, " ")), 72)
}

// resultPreview sanitizes tool output and caps it to a generous number of
// lines, so the transcript can show a useful excerpt (rendered by renderSteps)
// without retaining the unbounded output of a chatty tool.
func resultPreview(data string) string {
	s := sanitize(data)
	lines := strings.Split(s, "\n")
	const cap = 200
	if len(lines) > cap {
		lines = lines[:cap]
	}
	return strings.Join(lines, "\n")
}

// isSubagent reports whether a tool name denotes a sub-agent delegation. The
// substrings mirror toolGlyph / toolProgress so the three stay consistent.
func isSubagent(name string) bool {
	n := strings.ToLower(name)
	return strings.Contains(n, "delegate") ||
		strings.Contains(n, "subagent") ||
		strings.Contains(n, "task")
}

// looksLikeError reports whether a tool result reads as a failure. It is
// deliberately conservative — keyed off leading error tokens and a couple of
// unambiguous shell phrases — so ordinary output that merely mentions "error"
// is not tinted red.
func looksLikeError(s string) bool {
	t := strings.ToLower(strings.TrimSpace(s))
	switch {
	case strings.HasPrefix(t, "error"),
		strings.HasPrefix(t, "fatal"),
		strings.HasPrefix(t, "panic:"),
		strings.HasPrefix(t, "traceback"),
		strings.HasPrefix(t, "exception"),
		strings.HasPrefix(t, "exit status"):
		return true
	}
	return strings.Contains(t, "command not found") ||
		strings.Contains(t, "no such file or directory")
}

// attachSubLog appends a sub-agent activity line to the most recent sub-agent
// step in message i, reporting whether one was found.
func (m *Model) attachSubLog(i int, line string) bool {
	const maxSubLogs = 8
	steps := m.msgs[i].steps
	for j := len(steps) - 1; j >= 0; j-- {
		if !steps[j].subagent {
			continue
		}
		if len(steps[j].logs) < maxSubLogs {
			steps[j].logs = append(steps[j].logs, sanitize(line))
		}
		return true
	}
	return false
}

func collapse(s string) string {
	return strings.Join(strings.Fields(sanitize(s)), " ")
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

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
