package tui

// Sub-agent live telemetry: the agentCard model, subagent_state ingestion,
// and card rendering. Driven by the per-task lifecycle frames odek serve
// v1.30+ emits (started → active → finished); every field is wire-derived
// and sanitized on ingest, and cards render only for tasks that report.

import (
	"fmt"
	"strings"
	"time"

	"github.com/BackendStack21/bodek/internal/client"
)

// agentCard is one delegated task's live telemetry inside a sub-agent step.
type agentCard struct {
	taskID string
	idx    int
	phase  string // started | active | finished
	status string // running | success | partial | error | cancelled | timeout
	step   int
	tool   string
	iters  int
	tokens int
	durS   float64
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
