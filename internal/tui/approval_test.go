package tui

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	ws "golang.org/x/net/websocket"

	"github.com/BackendStack21/bodek/internal/client"
)

// approvalRecorder builds a Model against a stand-in that records every
// approval_response action it receives, so tests can assert the exact
// protocol reply a key sequence produced.
func approvalRecorder(t *testing.T) (*Model, chan string, chan string) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())

	actions := make(chan string, 4)
	skills := make(chan string, 4)
	mux := http.NewServeMux()
	mux.Handle("/ws", ws.Handler(func(c *ws.Conn) {
		for {
			var d []byte
			if err := ws.Message.Receive(c, &d); err != nil {
				return
			}
			var msg struct {
				Type   string `json:"type"`
				Action string `json:"action"`
			}
			if json.Unmarshal(d, &msg) != nil {
				continue
			}
			switch msg.Type {
			case "approval_response":
				actions <- msg.Action
			case "skill_prompt_response":
				skills <- msg.Action
			}
		}
	}))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	cl, err := client.Dial("ws"+strings.TrimPrefix(srv.URL, "http")+"/ws", srv.URL, srv.URL, "")
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	t.Cleanup(func() { cl.Close() })

	m := New(cl, Options{Model: "m"})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	return m, actions, skills
}

// awaitAction reads the next recorded approval_response action.
func awaitAction(t *testing.T, actions chan string) string {
	t.Helper()
	select {
	case a := <-actions:
		return a
	case <-time.After(2 * time.Second):
		t.Fatal("no approval_response received")
		return ""
	}
}

