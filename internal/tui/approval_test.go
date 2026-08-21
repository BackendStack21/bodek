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

// approvalRecorder builds a Model against a stand-in that records every
// approval_response action it receives, so tests can assert the exact
// protocol reply a key sequence produced.
func approvalRecorder(t *testing.T) (*Model, chan string) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())

	actions := make(chan string, 4)
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
			if json.Unmarshal(d, &msg) == nil && msg.Type == "approval_response" {
				actions <- msg.Action
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
	return m, actions
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

// TestApprovalEnterConfirmsHighlight verifies that only enter on the
// highlighted option answers the approval, with the same protocol replies as
// before (approve / deny / trust).
func TestApprovalEnterConfirmsHighlight(t *testing.T) {
	m, actions := approvalRecorder(t)
	cases := []struct {
		name       string
		allowTrust bool
		keys       []string
		want       string
	}{
		{"approve is the default highlight", false, []string{"enter"}, "approve"},
		{"deny one down", false, []string{"down", "enter"}, "deny"},
		{"trust at the bottom when offered", true, []string{"down", "down", "enter"}, "trust"},
		{"left/right also navigate", true, []string{"right", "right", "left", "enter"}, "deny"},
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

// TestApprovalEscDenies verifies esc is the abort path: it denies even when
// the highlight sits on another option.
func TestApprovalEscDenies(t *testing.T) {
	m, actions := approvalRecorder(t)
	m.handleEvent(client.Event{Type: "approval_request", ID: "apr", AllowTrust: true})
	m.Update(key("down")) // highlight elsewhere — esc must not confirm it
	_, cmd := m.Update(key("esc"))
	exec(cmd)
	if m.curApproval() != nil {
		t.Fatal("approval still pending after esc")
	}
	if got := awaitAction(t, actions); got != "deny" {
		t.Errorf("action = %q, want deny", got)
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
