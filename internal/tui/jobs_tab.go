package tui

import (
	"errors"
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/BackendStack21/bodek/internal/client"
)

// ── background jobs tab + lifecycle watcher ──────────────────────────────────
//
// odek v1.38 ships background commands but pushes nothing to WS clients: job
// lifecycle lands in the /api/events ring only (hashes and counters), and the
// agent-side completion notice never leaves the LLM payload. bodek therefore
// watches GET /api/jobs itself: a 10s cadence whenever a session is live —
// the canonical case is a job finishing while the operator reads output —
// stepping up to 3s while the tab is visible. Terminal transitions surface
// as alert-tier notes naming the command and fire the attention layer; the
// tab is the full surface (rows, output detail, stop).

const (
	jobsPollEvery  = 3 * time.Second  // tab visible: live view
	jobsWatchEvery = 10 * time.Second // background: completion watching
)

// jobsFetchedMsg carries one /api/jobs snapshot (tab poll or watcher tick).
type jobsFetchedMsg struct {
	jobs []client.Job
	err  error
}

// jobsTickMsg re-arms the cadence; watch selects the background chain.
type jobsTickMsg struct {
	seq   int
	watch bool
}

// jobOutputMsg carries one output chunk for the detail view.
type jobOutputMsg struct {
	jobID string
	out   client.JobOutput
	err   error
}

// jobStopDoneMsg reports a stop POST outcome (status is the server's
// terminal state, e.g. "killed").
type jobStopDoneMsg struct {
	jobID  string
	status string
	err    error
}

// openJobs opens the jobs tab. The watcher generation is invalidated so the
// tab owns the cadence while visible.
func (m *Model) openJobs() tea.Cmd {
	m.panel = panelJobs
	m.panelSel = 0
	m.panelEdit = panelEditNone
	m.jobsWatchSeq++ // a pending watcher tick would double-fetch
	m.relayout()
	m.refresh()
	if m.jobsOff {
		m.panelMsg = "background commands need odek ≥ v1.38.0"
		m.refresh()
		return nil
	}
	if m.cl == nil || m.sessionID == "" || m.authToken == "" {
		m.panelMsg = "no active session"
		m.refresh()
		return nil
	}
	m.panelMsg = "loading jobs…"
	m.refresh()
	return m.fetchJobs()
}

// fetchJobs snapshots GET /api/jobs. The session identity is captured at
// build time, and every tick rebuilds the command — a session switch rebinds
// the next fetch automatically.
func (m *Model) fetchJobs() tea.Cmd {
	if m.cl == nil || m.sessionID == "" || m.authToken == "" {
		return nil
	}
	cl, sid, tok := m.cl, m.sessionID, m.authToken
	return func() tea.Msg {
		jobs, err := cl.Jobs(sid, tok)
		return jobsFetchedMsg{jobs: jobs, err: err}
	}
}

// kickJobsFetch returns an immediate snapshot fetch for a bg_job push
// frame (odek ≥ v1.40). The REST watcher stays as the fallback; nil when
// the surface is unavailable or there is no live session to fetch with
// (fetchJobs no-ops on both).
func (m *Model) kickJobsFetch() tea.Cmd {
	if m.jobsOff {
		return nil
	}
	return m.fetchJobs()
}

// resetJobsState drops the snapshot and the watcher diff map so the next
// applyJobs re-baselines silently. Jobs are session-scoped: a switch,
// resume, or /new must not treat the next session's pre-existing jobs as
// fresh starts, nor keep the previous session's rows on the tab.
func (m *Model) resetJobsState() {
	m.jobs = nil
	m.jobsPrev = nil
}

