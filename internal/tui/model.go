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
	name   string
	arg    string
	result string
	done   bool
}

// message is one entry in the transcript.
type message struct {
	role      role
	content   string // raw text/markdown
	rendered  string // cached glamour render (assistant, finalized)
	steps     []step
	streaming bool
}

// Options carries startup display info into the model.
type Options struct {
	Model   string
	Sandbox bool
	CWD     string
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

	status   string
	notices  []string
	disconn  bool
	quitting bool

	gradRule  string // cached full-width gradient rule
	gradRuleW int
}

// autocomplete holds the @-reference completion popup state.
type autocomplete struct {
	open    bool
	loading bool
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
	return tea.Batch(textarea.Blink, m.sp.Tick, listen(m.events))
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
		if msg.seq != m.ac.seq {
			return m, nil // stale response
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
		case "tab", "enter":
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
		}
		m.sandbox = ev.Sandbox

	case "thinking":
		m.thinking.WriteString(ev.Content)
		m.status = "thinking"

	case "token":
		if i := m.cur(); i >= 0 {
			m.msgs[i].content += ev.Content
			m.msgs[i].streaming = true
		}
		m.status = "responding"

	case "tool_call":
		arg := argPreview(ev.Data)
		if i := m.cur(); i >= 0 {
			m.msgs[i].steps = append(m.msgs[i].steps, step{name: ev.Name, arg: arg})
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
					steps[j].result = linePreview(ev.Data)
					break
				}
			}
		}
		m.lastTool = ""
		m.lastArg = ""

	case "done":
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
		m.addNote("skill · " + strings.TrimSpace(ev.SubType+" "+ev.SkillName))
	case "memory_event":
		m.addNote("memory · " + strings.TrimSpace(ev.SubType+" "+ev.Target))
	case "agent_signal":
		m.addNote("signal · " + strings.TrimSpace(ev.SubType+" "+ev.Detail))
	case "subagent_log":
		m.addNote("subagent · " + strings.TrimSpace(ev.SubType+" "+ev.Name))

	case client.EventDisconnected:
		m.disconn = true
		m.busy = false
		m.status = "disconnected"
		m.addNote("disconnected from odek serve")
		m.refresh()
		return m, nil
	}

	m.refresh()
	return m, listen(m.events)
}

// ── actions ──────────────────────────────────────────────────────────────

func (m *Model) submit() tea.Cmd {
	if m.busy || m.disconn {
		return nil
	}
	text := strings.TrimSpace(m.ta.Value())
	if text == "" {
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
	m.notices = append(m.notices, s)
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
		// Re-render finalized assistant messages at the new width.
		for i := range m.msgs {
			if m.msgs[i].role == roleAsst && !m.msgs[i].streaming {
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

// syncAC re-evaluates the input for an @-reference and kicks off a search.
func (m *Model) syncAC() tea.Cmd {
	q, ok := activeRef(m.ta.Value())
	if !ok {
		if m.ac.open {
			m.closeAC()
		}
		return nil
	}
	if m.ac.open && q == m.ac.query {
		return nil // nothing changed
	}
	m.ac.open = true
	m.ac.loading = true
	m.ac.query = q
	m.ac.sel = 0
	m.ac.seq++
	seq := m.ac.seq
	m.relayout()
	m.refresh()

	cl := m.cl
	return func() tea.Msg {
		items, err := cl.Resources(q, 6)
		if err != nil {
			return acResultMsg{seq: seq, items: nil}
		}
		return acResultMsg{seq: seq, items: items}
	}
}

// acceptCompletion inserts the selected resource reference into the input.
func (m *Model) acceptCompletion() {
	if len(m.ac.items) == 0 {
		m.closeAC()
		return
	}
	item := m.ac.items[m.ac.sel]
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
	for _, key := range []string{"command", "cmd", "path", "file", "pattern", "query", "url"} {
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

// linePreview returns the first meaningful line of tool output, truncated.
func linePreview(data string) string {
	return truncate(collapse(data), 72)
}

func collapse(s string) string {
	return strings.Join(strings.Fields(s), " ")
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
