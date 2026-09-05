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
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

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
	name       string
	arg        string
	result     string // sanitized tool output (multi-line); excerpted at render
	done       bool
	isErr      bool          // the result reads as a failure (tints the status glyph red)
	subagent   bool          // this call delegates to a sub-agent (renders its log tree)
	logs       []string      // nested sub-agent activity, from subagent_log events
	agents     []*agentCard  // live per-task telemetry, from subagent_state frames
	manifest   []taskSlot    // delegate arg parsed at tool-call time: per-task identity
	resultCard *agentResult  // framed result envelope (delegate tools)
	expanded   bool          // user has expanded this step to show full output/logs
	agentSel   int           // focused chip: 0 = none, else 1-based SA number
	started    time.Time     // when the tool_call arrived; zero for resumed history
	dur        time.Duration // wall-clock the call took; 0 until the result lands
	// Render cache for finished steps (peek/detail are expensive to re-parse).
	blockCache     string
	blockRefs      []stepRef
	blockWidth     int
	blockExpanded  bool
	blockExpandAll bool
}

// stepRef maps a rendered transcript line to a specific step for mouse
// hit-testing.
type stepRef struct {
	msgIdx   int
	stepIdx  int
	line     int
	agentIdx int // chip target (task idx); ignored unless x1 > x0
	x0, x1   int // chip hit box in viewport columns; 0,0 = head row
}

// turnItem is one entry in a turn's chronological timeline: a reasoning
// block, a tool call, or a response text segment (one think→reply cycle),
// in arrival order.
type turnItem struct {
	thinking bool          // true = reasoning block
	reply    bool          // true = response text segment
	text     string        // thinking / reply text (stored in full)
	stepIdx  int           // index into msg.steps when a tool call
	open     bool          // reasoning: user wants the full block (live turns auto-open)
	rendered string        // cached glamour render (finalized reply segments)
	started  time.Time     // reasoning: when the block opened; zero on replay
	dur      time.Duration // reasoning: sealed when the cycle yields; 0 while live
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
	role       role
	content    string // raw text/markdown
	rendered   string // cached glamour render (assistant, finalized)
	thinking   string // captured reasoning for this turn (finalized)
	steps      []step
	items      []turnItem // chronological timeline of reasoning blocks and tool calls
	streaming  bool
	stats      *turnStats // finalized-turn telemetry; nil while streaming / for history
	raw        bool       // content is pre-styled; render verbatim, never re-render
	sentAt     time.Time  // user turns: when the prompt was submitted (drives the head's age)
	collapsed  bool       // turn card folded to its head + summary line (c)
	systemWake bool       // server-initiated turn (background-job wake): marker on the card
}

