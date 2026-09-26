package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/BackendStack21/bodek/internal/client"
)

func TestFocusedToolInspectionKeepsPageVisibleAtTwelveRows(t *testing.T) {
	m := newTestModel()
	m.resize(40, 12)
	m.ta.SetValue("draft stays here")
	m.msgs = []message{{role: roleAsst, steps: []step{{
		name: "shell", arg: "go test ./...", done: true,
		callArgs: `{"command":"go test ./...","workdir":"/workspace","description":"` + strings.Repeat("inspect every argument ", 10) + `"}`,
		result:   "ok\ncoverage: 99%",
	}}}}
	m.refresh()
	m.openInspectStep(0, 0)
	first := plain(m.View())
	if strings.Contains(first, "draft stays here") || !strings.Contains(first, "inspect shell") {
		t.Fatalf("inspection must preserve the draft behind a named compact line:\n%s", first)
	}
	if !strings.Contains(first, "invocation") || !strings.Contains(first, "Alt+I") || !strings.Contains(first, "PgUp/Dn") {
		t.Fatalf("selected action, copy, and page hints must be visible:\n%s", first)
	}
	if rows := strings.Count(first, "\n") + 1; rows > m.height {
		t.Fatalf("focused view uses %d rows in a %d-row terminal:\n%s", rows, m.height, first)
	}
	m.handleInspectKey(key("pgdown"))
	second := plain(m.View())
	if second == first || !strings.Contains(second, "inspect shell") {
		t.Fatalf("paging must visibly change the detail page and keep identity:\n%s", second)
	}
	m.clearInspect()
	if got := m.ta.Value(); got != "draft stays here" {
		t.Fatalf("inspection changed draft: %q", got)
	}
}

func TestFocusedInspectionReplacesGlobalDetails(t *testing.T) {
	m := newTestModel()
	m.resize(40, 16)
	m.msgs = []message{{role: roleAsst, steps: []step{
		{name: "read_file", expanded: true, callArgs: `{"path":"one.go"}`},
		{name: "delegate_tasks", subagent: true, expanded: true, agentSel: 1,
			callArgs: `{"tasks":[{"goal":"review"}]}`,
			agents:   []*agentCard{{idx: 0, phase: "finished", status: "success", goal: "review"}}},
	}}}
	m.expandAll = true
	m.refresh()
	m.openInspectStep(0, 1)
	if m.expandAll || m.msgs[0].steps[0].expanded || !m.msgs[0].steps[1].expanded {
		t.Fatal("focused inspection must leave exactly the selected step expanded")
	}
	if m.msgs[0].steps[1].focusedIdx() != -1 {
		t.Fatal("opening the parent step must show its invocation before child focus")
	}
	if view := plain(m.View()); !strings.Contains(view, "invocation") {
		t.Fatalf("focused parent invocation is not visible:\n%s", view)
	}
	m.handleInspectKey(key("up"))
	if m.msgs[0].steps[1].expanded || m.inspect == nil || m.inspect.stepIdx != 0 {
		t.Fatal("moving inspection must close the old body and select the prior step")
	}
}

func TestInspectorTraversesReasoningAndReturnsToDraft(t *testing.T) {
	m := newTestModel()
	m.resize(40, 16)
	m.ta.SetValue("draft")
	m.msgs = []message{{role: roleAsst,
		steps: []step{{name: "shell", callArgs: `{"command":"go test ./..."}`}},
		items: []turnItem{{thinking: true, text: "Check the tests."}, {stepIdx: 0}},
	}}
	m.refresh()
	m.openInspectStep(0, 0)
	m.handleInspectKey(key("up"))
	if m.inspect == nil || m.inspect.stepIdx != -1 || m.msgs[0].steps[0].expanded {
		t.Fatal("Up did not move from the tool to the prior reasoning block")
	}
	m.handleInspectKey(key("enter"))
	if !m.msgs[0].items[0].open || !strings.Contains(plain(m.View()), "Check the tests.") {
		t.Fatal("Enter did not open the selected reasoning block")
	}
	m.handleInspectKey(key("esc"))
	if m.inspect != nil || m.ta.Value() != "draft" {
		t.Fatal("Escape did not return to the preserved draft")
	}
}

