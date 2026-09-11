package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/BackendStack21/bodek/internal/client"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func TestInspectReachesToolAfterReasoning(t *testing.T) {
	m := newTestModel()
	busyTurn(m)
	m.msgs[1].items = []turnItem{{thinking: true, text: "reasoning"}, {stepIdx: 0}, {stepIdx: 1}}
	m.msgs[1].steps = []step{{name: "shell", done: true, result: "first"}, {name: "shell", done: true, result: "second"}}
	m.moveInspect(false)
	if m.inspect == nil || m.inspect.itemIdx != 0 {
		t.Fatal("first traversal must visibly select reasoning")
	}
	m.handleKey(key("down"))
	m.Update(key("enter"))
	if m.inspect.stepIdx != 0 || !m.msgs[1].steps[0].expanded || m.msgs[1].steps[1].expanded {
		t.Fatal("Enter must expand only the selected tool")
	}
	m.handleKey(key("down"))
	m.Update(key("enter"))
	if !m.msgs[1].steps[1].expanded {
		t.Fatal("down must reach the next tool")
	}
	m.handleKey(key("up"))
	if m.inspect.stepIdx != 0 {
		t.Fatal("up must move backward")
	}
	m.Update(key("esc"))
	if m.inspect != nil || m.confirm != confirmNone {
		t.Fatal("Escape must return to composer without cancelling")
	}
}

func TestInspectTypingReturnsToComposer(t *testing.T) {
	m := newTestModel()
	busyTurn(m)
	m.msgs[1].steps = []step{{name: "shell"}}
	m.moveInspect(false)
	m.Update(key("h"))
	if m.inspect != nil || m.ta.Value() != "h" {
		t.Fatalf("typing should resume composer: %q", m.ta.Value())
	}
}

func TestStopShortcutIgnoresInspectAndPanels(t *testing.T) {
	for _, panel := range []panelMode{panelNone, panelSessions, panelModels} {
		m := newTestModel()
		busyTurn(m)
		m.panel = panel
		m.expandAll = true
		m.msgs[1].steps = []step{{expanded: true}, {expanded: true}, {expanded: true}}
		m.Update(tea.KeyMsg{Type: tea.KeyCtrlX})
		if m.confirm != confirmCancel {
			t.Fatalf("panel %v did not arm stop", panel)
		}
		if !m.busy {
			t.Fatal("must retain deliberate confirmation")
		}
	}
}

func TestExpandedToolResponsesStayBoundedAndPage(t *testing.T) {
	for _, height := range []int{16, 24, 40} {
		m := newTestModel()
		m.resize(60, height)
		busyTurn(m)
		var lines []string
		for i := 0; i < 100; i++ {
			lines = append(lines, fmt.Sprintf("line %03d %s", i, strings.Repeat("x", 200)))
		}
		m.msgs[1].steps = []step{{name: "shell", done: true, expanded: true, result: strings.Join(lines, "\n")}}
		m.inspect = &inspectTarget{1, 0, -1}
		out, _, rows := m.renderStep(m.msgs[1].steps[0], false, 1, 0, 0)
		if rows > m.toolDetailRows()+2 {
			t.Fatalf("height %d: expanded body grew to %d rows", height, rows)
		}
		for _, line := range strings.Split(out, "\n") {
			if lipgloss.Width(line) > m.vp.Width {
				t.Fatalf("response overflow: %d", lipgloss.Width(line))
			}
		}
		if strings.Contains(out, "line 099") {
			t.Fatal("first page should not contain tail")
		}
		m.handleKey(key("pgdown"))
		next, _, _ := m.renderStep(m.msgs[1].steps[0], false, 1, 0, 0)
		if next == out || m.msgs[1].steps[0].detailOffset == 0 {
			t.Fatal("paging did not advance")
		}
		for i := 0; i < 100/m.toolDetailRows()+3; i++ {
			m.handleKey(key("pgdown"))
		}
		last := m.msgs[1].steps[0].detailOffset
		m.handleKey(key("pgup"))
		if m.msgs[1].steps[0].detailOffset >= last {
			t.Fatal("paging beyond tail must not trap navigation")
		}
	}
}

func TestWorkflowChromeFitsNarrowAndShortTerminals(t *testing.T) {
	for _, width := range []int{40, 80, 120} {
		for _, height := range []int{16, 24, 32} {
			for _, state := range []string{"sessions", "models", "approval", "friction", "friction-edit", "disconnected", "confirm"} {
				t.Run(fmt.Sprintf("%s-%dx%d", state, width, height), func(t *testing.T) {
					m := newTestModel()
					m.resize(width, height)
					switch state {
					case "sessions":
						m.panel = panelSessions
					case "models":
						m.panel = panelModels
					case "approval", "friction", "friction-edit":
						busyTurn(m)
						m.handleEvent(client.Event{Type: "approval_request", ID: "a", Command: strings.Repeat("echo output; ", 100), AllowTrust: true, Friction: strings.HasPrefix(state, "friction")})
						m.apprExpanded = true
						m.apprEditing = state == "friction-edit"
					case "disconnected":
						m.disconn = true
						m.status = "server shut down"
					case "confirm":
						m.confirm = confirmQuit
					}
					m.relayout()
					m.refresh()
					view := m.View()
					if rows := strings.Count(view, "\n") + 1; rows > height {
						t.Errorf("%d rows exceeds %d (composer=%d approval=%d input=%d viewport=%d):\n%s", rows, height, m.ta.Height(), lineCount(m.approvalPanel()), m.inputAreaHeight(), m.vp.Height, plain(view))
					}
					for i, line := range strings.Split(view, "\n") {
						if n := lipgloss.Width(line); n > width {
							t.Errorf("line %d: %d cells exceeds %d: %q", i, n, width, plain(line))
						}
					}
				})
			}
		}
	}
}

func TestFrictionPasteRemainsBounded(t *testing.T) {
	m := newTestModel()
	m.resize(40, 12)
	m.busy = true
	m.handleEvent(client.Event{Type: "approval_request", ID: "a", Friction: true})
	m.Update(key("alt+a"))
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(strings.Repeat("x\n", 1000)), Paste: true})
	if len([]rune(m.apprTyped)) > 64 {
		t.Fatal("confirmation editor is unbounded")
	}
	out := m.View()
	if rows := strings.Count(out, "\n") + 1; rows > m.height {
		t.Fatalf("pasted confirmation broke the screen: %d rows", rows)
	}
	if m.curApproval() == nil {
		t.Fatal("pasting must never decide approval")
	}
}

func TestLargeResultPreviewHasExplicitLimits(t *testing.T) {
	out := resultPreview(strings.Repeat("界", 100000))
	if len(out) > 128*1024+100 || !strings.Contains(out, "limited to 128 KiB") {
		t.Fatal("large single-line output must be bounded and labelled")
	}
	out = resultPreview(strings.Repeat("line\n", 1000))
	if strings.Count(out, "\n") > 200 || !strings.Contains(out, "omitted from preview") {
		t.Fatal("line-limited output must explain omitted content")
	}
}