// Options carries startup display info into the model.
type Options struct {
	Model       string
	Sandbox     bool
	CWD         string
	LogPath     string // file the spawned server's stderr is captured to, if any
	OdekVersion string // engine version for the header (empty when attached/unknown)
	Version     string // bodek's own version; drives the startup update check

	// Attention controls (see attention.go): Bell rings the terminal bell
	// when a turn completes or an approval waits (--bel=false mutes);
	// Notify raises desktop notifications via OSC 9 (--notify).
	Bell   bool
	Notify bool

	// Mouse reports that the terminal sends mouse events (--mouse). The
	// queue strip gates its ▲▼✕ controls on it: glyphs without tracking
	// are dead pixels, so mouseless runs get a ^Q hint instead.
	Mouse bool

	// Theme names the startup palette (ember-dark, ember-light,
	// high-contrast, classic). Empty defers to BODEK_THEME, then the
	// settings file — the same order /theme persists into.
	Theme string

	// Verbosity names the startup noise dial (quiet, normal, detailed).
	// Empty keeps the default: normal.
	Verbosity string

	// OnThemeChange persists a runtime /theme switch. Nil (tests, embedded
	// uses) skips persistence; an error surfaces as a note while the
	// switch still applies for this run.
	OnThemeChange func(name string) error

	// Plain selects the linear rendering mode: no alt-screen, append-only
	// scrollback transcript, severity prefixes instead of color (--plain).
	Plain bool

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
	bell   bool // terminal bell on done / approval (--bel)
	notify bool // OSC 9 desktop notifications (--notify)
	plain  bool // linear mode: scrollback transcript, minimal chrome (--plain)

	width, height int
	ready         bool

	vp   viewport.Model
	ta   textarea.Model
	sp   spinner.Model
	glam *glamour.TermRenderer

	msgs      []message
	curIdx    int // index of the streaming assistant message, -1 when idle
	busy      bool
	wakeArmed bool // bg_wake seen but its turn not carded yet: arms the lazy wake marker
	runStart  time.Time
	lastTool  string
	lastArg   string

	approvals     []client.Event   // pending approval queue — odek runs parallel tools, so requests FIFO
	apprDeadlines []time.Time      // per-approval expiry, stamped on arrival (parallel to approvals)
	apprSel       int              // highlighted option in the approval panel
	apprExpanded  bool             // tab: show the full command/description text
	apprTyped     string           // friction mode: the literal word being typed ("approve")
	ac            autocomplete     // @-reference completion state
	pal           palState         // ⌘K command palette — the navigation spine
	skillSuggest  *client.Event    // pending skill suggestion card (skill_event "suggested")
	queue         []string         // prompts typed mid-turn, sent when the turn ends
	mouse         bool             // the terminal reports mouse events (--mouse)
	qfocus        bool             // the queue strip owns the keyboard (ctrl+q)
	qsel          int              // selected strip row while qfocus
	qarm          int              // armed-for-delete row while qfocus (-1 = none)
	lastPrompt    string           // most recent prompt sent — /retry re-sends it
	homePrompt    string           // last user prompt, kept across ^L for the session home
	homeReceipt   string           // last turn's coding receipt, shown on the cleared home
	homeSess      []client.Session // recent sessions for the home dashboard
	homeSessDone  bool             // the current clear's fetch already ran
	homeSessGen   int              // bumped per clear; stale fetches dropped
	focusIdx      int              // transcript cursor: turn head alt+↑/↓ last jumped to (-1 none)

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
	// Detail submode for the management tabs: Enter expands the selected
	// row into a readable block (skill description, full fact text, MCP
	// args, raw config JSON) — the promote/delete gates assume the human
	// can see what they are gating.
	panelDetail  bool // management tab: detail view open
	detailScroll int  // detail view top-line scroll offset
	popover      bool // cockpit overlay (h): server/link/budget/session consolidation

	// Sessions panel state: server-side search plus paged "load more".
	sessQuery   string        // applied search text (server-side substring match)
	sessHasMore bool          // last page was full — more may follow
	panelEdit   panelEditMode // text-entry submode while a panel is open
	panelDraft  string        // the text being edited (search query / rename)
	confirm     confirmKind   // armed destructive action: y fires, any other key disarms
	stopTarget  string        // task_id armed by confirmStopAgent

	agentsReg []client.SubagentEntry // agents tab: sub-agent registry snapshot
	agentsSeq int                    // agents-tab poll generation; stale ticks drop

	// Background jobs tab + lifecycle watcher (odek v1.38+ /api/jobs — the
	// engine pushes nothing for job lifecycle, so bodek watches REST).
	jobs          []client.Job
	jobsPrev      map[string]string // watcher diff state: id → last status
	jobsSeq       int               // tab poll generation
	jobsWatchSeq  int               // 10s watcher generation
	jobsOff       bool              // server predates /api/jobs — stop watching
	jobsOut       string            // detail: fetched output (sanitized at render)
	jobsOutID     string            // detail: which job the output belongs to
	jobsOutCursor int               // detail: next chunk cursor (0 = end)
	stopJobID     string            // job armed by confirmStopJob

	liveTasks map[string]*agentCard // every unfinished card, any turn: stop paths resolve across turns

	// Drawer state: runs polling + the events feed.
	runs            []client.Run
	feed            []client.RuntimeEvent
	runsSeq         int    // poll generation; a stale tick after closing is dropped
	evSessionFilter bool   // events tab: filter the ring to the active session
	evRunFilter     string // events tab: drill-in — the ring of one run (wins over session)

	// Management tab state (memory / skills / tools / config).
	memView     client.MemoryView
	memRows     []memRow
	memTarget   string // add-fact editor target ("user" | "env")
	skills      []client.Skill
	toolRows    []toolRow
	cfgRows     []cfgRow
	shutdownReq bool // shutdown sent — the socket drop is expected, not a failure

	// Cockpit live snapshots (one-shot fetch on open).
	healthSnap *client.Health
	usageSnap  *client.Usage

	sessCtxTok int
	subCosts   map[string]float64 // finished sub-agent final costs by task id (wire v2 P6)
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

	// Planning surface state (see plan.go): WS triggers → debounced REST fetch.
	plan             client.PlanSnapshot // last accepted snapshot
	planVer          int                 // accepted snapshot version (monotonic guard)
	planInit         bool                // any snapshot accepted for this session
	planAvail        planAvailability    // endpoint health tri-state
	planTrig         bool                // a plan tool_call awaits tail-batch pickup
	planResetPending bool                // session changed; reset+refetch at tail
	freshStart       bool                // /new drop: reconnect lands on a fresh session
	planDebSeq       int                 // debounce window sequence
	planReqSeq       int                 // fetch request sequence
	planPollSeq      int                 // armed poll tick sequence

	status     string
	notices    []string
	noticeExp  []time.Time     // parallel to notices; when each one fades
	hintsShown map[string]bool // JIT hints already delivered (hints.go)
	verbosity  int             // noise dial: 0 normal · 1 quiet · 2 detailed
	disconn    bool
	quitting   bool

	gradRule  string // cached full-width gradient rule
	gradRuleW int
	logoCache string // cached gradient logo (width-independent)

	convPrefix       string          // joined finalized prefix (assembled from msgBlocks)
	convPrefixRefs   []stepRef       // step header line index for the cached prefix
	convPrefixTurn   []stepRef       // turn-head line index for the cached prefix (stepIdx -1)
	convPrefixMsgs   []stepRef       // per-message first-line index for the cached prefix
	convCount        int             // messages the prefix covers (-1 = wholesale invalid)
	msgBlocks        []msgBlockCache // per-message render cache for the finalized prefix
	tailClockPending bool            // coalesced live step-clock refresh scheduled
	tailClockSeq     int
	stepLineIndex    []stepRef // full transcript step index for mouse hit-testing
	turnLineIndex    []stepRef // full transcript turn-head index (stepIdx -1) for jump/collapse
	msgLineIndex     []stepRef // full transcript per-message first-line index for find jumps
	find             findState // transcript search bar (alt+f)

	renderPending bool // a coalesced streaming render is scheduled
	renderSeq     int  // bumped per scheduled flush, to drop stale ticks
}

