package tui

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/BackendStack21/bodek/internal/client"
)

// A queue drain sends the queued prompt without wiping what the user is
// typing meanwhile (sendPrompt used to reset the composer on every path).
func TestRegressQueueDrainKeepsDraft(t *testing.T) {
	m := newTestModel()
	m.queue = []string{"queued B"}
	m.ta.SetValue("half typed follow-up")
	if cmd := m.sendQueued(); cmd == nil {
		t.Fatal("queued prompt was not sent")
	}
	if got := m.ta.Value(); got != "half typed follow-up" {
		t.Fatalf("draft = %q after drain", got)
	}
	if m.lastPrompt != "queued B" {
		t.Fatalf("lastPrompt = %q", m.lastPrompt)
	}
}

// A bare a/d/t typed while a text field is on screen must not decide an
// approval that arrived meanwhile; Alt still does.
func TestRegressBareApprovalKeysSkipOpenTextFields(t *testing.T) {
	for name, open := range map[string]func(m *Model){
		"find":   func(m *Model) { m.openFind() },
		"search": func(m *Model) { m.panel = panelSessions; m.panelEdit = panelEditSearch },
	} {
		m := newTestModel()
		open(m)
		m.handleEvent(client.Event{Type: "approval_request", ID: "apr", Risk: "shell_exec", Command: "rm x"})
		m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
		if m.curApproval() == nil {
			t.Fatalf("%s: bare 'a' approved while the field was open", name)
		}
		m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}, Alt: true})
		if m.curApproval() != nil {
			t.Fatalf("%s: alt+a did not approve", name)
		}
	}
}

// An unknown slash name keeps the draft (it may be a path-led prompt).
func TestRegressUnknownCommandKeepsDraft(t *testing.T) {
	m := newTestModel()
	m.ta.SetValue("/etc/hosts has a bad entry")
	m.runCommandLine("/etc/hosts has a bad entry")
	if got := m.ta.Value(); got != "/etc/hosts has a bad entry" {
		t.Fatalf("draft = %q", got)
	}
}

// Typing an exact command name selects it over longer prefix matches.
func TestRegressExactCommandLeadsPopup(t *testing.T) {
	m := newTestModel()
	m.openCmdAC("ru")
	m.ac.sel = len(m.ac.items) - 1
	m.openCmdAC("run")
	if got := m.ac.items[m.ac.sel].ID; got != "/run" {
		t.Fatalf("selected %q for /run", got)
	}
}

// The find bar takes spaces (KeySpace) for multi-word queries.
func TestRegressFindAcceptsSpace(t *testing.T) {
	m := newTestModel()
	m.openFind()
	m.handleFindKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	m.handleFindKey(tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}})
	m.handleFindKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'b'}})
	if got := string(m.find.query); got != "a b" {
		t.Fatalf("query = %q", got)
	}
}

// The palette keeps multi-rune reads and pastes, not only single runes.
func TestRegressPaletteKeepsMultiRuneInput(t *testing.T) {
	m := newTestModel()
	m.togglePalette()
	m.handlePaletteKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("se")})
	m.handlePaletteKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s\x07"), Paste: true})
	m.handlePaletteKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("shift+enter")})
	if m.pal.query != "ses" {
		t.Fatalf("query = %q", m.pal.query)
	}
}

// A session frame inside a drained batch must keep the jobs watcher chain:
// arming bumps the generation, so dropping its tick silenced completions.
func TestRegressBatchedSessionKeepsJobsWatcher(t *testing.T) {
	old := jobsWatchEvery
	jobsWatchEvery = time.Millisecond
	defer func() { jobsWatchEvery = old }()
	m := newJobsTestModel(t, nil)
	_, cmd := m.ingestWireBatch([]client.Event{
		{Type: "session", SessionID: "s1", AuthToken: "tok"},
		{Type: "server_info"},
	})
	found := false
	for _, name := range runCmdTree(cmd) {
		if name == "tui.jobsTickMsg" {
			found = true
		}
	}
	if !found {
		t.Fatal("batched session frame dropped the watcher tick")
	}
}

// The armed ^L confirm must not wipe a turn that started meanwhile.
func TestRegressClearConfirmRefusesWhileBusy(t *testing.T) {
	m := newTestModel()
	m.armConfirm(confirmClear, "the conversation")
	m.msgs = append(m.msgs, message{role: roleAsst, streaming: true})
	m.curIdx, m.busy = 0, true
	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if len(m.msgs) != 1 || m.curIdx != 0 {
		t.Fatalf("clear ran mid-turn: msgs=%d cur=%d", len(m.msgs), m.curIdx)
	}
}

// A failed shutdown re-arms reconnect; a successful one interrupts the turn.
func TestRegressShutdownLatch(t *testing.T) {
	m := newTestModel()
	m.shutdownReq = true
	m.Update(shutdownDoneMsg{err: errors.New("403")})
	if m.shutdownReq {
		t.Fatal("failed shutdown left the latch set")
	}
	m.shutdownReq = true
	m.msgs = append(m.msgs, message{role: roleAsst, streaming: true})
	m.curIdx, m.busy = 0, true
	m.handleEvent(client.Event{Type: client.EventDisconnected})
	if !strings.Contains(m.msgs[0].content, "Interrupted") {
		t.Fatalf("shutdown mid-turn left no marker: %q", m.msgs[0].content)
	}
}

// A plan call cut off before its tool_result must still confirm at the
// turn boundary, or the never-written plan sticks on screen.
func TestRegressAbortedPlanCallConfirms(t *testing.T) {
	m := newTestModel()
	m.sessionID = "s1"
	m.busy = true
	if !m.applyPlanMutation(`{"verb":"create","steps":[{"id":"a","title":"ghost"}]}`) {
		t.Fatal("patch not applied")
	}
	m.busy = false // the turn ended without tool_result
	before := m.planDebSeq
	if cmd := m.planFollowup(); cmd == nil || m.planDebSeq != before+1 {
		t.Fatal("turn end did not schedule the confirm fetch")
	}
}

// A reaped job above the selection must not retarget detail or stop.
func TestRegressJobsSelectionAnchorsByID(t *testing.T) {
	m := newTestModel()
	m.panel = panelJobs
	m.applyJobs([]client.Job{
		{ID: "bg_a", Status: "exited"}, {ID: "bg_b", Status: "running"}, {ID: "bg_c", Status: "running"},
	}, nil)
	m.panelSel = 1
	m.applyJobs([]client.Job{{ID: "bg_b", Status: "running"}, {ID: "bg_c", Status: "running"}}, nil)
	if got := m.jobs[m.panelSel].ID; got != "bg_b" {
		t.Fatalf("selection moved to %s", got)
	}
}