// applyJobs stores a snapshot; watcher diffs become alert-tier notes — a
// finished job is actionable, not housekeeping — and terminal transitions
// fire the attention layer (bell / OSC 9). The first successful snapshot
// baselines silently — jobs that predate the attach belong to the tab, not
// the transcript. Returns the attention cmd (nil without a transition).
func (m *Model) applyJobs(jobs []client.Job, err error) tea.Cmd {
	if err != nil {
		if errors.Is(err, client.ErrJobsUnavailable) {
			m.jobsOff = true
			m.jobsWatchSeq++ // never poll this surface again
			if m.panel == panelJobs {
				m.panelMsg = "background commands need odek ≥ v1.38.0"
				m.refresh()
			}
			return nil
		}
		if m.panel == panelJobs {
			// Transient network blips surface here only when the operator
			// is looking; the silent watcher keeps ticking.
			m.panelMsg = "error: " + err.Error()
			m.refresh()
		}
		return nil
	}
	m.jobs = jobs
	var attn tea.Cmd
	if m.jobsPrev == nil {
		m.jobsPrev = make(map[string]string, len(jobs))
		for _, j := range jobs {
			m.jobsPrev[j.ID] = j.Status
		}
		// Jobs vanish only at session end (reaped) — drop their diff state
		// so the map cannot grow over a long-lived serve.
		live := make(map[string]string, len(jobs))
		for _, j := range jobs {
			live[j.ID] = m.jobsPrev[j.ID]
		}
		m.jobsPrev = live
	} else {
		for _, j := range jobs {
			old, seen := m.jobsPrev[j.ID]
			switch {
			case !seen:
				m.addTransientNote("bg · " + jobStatusGlyph(j.Status) + " " + sanitize(j.ID) + " · " + sanitize(j.Command))
			case old == "running" && j.Status != "running":
				// Alert tier + attention: a finished job must survive a
				// glance-away, and the operator opted into knowing.
				m.addNote(jobExitNote(j))
				attn = m.attentionCmd(m.attentionFor(jobAttentionKind(j)))
			}
			m.jobsPrev[j.ID] = j.Status
		}
	}
	if m.panel == panelJobs {
		if m.confirm != confirmStopJob {
			m.panelMsg = "" // keep an armed gate's prompt visible
		}
		m.refresh()
	}
	if m.panelSel >= m.panelLen() {
		m.panelSel = max(m.panelLen()-1, 0)
	}
	return attn
}

// rearmJobs schedules the next fetch: the fast chain while the tab is
// visible, the slow watcher otherwise.
func (m *Model) rearmJobs() tea.Cmd {
	if m.jobsOff {
		return nil
	}
	if m.panel == panelJobs {
		return m.armJobsTab()
	}
	return m.armJobsWatch()
}

// armJobsTab schedules the next tab poll.
func (m *Model) armJobsTab() tea.Cmd {
	m.jobsSeq++
	seq := m.jobsSeq
	return tea.Tick(jobsPollEvery, func(time.Time) tea.Msg {
		return jobsTickMsg{seq: seq, watch: false}
	})
}

// armJobsWatch schedules the next background tick. It no-ops when there is
// nothing to watch: surface absent, no connection, or no live session.
func (m *Model) armJobsWatch() tea.Cmd {
	if m.jobsOff || m.cl == nil || m.sessionID == "" || m.authToken == "" {
		return nil
	}
	m.jobsWatchSeq++
	seq := m.jobsWatchSeq
	return tea.Tick(jobsWatchEvery, func(time.Time) tea.Msg {
		return jobsTickMsg{seq: seq, watch: true}
	})
}

// handleJobsTick fetches only for the newest generation of the chain that
// fired. A live-generation tab tick that lands after the tab closed hands
// the cadence to the background watcher — otherwise a stable session would
// stop seeing start/exit notes the moment the drawer folds.
func (m *Model) handleJobsTick(msg jobsTickMsg) tea.Cmd {
	if msg.watch {
		if msg.seq != m.jobsWatchSeq {
			return nil
		}
	} else if msg.seq != m.jobsSeq {
		return nil // a newer tab tick owns the fast chain
	} else if m.panel != panelJobs {
		return m.armJobsWatch() // baton handoff to the 10s watcher
	}
	return m.fetchJobs()
}

// ── stop flow ────────────────────────────────────────────────────────────────

// stopSelectedJob arms the two-step gate on the highlighted running job.
func (m *Model) stopSelectedJob() tea.Cmd {
	if m.panel != panelJobs || m.panelSel >= len(m.jobs) {
		return m.transientNoteCmd("no job selected")
	}
	j := m.jobs[m.panelSel]
	if j.Status != "running" {
		return m.transientNoteCmd("bg job already finished")
	}
	m.stopJobID = j.ID
	return m.armConfirm(confirmStopJob, sanitize(j.ID))
}

// fireJobStop POSTs the stop for the armed gate. The server answers only
// after the job reaped or hit its kill grace — the dedicated long-budget
// client in the client package carries the wait.
func (m *Model) fireJobStop() tea.Cmd {
	if m.cl == nil || m.stopJobID == "" {
		return nil
	}
	cl, sid, tok := m.cl, m.sessionID, m.authToken
	id := m.stopJobID
	m.stopJobID = ""
	return func() tea.Msg {
		status, err := cl.StopJob(sid, tok, id)
		return jobStopDoneMsg{jobID: id, status: status, err: err}
	}
}

// handleJobStopDone acknowledges the stop and refetches the authoritative
// state (the POST's killed status is confirmed by the next list snapshot).
func (m *Model) handleJobStopDone(msg jobStopDoneMsg) tea.Cmd {
	switch {
	case errors.Is(msg.err, client.ErrJobUnknown):
		return m.transientNoteCmd("bg " + sanitize(msg.jobID) + " · unknown (already gone)")
	case msg.err != nil:
		m.addNote("bg stop failed · " + msg.err.Error())
		m.refresh()
		return m.noticeSweep()
	default:
		m.addTransientNote(jobStatusGlyph(msg.status) + " " + sanitize(msg.jobID) + " " + msg.status)
		return tea.Batch(m.fetchJobs(), m.noticeSweep())
	}
}

