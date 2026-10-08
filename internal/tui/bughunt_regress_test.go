package tui

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/BackendStack21/bodek/internal/client"
)

// runCmdTree executes a command tree (expanding batches) and returns the type
// names of every message produced within the window.
func runCmdTree(cmd tea.Cmd) []string {
	var mu sync.Mutex
	var got []string
	var wg sync.WaitGroup
	var walk func(c tea.Cmd)
	walk = func(c tea.Cmd) {
		if c == nil {
			return
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			done := make(chan tea.Msg, 1)
			go func() { done <- c() }()
			select {
			case msg := <-done:
				if b, ok := msg.(tea.BatchMsg); ok {
					for _, sub := range b {
						walk(sub)
					}
					return
				}
				mu.Lock()
				got = append(got, fmt.Sprintf("%T", msg))
				mu.Unlock()
			case <-time.After(1500 * time.Millisecond):
			}
		}()
	}
	walk(cmd)
	wg.Wait()
	return got
}

func drainStandInEvents(m *Model) {
	time.Sleep(150 * time.Millisecond)
	for {
		select {
		case <-m.events:
		default:
			return
		}
	}
}

func countMsgs(types []string, sub string) int {
	n := 0
	for _, t := range types {
		if strings.Contains(t, sub) {
			n++
		}
	}
	return n
}

// F1a: a queued prompt drained by a batched `done` must still be sent.
func TestRegressBatchSendsQueuedPrompt(t *testing.T) {
	run := func(batch bool) (int, int) {
		m := wired(t)
		drainStandInEvents(m)
		streamingTurn(m)
		m.queue = []string{"next"}
		evs := []client.Event{{Type: "token", Content: "hi"}, {Type: "done"}}
		var cmd tea.Cmd
		if batch {
			_, cmd = m.ingestWireBatch(evs)
		} else {
			var cmds []tea.Cmd
			for _, e := range evs {
				_, c := m.ingestWireEvent(e)
				cmds = append(cmds, c)
			}
			cmd = tea.Batch(cmds...)
		}
		types := runCmdTree(cmd)
		return len(m.queue), countMsgs(types, "eventMsg") + countMsgs(types, "eventBatchMsg")
	}
	qs, sentS := run(false)
	qb, sentB := run(true)
	t.Logf("single: queue=%d replies=%d; batch: queue=%d replies=%d", qs, sentS, qb, sentB)
	if qs != 0 || qb != 0 {
		t.Fatalf("queue should drain in both paths")
	}
	if sentS == 0 {
		t.Skip("stand-in produced no observable reply on the single path")
	}
	if sentB == 0 {
		t.Fatal("batch path popped the queue and went busy, but never sent the prompt")
	}
}

// F1c: bg_job frames inside a batch must trigger a jobs fetch.
func TestRegressBatchKickFetches(t *testing.T) {
	m := wired(t)
	m.sessionID, m.authToken = "s1", "a1"
	drainStandInEvents(m)
	_, sc := m.ingestWireEvent(client.Event{Type: "bg_job"})
	if countMsgs(runCmdTree(sc), "jobsFetchedMsg") == 0 {
		t.Skip("single path does not fetch either; test is vacuous")
	}
	drainStandInEvents(m)
	_, cmd := m.ingestWireBatch([]client.Event{{Type: "bg_job"}})
	types := runCmdTree(cmd)
	if countMsgs(types, "jobsFetchedMsg") == 0 {
		t.Fatalf("bg_job in a batch never fetched /api/jobs; msgs=%v", types)
	}
}

// F1b: done bell/attention inside a batch.
func TestRegressBatchDoneAttention(t *testing.T) {
	run := func(batch bool) int {
		m := wired(t)
		drainStandInEvents(m)
		m.bell = true
		streamingTurn(m)
		evs := []client.Event{{Type: "token", Content: "hi"}, {Type: "done"}}
		var cmd tea.Cmd
		if batch {
			_, cmd = m.ingestWireBatch(evs)
		} else {
			var cmds []tea.Cmd
			for _, e := range evs {
				_, c := m.ingestWireEvent(e)
				cmds = append(cmds, c)
			}
			cmd = tea.Batch(cmds...)
		}
		types := runCmdTree(cmd)
		return countMsgs(types, "exec")
	}
	s, b := run(false), run(true)
	t.Logf("single exec msgs=%d batch=%d", s, b)
	if s == 0 {
		t.Skip("single path produced no observable attention cmd")
	}
	if b == 0 {
		t.Fatal("done bell lost when the frame arrives in a batch")
	}
}

// F4: sub-agent phase/status must be sanitized.
func TestRegressSubagentPhaseSanitized(t *testing.T) {
	m := newTestModel()
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	streamingTurn(m)
	cur := &m.msgs[m.cur()]
	cur.steps = append(cur.steps, step{name: "delegate_tasks", subagent: true})
	cur.items = append(cur.items, turnItem{stepIdx: 0})
	ok := m.attachSubState(m.cur(), client.Event{Type: "subagent_state", TaskID: "t1", TaskIdx: 0,
		Phase: "x\x1b]0;PWN\x07y", Status: "e\x1b[2Jz"})
	if !ok {
		t.Fatal("attachSubState did not attach")
	}
	for _, c := range cur.steps[0].agents {
		if b := agentBeatLine(c); strings.Contains(b, "\x1b]0;PWN") || strings.Contains(b, "\x1b[2Jz") {
			t.Fatalf("raw escape in beat line: %q", b)
		}
		if strings.Contains(c.phase, "\x1b") || strings.Contains(c.status, "\x1b") {
			t.Fatalf("card stores raw escapes: %q %q", c.phase, c.status)
		}
	}
}

