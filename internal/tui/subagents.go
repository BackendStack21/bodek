package tui

// Sub-agent live telemetry: the agentCard model, subagent_state ingestion,
// and card rendering. Driven by the per-task lifecycle frames odek serve
// v1.30+ emits (started → active → finished); every field is wire-derived
// and sanitized on ingest, and cards render only for tasks that report.

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/BackendStack21/bodek/internal/client"
)

// agentCard is one delegated task's live telemetry inside a sub-agent step.
type agentCard struct {
	taskID   string
	idx      int
	phase    string // started | active | finished
	status   string // running | success | partial | error | cancelled | timeout
	step     int
	tool     string
	iters    int
	tokens   int
	durS     float64
	stopSent bool // subagent_cancel sent; terminal frame still pending
}

// finished reports whether the card reached a terminal state.
func (a *agentCard) finished() bool { return a.phase == "finished" }

// glyph picks the status glyph: live ⟳, then the terminal set mirroring
// odek's status framing (user cancel and deadline timeout never conflate).
func (a *agentCard) glyph() string {
	if !a.finished() {
		return "⟳"
	}
	switch a.status {
	case "success":
		return "✓"
	case "partial":
		return "◐"
	case "error":
		return "✗"
	case "cancelled":
		return "⊘"
	case "timeout":
		return "⏱"
	default:
		return "•"
	}
}

// failed reports terminal states that render in the error style.
func (a *agentCard) failed() bool {
	switch a.status {
	case "error", "cancelled", "timeout":
		return true
	}
	return false
}

// card finds a step's agent card by task id.
func (s *step) card(taskID string) *agentCard {
	for _, a := range s.agents {
		if a.taskID == taskID {
			return a
		}
	}
	return nil
}

// attachSubState routes a subagent_state frame into the message's in-flight
// sub-agent step, upserting the task's card in place. It returns false when
// there is no live sub-agent step to attach to — the caller falls back to a
// transient notice (resumed turn, idle, stray frame). Idempotent: replayed
// frames overwrite the same card.
func (m *Model) attachSubState(i int, ev client.Event) bool {
	if ev.TaskID == "" {
		return false
	}
	msg := &m.msgs[i]
	for j := len(msg.steps) - 1; j >= 0; j-- {
		if !msg.steps[j].subagent || msg.steps[j].done {
			continue
		}
		s := &msg.steps[j]
		card := s.card(ev.TaskID)
		if card == nil {
			card = &agentCard{taskID: ev.TaskID, idx: ev.TaskIdx, status: "running"}
			s.agents = append(s.agents, card)
		}
		if ev.Phase != "" {
			card.phase = ev.Phase
		}
		if ev.Status != "" {
			card.status = ev.Status
		}
		card.step = ev.Step
		if ev.Tool != "" {
			card.tool = collapse(ev.Tool)
		}
		card.iters = ev.Iterations
		card.tokens = ev.TokensUsed
		card.durS = ev.DurationSeconds
		return true
	}
	return false
}

// agentCardLine renders one card: glyph + label + the live telemetry tail —
// "⟳ SA1 · step 7 · read main.go · 5 it · 3.2k tok" while running,
// "✓ SA2 · 6 it · 3.2k tok · 4.2s" once terminal.
func agentCardLine(a *agentCard) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s SA%d", a.glyph(), a.idx+1)
	if !a.finished() {
		if a.step > 0 {
			fmt.Fprintf(&b, " · step %d", a.step)
		}
		if a.tool != "" {
			b.WriteString(" · " + a.tool)
		}
	} else if a.status != "" && a.status != "success" {
		b.WriteString(" · " + a.status)
	}
	if !a.finished() && a.stopSent {
		b.WriteString(" · stop sent")
	}
	if a.iters > 0 {
		fmt.Fprintf(&b, " · %d it", a.iters)
	}
	if a.tokens > 0 {
		b.WriteString(" · " + human(a.tokens) + " tok")
	}
	if a.durS > 0 {
		b.WriteString(" · " + formatStepDur(time.Duration(a.durS*float64(time.Second))))
	}
	return b.String()
}

