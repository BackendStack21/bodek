package tui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	ws "golang.org/x/net/websocket"

	"github.com/BackendStack21/bodek/internal/client"
)

// wired builds a Model backed by a live in-process odek-serve stand-in.
func wired(t *testing.T) *Model {
	t.Helper()
	t.Setenv("HOME", t.TempDir())

	mux := http.NewServeMux()
	mux.Handle("/ws", ws.Handler(func(c *ws.Conn) {
		for {
			var d []byte
			if err := ws.Message.Receive(c, &d); err != nil {
				return
			}
			_ = ws.JSON.Send(c, map[string]any{"type": "session", "session_id": "s1", "auth_token": "a1", "model": "m"})
			_ = ws.Message.Send(c, `{"type":"done","latency":1}`)
		}
	}))
	mux.HandleFunc("/api/sessions", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]client.Session{{ID: "s1", Task: "first task", Turns: 1, UpdatedAt: time.Now()}})
	})
	mux.HandleFunc("/api/sessions/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.Header().Set("X-Session-Token", "a1")
		json.NewEncoder(w).Encode(client.Session{
			ID: "s1", Model: "m",
			Messages: []client.SessionMessage{
				{Role: "user", Content: "hi"},
				{Role: "assistant", Content: "hello there"},
				{Role: "assistant", Content: ""}, // skipped (empty)
			},
		})
	})
	mux.HandleFunc("/api/models", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]client.ModelInfo{{ID: "m1", Description: "one", Current: true}})
	})
	mux.HandleFunc("/api/resources", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]client.Resource{{ID: "@main.go", Type: "file", Label: "main.go", Detail: "1 KB"}})
	})
	mux.HandleFunc("/api/cancel", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	cl, err := client.Dial("ws"+strings.TrimPrefix(srv.URL, "http")+"/ws", srv.URL, srv.URL, "tok")
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	t.Cleanup(func() { cl.Close() })

	m := New(cl, Options{Model: "m"})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	return m
}

func exec(cmd tea.Cmd) tea.Msg {
	if cmd == nil {
		return nil
	}
	return cmd()
}

func key(s string) tea.KeyMsg {
	switch s {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "pgup":
		return tea.KeyMsg{Type: tea.KeyPgUp}
	case "ctrl+c":
		return tea.KeyMsg{Type: tea.KeyCtrlC}
	case "ctrl+r":
		return tea.KeyMsg{Type: tea.KeyCtrlR}
	case "ctrl+o":
		return tea.KeyMsg{Type: tea.KeyCtrlO}
	case "ctrl+t":
		return tea.KeyMsg{Type: tea.KeyCtrlT}
	case "ctrl+l":
		return tea.KeyMsg{Type: tea.KeyCtrlL}
	case "ctrl+j":
		return tea.KeyMsg{Type: tea.KeyCtrlJ}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	}
}

func TestInitAndBasicKeys(t *testing.T) {
	m := wired(t)
	if cmd := m.Init(); cmd == nil {
		t.Error("Init returned nil cmd")
	}
	// Typing a normal rune.
	m.Update(key("h"))
	// Toggle thinking.
	m.Update(key("ctrl+t"))
	if !m.thinkOn {
		t.Error("ctrl+t did not enable thinking")
	}
	m.Update(key("ctrl+t"))
	// Clear (not busy).
	m.msgs = append(m.msgs, message{role: roleUser, content: "x"})
	m.Update(key("ctrl+l"))
	if len(m.msgs) != 0 {
		t.Error("ctrl+l did not clear")
	}
	// Scroll + newline + spinner tick + mouse.
	m.Update(key("pgup"))
	m.Update(key("ctrl+j"))
	m.Update(tea.MouseMsg{})
}

