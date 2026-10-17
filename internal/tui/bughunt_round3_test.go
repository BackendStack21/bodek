package tui

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

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
		if m.ta.Value() != "" {
			t.Fatalf("%s: swallowed letter leaked into the draft: %q", name, m.ta.Value())
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

// Two files sharing a base name both stay attached; the same path restages.
func TestRegressAttachSameBaseNameKeepsBoth(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a", "README.md")
	b := filepath.Join(dir, "b", "README.md")
	for _, p := range []string{a, b} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(p), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	m := newTestModel()
	m.attachFile(a)
	m.attachFile(b)
	m.attachFile(b)
	if len(m.attachments) != 2 || m.attachments[0].Name == m.attachments[1].Name {
		t.Fatalf("attachments = %+v", m.attachments)
	}
	if !strings.HasSuffix(m.attachments[1].Content, filepath.Join("b", "README.md")) {
		t.Fatalf("second attachment holds %q", m.attachments[1].Content)
	}
}

// /run uses the pending model without dropping it for the next turn.
func TestRegressHeadlessRunKeepsPendingModel(t *testing.T) {
	m := newTestModel()
	m.pendModel = "other-model"
	m.startHeadlessRun("do it")
	if m.pendModel != "other-model" {
		t.Fatalf("pendModel = %q", m.pendModel)
	}
}

// A ✕ inside the prompt text is not the delete control.
func TestRegressQueueClickIgnoresGlyphsInPromptText(t *testing.T) {
	m := newTestModel()
	m.queue = []string{"fix ✕ button"}
	m.qfocus = true
	row := unstyle(strings.Split(m.queueStripView(), "\n")[0])
	first := strings.Index(row, "✕")
	m.queueStripClick(m.queueStripTop(), lipgloss.Width(row[:first]))
	if m.qarm != -1 {
		t.Fatal("click on the prompt's ✕ armed delete")
	}
}

// The stats sheet is windowed to its budget; lipgloss Height never clips.
func TestRegressStatsSheetFitsTerminal(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {40, 24}, {40, 14}, {120, 18}} {
		m := newTestModel()
		m.resize(size[0], size[1])
		for i := 0; i < 3; i++ {
			m.turnStats = append(m.turnStats, turnStats{toolCount: 2, outTok: 42, wall: 1500 * 1e6, ttftMs: 300})
		}
		m.openStats()
		if n := viewRows(m); n > size[1] {
			t.Fatalf("%dx%d: stats view is %d rows", size[0], size[1], n)
		}
	}
}

// Opening a detail relayouts (taller sheet) and its tail stays reachable.
func TestRegressConfigDetailFitsAndScrollsToEnd(t *testing.T) {
	m := newTestModel()
	m.resize(80, 30)
	var raw []string
	for i := 0; i < 60; i++ {
		raw = append(raw, fmt.Sprintf("  item-%02d", i))
	}
	raw = append(raw, "]END")
	m.panel = panelConfig
	m.cfgRows = []cfgRow{{kind: "cfg", k: "list", v: "·", raw: strings.Join(raw, "\n")}}
	m.relayout()
	m.panelSelect()
	if !m.panelDetail {
		t.Fatal("detail did not open")
	}
	if n := viewRows(m); n > 30 {
		t.Fatalf("detail view is %d rows on a 30-row terminal", n)
	}
	for i := 0; i < 200; i++ {
		m.handleKey(key("down"))
	}
	if !strings.Contains(plain(m.View()), "]END") {
		t.Fatal("final detail line unreachable")
	}
	m.handleKey(key("esc"))
	if n := viewRows(m); n != 30 {
		t.Fatalf("closing detail left a %d-row view", n)
	}
}

// A long clarify question is windowed and paged instead of pushing the
// header off-screen.
func TestRegressClarifyQuestionFitsTerminal(t *testing.T) {
	var q []string
	for i := 1; i <= 20; i++ {
		q = append(q, fmt.Sprintf("%d. option number %d", i, i))
	}
	for _, size := range [][2]int{{40, 24}, {120, 16}} {
		for _, queued := range []bool{false, true} {
			m := newTestModel()
			m.resize(size[0], size[1])
			if queued {
				m.queue = []string{"a", "b"}
			}
			m.handleEvent(client.Event{Type: "clarify_request", ID: "c1", Question: strings.Join(q, "\n")})
			if n := viewRows(m); n > size[1] {
				t.Fatalf("%dx%d queued=%v: clarify view is %d rows", size[0], size[1], queued, n)
			}
		}
	}
	m := newTestModel()
	m.resize(80, 24)
	m.handleEvent(client.Event{Type: "clarify_request", ID: "c1", Question: strings.Join(q, "\n")})
	if n := viewRows(m); n > 24 {
		t.Fatalf("clarify view is %d rows on a 24-row terminal", n)
	}
	for i := 0; i < 10; i++ {
		m.handleKey(tea.KeyMsg{Type: tea.KeyPgDown, Alt: true})
	}
	if !strings.Contains(plain(m.View()), "20. option number 20") {
		t.Fatal("last question line unreachable by paging")
	}
}

// A stale slash popup over an emptied draft must not disable the bare
// letters: Alt-less terminals would have no approval binding left.
func TestRegressStalePopupKeepsBareApprovalKeys(t *testing.T) {
	m := newTestModel()
	m.openCmdAC("")
	m.handleEvent(client.Event{Type: "approval_request", ID: "apr", Risk: "shell_exec", Command: "rm x"})
	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	if m.curApproval() != nil {
		t.Fatal("bare 'a' did not approve under a stale popup")
	}
}

// The overflow tail echoes a hidden prompt but owns no controls.
func TestRegressQueueTailRowHasNoControls(t *testing.T) {
	m := newTestModel()
	for i := 0; i < queueStripCap+2; i++ {
		m.queue = append(m.queue, fmt.Sprintf("p%d", i))
	}
	m.queue[len(m.queue)-1] = "fix ✕ button"
	m.qfocus = true
	m.qsel = len(m.queue) - 1
	rows := strings.Split(m.queueStripView(), "\n")
	tail := unstyle(rows[queueStripCap])
	i := strings.Index(tail, "✕")
	if i < 0 {
		t.Skip("tail row does not echo the selected prompt")
	}
	top := m.queueStripTop()
	m.queueStripClick(top+queueStripCap, lipgloss.Width(tail[:i]))
	m.queueStripClick(top+queueStripCap, lipgloss.Width(tail[:i]))
	if len(m.queue) != queueStripCap+2 || m.qsel != len(m.queue)-1 {
		t.Fatalf("tail click acted on a row: len=%d qsel=%d", len(m.queue), m.qsel)
	}
}