// agentRollup is the collapsed-head aggregate: "1/2 agents · 6.3k tok".
func agentRollup(s *step) string {
	if len(s.agents) == 0 {
		return ""
	}
	done, tokens := 0, 0
	for _, a := range s.agents {
		if a.finished() {
			done++
		}
		tokens += a.tokens
	}
	rollup := fmt.Sprintf("%d/%d agents", done, len(s.agents))
	if tokens > 0 {
		rollup += " · " + human(tokens) + " tok"
	}
	return rollup
}

// stateNoticeLine renders a subagent_state frame that had nowhere to attach
// as a transient notice line.
func stateNoticeLine(ev client.Event) string {
	parts := []string{fmt.Sprintf("state SA%d", ev.TaskIdx+1), ev.Phase, ev.Status}
	if ev.Step > 0 {
		parts = append(parts, fmt.Sprintf("step %d", ev.Step))
	}
	if ev.TokensUsed > 0 {
		parts = append(parts, human(ev.TokensUsed)+" tok")
	}
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, " · ")
}

// ── per-agent stop (subagent_cancel) ─────────────────────────────────────────

// stopAgentDoneMsg reports a failed WS stop so the failure isn't silent;
// success stays silent — the terminal subagent_state settles the card.
type stopAgentDoneMsg struct {
	taskID string
	err    error
}

// armStopAgent arms the stop gate for one task id.
func (m *Model) armStopAgent(taskID, label string) tea.Cmd {
	m.stopTarget = taskID
	return m.armConfirm(confirmStopAgent, "sub-agent "+label)
}

// stopAgent sends the WS subagent_cancel for one task. Self-guarding: a
// card that settled while the gate sat armed degrades to a notice, and the
// terminal state still comes exclusively from subagent_state.
func (m *Model) stopAgent(taskID string) tea.Cmd {
	if m.cl == nil || m.sessionID == "" || taskID == "" {
		return m.transientNoteCmd("nothing to stop")
	}
	card := m.liveCard(taskID)
	if card == nil {
		return m.transientNoteCmd("sub-agent already finished")
	}
	card.stopSent = true
	m.refresh()
	cl, sid, tok := m.cl, m.sessionID, m.authToken
	return func() tea.Msg {
		if err := cl.SendSubagentCancel(sid, tok, taskID); err != nil {
			return stopAgentDoneMsg{taskID: taskID, err: err}
		}
		return nil // the terminal subagent_state settles the card
	}
}

// firstLiveAgent picks the default stop target: the expanded sub-agent
// step's first live card, else the first live card of any in-flight step.
func (m *Model) firstLiveAgent() (id, label string, ok bool) {
	if i := m.cur(); i >= 0 {
		for pass := 0; pass < 2; pass++ {
			for j := range m.msgs[i].steps {
				s := &m.msgs[i].steps[j]
				if !s.subagent || s.done || (pass == 0 && !s.expanded && !m.expandAll) {
					continue
				}
				for _, a := range s.agents {
					if !a.finished() {
						return a.taskID, fmt.Sprintf("SA%d", a.idx+1), true
					}
				}
			}
		}
	}
	return "", "", false
}

// liveAgents collects the live cards of the current turn in card order.
func (m *Model) liveAgents() []*agentCard {
	var out []*agentCard
	if i := m.cur(); i >= 0 {
		for j := range m.msgs[i].steps {
			s := &m.msgs[i].steps[j]
			if !s.subagent || s.done {
				continue
			}
			for _, a := range s.agents {
				if !a.finished() {
					out = append(out, a)
				}
			}
		}
	}
	return out
}

// liveCard finds an unfinished card by task id across the current turn.
func (m *Model) liveCard(taskID string) *agentCard {
	for _, a := range m.liveAgents() {
		if a.taskID == taskID {
			return a
		}
	}
	return nil
}

