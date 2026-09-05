package tui

import (
	"strings"
	"testing"

	"github.com/BackendStack21/bodek/internal/client"
)

// TestEveryWindowClosesWithEsc pins the dismiss stack: every overlay and
// inspect chrome folds on ESC, and a leftover window must never leak
// through to cancel-turn.
func TestEveryWindowClosesWithEsc(t *testing.T) {
	t.Run("palette", func(t *testing.T) {
		m := newTestModel()
		m.Update(key("ctrl+k"))
		if !m.pal.open {
			t.Fatal("precondition: palette open")
		}
		m.Update(key("esc"))
		if m.pal.open {
			t.Error("esc did not close the palette")
		}
	})
	t.Run("drawer", func(t *testing.T) {
		m := newTestModel()
		m.panel = panelSessions
		m.relayout()
		m.Update(key("esc"))
		if m.panel != panelNone {
			t.Errorf("esc did not close the drawer: %v", m.panel)
		}
	})
	t.Run("drawer detail", func(t *testing.T) {
		m := newTestModel()
		m.panel = panelSkills
		m.panelDetail = true
		m.relayout()
		m.Update(key("esc"))
		if m.panelDetail {
			t.Error("esc did not fold the detail")
		}
		if m.panel != panelSkills {
			t.Errorf("first esc must keep the tab open, got %v", m.panel)
		}
		m.Update(key("esc"))
		if m.panel != panelNone {
			t.Errorf("second esc did not close the drawer: %v", m.panel)
		}
	})
	t.Run("drawer edit", func(t *testing.T) {
		m := newTestModel()
		m.panel = panelSessions
		m.panelEdit = panelEditSearch
		m.panelDraft = "foo"
		m.Update(key("esc"))
		if m.panelEdit != panelEditNone {
			t.Error("esc did not abandon the editor")
		}
		if m.panel != panelSessions {
			t.Errorf("first esc must keep the tab open, got %v", m.panel)
		}
	})
	t.Run("models", func(t *testing.T) {
		m := newTestModel()
		m.panel = panelModels
		m.Update(key("esc"))
		if m.panel != panelNone {
			t.Error("esc did not close the model picker")
		}
	})
	t.Run("queue panel", func(t *testing.T) {
		m := newTestModel()
		m.panel = panelQueue
		m.Update(key("esc"))
		if m.panel != panelNone {
			t.Error("esc did not close /queue")
		}
	})
	t.Run("cockpit", func(t *testing.T) {
		m := newTestModel()
		m.popover = true
		m.Update(key("esc"))
		if m.popover {
			t.Error("esc did not close the cockpit")
		}
	})
	t.Run("find", func(t *testing.T) {
		m := newTestModel()
		m.openFind()
		m.Update(key("esc"))
		if m.find.open {
			t.Error("esc did not close find")
		}
	})
	t.Run("attach popup", func(t *testing.T) {
		m := newTestModel()
		m.ac.open = true
		m.ac.items = []client.Resource{{ID: "@a", Label: "a"}}
		m.Update(key("esc"))
		if m.ac.open {
			t.Error("esc did not close @ completion")
		}
	})
	t.Run("queue strip", func(t *testing.T) {
		m := newTestModel()
		busyTurn(m)
		m.queue = []string{"held"}
		unfoldQueue(m)
		m.Update(key("esc"))
		if m.qfocus {
			t.Error("esc did not leave queue focus")
		}
	})
	t.Run("skill chip", func(t *testing.T) {
		m := newTestModel()
		m.skillSuggest = &client.Event{SkillName: "deploy"}
		m.Update(key("esc"))
		if m.skillSuggest != nil {
			t.Error("esc did not skip the skill chip")
		}
	})
	t.Run("expand all", func(t *testing.T) {
		m := newTestModel()
		busyTurn(m)
		m.expandAll = true
		m.Update(key("esc"))
		if m.expandAll {
			t.Error("esc did not fold ^E details")
		}
		if m.confirm != confirmNone {
			t.Error("esc on ^E must not arm cancel")
		}
	})
	t.Run("open thinking", func(t *testing.T) {
		m := newTestModel()
		m.msgs = []message{{role: roleAsst, items: []turnItem{
			{thinking: true, text: "plan", open: true},
		}}}
		m.Update(key("esc"))
		if m.msgs[0].items[0].open {
			t.Error("esc did not fold the reasoning block")
		}
	})
	t.Run("agent focus", func(t *testing.T) {
		m := newTestModel()
		m.msgs = []message{{role: roleAsst, steps: []step{
			{name: "delegate_tasks", subagent: true, agentSel: 1, expanded: true,
				agents: []*agentCard{{taskID: "t1", idx: 0, phase: "active", status: "running"}}},
		}}}
		m.Update(key("esc"))
		if m.msgs[0].steps[0].agentSel != 0 {
			t.Error("esc did not clear agent focus")
		}
		if !m.msgs[0].steps[0].expanded {
			t.Error("first esc should keep the parent step expanded")
		}
		m.Update(key("esc"))
		if m.msgs[0].steps[0].expanded {
			t.Error("second esc did not fold the step")
		}
	})
	t.Run("expanded step", func(t *testing.T) {
		m := newTestModel()
		m.msgs = []message{{role: roleAsst, steps: []step{
			{name: "shell", expanded: true, result: "ok"},
		}}}
		m.Update(key("esc"))
		if m.msgs[0].steps[0].expanded {
			t.Error("esc did not fold the step")
		}
	})
	t.Run("help card", func(t *testing.T) {
		m := newTestModel()
		m.showHelp()
		if len(m.msgs) == 0 {
			t.Fatal("precondition: help card")
		}
		m.Update(key("esc"))
		if len(m.msgs) != 0 {
			t.Error("esc did not dismiss the help card")
		}
	})
	t.Run("stats sheet", func(t *testing.T) {
		m := newTestModel()
		m.showStats()
		if m.panel != panelStats {
			t.Fatal("precondition: stats sheet")
		}
		m.Update(key("esc"))
		if m.panel != panelNone {
			t.Error("esc did not close the stats sheet")
		}
	})
}