// New builds the initial model.
func New(cl *client.Client, opts Options) *Model {
	// The startup palette: an explicit option wins, else BODEK_THEME, then
	// the persisted settings default — all resolved by themeName().
	if canonical, ok := canonicalTheme(opts.Theme); ok {
		themeOverride = canonical
	}
	th := newTheme()

	ta := textarea.New()
	ta.Placeholder = "Ask odek to build, fix, explore… (⏎ send · ⇧⏎ newline · ↑ scroll · ^P history)"
	// No per-line prompt glyphs: the rounded box frames the composer, and
	// bubbles repeats the prompt on every row — a stacked ❯❯❯ reads as noise.
	ta.Prompt = " "
	ta.ShowLineNumbers = false
	ta.CharLimit = 0
	ta.SetHeight(3)
	ta.FocusedStyle.CursorLine = th.taCursorLine
	ta.Focus()

	sp := spinner.New()
	// A smooth braille spinner reads as fluid motion at small size. With
	// motion disabled (NO_MOTION=1) a single static frame replaces it — same
	// footprint, zero animation.
	sp.Spinner = spinner.Spinner{
		Frames: []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"},
		FPS:    time.Second / 12,
	}
	if !motionEnabled {
		sp.Spinner = spinner.Spinner{Frames: []string{"⠿"}, FPS: time.Second}
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
		focusIdx:     -1,
		qarm:         -1,
		model:        opts.Model,
		sandbox:      opts.Sandbox,
		mouse:        opts.Mouse,
		thinkOn:      false,
		verbosity:    verbosityFrom(opts.Verbosity),
		expandAll:    verbosityFrom(opts.Verbosity) == verbosityDetailed,
		status:       "ready",
		odekVersion:  opts.OdekVersion,
		bodekVersion: opts.Version,
		bell:         opts.Bell,
		notify:       opts.Notify,
		plain:        opts.Plain,
	}
}

func (m *Model) Init() tea.Cmd {
	return tea.Batch(textarea.Blink, m.sp.Tick, listen(m.events),
		m.fetchModels(), m.fetchLimits(), m.checkUpdate(),
		m.armHeartbeat())
}