// stopByLabel resolves /stop <SA#> (or a bare number) to a live card and
// arms the stop gate. With no argument it lists the live labels.
func (m *Model) stopByLabel(args string) tea.Cmd {
	live := m.liveAgents()
	if len(live) == 0 {
		return m.transientNoteCmd("no running sub-agents")
	}
	ref := strings.ToLower(strings.TrimSpace(args))
	ref = strings.TrimPrefix(ref, "sa")
	n := 0
	if v, err := strconv.Atoi(ref); err == nil {
		n = v
	}
	if n == 0 {
		labels := make([]string, 0, len(live))
		for _, a := range live {
			labels = append(labels, fmt.Sprintf("SA%d", a.idx+1))
		}
		return m.transientNoteCmd("running: " + strings.Join(labels, ", ") + " — /stop <#>")
	}
	for _, a := range live {
		if a.idx+1 == n {
			return m.armStopAgent(a.taskID, fmt.Sprintf("SA%d", a.idx+1))
		}
	}
	return m.transientNoteCmd(fmt.Sprintf("SA%d is not running", n))
}

// ── structured result card (framed results, odek M0) ─────────────────────────

// agentResult is the framed result a sub-agent returns inside its parent's
// tool_result (headline capped at 2048 by serve): status, summary, changed
// files, and usage — parsed tolerantly, rendered as a card in the details.
type agentResult struct {
	status  string
	summary string
	files   []string
	tokens  int
	iters   int
}

// parseAgentResult extracts the framed-result envelope from delegate tool
// output. Tolerant by design: it parses only a JSON object carrying both
// status and summary — anything else (prose, partial JSON, older servers)
// returns nil and the generic preview stays.
func parseAgentResult(data string) *agentResult {
	obj := firstJSONObject(data)
	if obj == "" {
		return nil
	}
	var env struct {
		Status       string   `json:"status"`
		Summary      string   `json:"summary"`
		FilesChanged []string `json:"files_changed"`
		TokensUsed   int      `json:"tokens_used"`
		Iterations   int      `json:"iterations"`
	}
	if err := json.Unmarshal([]byte(obj), &env); err != nil {
		return nil
	}
	if env.Status == "" || env.Summary == "" {
		return nil
	}
	r := &agentResult{status: env.Status, summary: collapse(env.Summary), tokens: env.TokensUsed, iters: env.Iterations}
	for _, f := range env.FilesChanged {
		if f = collapse(f); f != "" {
			r.files = append(r.files, f)
		}
	}
	return r
}

// firstJSONObject returns the first balanced top-level JSON object in s —
// the framed result may be followed by artifact metadata lines.
func firstJSONObject(s string) string {
	start := strings.IndexByte(s, '{')
	if start < 0 {
		return ""
	}
	depth, inStr, esc := 0, false, false
	for i := start; i < len(s); i++ {
		c := s[i]
		if inStr {
			switch {
			case esc:
				esc = false
			case c == '\\':
				esc = true
			case c == '"':
				inStr = false
			}
			continue
		}
		switch c {
		case '"':
			inStr = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return s[start : i+1]
			}
		}
	}
	return ""
}

// agentResultLines renders the structured result card for the expanded step
// details: summary, status/usage head, then one line per changed file.
func agentResultLines(m *Model, r *agentResult, budget int) []string {
	th := m.th
	parts := []string{r.status, fmt.Sprintf("%d files", len(r.files))}
	if r.iters > 0 {
		parts = append(parts, fmt.Sprintf("%d it", r.iters))
	}
	if r.tokens > 0 {
		parts = append(parts, human(r.tokens)+" tok")
	}
	head := strings.Join(parts, " · ")
	style := th.stepRes
	if r.status == "error" {
		style = th.stepErr
	}
	lines := []string{th.stepRes.Render(truncate(r.summary, budget))}
	lines = append(lines, style.Render(truncate(head, budget)))
	for _, f := range r.files {
		lines = append(lines, th.stepRes.Render(truncate("· "+f, budget)))
	}
	return lines
}