func TestFocusedSubagentInspectionKeepsChipsAndPage(t *testing.T) {
	m := newTestModel()
	m.resize(40, 12)
	s := step{name: "delegate_tasks", subagent: true, done: true,
		callArgs: `{"tasks":[{"goal":"inspect auth"},{"goal":"verify tests"}]}`,
		result:   "agents finished"}
	for i := 0; i < 4; i++ {
		s.agents = append(s.agents, &agentCard{idx: i, phase: "finished", status: "success", goal: fmt.Sprintf("review area %d", i)})
	}
	m.msgs = []message{{role: roleAsst, steps: []step{s}}}
	m.refresh()
	m.openInspectStep(0, 0)
	transcript := plain(m.conversation())
	for i := 1; i <= 4; i++ {
		if !strings.Contains(transcript, fmt.Sprintf("SA%d", i)) {
			t.Fatalf("sub-agent chip SA%d disappeared from transcript:\n%s", i, transcript)
		}
	}
	view := plain(m.View())
	if !strings.Contains(view, "inspect delegate_tasks") || !strings.Contains(view, `"tasks"`) {
		t.Fatalf("short inspector lost identity or substantive invocation content:\n%s", view)
	}
	if rows := strings.Count(view, "\n") + 1; rows > m.height {
		t.Fatalf("sub-agent inspection uses %d rows in a %d-row terminal", rows, m.height)
	}
	m.handleInspectKey(key("right"))
	if got := m.msgs[0].steps[0].focusedIdx(); got != 0 {
		t.Fatalf("Right did not focus the first sub-agent chip: %d", got)
	}
	m.handleInspectKey(key("right"))
	if got := m.msgs[0].steps[0].focusedIdx(); got != 1 {
		t.Fatalf("Right did not advance to the next sub-agent chip: %d", got)
	}
	m.handleInspectKey(key("enter"))
	if m.msgs[0].steps[0].expanded {
		t.Fatal("Enter did not close the focused step")
	}
	m.handleInspectKey(key("enter"))
	if !m.msgs[0].steps[0].expanded || m.msgs[0].steps[0].focusedIdx() != -1 {
		t.Fatal("reopening the step did not restore its parent invocation")
	}
}

func TestNarrowApprovalShowsCommandAndChoices(t *testing.T) {
	m := newTestModel()
	m.resize(40, 12)
	busyTurn(m)
	m.handleEvent(client.Event{Type: "approval_request", ID: "approval", Name: "shell", Risk: "shell_exec",
		Command: "rm -rf build/", Description: "clean build artifacts", AllowTrust: true})
	view := plain(m.View())
	for _, want := range []string{"Command: rm -rf build/", "a once", "d deny", "t trust class", "Tab"} {
		if !strings.Contains(view, want) {
			t.Fatalf("40-column approval missing %q:\n%s", want, view)
		}
	}
	if rows := strings.Count(view, "\n") + 1; rows > m.height {
		t.Fatalf("approval uses %d rows in a %d-row terminal", rows, m.height)
	}
}

func TestOperationApprovalNamesResource(t *testing.T) {
	m := newTestModel()
	m.resize(40, 16)
	busyTurn(m)
	m.handleEvent(client.Event{Type: "approval_request", ID: "operation", IsOperation: true,
		Risk: "local_write", Command: "config/settings.json"})
	view := plain(m.View())
	if !strings.Contains(view, "Resource: config/settings.json") || strings.Contains(view, "Command: config/settings.json") {
		t.Fatalf("operation approval must identify its resource:\n%s", view)
	}
}

func TestApprovalDetailsExposeUnknownScopeAndInvisibleCommand(t *testing.T) {
	m := newTestModel()
	m.resize(40, 24)
	m.approvals = []client.Event{{Type: "approval_request", Name: "shell", Risk: "shell_exec",
		Command: "echo safe\x1b[31m\u202Ebad", AllowTrust: true}}
	m.apprExpanded = true
	var pages strings.Builder
	for offset := 0; offset < 20; offset++ {
		m.apprOffset = offset
		pages.WriteString(plain(m.approvalBody()))
		pages.WriteByte('\n')
	}
	all := pages.String()
	for _, want := range []string{`\x1B`, `\u202E`, "Working directory: not supplied by", "connection ends"} {
		if !strings.Contains(all, want) {
			t.Fatalf("expanded approval did not expose %q across pages:\n%s", want, all)
		}
	}
	if strings.ContainsRune(all, '\x1b') || strings.ContainsRune(all, '\u202e') {
		t.Fatal("raw control or bidi character reached approval display")
	}
	m.approvals[0].Command = ""
	m.apprOffset = 0
	if got := plain(m.approvalBody()); !strings.Contains(got, "Command not supplied by odek") {
		t.Fatalf("tool name must not be presented as a missing command: %q", got)
	}
	m.approvals[0].Risk = "custom\nDeny disabled"
	if got := approvalRiskLabel(m.approvals[0].Risk); strings.ContainsRune(got, '\n') || !strings.Contains(got, `\n`) {
		t.Fatalf("risk label must show an escaped newline on one row: %q", got)
	}
}