func TestSubmitFlow(t *testing.T) {
	m := wired(t)
	m.ta.SetValue("do something")
	_, cmd := m.Update(key("enter"))
	if !m.busy {
		t.Fatal("model not busy after submit")
	}
	if len(m.msgs) != 2 {
		t.Fatalf("expected user+assistant messages, got %d", len(m.msgs))
	}
	exec(cmd) // sends the prompt over the socket

	// Drain the server's response events into the model.
	deadline := time.After(3 * time.Second)
	for m.busy {
		select {
		case ev := <-m.cl.Events:
			m.handleEvent(ev)
		case <-deadline:
			t.Fatal("did not receive done")
		}
	}
	if m.sessionID != "s1" || m.authToken != "a1" {
		t.Errorf("session/token not captured: %q/%q", m.sessionID, m.authToken)
	}
}

func TestEventHandling(t *testing.T) {
	m := wired(t)
	m.msgs = append(m.msgs, message{role: roleUser, content: "q"}, message{role: roleAsst, streaming: true})
	m.curIdx = 1
	m.busy = true
	m.runStart = time.Now()

	evs := []client.Event{
		{Type: "thinking", Content: "hmm"},
		{Type: "tool_call", Name: "shell", Data: `{"command":"go test"}`},
		{Type: "tool_result", Name: "shell", Data: "ok"},
		{Type: "token", Content: "answer"},
		{Type: "skill_event", SubType: "loaded", SkillName: "x"},
		{Type: "memory_event", SubType: "merge", Target: "user"},
		{Type: "agent_signal", SubType: "trim", Detail: "ctx"},
		{Type: "subagent_log", SubType: "start", Name: "t0"},
		{Type: "done", SessionContextTokens: 100, SessionOutputTokens: 20, Latency: 2},
	}
	for _, ev := range evs {
		m.handleEvent(ev)
	}
	if m.busy {
		t.Error("still busy after done")
	}
	if len(m.notices) == 0 {
		t.Error("expected notices from engine events")
	}
	_ = m.View()

	// Error event path.
	m.msgs = append(m.msgs, message{role: roleAsst, streaming: true})
	m.curIdx = len(m.msgs) - 1
	m.busy = true
	m.handleEvent(client.Event{Type: "error", Message: "boom"})
	if m.busy {
		t.Error("error did not clear busy")
	}

	// Disconnect.
	m.handleEvent(client.Event{Type: client.EventDisconnected})
	if !m.disconn {
		t.Error("disconnect not recorded")
	}
	_ = m.View()
}

func TestApprovalFlow(t *testing.T) {
	m := wired(t)
	m.busy = true
	m.handleEvent(client.Event{Type: "approval_request", ID: "apr-1", Risk: "shell_exec",
		Name: "shell", Command: "rm x", Description: "delete", AllowTrust: true})
	if m.approval == nil {
		t.Fatal("approval not set")
	}
	out := m.View()
	if !strings.Contains(plain(out), "approval required") {
		t.Error("approval panel missing")
	}
	// Trust, then a fresh approval and deny, then approve.
	for _, action := range []string{"t", "d", "a"} {
		m.handleEvent(client.Event{Type: "approval_request", ID: "id", AllowTrust: true})
		_, cmd := m.Update(key(action))
		exec(cmd)
		if m.approval != nil {
			t.Errorf("approval not cleared after %q", action)
		}
	}
	// ctrl+c during approval quits.
	m.handleEvent(client.Event{Type: "approval_request", ID: "id"})
	_, cmd := m.Update(key("ctrl+c"))
	_ = cmd
}