// TestApprovalAltDecisions verifies that only explicit Alt chords answer an
// approval; bare letters and Enter remain composer input.
func TestApprovalAltDecisions(t *testing.T) {
	m, actions, _ := approvalRecorder(t)
	cases := []struct {
		name       string
		allowTrust bool
		keys       []string
		want       string
	}{
		{"explicit approve", false, []string{"alt+a"}, "approve"},
		{"explicit deny", false, []string{"alt+d"}, "deny"},
		{"explicit trust when offered", true, []string{"alt+t"}, "trust"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m.handleEvent(client.Event{Type: "approval_request", ID: "apr", AllowTrust: tc.allowTrust})
			var cmd tea.Cmd
			for _, k := range tc.keys {
				_, cmd = m.Update(key(k))
			}
			exec(cmd)
			if m.curApproval() != nil {
				t.Fatal("approval still pending after enter")
			}
			if got := awaitAction(t, actions); got != tc.want {
				t.Errorf("action = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestApprovalBareInputAndEnterStayComposer(t *testing.T) {
	m := newTestModel()
	busyTurn(m)
	m.ta.SetValue("draft")
	m.handleEvent(client.Event{Type: "approval_request", ID: "apr"})
	for _, k := range []string{"a", "d", "t", "up", "down"} {
		m.Update(key(k))
	}
	if m.curApproval() == nil {
		t.Fatal("bare input or cursor key answered the approval")
	}
	if got := m.ta.Value(); got != "draftadt" {
		t.Errorf("composer draft = %q, want bare decision letters preserved", got)
	}
	m.Update(key("enter"))
	if m.curApproval() == nil {
		t.Fatal("Enter with a draft must not answer the approval")
	}
	if len(m.queue) != 1 || m.queue[0] != "draftadt" {
		t.Fatalf("Enter should queue the draft, got queue=%v", m.queue)
	}
}

func TestApprovalEscKeepsRequest(t *testing.T) {
	m, actions, _ := approvalRecorder(t)
	busyTurn(m)
	m.handleEvent(client.Event{Type: "approval_request", ID: "apr", AllowTrust: true})
	m.Update(key("tab"))
	m.Update(key("esc"))
	if m.apprExpanded || m.curApproval() == nil {
		t.Fatal("first esc should collapse without deciding")
	}
	m.Update(key("esc"))
	if m.curApproval() == nil || m.confirm != confirmCancel {
		t.Fatal("second esc should arm cancellation without denying")
	}
	select {
	case got := <-actions:
		t.Fatalf("Esc unexpectedly sent approval action %q", got)
	default:
	}
}

func TestApprovalArrivalPreservesTypingAndEnterQueuesDraft(t *testing.T) {
	m := newTestModel()
	busyTurn(m)
	m.ta.SetValue("draft")
	m.handleEvent(client.Event{Type: "approval_request", ID: "apr"})
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(" pasted\nline")})
	if m.curApproval() == nil {
		t.Fatal("approval arrival or paste answered the request")
	}
	if got := m.ta.Value(); got != "draft pasted\nline" {
		t.Fatalf("draft after paste = %q", got)
	}
	m.Update(key("enter"))
	if m.curApproval() == nil {
		t.Fatal("Enter with a draft answered the approval")
	}
	if len(m.queue) != 1 || m.queue[0] != "draft pasted\nline" {
		t.Fatalf("Enter did not queue draft: %v", m.queue)
	}
}

func TestFrictionAltActivationAndLiteralConfirmation(t *testing.T) {
	m, actions, _ := approvalRecorder(t)
	busyTurn(m)
	m.handleEvent(client.Event{Type: "approval_request", ID: "apr", Friction: true, FrictionApprovals: 3})
	m.Update(key("a"))
	if !m.apprEditing || m.apprTyped != "" || m.ta.Value() != "" {
		t.Fatalf("bare 'a' should open a fresh friction editor: editing=%v typed=%q draft=%q", m.apprEditing, m.apprTyped, m.ta.Value())
	}
	m.Update(key("alt+a"))
	if !m.apprEditing || m.apprTyped != "" {
		t.Fatalf("Alt+A should activate a fresh friction editor: editing=%v typed=%q", m.apprEditing, m.apprTyped)
	}
	m.Update(key("approve"))
	if m.apprTyped != frictionWord {
		t.Fatalf("confirmation buffer = %q, want %q", m.apprTyped, frictionWord)
	}
	_, cmd := m.Update(key("enter"))
	exec(cmd)
	if m.curApproval() != nil || m.apprEditing {
		t.Fatal("literal confirmation did not clear the approval editor")
	}
	if got := awaitAction(t, actions); got != "approve" {
		t.Errorf("action = %q, want approve", got)
	}
}

func TestFrictionEscReturnsToComposerAndAltDDenies(t *testing.T) {
	m, actions, _ := approvalRecorder(t)
	m.handleEvent(client.Event{Type: "approval_request", ID: "apr", Friction: true})
	m.Update(key("alt+a"))
	m.Update(key("approve"))
	m.Update(key("esc"))
	if m.apprEditing || m.apprTyped != "" || m.curApproval() == nil {
		t.Fatalf("Esc should leave friction editing without deciding: editing=%v typed=%q pending=%v", m.apprEditing, m.apprTyped, m.curApproval() != nil)
	}
	_, cmd := m.Update(key("alt+d"))
	exec(cmd)
	if got := awaitAction(t, actions); got != "deny" {
		t.Errorf("action = %q, want deny", got)
	}
}

func TestFrictionExpiryClearsEditorForReplacement(t *testing.T) {
	m, _, _ := approvalRecorder(t)
	m.handleEvent(client.Event{Type: "approval_request", ID: "apr-1", Friction: true})
	m.Update(key("alt+a"))
	m.Update(key("approve"))
	m.handleEvent(client.Event{Type: "approval_request", ID: "apr-2"})
	m.apprDeadlines[0] = time.Now().Add(-time.Second)
	m.Update(approvalExpireMsg{})
	if got := m.curApproval(); got == nil || got.ID != "apr-2" {
		t.Fatalf("replacement approval = %+v", got)
	}
	if m.apprEditing || m.apprTyped != "" {
		t.Fatalf("expired head left friction editor state: editing=%v typed=%q", m.apprEditing, m.apprTyped)
	}
}

func TestFrictionEditorFitsShortTerminal(t *testing.T) {
	m := newTestModel()
	m.resize(40, 12)
	m.busy = true
	m.handleEvent(client.Event{Type: "approval_request", ID: "apr", Friction: true, FrictionApprovals: 3})
	m.Update(key("alt+a"))
	if !m.apprEditing {
		t.Fatal("Alt+A did not activate friction editing")
	}
	if got := viewRows(m); got != m.height {
		t.Fatalf("short friction view = %d rows, terminal = %d", got, m.height)
	}
	for _, line := range strings.Split(plain(m.View()), "\n") {
		if len([]rune(line)) > m.width {
			t.Fatalf("short friction line exceeds width: %d > %d: %q", len([]rune(line)), m.width, line)
		}
	}
}

// TestApprovalExpandToggle verifies the panel starts collapsed to one
// truncated line and tab reveals the full command/description text without
// changing the total screen height.
func TestApprovalExpandToggle(t *testing.T) {
	m := newTestModel()
	height := func() int { return strings.Count(m.View(), "\n") + 1 }
	cmd := "git push origin " + strings.Repeat("some/really/long/path/", 8) + "end-marker"
	m.handleEvent(client.Event{Type: "approval_request", ID: "apr",
		Name: "shell", Command: cmd, Description: "push it"})

	collapsed := plain(m.approvalPanel())
	if !strings.Contains(collapsed, "…") {
		t.Error("collapsed panel should truncate the command")
	}
	if strings.Contains(collapsed, "end-marker") {
		t.Error("collapsed panel leaked the full command tail")
	}
	base := height()

	m.Update(key("tab"))
	if out := plain(m.approvalPanel()); !strings.Contains(out, "end-marker") {
		t.Errorf("expanded panel should show the full command:\n%s", out)
	}
	if got := height(); got != base {
		t.Errorf("view height changed when panel expanded: %d → %d rows", base, got)
	}

	m.Update(key("tab"))
	if plain(m.approvalPanel()) != collapsed {
		t.Error("second tab should restore the collapsed panel")
	}
}

// TestApprovalScrollWhilePending verifies the transcript scroll keys keep
// working while the approval panel waits for a decision.
func TestApprovalScrollWhilePending(t *testing.T) {
	m := newTestModel()
	md := strings.Repeat("transcript line\n", 60)
	// Pre-rendered verbatim (finalized messages use msg.rendered as-is).
	m.msgs = append(m.msgs, message{role: roleAsst, content: md, rendered: md})
	m.refresh()
	if m.vp.TotalLineCount() <= m.vp.Height {
		t.Fatal("test transcript should be taller than the viewport")
	}
	m.handleEvent(client.Event{Type: "approval_request", ID: "apr", Command: "rm x"})
	if m.curApproval() == nil {
		t.Fatal("approval not set")
	}

	bottom := m.vp.YOffset
	m.Update(key("pgup"))
	if m.vp.YOffset >= bottom {
		t.Errorf("pgup did not scroll while approval pending: yoffset=%d, was=%d", m.vp.YOffset, bottom)
	}
	m.Update(key("ctrl+g"))
	if !m.vp.AtBottom() {
		t.Errorf("ctrl+g did not jump to the latest while approval pending: yoffset=%d", m.vp.YOffset)
	}
	m.Update(key("pgup"))
	up := m.vp.YOffset
	m.Update(key("ctrl+d"))
	if m.vp.YOffset <= up {
		t.Errorf("ctrl+d did not scroll down while approval pending: yoffset=%d, was=%d", m.vp.YOffset, up)
	}
	m.Update(key("pgdown"))
	if !m.vp.AtBottom() {
		t.Errorf("pgdown did not return to the bottom: yoffset=%d", m.vp.YOffset)
	}
	if m.curApproval() == nil {
		t.Error("scrolling must not answer the approval")
	}
}

// A failed approval_response write must restore the popped head and keep
// the turn running. Routing that failure through errMsg used to finalize
// the turn and wipe remaining queued requests — the engine is still
// waiting, and a queued prompt would then fire mid-approval.
func TestApprovalSendFailureRestoresHead(t *testing.T) {
	m, _, _ := approvalRecorder(t)
	busyTurn(m)
	m.handleEvent(client.Event{Type: "approval_request", ID: "apr-1",
		Risk: "shell_exec", Command: "rm a"})
	m.handleEvent(client.Event{Type: "approval_request", ID: "apr-2",
		Risk: "shell_exec", Command: "rm b"})
	if err := m.cl.Close(); err != nil {
		t.Fatal(err)
	}

	_, cmd := m.Update(key("alt+a"))
	if cmd == nil {
		t.Fatal("Alt+A must yield a send cmd")
	}
	m.Update(exec(cmd))

	if !m.busy {
		t.Error("send failure must not end the turn")
	}
	a := m.curApproval()
	if a == nil || a.ID != "apr-1" {
		t.Fatalf("failed send must restore the head, got %+v", a)
	}
	if len(m.approvals) != 2 {
		t.Fatalf("remaining queue dropped: %d", len(m.approvals))
	}
}

// A send-fail that lands after the turn already ended (disconnect, done)
// must not re-arm the form — that would recapture the keyboard the
// v1.4.2 teardown just released.
func TestApprovalSendErrAfterDisconnectDoesNotRestore(t *testing.T) {
	m := newTestModel()
	busyTurn(m)
	m.handleEvent(client.Event{Type: "approval_request", ID: "apr-1",
		Risk: "shell_exec", Command: "rm a"})
	m.handleEvent(client.Event{Type: client.EventDisconnected})
	if m.curApproval() != nil {
		t.Fatal("precondition: disconnect must drop approvals")
	}

	m.Update(approvalSendErrMsg{
		ev:  client.Event{Type: "approval_request", ID: "apr-1"},
		err: errors.New("send failed"),
	})
	if m.curApproval() != nil {
		t.Fatal("late send-fail must not re-arm a dead approval")
	}
	if m.busy {
		t.Error("late send-fail must not reopen the turn")
	}
}

// A turn that ends (done / error) while an approval is still queued must
// drop the form the same way disconnect already does. Leaving it armed
// captures the keyboard (and the footer) after the engine has moved on,
// so ⏎ never sends the next prompt.
func TestTurnEndClearsStaleApprovals(t *testing.T) {
	for _, end := range []struct {
		name string
		ev   client.Event
		want string
	}{
		{"done", client.Event{Type: "done", Latency: 1}, "ready"},
		{"error", client.Event{Type: "error", Message: "boom"}, "error"},
		{"cancel", client.Event{Type: "error", Message: "context canceled"}, "ready"},
	} {
		t.Run(end.name, func(t *testing.T) {
			m := newTestModel()
			busyTurn(m)
			m.handleEvent(client.Event{Type: "approval_request", ID: "apr-1",
				Risk: "shell_exec", Command: "rm x"})
			if m.curApproval() == nil {
				t.Fatal("precondition: approval_request must arm the queue")
			}

			m.handleEvent(end.ev)

			if m.curApproval() != nil {
				t.Fatal("turn end must drop stale approvals")
			}
			if len(m.apprDeadlines) != 0 {
				t.Fatalf("turn end left %d approval deadlines", len(m.apprDeadlines))
			}
			if m.status != end.want {
				t.Errorf("status = %q, want %q", m.status, end.want)
			}
			foot := plain(m.footer())
			if strings.Contains(foot, "pprove") || strings.Contains(foot, "eny") {
				t.Errorf("footer still shows approval hints after %s: %q", end.name, foot)
			}
		})
	}
}

// A local write failure (errMsg) ends the turn the same way a server
// error event does. Leaving the approval form armed captures the keyboard
// after busy is already false, so ⏎ retries the dead request instead of
// sending a prompt.
func TestErrMsgClearsStaleApprovals(t *testing.T) {
	m := newTestModel()
	busyTurn(m)
	m.handleEvent(client.Event{Type: "approval_request", ID: "apr-1",
		Risk: "shell_exec", Command: "rm x"})
	if m.curApproval() == nil {
		t.Fatal("precondition: approval_request must arm the queue")
	}

	m.Update(errMsg{err: errors.New("write failed")})

	if m.busy {
		t.Error("errMsg should clear busy")
	}
	if m.curApproval() != nil {
		t.Fatal("errMsg must drop stale approvals")
	}
	if len(m.apprDeadlines) != 0 {
		t.Fatalf("errMsg left %d approval deadlines", len(m.apprDeadlines))
	}
	if m.status != "error" {
		t.Errorf("status = %q, want error", m.status)
	}
}