func TestFrictionApprovalInspectionPreservesDraftAndDecision(t *testing.T) {
	m := newTestModel()
	m.resize(40, 16)
	m.ta.SetValue("follow-up draft")
	m.approvals = []client.Event{{Type: "approval_request", ID: "pending", Name: "shell",
		Risk: "shell_exec", Command: "rm -rf build/", Friction: true}}
	m.refresh()
	m.handleApprovalKey(key("tab"))
	if !m.apprExpanded || !strings.Contains(plain(m.View()), "Command: rm -rf build/") {
		t.Fatal("Tab did not reveal the pending command")
	}
	m.handleApprovalKey(key("pgdown"))
	m.handleApprovalKey(key("ctrl+g"))
	m.handleApprovalKey(key("esc"))
	if m.apprExpanded || m.apprEditing || len(m.approvals) != 1 || m.ta.Value() != "follow-up draft" {
		t.Fatal("inspecting a friction approval changed the draft or decision state")
	}
}

func TestToolHintFitsFortyColumns(t *testing.T) {
	m := newTestModel()
	m.resize(40, 32)
	busyTurn(m)
	m.handleEvent(client.Event{Type: "tool_call", Name: "shell", Data: `{"command":"go test ./..."}`})
	view := plain(m.View())
	if !strings.Contains(view, "tip: click a step to inspect") {
		t.Fatalf("one-time tool hint was cut off at 40 columns:\n%s", view)
	}
	for _, line := range strings.Split(m.View(), "\n") {
		if got := lipgloss.Width(line); got > 40 {
			t.Fatalf("hint view line uses %d columns: %q", got, plain(line))
		}
	}
}

func TestResponsiveHintsKeepActionsReadable(t *testing.T) {
	for _, tc := range []struct {
		key, full string
	}{
		{hintQueue, "^Q opens the queue"},
		{hintSwarm, "/agents shows all agents"},
		{hintSteps, "click a step to inspect"},
		{hintCtx, "ctx = context in use"},
	} {
		m := newTestModel()
		m.resize(40, 20)
		m.teach(tc.key, "an intentionally long hint that must not be shown at this width")
		got := m.notices[len(m.notices)-1]
		if !strings.Contains(got, tc.full) || lipgloss.Width(got) > 36 {
			t.Errorf("%s hint incomplete at 40 columns: %q", tc.key, got)
		}
	}
	m := newTestModel()
	m.resize(20, 20)
	m.teach(hintSteps, "long hint")
	if got := m.notices[len(m.notices)-1]; !strings.Contains(got, "F1 help") || lipgloss.Width(got) > 16 {
		t.Errorf("tiny terminal hint incomplete: %q", got)
	}
}

func TestConciseTurnFootShowsOutcomeAndKeepsDiagnostics(t *testing.T) {
	m := newTestModel()
	m.resize(40, 20)
	msg := message{role: roleAsst, stats: &turnStats{latency: 2.5, toolCount: 2, ctxTok: 1000, outTok: 200}}
	if got := plain(m.turnStatFoot(msg)); !strings.Contains(got, "✓ done · 2.5s · 2 tools") || strings.Contains(got, "⌂") {
		t.Fatalf("default receipt is not outcome-first and concise: %q", got)
	} else if lipgloss.Width(got) != m.vp.Width || !strings.HasPrefix(got, " ") {
		t.Fatalf("default receipt is not aligned to the right edge: %q", got)
	}
	msg.failed = true
	if got := plain(m.turnStatFoot(msg)); !strings.Contains(got, "✗ failed") {
		t.Fatalf("failed turn lacks an outcome: %q", got)
	}
	m.expandAll = true
	if got := plain(m.turnStatFoot(msg)); !strings.Contains(got, "⌂ 1k") || !strings.Contains(got, "↳ 200") {
		t.Fatalf("global details lost full turn telemetry: %q", got)
	} else if lipgloss.Width(got) != m.vp.Width {
		t.Fatalf("detailed receipt is not aligned to the right edge: %q", got)
	}
	m.expandAll = false
	msg.collapsed = true
	msg.steps = []step{{name: "shell", done: true}}
	msg.items = []turnItem{{stepIdx: 0}}
	if got := plain(m.turnStatFoot(msg)); !strings.Contains(got, "✗ failed") {
		t.Fatalf("folded turn needs a footer when its head cannot show the tally: %q", got)
	}
}

