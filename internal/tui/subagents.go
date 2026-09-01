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
	phase    string // queued | started | active | finished
	status   string // running | queued | success | partial | budget_exhausted | error | cancelled | timeout
	step     int
	tool     string
	iters    int
	tokens   int
	durS     float64
	stopSent bool      // subagent_cancel sent; terminal frame still pending
	goal     string    // manifest goal excerpt (parent arg; "" when unknown)
	seen     time.Time // first frame locally observed; drives the live elapsed
	lost     bool      // socket dropped while running: retired, never a ghost

	// wire v2 identity/budget/cost (omitempty on the wire; ""/0 = unreported)
	profile       string
	maxRisk       string
	budgetS       int
	budgetIt      int
	costUSD       float64
	budgetCostUSD float64
	artifacts     []client.StateArtifact
}

// finished reports whether the card reached a terminal state.
func (a *agentCard) finished() bool { return a.phase == "finished" }

// glyph picks the status glyph: live ⟳, then the terminal set mirroring
// odek's status framing (user cancel and deadline timeout never conflate).
func (a *agentCard) glyph() string {
	if !a.finished() {
		switch {
		case a.lost:
			return "×" // orphaned by a disconnect: dead, not spinning
		case a.phase == "queued":
			return "◌" // accepted by the engine, not spawned yet
		}
		return "⟳"
	}
	switch a.status {
	case "success":
		return "✓"
	case "partial", "budget_exhausted":
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
			card = &agentCard{taskID: ev.TaskID, idx: ev.TaskIdx, status: "running", seen: time.Now()}
			// Seed identity from the delegate manifest (the pre-v2 fallback):
			// goals/profiles/risk ride the parent's tool_call arg, never the
			// old wire frames. Newer frames overwrite with effective values.
			if ev.TaskIdx >= 0 && ev.TaskIdx < len(s.manifest) {
				slot := s.manifest[ev.TaskIdx]
				card.goal, card.profile, card.maxRisk = slot.goal, slot.profile, slot.maxRisk
			}
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
		// Wire-v2 identity/budget/cost: non-empty wire values overwrite the
		// manifest-seeded guesses (queued frames carry the requested profile
		// and risk; started+ frames carry the effective ones).
		if ev.Goal != "" {
			card.goal = collapse(ev.Goal)
		}
		if ev.Profile != "" {
			card.profile = collapse(ev.Profile)
		}
		if ev.MaxRisk != "" {
			card.maxRisk = collapse(ev.MaxRisk)
		}
		if ev.BudgetSeconds > 0 {
			card.budgetS = ev.BudgetSeconds
		}
		if ev.BudgetIterations > 0 {
			card.budgetIt = ev.BudgetIterations
		}
		if ev.CostUSD > 0 {
			card.costUSD = ev.CostUSD
		}
		if ev.BudgetCostUSD > 0 {
			card.budgetCostUSD = ev.BudgetCostUSD
		}
		if len(ev.Artifacts) > 0 {
			card.artifacts = ev.Artifacts
		}
		if card.lost {
			card.lost = false // frames resumed after a reconnect: alive again
		}
		// Registry bookkeeping: every unfinished card stays reachable regardless
		// of which turn owns it, so stops resolve across turns.
		if card.finished() {
			m.untrackLive(card.taskID)
		} else {
			m.trackLive(card)
		}
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
		switch {
		case a.lost:
			b.WriteString(" · lost on disconnect")
		case a.phase == "queued":
			b.WriteString(" · queued")
		default:
			if a.step > 0 {
				fmt.Fprintf(&b, " · step %d", a.step)
			}
			if a.tool != "" {
				b.WriteString(" · " + a.tool)
			}
			if !a.seen.IsZero() {
				// Client-side elapsed: state frames arrive in bursts, so the
				// server-reported duration freezes between them. Paired with
				// the wall-clock cap when the engine declares one.
				elapsed := formatStepDur(time.Since(a.seen))
				if a.budgetS > 0 {
					elapsed += "/" + formatStepDur(time.Duration(a.budgetS)*time.Second)
				}
				b.WriteString(" · " + elapsed)
			}
		}
	} else if a.status != "" && a.status != "success" {
		b.WriteString(" · " + a.status)
	}
	if !a.finished() && a.stopSent {
		b.WriteString(" · stop sent")
	}
	if a.iters > 0 {
		if a.budgetIt > 0 {
			fmt.Fprintf(&b, " · it %d/%d", a.iters, a.budgetIt)
		} else {
			fmt.Fprintf(&b, " · %d it", a.iters)
		}
	}
	if a.tokens > 0 {
		b.WriteString(" · " + human(a.tokens) + " tok")
	}
	if a.durS > 0 {
		b.WriteString(" · " + formatStepDur(time.Duration(a.durS*float64(time.Second))))
	}
	if a.costUSD > 0 {
		c := fmtCost(a.costUSD)
		if a.budgetCostUSD > 0 {
			c += "/" + strings.TrimPrefix(fmtCost(a.budgetCostUSD), "~")
		}
		b.WriteString(" · " + c)
	}
	// The goal renders last on purpose: right-edge truncation on narrow
	// terminals eats the garnish before the vitals.
	if a.goal != "" {
		b.WriteString(" · " + a.goal)
	}
	return b.String()
}