// F5: drawer detail meta must be sanitized.
func TestRegressDrawerDetailSanitized(t *testing.T) {
	m := newTestModel()
	m.panel = panelJobs
	m.jobs = []client.Job{{ID: "j1", Command: "c", Status: "x\x1b]52;c;AAAA\x07"}}
	out := strings.Join(m.mgmtDetailLines(60), "\n")
	if strings.Contains(out, "\x1b]52") {
		t.Fatalf("jobs detail leaked OSC 52: %q", out)
	}
	m.panel = panelAgents
	m.agentsReg = []client.SubagentEntry{{TaskID: "t", Goal: "g", Phase: "a\x1b[2Jb", Status: "s\x1b]52;c;QQ\x07"}}
	out = strings.Join(m.mgmtDetailLines(60), "\n")
	if strings.Contains(out, "\x1b[2J") || strings.Contains(out, "\x1b]52") {
		t.Fatalf("agents detail leaked escapes: %q", out)
	}
}

func TestRegressJobExitNoteSanitized(t *testing.T) {
	n := jobExitNote(client.Job{ID: "j", Command: "c", Status: "x\x1b]52;c;AA\x07"})
	if strings.Contains(n, "\x1b]52") {
		t.Fatalf("job exit note leaked escapes: %q", n)
	}
}

// F7: popover must fit the terminal.
func TestRegressPopoverFitsTerminal(t *testing.T) {
	m := newTestModel()
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	if n := strings.Count(m.View(), "\n") + 1; n > 20 {
		t.Fatalf("baseline view already %d rows", n)
	}
	m.popover = true
	v := m.View()
	if n := strings.Count(v, "\n") + 1; n > 20 {
		t.Fatalf("popover view is %d rows on a 20-row terminal", n)
	}
}

// F8: wide runes / newlines in drawer detail must stay within the panel.
func TestRegressDrawerDetailWidthBound(t *testing.T) {
	m := newTestModel()
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	m.panel = panelJobs
	m.panelSel = 0
	m.jobs = []client.Job{{ID: "j1", Command: "one line", Status: "running"}}
	m.panelDetail = true
	if n := strings.Count(m.View(), "\n") + 1; n > 20 {
		t.Fatalf("baseline detail view already %d rows", n)
	}
	m.jobs[0].Command = strings.Repeat("a\n", 40)
	v := m.View()
	if n := strings.Count(v, "\n") + 1; n > 20 {
		t.Fatalf("job detail view is %d rows on a 20-row terminal", n)
	}
}

// F9: palette backspace must be rune-safe.
func TestRegressPaletteBackspaceRune(t *testing.T) {
	m := newTestModel()
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.Update(tea.KeyMsg{Type: tea.KeyCtrlK})
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("é")})
	m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	if !utf8.ValidString(m.pal.query) || m.pal.query != "" {
		t.Fatalf("palette query after backspace = %q", m.pal.query)
	}
}

// F10: plainClip must not split a multibyte character.
func TestRegressPlainClipUTF8(t *testing.T) {
	for _, s := range []string{strings.Repeat("a", 159) + "é" + "zzz", strings.Repeat("a", 158) + "日本"} {
		if got := plainClip(s); !utf8.ValidString(got) {
			t.Fatalf("plainClip produced invalid UTF-8: %q", got[len(got)-6:])
		}
	}
}

// F11: delete reply must remove the right row even if the selection moved.
func TestRegressSessionDeletedAfterSelectionMoved(t *testing.T) {
	m := wired(t)
	m.sessions = []client.Session{{ID: "a"}, {ID: "b"}, {ID: "c"}}
	m.panelSel = 1
	m.handleSessionDeleted(sessionDeletedMsg{id: "a"})
	for _, s := range m.sessions {
		if s.ID == "a" {
			t.Fatalf("deleted session still listed: %v", m.sessions)
		}
	}
}

// F12: a brand-new turn must not show a stale age.
func TestRegressStaleAgeNewTurn(t *testing.T) {
	m := wired(t)
	m.lastEvent = time.Now().Add(-10 * time.Minute)
	m.sendPrompt("hello")
	if got := m.staleAge(); got != "" {
		t.Fatalf("fresh event shows stale age %q", got)
	}
}

// F13: shift+enter sentinel must not be typed into the find query.
func TestRegressFindIgnoresShiftEnter(t *testing.T) {
	m := newTestModel()
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.msgs = append(m.msgs, message{role: roleAsst, content: "hello"})
	m.Update(key("alt+f"))
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("shift+enter")})
	if q := string(m.find.query); q != "" {
		t.Fatalf("find query = %q", q)
	}
}

// F14: clearing the conversation resets copyMark.
func TestRegressClearResetsCopyMark(t *testing.T) {
	m := newTestModel()
	m.copyMark = copySpan{msgIdx: 3, set: true}
	m.clearConversation()
	if m.copyMark != (copySpan{}) {
		t.Fatalf("copyMark survived clear: %+v", m.copyMark)
	}
}