func TestAutocompleteFlow(t *testing.T) {
	m := wired(t)
	m.ta.SetValue("explain @m")
	exec(m.syncAC()) // fires the search; deliver result synchronously
	// The result arrives as acResultMsg via the cmd return.
	msg := m.syncAC()
	_ = msg
	// Simulate the resource result.
	m.Update(acResultMsg{seq: m.ac.seq, items: []client.Resource{{ID: "@main.go", Type: "file", Label: "main.go"}}})
	if !m.ac.open {
		t.Fatal("autocomplete not open")
	}
	out := plain(m.View())
	if !strings.Contains(out, "main.go") {
		t.Error("popup missing item")
	}
	m.Update(key("down"))
	m.Update(key("up"))
	m.Update(key("enter")) // accept
	if m.ac.open {
		t.Error("autocomplete should close after accept")
	}
	if !strings.Contains(m.ta.Value(), "@main.go") {
		t.Errorf("reference not inserted: %q", m.ta.Value())
	}
	// Stale result is ignored.
	m.Update(acResultMsg{seq: -999, items: nil})
	// Esc closes an open popup.
	m.ta.SetValue("@x")
	exec(m.syncAC())
	m.Update(acResultMsg{seq: m.ac.seq, items: nil})
	m.Update(key("esc"))
}

func TestSessionsPanel(t *testing.T) {
	m := wired(t)
	// Open sessions: exec the cmd, deliver the result.
	cmd := m.openSessions()
	m.Update(exec(cmd))
	if m.panel != panelSessions || len(m.sessions) != 1 {
		t.Fatalf("sessions panel state: panel=%d n=%d", m.panel, len(m.sessions))
	}
	_ = plain(m.View())

	// Navigate and resume.
	m.Update(key("down"))
	m.Update(key("up"))
	_, rcmd := m.Update(key("enter")) // resumeSession
	m.Update(exec(rcmd))              // sessionDetailMsg
	if m.sessionID != "s1" {
		t.Errorf("resume did not set session: %q", m.sessionID)
	}
	if len(m.msgs) == 0 {
		t.Error("resume did not replay transcript")
	}

	// Reopen and delete.
	m.Update(exec(m.openSessions()))
	_, dcmd := m.Update(key("d"))
	m.Update(exec(dcmd))
	if len(m.sessions) != 0 {
		t.Errorf("delete did not remove session: %d", len(m.sessions))
	}
	m.Update(key("esc")) // close
	if m.panel != panelNone {
		t.Error("panel not closed")
	}
}

func TestModelsPanel(t *testing.T) {
	m := wired(t)
	m.Update(exec(m.openModels()))
	if m.panel != panelModels || len(m.models) != 1 {
		t.Fatalf("models panel: panel=%d n=%d", m.panel, len(m.models))
	}
	_ = plain(m.View())
	m.Update(key("enter")) // select
	if m.pendModel != "m1" {
		t.Errorf("model not selected: %q", m.pendModel)
	}
}

func TestCancelFlow(t *testing.T) {
	m := wired(t)
	m.busy = true
	m.sessionID = "s1"
	m.authToken = "a1"
	_, cmd := m.Update(key("esc"))
	if msg := exec(cmd); msg != nil {
		if cd, ok := msg.(cancelDoneMsg); ok && cd.err != nil {
			t.Errorf("cancel error: %v", cd.err)
		}
	}
	// cancelDoneMsg with error path.
	m.Update(cancelDoneMsg{err: errTest{}})
}

type errTest struct{}

func (errTest) Error() string { return "x" }

func TestErrMsgAndPanelErrors(t *testing.T) {
	m := wired(t)
	m.Update(errMsg{err: errTest{}})
	// Panel async error branches.
	m.handleSessionsMsg(sessionsMsg{err: errTest{}})
	m.handleModelsMsg(modelsMsg{err: errTest{}})
	m.handleSessionDetail(sessionDetailMsg{err: errTest{}})
	m.handleSessionDeleted(sessionDeletedMsg{id: "s1", err: errTest{}})
	// Empty-result branches.
	m.handleSessionsMsg(sessionsMsg{items: nil})
	m.handleModelsMsg(modelsMsg{items: nil})
}

func TestElapsed(t *testing.T) {
	m := wired(t)
	if m.elapsed() != "" {
		t.Error("elapsed should be empty before a run")
	}
	m.runStart = time.Now().Add(-2 * time.Second)
	if m.elapsed() == "" {
		t.Error("elapsed should be non-empty during a run")
	}
}