// agentRollup is the collapsed-head aggregate: "1/2 agents · 6.3k tok",
// with the failure count spelled out once one exists — "2/3 · 1 ✗ · 8.1k tok".
func agentRollup(s *step) string {
	if len(s.agents) == 0 {
		return ""
	}
	done, failed, queued, tokens := 0, 0, 0, 0
	for _, a := range s.agents {
		if a.finished() {
			done++
		}
		if a.failed() {
			failed++
		}
		if a.phase == "queued" {
			queued++
		}
		tokens += a.tokens
	}
	rollup := fmt.Sprintf("%d/%d agents", done, len(s.agents))
	if failed > 0 {
		rollup += fmt.Sprintf(" · %d ✗", failed)
	}
	if queued > 0 {
		rollup += fmt.Sprintf(" · %d queued", queued)
	}
	if tokens > 0 {
		rollup += " · " + human(tokens) + " tok"
	}
	return rollup
}

// cardTrustBadge renders a card's wire-v2 trust line — the resolved profile
// and the effective risk ceiling the engine reports. "" when unreported.
func cardTrustBadge(a *agentCard) string {
	var parts []string
	if a.profile != "" {
		parts = append(parts, "profile="+a.profile)
	}
	if a.maxRisk != "" {
		parts = append(parts, "risk="+a.maxRisk)
	}
	return strings.Join(parts, " · ")
}

// fmtCost renders an estimated cost: "~$0.0421" — shortest exact decimal
// representation. Absent costs never render as $0 upstream.
func fmtCost(v float64) string {
	return "~$" + strconv.FormatFloat(v, 'f', -1, 64)
}

// ── delegate manifest (per-task identity from the parent's arg) ───────────────

// taskSlot is one delegate_tasks argument entry, parsed at tool-call time:
// the identity the per-task frames don't carry. Fields are sanitized on
// ingest — slots render verbatim afterwards.
type taskSlot struct {
	goal    string // collapsed excerpt, ≤32 runes ("" when absent)
	profile string // requested profile id, as sent ("" when unset)
	maxRisk string // requested risk ceiling, as sent ("" when unset)
}