// ── output detail ────────────────────────────────────────────────────────────

// openJobDetail expands the selected job into its output view and pulls the
// first chunk. Output is wire content: sanitize() runs at render time.
func (m *Model) openJobDetail() tea.Cmd {
	if m.panelSel >= len(m.jobs) {
		return nil
	}
	m.panelDetail = true
	m.detailScroll = 0
	m.jobsOut = ""
	m.jobsOutCursor = 0
	m.jobsOutID = m.jobs[m.panelSel].ID
	m.refresh()
	return m.fetchJobOutput(0)
}

// fetchJobOutput pulls one chunk; since 0 reads from the top. next_cursor 0
// means end — further output (a still-running job) is fetched by folding the
// detail and reopening, which resets the view to the ring's head.
func (m *Model) fetchJobOutput(since int) tea.Cmd {
	if m.cl == nil || m.jobsOutID == "" {
		return nil
	}
	cl, sid, tok, id := m.cl, m.sessionID, m.authToken, m.jobsOutID
	return func() tea.Msg {
		out, err := cl.JobOutput(sid, tok, id, since)
		return jobOutputMsg{jobID: id, out: out, err: err}
	}
}

func (m *Model) handleJobOutput(msg jobOutputMsg) tea.Cmd {
	if msg.jobID != m.jobsOutID {
		return nil // a stale chunk for a detail the operator already refocused
	}
	if msg.err != nil {
		if errors.Is(msg.err, client.ErrJobUnknown) {
			m.jobsOut = "job ended — output no longer addressable"
		} else {
			m.jobsOut = "output: " + msg.err.Error()
		}
		m.refresh()
		return nil
	}
	m.jobsOutCursor = msg.out.NextCursor
	m.jobsOut += msg.out.Output
	m.refresh()
	return nil
}

// ── rendering ────────────────────────────────────────────────────────────────

// jobRowsRender renders the jobs tab: one row per job — status glyph, id,
// command head, and a compact status/runtime tail.
func (m *Model) jobRowsRender(w int) []string {
	th := m.th
	rows := make([]string, 0, len(m.jobs))
	for i, j := range m.jobs {
		detail := fmt.Sprintf("  %s · %s", sanitize(j.Status), fmtRuntime(j.RuntimeS))
		if j.ExitCode != nil {
			detail += fmt.Sprintf(" · exit %d", *j.ExitCode)
		}
		budget := w - 2 - lipgloss.Width(detail)
		label := jobStatusGlyph(j.Status) + " " + sanitize(j.ID) + " · " + sanitize(j.Command)
		prefix, lab := "  ", th.acItem.Render(truncate(label, budget))
		if i == m.panelSel {
			prefix, lab = "› ", th.acSel.Render(truncate(label, budget))
		}
		rows = append(rows, prefix+lab+th.acDim.Render(detail))
	}
	if len(rows) == 0 {
		rows = append(rows, th.acDim.Render("no background jobs — the agent starts them with bg_start"))
	}
	return rows
}

func jobStatusGlyph(status string) string {
	switch status {
	case "running":
		return "●"
	case "exited":
		return "✓"
	case "failed":
		return "✗"
	case "timeout":
		return "⏱"
	case "killed":
		return "⦸"
	}
	return "·"
}

// jobExitNote is the watcher's terminal-transition note: glyph, id, command
// head, terminal status, exit code when the server reported one, humanized
// runtime.
func jobExitNote(j client.Job) string {
	s := jobStatusGlyph(j.Status) + " " + sanitize(j.ID) + " · " + truncate(sanitize(j.Command), 48)
	if j.ExitCode != nil {
		s += " — " + j.Status + fmt.Sprintf(" %d", *j.ExitCode)
	} else {
		s += " — " + j.Status
	}
	return s + " · " + fmtRuntime(j.RuntimeS)
}

// jobAttentionKind maps a terminal job status to its attention kind.
func jobAttentionKind(j client.Job) attentionKind {
	switch j.Status {
	case "failed", "timeout", "killed":
		return attentionJobFailed
	default:
		return attentionJobDone
	}
}

// fmtRuntime humanizes seconds the way the transcript does durations.
func fmtRuntime(s float64) string {
	if s < 0 {
		s = 0
	}
	d := time.Duration(s * float64(time.Second)).Round(time.Second)
	if h := int(d.Hours()); h > 0 {
		return fmt.Sprintf("%dh%02dm%02ds", h, int(d.Minutes())%60, int(d.Seconds())%60)
	}
	if d >= time.Minute {
		return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%.1fs", s)
}
