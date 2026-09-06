package tui

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/BackendStack21/bodek/internal/client"
)

// defaultApprovalTTL mirrors odek's interactive approval lifetime: the
// wsApprover waits 60s for a response before failing the prompt with
// "approval timeout" (and the agent moves on to an alternative path). The
// protocol frame does not carry the value yet, so this constant tracks the
// server default; a frame-provided timeout_seconds always wins once odek
// sends it.
const defaultApprovalTTL = 60 * time.Second

// approvalUrgentSecs is how close to the deadline the countdown turns
// urgent — the last stretch in which a decision is still realistic.
const approvalUrgentSecs = 10

// approvalExpireMsg fires once a second while an approval form is open:
// each tick re-renders the countdown, drops expired queue heads, and re-arms
// itself. The chain stops when nothing is pending (mirrors noticeSweep).
type approvalExpireMsg struct{}

// approvalTTL resolves the approval lifetime: the frame-provided value when
// odek sends one, otherwise the server's interactive default.
func approvalTTL(ev client.Event) time.Duration {
	if ev.TimeoutSeconds > 0 {
		return time.Duration(ev.TimeoutSeconds) * time.Second
	}
	return defaultApprovalTTL
}

// stampApprovalDeadline records when the newly queued request dies. The
// server's clock starts when it sends the frame, so the arrival instant is
// the closest client-side approximation (single-digit ms skew locally).
func (m *Model) stampApprovalDeadline(ev client.Event) {
	m.apprDeadlines = append(m.apprDeadlines, time.Now().Add(approvalTTL(ev)))
}

// apprSecondsLeft is the queue head's remaining lifetime in whole seconds
// (ceiling), or 0 when unstamped or already expired — the countdown renders
// only while there is time left to show.
func (m *Model) apprSecondsLeft() int {
	if len(m.apprDeadlines) == 0 {
		return 0
	}
	d := time.Until(m.apprDeadlines[0])
	if d <= 0 {
		return 0
	}
	return int((d + time.Second - 1) / time.Second)
}

// approvalSweep arms the next expiry tick while the form is open; nil when
// nothing is pending, so the timer chain stops itself.
func (m *Model) approvalSweep() tea.Cmd {
	if len(m.approvals) == 0 {
		return nil
	}
	return tea.Tick(time.Second, func(time.Time) tea.Msg {
		return approvalExpireMsg{}
	})
}

// handleApprovalExpiry prunes every entry whose deadline has passed — not
// just the head: with per-frame timeout_seconds (future odek) a short-TTL
// request can expire mid-queue while the head is still alive. The engine
// already failed those prompts ("approval timeout") and moved on, so a
// lingering form only invites approving a dead request. Teardown mirrors
// answer() only when the head actually changed — a surviving head keeps its
// selection state — plus a strip notice and a plain scrollback line so the
// autoclose is never silent. A fully emptied queue also parks focus on the
// latest transcript message (viewport + focusIdx).
func (m *Model) handleApprovalExpiry(now time.Time) tea.Cmd {
	var oldHead client.Event
	hadHead := len(m.approvals) > 0
	if hadHead {
		oldHead = m.approvals[0]
	}

	kept := m.approvals[:0]
	keptDL := m.apprDeadlines[:0]
	dropped := 0
	for i, a := range m.approvals {
		var dl time.Time
		if i < len(m.apprDeadlines) {
			dl = m.apprDeadlines[i]
		}
		if !dl.IsZero() && !now.Before(dl) {
			dropped++
			continue
		}
		kept = append(kept, a)
		keptDL = append(keptDL, dl)
	}
	m.approvals = kept
	m.apprDeadlines = keptDL

	if dropped == 0 {
		return nil
	}
	// Operator-facing: the card they were staring at just vanished. Use
	// transientNoteCmd so quiet does not swallow it and so the sweep
	// arms from Update (addTransientNote would leave a sticky idle note).
	const note = "approval expired · odek will find an alternative"
	cmds := []tea.Cmd{m.transientNoteCmd(note)}
	if len(m.approvals) > 0 {
		m.setRunStatus("approval required")
	} else if m.busy {
		m.setRunStatus("thinking") // engine continues on an alternative path
	} else {
		m.setRunStatus("ready")
	}
	if len(m.approvals) == 0 || m.approvals[0].ID != oldHead.ID {
		m.resetApprovalInput()
		m.relayout()
	}
	if len(m.approvals) == 0 {
		// The card is gone — jump to the latest transcript message so the
		// operator lands on the turn the engine continues, not the scroll
		// position they held while the form was up (refresh only sticks
		// when already at the bottom).
		m.focusLatestTranscript()
	}
	if m.plain {
		cmds = append(cmds, tea.Println("· "+note))
	}
	return tea.Batch(cmds...)
}

// focusLatestTranscript parks the copy cursor and viewport on the newest
// transcript message. Used when the approval form autocloses: the card
// released the keyboard, and the eyes should follow the live turn.
func (m *Model) focusLatestTranscript() {
	if n := len(m.msgs); n > 0 {
		m.focusIdx = n - 1
	}
	m.vp.GotoBottom()
}