// parseDelegateManifest extracts per-task slots from a delegate_tasks JSON
// arg: {"tasks":[…]} or a bare array. Tolerant by design — string entries
// become goal-only slots, objects contribute goal/profile, and anything
// unparseable returns nil so the step renders exactly as before.
func parseDelegateManifest(data string) []taskSlot {
	var slots []taskSlot
	appendSlot := func(raw json.RawMessage) {
		var str string
		if err := json.Unmarshal(raw, &str); err == nil {
			slots = append(slots, taskSlot{goal: excerptGoal(str)})
			return
		}
		var obj struct {
			Goal    string `json:"goal"`
			Profile string `json:"profile"`
			MaxRisk string `json:"max_risk"`
		}
		if err := json.Unmarshal(raw, &obj); err == nil {
			slots = append(slots, taskSlot{goal: excerptGoal(obj.Goal), profile: collapse(obj.Profile), maxRisk: collapse(obj.MaxRisk)})
		}
	}
	var env struct {
		Tasks []json.RawMessage `json:"tasks"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(data)), &env); err == nil && len(env.Tasks) > 0 {
		for _, raw := range env.Tasks {
			appendSlot(raw)
		}
		return slots
	}
	var bare []json.RawMessage
	if err := json.Unmarshal([]byte(strings.TrimSpace(data)), &bare); err == nil {
		for _, raw := range bare {
			appendSlot(raw)
		}
		return slots
	}
	return nil
}

// excerptGoal collapses a task goal to a ≤32-rune one-line excerpt.
func excerptGoal(s string) string {
	return truncate(collapse(s), 32)
}

// pendingAgentLines renders manifest slots that have not reported a card yet
// — labelled inference: the parent declared the task, the wire has not
// confirmed it. Empty once every slot has a frame.
func (s *step) pendingAgentLines() []string {
	if len(s.manifest) == 0 {
		return nil
	}
	var out []string
	for k := range s.manifest {
		if s.cardByIdx(k) != nil {
			continue
		}
		line := fmt.Sprintf("◌ SA%d · pending (not yet reported)", k+1)
		if g := s.manifest[k].goal; g != "" {
			line += " · " + g
		}
		out = append(out, line)
	}
	return out
}

// cardByIdx finds a step's agent card by task index.
func (s *step) cardByIdx(idx int) *agentCard {
	for _, a := range s.agents {
		if a.idx == idx {
			return a
		}
	}
	return nil
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

// trackLive registers an unfinished card so stops resolve it from any turn —
// the drawer registry and /stop must not be scoped to whatever turn happens
// to be current at keypress time.
func (m *Model) trackLive(a *agentCard) {
	if m.liveTasks == nil {
		m.liveTasks = make(map[string]*agentCard)
	}
	m.liveTasks[a.taskID] = a
}

// untrackLive drops a card from the live registry once it settles.
func (m *Model) untrackLive(taskID string) {
	if m.liveTasks != nil {
		delete(m.liveTasks, taskID)
	}
}

// liveCard finds an unfinished card by task id: the cross-turn live registry
// first, then the current-turn scan as a fallback.
func (m *Model) liveCard(taskID string) *agentCard {
	if a := m.liveTasks[taskID]; a != nil && !a.finished() {
		return a
	}
	for _, a := range m.liveAgents() {
		if a.taskID == taskID {
			return a
		}
	}
	return nil
}

// loseLiveAgents retires every in-flight card — a socket drop orphans them
// (frames never replay), so they must stop claiming to be live. Returns how
// many cards were retired; 0 means nothing looked alive.
func (m *Model) loseLiveAgents() int {
	n := 0
	for _, a := range m.liveTasks {
		if !a.finished() && !a.lost {
			a.lost = true
			n++
		}
	}
	m.liveTasks = nil
	return n
}

// subagentTerminalNote surfaces a card's terminal state: failures stick (no
// autoclose) until the turn finalizes — a ✗ buried in an eight-agent swarm
// must not scroll by; user-initiated cancels stay transient (you did that).
func (m *Model) subagentTerminalNote(ev client.Event) {
	if ev.Phase != "finished" {
		return
	}
	switch ev.Status {
	case "error", "timeout":
		m.pushNote(fmt.Sprintf("sub-agent SA%d %s", ev.TaskIdx+1, ev.Status), time.Time{})
	case "cancelled":
		m.addTransientNote(fmt.Sprintf("sub-agent SA%d cancelled", ev.TaskIdx+1))
	}
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
	status       string
	summary      string
	files        []string
	tokens       int
	iters        int
	costUSD      float64
	artifacts    []client.ResultArtifact
	denials      []client.ResultDenial
	denialsTotal int
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
		Status       string                  `json:"status"`
		Summary      string                  `json:"summary"`
		FilesChanged []string                `json:"files_changed"`
		TokensUsed   int                     `json:"tokens_used"`
		Iterations   int                     `json:"iterations"`
		CostUSD      float64                 `json:"cost_usd"`
		Artifacts    []client.ResultArtifact `json:"artifacts"`
		Denials      []client.ResultDenial   `json:"denials"`
		DenialsTotal int                     `json:"denials_total"`
	}
	if err := json.Unmarshal([]byte(obj), &env); err != nil {
		return nil
	}
	if env.Status == "" || env.Summary == "" {
		return nil
	}
	r := &agentResult{status: env.Status, summary: collapse(env.Summary), tokens: env.TokensUsed, iters: env.Iterations,
		costUSD: env.CostUSD, artifacts: env.Artifacts, denials: env.Denials, denialsTotal: env.DenialsTotal}
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
// details: summary, status/usage head, changed files, then artifact refs and
// the policy denials the run hit.
func agentResultLines(m *Model, r *agentResult, budget int) []string {
	th := m.th
	parts := []string{r.status, fmt.Sprintf("%d files", len(r.files))}
	if r.iters > 0 {
		parts = append(parts, fmt.Sprintf("%d it", r.iters))
	}
	if r.tokens > 0 {
		parts = append(parts, human(r.tokens)+" tok")
	}
	if r.costUSD > 0 {
		parts = append(parts, fmtCost(r.costUSD))
	}
	if r.denialsTotal > 0 {
		parts = append(parts, fmt.Sprintf("⊘ %d denied", r.denialsTotal))
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
	for _, art := range r.artifacts {
		al := "⎘ " + collapse(art.ID)
		if art.URI != "" {
			al += " · " + collapse(art.URI)
		}
		if art.SizeBytes != nil && *art.SizeBytes > 0 {
			al += fmt.Sprintf(" (%s)", human(int(*art.SizeBytes)))
		}
		lines = append(lines, th.stepRes.Render(truncate(al, budget)))
	}
	for i, d := range r.denials {
		if i == 4 {
			lines = append(lines, th.stepRes.Render(truncate(fmt.Sprintf("… +%d more", len(r.denials)-4), budget)))
			break
		}
		dl := "⊘ " + collapse(d.Tool)
		if d.Class != "" {
			dl += " (" + collapse(d.Class) + ")"
		}
		if d.Reason != "" {
			dl += " — " + collapse(d.Reason)
		}
		lines = append(lines, th.stepRes.Render(truncate(dl, budget)))
	}
	return lines
}