// pingEvery is the application-level heartbeat cadence (20s). Matches the
// odek ≥ v2.1.0 server keepalive and stays under typical 30–60s proxy idle
// timeouts so a thinking turn that is silent for minutes does not drop the
// socket. The server answers inline — even mid-run — so the ping doubles
// as a liveness probe and the pong refreshes the server snapshot.
const pingEvery = 20 * time.Second

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
		if m.ac.loading {
			m.refresh()
			return m, cmd
		}
		// The status-line spinner animates outside the viewport; only refresh
		// the transcript for live transcript clocks (the streaming head
		// counter, running step timers), and coalesce that lane.
		if m.busy && m.hasLiveStepClock() {
			return m, tea.Batch(cmd, m.queueTailClock())
		}
		return m, cmd

	case tailClockFlushMsg:
		if msg.seq == m.tailClockSeq && m.tailClockPending {
			m.tailClockPending = false
			if m.busy {
				m.refresh()
			}
		}
		return m, nil

	case heartbeatMsg:
		return m, m.handleHeartbeat()

	case renderFlushMsg:
		if msg.seq == m.renderSeq && m.renderPending {
			m.renderPending = false
			m.refreshStreamingReplies()
			m.refresh()
		}
		return m, nil

	case errMsg:
		m.busy = false
		m.status = "error"
		// Close out the turn sendPrompt opened — otherwise the transcript
		// keeps a phantom streaming assistant message with no reply. Same
		// classified card as a server-side error event; the note is the
		// fallback when no turn is open to hold it.
		if i := m.cur(); i >= 0 {
			setTurnMarker(&m.msgs[i], m.errorCard(errText(msg.err)))
		} else {
			m.addNote("error: " + errText(msg.err))
		}
		m.finalize()
		// Pending approvals die with the turn — the same contract done /
		// error / disconnect already document. Leaving them armed captures
		// the keyboard after busy is already false.
		m.approvals = nil
		m.apprDeadlines = nil
		m.resetApprovalInput()
		m.relayout() // the busy status line releases its row
		m.refresh()
		return m, tea.Batch(m.sendQueued(), m.noticeSweep())

	case approvalSendErrMsg:
		// Restore the popped head: the engine never saw the reply. Stay
		// busy and keep the remaining queue — unlike errMsg, this is not
		// a turn-ending write failure.
		reason := errText(msg.err)
		if m.disconn || !m.busy {
			// Disconnect / done / error already dropped the form. A late
			// send-fail must not re-arm a dead gate over the keyboard.
			m.addNote("approval send failed — " + reason)
			m.refresh()
			return m, m.noticeSweep()
		}
		m.approvals = append([]client.Event{msg.ev}, m.approvals...)
		m.apprDeadlines = append([]time.Time{msg.dl}, m.apprDeadlines...)
		m.status = "approval required"
		m.resetApprovalInput()
		m.addNote("approval send failed — " + reason)
		m.relayout()
		m.refresh()
		return m, tea.Batch(m.noticeSweep(), m.approvalSweep())

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

	case sessionUpdatedMsg:
		return m, m.handleSessionUpdated(msg)

	case sessionRenamedMsg:
		return m, m.handleSessionRenamed(msg)

	case sessionExportedMsg:
		return m, m.handleSessionExported(msg)

	case copyResultMsg:
		if msg.err != nil {
			m.addNote("copy failed — " + msg.err.Error())
			m.refresh()
			return m, nil
		}
		return m, m.transientNoteCmd(fmt.Sprintf("copied %d bytes via %s", msg.n, msg.tool))

	case sessionSwitchMsg:
		return m, m.handleSessionSwitch(msg)

	case palSessionsMsg:
		return m, m.handlePalSessions(msg)

	case homeSessionsMsg:
		m.handleHomeSessions(msg)
		return m, nil

	case runsMsg:
		if m.panel == panelRuns {
			m.handleRunsMsg(msg)
			m.refresh()
			return m, m.armRunPoll() // keeps the 3s chain alive while visible
		}
		return m, nil

	case eventsMsg:
		if m.panel == panelEvents {
			m.handleEventsMsg(msg)
			m.refresh()
		}
		return m, nil

	case runsTickMsg:
		return m, m.handleRunsTick(msg)

	case agentsTickMsg:
		return m, m.handleAgentsTick(msg)

	case planMsg:
		return m, m.handlePlanMsg(msg)

	case planDebounceMsg:
		return m, m.handlePlanDebounce(msg)

	case planTickMsg:
		return m, m.handlePlanTick(msg)

	case runActionMsg:
		return m, m.handleRunAction(msg)

	case runApprovalsMsg:
		return m, m.handleRunApprovals(msg)

	case runStartedMsg:
		return m, m.handleRunStarted(msg)

	case cockpitMsg:
		return m, m.handleCockpitMsg(msg)

	case shutdownDoneMsg:
		if msg.err != nil {
			m.addNote("shutdown failed: " + msg.err.Error())
			m.refresh()
		}
		return m, m.noticeSweep()

	case mgmtMsg:
		m.handleMgmtMsg(msg)
		m.refresh()
		if msg.tab == panelAgents && m.panel == panelAgents {
			return m, m.armAgentsPoll() // keeps the 3s chain alive while visible
		}
		return m, nil

	case jobsTickMsg:
		return m, m.handleJobsTick(msg)

	case jobsFetchedMsg:
		attn := m.applyJobs(msg.jobs, msg.err)
		m.refresh()
		return m, tea.Batch(m.rearmJobs(), m.noticeSweep(), attn)

	case jobOutputMsg:
		return m, m.handleJobOutput(msg)

	case jobStopDoneMsg:
		return m, m.handleJobStopDone(msg)

	case mgmtActionMsg:
		if m.panel == msg.tab {
			return m, m.afterMgmtAction(msg)
		}
		return m, nil

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
		return m, m.noticeSweep()

	case stopAgentDoneMsg:
		if msg.err != nil {
			// The stop never left — un-advertise it so the card doesn't
			// claim a stop is in flight.
			if a := m.liveCard(msg.taskID); a != nil {
				a.stopSent = false
			}
			m.addNote("stop failed · " + msg.err.Error())
			m.refresh()
		}
		return m, m.noticeSweep()

	case updateCheckMsg:
		// Silent on error or when already current: the hint only ever nags
		// once, at startup, when a newer release is confirmed.
		if msg.err == nil && update.Newer(msg.latest, m.bodekVersion) {
			m.addNote(fmt.Sprintf("⬆ bodek %s available — run `bodek upgrade`", msg.latest))
			m.refresh()
		}
		return m, m.noticeSweep()

	case eventMsg:
		ev := client.Event(msg)
		mm, cmd := m.handleEvent(ev)
		if pm := mm.(*Model); pm.plain {
			cmd = tea.Batch(cmd, pm.plainPrintCmd(ev))
		}
		if ev.Type == "session" && m.sessionID != "" && m.authToken != "" {
			// (Re)bind the jobs watcher to the live session — connect,
			// reconnect, and session switches all re-fire this frame;
			// stale generations just drop.
			cmd = tea.Batch(cmd, m.armJobsWatch())
		}
		return mm, cmd

	case reconnectMsg:
		return m.handleReconnect(msg)

	case noticeExpireMsg:
		m.pruneNotices(time.Now())
		m.refresh()
		return m, m.noticeSweep() // re-arm while pending notices remain

	case approvalExpireMsg:
		drop := m.handleApprovalExpiry(time.Now())
		m.refresh()
		return m, tea.Batch(drop, m.approvalSweep()) // re-arm while a form is open

	case tea.MouseMsg:
		if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft && m.panel == panelNone && !m.ac.open {
			// Clicking the cockpit (the header's top rows) toggles the
			// server/budget/session popover — WebUI health-popover parity.
			if msg.Y >= 0 && msg.Y < headerHeight {
				if !m.popover {
					cmd := m.openCockpit()
					m.refresh()
					return m, cmd
				}
				m.popover = false
				m.refresh()
				return m, nil
			}
			// The queue strip owns the rows between the status line and the
			// input: its controls act on their row.
			if m.queueStripClick(msg.Y, msg.X) {
				m.refresh()
				return m, nil
			}
			// Viewport content begins below the header (2 rows).
			top := 2
			if msg.Y >= top && msg.Y < top+m.vp.Height {
				line := msg.Y - top + m.vp.YOffset
				// Turn heads toggle their card; step heads toggle details.
				if msgIdx, ok := m.turnAtLine(line); ok {
					m.toggleCollapseAt(msgIdx)
					m.refresh()
					return m, nil
				}
				if msgIdx, stepIdx, agentIdx, ok := m.chipAt(line, msg.X); ok {
					m.selectAgentChip(msgIdx, stepIdx, agentIdx)
					m.refresh()
					return m, nil
				}
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
	m.syncComposer()
	return m, cmd
}

func (m *Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// An armed destructive confirm captures the keyboard first: y fires the
	// pending delete, ANY other key — palette chords, navigation, esc —
	// disarms. Deletes never fire on the keypress that armed them.
	if m.confirm != confirmNone {
		return m.handleConfirmKey(msg)
	}
	// The palette works from every rung of the modality ladder.
	if m.pal.open {
		return m.handlePaletteKey(msg)
	}
	if msg.String() == "ctrl+k" {
		return m, m.togglePalette()
	}

	// ESC folds inspect chrome (drawer / cockpit / find / @ / queue)
	// before the approval form — AGENTS.md stack. Decision keys (A/D/T/⏎)
	// still answer even if a leftover sheet is open.
	if msg.String() == "esc" {
		if m.panel != panelNone {
			return m.handlePanelKey(msg)
		}
		if m.popover {
			return m.handlePopoverKey(msg)
		}
		if m.find.open {
			return m.handleFindKey(msg)
		}
		if m.ac.open {
			return m.handleACKey(msg)
		}
		if m.qfocus {
			return m.queueStripKey(msg)
		}
	}

	// Approval mode captures the keyboard until answered; only the transcript
	// scroll keys pass through to the viewport.
	if m.curApproval() != nil {
		return m.handleApprovalKey(msg)
	}

	// A full-area panel (sessions / models) captures the keyboard while open.
	if m.panel != panelNone {
		return m.handlePanelKey(msg)
	}

	// The cockpit popover pages its card; the run keeps streaming underneath.
	if m.popover {
		return m.handlePopoverKey(msg)
	}

	// The find bar captures keys while open — typed runes filter matches,
	// they never reach the composer.
	if m.find.open {
		return m.handleFindKey(msg)
	}

	// The @-reference popup captures navigation keys while open.
	if m.ac.open {
		return m.handleACKey(msg)
	}

	// A pending skill suggestion answers on alt-chords — never bare keys or
	// ⏎/esc, which belong to the composer.
	if mm, cmd, handled := m.handleSuggestKeys(msg.String()); handled {
		return mm, cmd
	}

	// No bare character keys are ever bound in the composer context — every
	// printable rune must reach the textarea, so prompts can start with any
	// character ("?why", "[TODO]", "reboot…"). Help, jumps, and the
	// disconnected retry live on non-character keys (F1, alt+arrows, ⏎).

	// Queue-strip focus captures everything (except quit) until esc/⏎/ctrl+q
	// returns it to the composer.
	if m.qfocus {
		return m.queueStripKey(msg)
	}

	if cmd := m.handleHomeResumeKey(msg.String()); cmd != nil {
		return m, cmd
	}

	switch msg.String() {
	case "ctrl+c":
		return m, m.armConfirm(confirmQuit, "bodek")
	case "esc":
		// Overlays and inspect chrome close first. Only a bare composer
		// ESC while busy arms the cancel gate.
		if ok, cmd := m.dismissChrome(); ok {
			return m, cmd
		}
		if m.busy {
			return m, m.armConfirm(confirmCancel, "the running turn")
		}
		return m, nil
	case "f1":
		m.showHelp()
		return m, nil
	case "ctrl+r":
		return m, m.openSessions()
	case "ctrl+o":
		return m, m.openModels()
	case "enter":
		return m, m.submit()
	case "shift+enter", "alt+enter", "ctrl+j":
		return m, tea.Batch(m.insertNewline(), m.syncAC())
	case "ctrl+q":
		// Queue-strip focus: a chord, so typing a q is never hijacked.
		// Only latches when there is something queued to manage.
		if m.queueHeld() {
			m.qfocus = true
			m.qsel = 0
			m.relayout()
			m.refresh()
		}
		return m, nil
	case "ctrl+s":
		// Stop one running sub-agent — a chord, so queue typing is never
		// hijacked — behind the same two-step gate as every destructive
		// action. /stop <SA#> targets a specific card.
		if m.busy {
			if id, label, ok := m.firstLiveAgent(); ok {
				return m, m.armStopAgent(id, label)
			}
			return m, m.transientNoteCmd("no running sub-agents")
		}
		return m, nil
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
		// The whole transcript is conversation-scope destructive: arm the
		// same two-step gate the panel row deletes use, idle-only like ^L.
		if !m.busy {
			return m, m.armConfirm(confirmClear, "the conversation")
		}
		return m, nil
	case "ctrl+e":
		// Global details toggle: reasoning previews and every step's full
		// output/logs (including in-flight ones) paint only while this is on;
		// the per-block and per-step toggles still layer on top.
		m.expandAll = !m.expandAll
		state := "off"
		if m.expandAll {
			state = "on"
		}
		cmd := m.transientNoteCmd("details " + state)
		m.invalidateAllMsgBlocks()
		for i := range m.msgs {
			for j := range m.msgs[i].steps {
				clearStepBlockCache(&m.msgs[i].steps[j])
			}
		}
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
	case "ctrl+y":
		// Copy the latest reply — a chord, so typing a y is never hijacked.
		return m, m.copyLastReply()
	case "alt+y":
		// Copy the focused turn — the one alt+↑/↓ last jumped to (falls back
		// to the latest reply). A chord, so typing a y is never hijacked.
		return m, m.copyFocusedTurn()
	case "alt+r":
		// Re-send the last prompt — a chord, so typing an r is never hijacked.
		return m, m.retryLast()
	case "ctrl+g":
		// Jump to the latest output. A ctrl binding, so typing a capital G
		// (even as the first character of a prompt) is never hijacked.
		m.vp.GotoBottom()
		return m, nil
	case "alt+up":
		// Turn-to-turn navigation on arrow chords — never characters, so
		// prompts like "[TODO] fix" or array literals always type.
		return m, m.jumpTurn(false)
	case "alt+down":
		return m, m.jumpTurn(true)
	case "alt+f":
		// Transcript search — a chord, so a bare f always types.
		m.openFind()
		return m, nil
	case "ctrl+f":
		// Fold/unfold the most recent turn card — long sessions scan top-down
		// when the noisy turns collapse to their telemetry head. A chord: bare
		// letters belong to the composer.
		m.toggleCollapseLast()
		return m, nil
	case "tab":
		// Cycle the latest swarm's focused chip when one is on screen;
		// otherwise open/close the most recent reasoning accordion; with
		// neither, toggle the latest step (keyboard parity for
		// click-to-expand).
		if !m.ac.open {
			if !m.cycleAgentFocus() && !m.toggleThinkingLast() {
				m.toggleLastStep()
			}
			m.refresh()
		}
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
	m.syncComposer()
	return m, tea.Batch(cmd, m.syncAC())
}

// ── actions ──────────────────────────────────────────────────────────────

// clearConversation wipes the transcript and the session-scoped telemetry, so
// /stats, the header gauge, and the footer start fresh after a clear instead
// of reporting turns, tools, tokens, and age from before it (mirrors the
// reset done when resuming a session).
func (m *Model) clearConversation() tea.Cmd {
	captureHome(m)
	m.msgs = nil
	m.curIdx = -1
	m.resetMsgBlocks()
	m.convPrefixRefs = nil
	m.find = findState{} // nothing left to search
	m.stepLineIndex = nil
	m.turnStats = nil
	m.toolTotal = 0
	m.sessionStart = time.Time{}
	m.sessCtxTok = 0
	m.sessOutTok = 0
	m.subCosts = nil
	m.winCtxTok = 0
	m.runCtxCum = 0
	m.lastLatency = 0
	m.refresh()
	// Fresh dashboard snapshot per clear: drop the stale rows, re-arm the
	// once-guard, and bump the generation so an in-flight older fetch can
	// never stamp this clear's home.
	m.homeSess = nil
	m.homeSessDone = false
	m.homeSessGen++
	return m.fetchHomeSessions()
}

// startFreshSession tears down the current conversation AND its server-side
// session: /clear only wipes the local view, while the session (history,
// context) keeps accumulating on the server. Dropping sessionID/authToken
// before the forced redial leaves adoptSession nothing to re-adopt, so the
// fresh connection stays sessionless — and odek mints a brand-new session
// (new ID, empty history and memory buffer) on the connection's first prompt.
// The old session stays on disk, resumable via /sessions.
func (m *Model) startFreshSession() tea.Cmd {
	homeFetch := m.clearConversation()
	m.sessionID = ""
	m.authToken = ""
	m.pendModel = m.model // the new session re-asserts the active model
	m.resetPlanState()
	m.resetJobsState()
	clearHome(m)
	m.freshStart = true
	m.refresh() // drop the session-home snapshot clearConversation just painted
	cl := m.cl
	return tea.Batch(homeFetch, func() tea.Msg {
		if cl != nil {
			_ = cl.Close() // the drop runs the standard disconnect→reconnect flow
		}
		return nil
	})
}

// curApproval returns the head of the approval queue, or nil when empty.
func (m *Model) curApproval() *client.Event {
	if len(m.approvals) == 0 {
		return nil
	}
	return &m.approvals[0]
}

// ── helpers ────────────────────────────────────────────────────────────────

// resolveMaxContext sets m.maxContext from an exact /api/models id match.
// No match leaves 0, which hides the gauge rather than guessing a window.
func (m *Model) resolveMaxContext() {
	m.maxContext = 0
	for _, md := range m.models {
		if md.ID == m.model {
			m.maxContext = md.MaxContext
			return
		}
	}
}

// fetchModels loads the advertised model catalog at startup so the
// context-window gauge knows the active model's budget without the picker
// ever being opened. The same list is the picker source (^O).
func (m *Model) fetchModels() tea.Cmd {
	cl := m.cl
	return func() tea.Msg {
		items, err := cl.Models()
		return modelsMsg{items: items, err: err}
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
	m.syncComposer() // the new width re-wraps content — refit the box
	m.gradRule = ""  // invalidate cached rule for the new width
	m.invalidateAllMsgBlocks()
	for i := range m.msgs {
		for j := range m.msgs[i].steps {
			clearStepBlockCache(&m.msgs[i].steps[j])
		}
	}
	m.relayout()

	wrap := w - 6
	if wrap < 20 {
		wrap = 20
	}
	if r, err := glamour.NewTermRenderer(
		glamour.WithStyles(answerGlamourStyle()),
		glamour.WithWordWrap(wrap),
	); err == nil {
		m.glam = r
		// Re-render finalized assistant messages at the new width. Pre-styled
		// cards (raw) are point-in-time snapshots and must never go through
		// glamour, which would mangle their embedded ANSI.
		for i := range m.msgs {
			if m.msgs[i].role != roleAsst || m.msgs[i].raw {
				continue
			}
			if m.msgs[i].streaming {
				for j := range m.msgs[i].items {
					if m.msgs[i].items[j].reply && strings.TrimSpace(m.msgs[i].items[j].text) != "" {
						m.msgs[i].items[j].rendered = m.render(m.msgs[i].items[j].text)
					}
				}
				continue
			}
			m.msgs[i].rendered = m.render(m.msgs[i].content)
			for j := range m.msgs[i].items {
				if m.msgs[i].items[j].reply {
					m.msgs[i].items[j].rendered = m.render(m.msgs[i].items[j].text)
				}
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
	sheet := m.drawerLayoutBudget()
	vpH := m.height - headerHeight - footerHeight - m.inputAreaHeight() - sheet
	// Degradation ladder, step 1: when the layout doesn't fit, give the
	// transcript rows back by shrinking the composer first — a one-visible-
	// row terminal beats a View that never fits (judge-5 E1).
	if vpH < 1 && m.ta.Height() > 1 {
		over := 1 - vpH
		m.ta.SetHeight(max(1, m.ta.Height()-over))
		sheet = m.drawerLayoutBudget()
		vpH = m.height - headerHeight - footerHeight - m.inputAreaHeight() - sheet
	}
	// Ladder, restore: with room again the composer returns to its
	// content-fitted height (syncComposer's bounds — not the old fixed 3).
	if vpH >= 4 && m.ta.Height() < m.desiredComposerHeight() {
		m.ta.SetHeight(m.desiredComposerHeight())
		sheet = m.drawerLayoutBudget()
		vpH = m.height - headerHeight - footerHeight - m.inputAreaHeight() - sheet
	}
	// Floor of 1, not 3: below the old floor the View was guaranteed taller
	// than the terminal (permanent scroll-jitter in alt-screen).
	if vpH < 1 {
		vpH = 1
	}
	m.vp.Width = m.width
	m.vp.Height = vpH
}

// inputAreaHeight is the number of rows below the transcript viewport — the
// input area plus the busy status line when it shows — so the viewport
// shrinks by exactly the right amount and the footer never moves.
func (m *Model) inputAreaHeight() int {
	h := m.ta.Height() + 2 // composer box: text rows + top/bottom border
	if m.curApproval() != nil {
		h += lineCount(m.approvalPanel()) // boxed card sits above the composer
	}
	if m.statusLineVisible() {
		h += 2 // busy status line + blank separator row above the input box
	}
	if m.pal.open {
		h += m.palHeight()
	}
	if m.ac.open {
		h += m.ac.height()
	}
	if m.find.open {
		h++ // the one-row search strip above the input box
	}
	h += m.shelfHeight()
	h += m.queueStripHeight()
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

// sanitize strips terminal-hostile content from untrusted text before it is
// rendered. Agent output — streamed tokens, tool results, file contents,
// resumed transcripts — is attacker-influenced; raw control bytes (C0, C1,
// notably ESC 0x1b and the 8-bit CSI/OSC forms) could drive ANSI/OSC escapes
// that move the cursor, clear the screen, or exfiltrate via OSC 52. Invisible
// and direction-forcing characters (bidi overrides, zero-width, soft hyphen,
// Unicode line separators) are dropped so displayed text can never disagree
// with itself. Newlines are kept; tabs expand to four spaces so width math
// never meets an ambiguous tab stop. Readable text survives untouched.
// Tabs are kept — copied prose (Makefiles, gofmt output) must paste intact;
// display paths expand them where cell math happens (truncate, clampLines).
func sanitize(s string) string {
	if !strings.ContainsFunc(s, needsSanitize) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if !needsSanitize(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// needsSanitize is the fast-path predicate: whether the rune is anything
// sanitize would rewrite.
func needsSanitize(r rune) bool {
	return isControl(r) || isInvisible(r)
}

// isControl reports whether r is a control character we strip from untrusted
// text: C0 controls, C1 controls (whose 8-bit CSI/OSC forms some emulators
// execute), and DEL — except newline and tab, which sanitize handles.
func isControl(r rune) bool {
	if r == '\n' || r == '\t' {
		return false
	}
	return r < 0x20 || (r >= 0x7f && r < 0xa0)
}

// isInvisible reports whether r is a zero-width, direction-forcing, or
// line-separating character that must never reach the display: it is either
// unseen payload or it can visually reverse text (a bidi override can dress
// a hostile approval command up as something harmless).
func isInvisible(r rune) bool {
	switch {
	case r == 0xad: // soft hyphen
		return true
	case r == 0x200b, r == 0x200e, r == 0x200f: // ZWSP, LRM, RLM
		return true
	case r >= 0x2028 && r <= 0x202e: // line/paragraph separators, bidi overrides
		return true
	case r == 0x2060: // word joiner
		return true
	case r >= 0x2066 && r <= 0x2069: // bidi isolates
		return true
	case r == 0xfeff: // BOM / zero-width no-break space
		return true
	}
	return false
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

// truncate cuts s to at most n display columns, marking the cut with an
// ellipsis. Budgets across the TUI are column counts derived from
// lipgloss.Width, so the cut is width-aware: CJK, emoji, and ZWJ sequences
// count their real cell width and graphemes are never split mid-cluster.
// Styled input is not expected — call sites truncate before styling.
func truncate(s string, n int) string {
	if n < 1 {
		return "" // no room even for the ellipsis (very narrow terminal)
	}
	if strings.Contains(s, "\t") {
		s = strings.ReplaceAll(s, "\t", "    ") // tabs are columns 1–8; width math needs a fixed pitch
	}
	if lipgloss.Width(s) <= n {
		return s
	}
	return ansi.Truncate(s, n, "…")
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
	clearStepBlockCache(&steps[stepIdx])
	m.invalidateMsgBlock(msgIdx)
}

func (m *Model) selectAgentChip(msgIdx, stepIdx, agentIdx int) {
	if msgIdx < 0 || msgIdx >= len(m.msgs) {
		return
	}
	steps := m.msgs[msgIdx].steps
	if stepIdx < 0 || stepIdx >= len(steps) {
		return
	}
	s := &steps[stepIdx]
	if s.focusedIdx() == agentIdx {
		s.clearAgentFocus()
	} else {
		s.setAgentFocus(agentIdx)
	}
	clearStepBlockCache(s)
	m.invalidateMsgBlock(msgIdx)
}

// cycleAgentFocus walks the latest sub-agent chip strip: none → first →
// next → none. Returns false when the turn has no swarm to focus.
func (m *Model) cycleAgentFocus() bool {
	for i := len(m.msgs) - 1; i >= 0; i-- {
		if m.msgs[i].role != roleAsst {
			continue
		}
		for j := len(m.msgs[i].steps) - 1; j >= 0; j-- {
			s := &m.msgs[i].steps[j]
			chips := s.agentChips()
			if len(chips) == 0 {
				continue
			}
			cur := s.focusedIdx()
			next := chips[0].idx
			if cur >= 0 {
				next = -1
				for k, c := range chips {
					if c.idx == cur && k+1 < len(chips) {
						next = chips[k+1].idx
						break
					}
				}
			}
			s.setAgentFocus(next)
			clearStepBlockCache(s)
			m.invalidateMsgBlock(i)
			return true
		}
		return false
	}
	return false
}

// toggleCollapseLast folds/unfolds the most recent assistant turn card — the
// one the reader is most likely looking at.
func (m *Model) toggleCollapseLast() {
	for i := len(m.msgs) - 1; i >= 0; i-- {
		msg := m.msgs[i]
		if msg.role != roleAsst || msg.raw || msg.streaming {
			continue
		}
		if len(msg.steps) == 0 && strings.TrimSpace(msg.content) == "" && strings.TrimSpace(msg.thinking) == "" {
			continue // nothing to fold
		}
		m.msgs[i].collapsed = !m.msgs[i].collapsed
		m.invalidateMsgBlock(i)
		m.refresh()
		return
	}
}

// toggleCollapseAt folds/unfolds the turn card at a transcript message index
// (mouse click on a turn head).
func (m *Model) toggleCollapseAt(msgIdx int) {
	if msgIdx < 0 || msgIdx >= len(m.msgs) || m.msgs[msgIdx].raw || m.msgs[msgIdx].streaming {
		return
	}
	m.msgs[msgIdx].collapsed = !m.msgs[msgIdx].collapsed
	m.invalidateMsgBlock(msgIdx)
}

// toggleThinkingLast opens/closes the most recent reasoning accordion block
// (tab): manual opens persist across the renderer's auto-collapse. Reports
// whether a block was found, so tab can fall through to the last step.
func (m *Model) toggleThinkingLast() bool {
	for i := len(m.msgs) - 1; i >= 0; i-- {
		if m.msgs[i].role != roleAsst {
			continue
		}
		for j := len(m.msgs[i].items) - 1; j >= 0; j-- {
			if m.msgs[i].items[j].thinking && strings.TrimSpace(m.msgs[i].items[j].text) != "" {
				m.msgs[i].items[j].open = !m.msgs[i].items[j].open
				m.invalidateMsgBlock(i)
				return true
			}
		}
		return false
	}
	return false
}

// toggleLastStep flips the most recent step's expansion — tab's final
// fallback, keyboard parity for click-to-expand aimed at the step the user
// just watched finish. Same latest-assistant-message scope as
// toggleThinkingLast; false when there is no step to toggle.
func (m *Model) toggleLastStep() bool {
	for i := len(m.msgs) - 1; i >= 0; i-- {
		if m.msgs[i].role != roleAsst {
			continue
		}
		if n := len(m.msgs[i].steps); n > 0 {
			clearStepBlockCache(&m.msgs[i].steps[n-1])
			m.msgs[i].steps[n-1].expanded = !m.msgs[i].steps[n-1].expanded
			m.invalidateMsgBlock(i)
			return true
		}
		return false
	}
	return false
}

// jumpTurn scrolls to the previous ([) or next (]) assistant turn head, so
// long sessions read turn-by-turn instead of line-by-line. Every jump and
// every boundary reports its landing — the copy target (alt+y) must be
// verifiable on screen.
func (m *Model) jumpTurn(next bool) tea.Cmd {
	if len(m.turnLineIndex) == 0 {
		return nil
	}
	cur := m.vp.YOffset
	var target = -1
	if next {
		// +1: a previous jump parks the view one line above a head — that
		// head must not count as "next" again, or the jump is a no-op.
		for _, r := range m.turnLineIndex {
			if r.line > cur+1 {
				target = r.line
				break
			}
		}
		if target < 0 {
			return m.transientNoteCmd("already at the last turn")
		}
	} else {
		for i := len(m.turnLineIndex) - 1; i >= 0; i-- {
			if m.turnLineIndex[i].line < cur {
				target = m.turnLineIndex[i].line
				break
			}
		}
		if target < 0 {
			m.vp.GotoTop()
			m.focusTurnAt(m.turnLineIndex[0].line)
			return m.transientNoteCmd(fmt.Sprintf("turn 1/%d", len(m.turnLineIndex)))
		}
	}
	if target > 0 {
		target-- // land with one line of context above the head
	}
	m.vp.SetYOffset(target)
	m.focusTurnAt(target + 1) // the head line we landed under
	m.refresh()
	for i, r := range m.turnLineIndex {
		if r.line == target+1 { // key off the landed line, not prior focus state
			return m.transientNoteCmd(fmt.Sprintf("turn %d/%d — alt+y copies this reply", i+1, len(m.turnLineIndex)))
		}
	}
	// The focus did not land (degenerate layout) — never claim alt+y will
	// copy something it won't.
	return m.transientNoteCmd("top of transcript — alt+y copies the latest reply")
}

// focusTurnAt records the turn head at the given viewport line as the copy
// target (alt+y). A no-op when no head sits on that line.
func (m *Model) focusTurnAt(line int) {
	if idx, ok := m.turnAtLine(line); ok {
		m.focusIdx = idx
	}
}

// scrollToMessage parks the viewport at a message's turn head (one line of
// context above it), bottom when the turn isn't indexed yet (in-flight).
func (m *Model) scrollToMessage(msgIdx int) {
	for _, r := range m.turnLineIndex {
		if r.msgIdx == msgIdx {
			off := r.line
			if off > 0 {
				off--
			}
			m.vp.SetYOffset(off)
			return
		}
	}
	m.vp.GotoBottom()
}

// turnAtLine maps a viewport content line to a turn head (stepIdx -1).
func (m *Model) turnAtLine(line int) (msgIdx int, ok bool) {
	for _, r := range m.turnLineIndex {
		if r.line == line {
			return r.msgIdx, true
		}
	}
	return 0, false
}

// stepAtLine maps a viewport content line to a step for mouse hit-testing,
// matching only the step's own header line (the chevron row) — a click on
// detail lines or prose below a step must not toggle it.
func (m *Model) stepAtLine(line int) (msgIdx, stepIdx int, ok bool) {
	for i := range m.stepLineIndex {
		r := m.stepLineIndex[i]
		if r.line > line {
			break
		}
		if r.line == line && r.x1 <= r.x0 {
			return r.msgIdx, r.stepIdx, true
		}
	}
	return
}

func (m *Model) chipAt(line, x int) (msgIdx, stepIdx, agentIdx int, ok bool) {
	for i := range m.stepLineIndex {
		r := m.stepLineIndex[i]
		if r.line != line || r.x1 <= r.x0 {
			continue
		}
		if x >= r.x0 && x < r.x1 {
			return r.msgIdx, r.stepIdx, r.agentIdx, true
		}
	}
	return
}