func TestFoldedTurnStatsSurviveCrowdedHead(t *testing.T) {
	for _, width := range []int{40, 80, 120} {
		m := newTestModel()
		m.resize(width, 20)
		msg := message{role: roleAsst, collapsed: true, content: "Finished.",
			stats: &turnStats{latency: 12, toolCount: 2},
			steps: []step{
				{name: "apply_patch", arg: "internal/tui/view.go", done: true, dur: 6 * time.Second,
					result: "--- a\n+++ b\n@@\n-old\n+new\n+newer\n"},
				{name: "shell", done: true, dur: 6 * time.Second,
					result: "ok  \tgithub.com/BackendStack21/bodek/internal/tui\t0.54s"},
			},
			items: []turnItem{{stepIdx: 0}, {stepIdx: 1}},
		}
		rendered, _ := m.renderMessage(msg, 0, 0)
		lines := strings.Split(plain(rendered), "\n")
		if !strings.Contains(lines[0], "2 tools · 12.0s") {
			t.Fatalf("width %d: folded stats disappeared:\n%s", width, plain(rendered))
		}
		if lipgloss.Width(lines[0]) != m.vp.Width {
			t.Fatalf("width %d: folded tally is not right-aligned: %q", width, lines[0])
		}
		if strings.Contains(lines[0], "✓ tests") || !strings.Contains(plain(rendered), "✓ tests") {
			t.Fatalf("width %d: coding receipt should remain in folded summary:\n%s", width, plain(rendered))
		}
		m.expandAll = true
		detailed, _ := m.renderMessage(msg, 0, 0)
		if !strings.Contains(plain(detailed), "⌂") || !strings.Contains(plain(detailed), "↳") {
			t.Fatalf("width %d: global details hid folded-turn telemetry:\n%s", width, plain(detailed))
		}
	}
}

func TestFoldedTurnFallsBackToFooterWhenTallyCannotFit(t *testing.T) {
	m := newTestModel()
	m.resize(24, 20)
	msg := message{role: roleAsst, collapsed: true, content: "Finished.",
		stats: &turnStats{latency: 12, toolCount: 1},
		steps: []step{{name: "delegate_tasks", done: true, dur: 12 * time.Second,
			agents: make([]*agentCard, 10)}},
		items: []turnItem{{stepIdx: 0}},
	}
	if got := foldTally(msg); !strings.HasPrefix(got, "1 tool · 10 agents") {
		t.Fatalf("singular tool tally is incorrect: %q", got)
	}
	rendered, _ := m.renderMessage(msg, 0, 0)
	lines := strings.Split(plain(rendered), "\n")
	if strings.Contains(lines[0], "10 agents") || !strings.Contains(lines[len(lines)-1], "✓ done") {
		t.Fatalf("unfittable tally should use the right-aligned footer:\n%s", plain(rendered))
	}
	if lipgloss.Width(lines[len(lines)-1]) != m.vp.Width {
		t.Fatalf("fallback footer does not reach right edge: %q", lines[len(lines)-1])
	}
}

func TestTurnStatsEndAtRightEdgeInRenderedCard(t *testing.T) {
	for _, width := range []int{40, 80, 120} {
		m := newTestModel()
		m.resize(width, 20)
		msg := message{role: roleAsst, content: "Work complete.", stats: &turnStats{latency: 1.2, toolCount: 1}}
		for _, details := range []bool{false, true} {
			m.expandAll = details
			rendered, _ := m.renderMessage(msg, 0, 0)
			lines := strings.Split(plain(rendered), "\n")
			foot := lines[len(lines)-1]
			if lipgloss.Width(foot) != m.vp.Width {
				t.Errorf("width %d, details %t: stat row ends at %d:\n%s", width, details, lipgloss.Width(foot), plain(rendered))
			}
		}
	}
}

func TestClickOpensFocusedStepAtShortHeight(t *testing.T) {
	m := newTestModel()
	m.resize(40, 12)
	m.msgs = []message{{role: roleAsst, steps: []step{{name: "shell", arg: "go test", callArgs: `{"command":"go test ./..."}`}}}}
	m.refresh()
	var line int
	for _, ref := range m.stepLineIndex {
		if ref.msgIdx == 0 && ref.stepIdx == 0 && ref.x1 <= ref.x0 {
			line = ref.line
			break
		}
	}
	m.vp.SetYOffset(line)
	y := headerHeight + line - m.vp.YOffset
	m.Update(tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonLeft, X: 8, Y: y})
	if !m.inspectChrome() {
		t.Fatal("click did not open the focused inspector")
	}
	view := plain(m.View())
	if !strings.Contains(view, "inspect shell") || !strings.Contains(view, "invocation") {
		t.Fatalf("clicked command page is not visible:\n%s", view)
	}
}