func TestApprovalEscCollapsesThenDenies(t *testing.T) {
	m, actions, _ := approvalRecorder(t)
	m.handleEvent(client.Event{Type: "approval_request", ID: "apr", Command: "rm x"})
	m.Update(key("tab"))
	if !m.apprExpanded {
		t.Fatal("precondition: approval expanded")
	}
	m.Update(key("esc"))
	if m.apprExpanded {
		t.Fatal("first esc should collapse the expanded command")
	}
	if m.curApproval() == nil {
		t.Fatal("first esc must not deny")
	}
	_, cmd := m.Update(key("esc"))
	exec(cmd)
	if m.curApproval() != nil {
		t.Fatal("second esc should deny")
	}
	if got := awaitAction(t, actions); got != "deny" {
		t.Errorf("action = %q, want deny", got)
	}
}

func TestEscBusyCancelsOnlyWhenBare(t *testing.T) {
	m := newTestModel()
	busyTurn(m)
	m.sessionID = "s1"
	m.Update(key("esc"))
	if m.confirm != confirmCancel {
		t.Fatalf("bare esc while busy should arm cancel, got %v", m.confirm)
	}
}

// Drawer (and the rest of the inspect chrome) sits above the approval
// form on the ESC stack. A leftover sessions sheet must fold first;
// denying the live request on that keypress is the wrong rung.
func TestEscClosesDrawerBeforeApproval(t *testing.T) {
	m := newTestModel()
	m.panel = panelSessions
	m.relayout()
	m.handleEvent(client.Event{Type: "approval_request", ID: "apr", Command: "rm x"})
	if m.curApproval() == nil {
		t.Fatal("precondition: approval armed")
	}

	m.Update(key("esc"))

	if m.panel != panelNone {
		t.Errorf("esc must close the drawer first, got %v", m.panel)
	}
	if m.curApproval() == nil {
		t.Fatal("esc must not deny the approval while the drawer was open")
	}
}

func TestHelpCardTeachesEscCloses(t *testing.T) {
	m := newTestModel()
	m.showHelp()
	card := plain(m.msgs[len(m.msgs)-1].content)
	if !strings.Contains(strings.ToLower(card), "close overlay") {
		t.Errorf("help should teach esc closes windows:\n%s", card)
	}
}
