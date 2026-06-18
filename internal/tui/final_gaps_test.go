package tui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	ws "golang.org/x/net/websocket"

	"github.com/BackendStack21/bodek/internal/client"
)

func TestUpdateEventMsgAndDefault(t *testing.T) {
	m := wired(t)
	// eventMsg routed through Update.
	m.msgs = append(m.msgs, message{role: roleAsst, streaming: true})
	m.curIdx = 0
	m.Update(eventMsg(client.Event{Type: "token", Content: "hi"}))
	if m.msgs[0].content != "hi" {
		t.Errorf("token via Update not applied: %q", m.msgs[0].content)
	}
	// An unrecognised message type falls through to the textarea.
	m.Update(12345)
}

func TestHeaderThinkAndSandbox(t *testing.T) {
	m := wired(t)
	m.thinkOn = true
	m.sandbox = true
	if !strings.Contains(plain(m.header()), "sandboxed") {
		t.Error("sandbox shield missing from header")
	}
}

func TestRenderNoteAndStepDefaultIcon(t *testing.T) {
	m := wired(t)
	// roleNote rendering.
	out := m.renderMessage(message{role: roleNote, content: "a note"})
	if !strings.Contains(plain(out), "a note") {
		t.Error("note message not rendered")
	}
	// A finalized assistant message with an unfinished step uses the ▸ icon.
	msg := message{role: roleAsst, content: "done", steps: []step{{name: "shell", done: false}}}
	out = m.renderMessage(msg)
	if !strings.Contains(plain(out), "shell") {
		t.Error("step not rendered")
	}
}

func TestAcPopupLoadingAndEmpty(t *testing.T) {
	m := wired(t)
	m.ac.open = true
	m.ac.loading = true
	if !strings.Contains(plain(m.acPopup()), "searching") {
		t.Error("loading popup missing")
	}
	m.ac.loading = false
	m.ac.items = nil
	m.ac.query = "zzz"
	if !strings.Contains(plain(m.acPopup()), "no matching") {
		t.Error("empty popup missing")
	}
}

func TestFooterBusyAndRenderPanelSmall(t *testing.T) {
	m := wired(t)
	m.busy = true
	if !strings.Contains(plain(m.footer()), "cancel") {
		t.Error("busy footer should show cancel hint")
	}
	// renderPanel with a tiny height exercises the visible<1 clamp.
	m.panel = panelSessions
	m.panelMsg = "loading…"
	m.sessions = []client.Session{{ID: "s1", Task: ""}} // untitled branch
	_ = m.renderPanel(80, 3)
}

func TestWindowRowsStartClamp(t *testing.T) {
	rows := []string{"a", "b", "c", "d", "e"}
	got := windowRows(rows, 0, 3) // sel-n/2 = -1 → clamp to 0
	if len(got) != 3 || got[0] != "a" {
		t.Errorf("windowRows start-clamp = %v", got)
	}
}

func TestPanelNavMovement(t *testing.T) {
	m := wired(t)
	m.panel = panelSessions
	m.sessions = []client.Session{{ID: "a"}, {ID: "b"}, {ID: "c"}}
	m.Update(key("down"))
	if m.panelSel != 1 {
		t.Errorf("down → sel=%d", m.panelSel)
	}
	m.Update(key("up"))
	if m.panelSel != 0 {
		t.Errorf("up → sel=%d", m.panelSel)
	}
}

func TestHandleSessionDeletedDecrement(t *testing.T) {
	m := wired(t)
	m.panel = panelSessions
	m.sessions = []client.Session{{ID: "a"}, {ID: "b"}}
	m.panelSel = 1
	m.handleSessionDeleted(sessionDeletedMsg{id: "b"})
	if len(m.sessions) != 1 || m.panelSel != 0 {
		t.Errorf("after delete: n=%d sel=%d", len(m.sessions), m.panelSel)
	}
}

func TestArgPreviewKnownKeyNonString(t *testing.T) {
	if got := argPreview(`{"command":123}`); got != "" {
		t.Errorf("argPreview non-string command = %q", got)
	}
}

func TestResizeReRendersFinalized(t *testing.T) {
	m := wired(t)
	m.msgs = append(m.msgs, message{role: roleAsst, content: "# hi", streaming: false})
	m.resize(120, 40) // triggers the finalized re-render loop
	if m.msgs[0].rendered == "" {
		t.Error("finalized assistant message should be re-rendered on resize")
	}
}

func TestSubmitAndAnswerSendErrors(t *testing.T) {
	m := wired(t)
	m.cl.Close() // force subsequent socket writes to fail

	m.ta.SetValue("hello")
	if msg := exec(m.submit()); msg == nil {
		t.Error("submit cmd should yield an errMsg when the socket is closed")
	} else if _, ok := msg.(errMsg); !ok {
		t.Errorf("submit error = %T, want errMsg", msg)
	}

	m.approval = &client.Event{ID: "x"}
	if msg := exec(m.answer("approve")); msg == nil {
		t.Error("answer cmd should yield an errMsg when the socket is closed")
	}
}

// downModel builds a model whose server is then shut down, so REST calls fail.
func downModel(t *testing.T) *Model {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	mux := http.NewServeMux()
	mux.Handle("/ws", ws.Handler(func(c *ws.Conn) {
		for {
			var d []byte
			if ws.Message.Receive(c, &d) != nil {
				return
			}
		}
	}))
	srv := httptest.NewServer(mux)
	cl, err := client.Dial("ws"+strings.TrimPrefix(srv.URL, "http")+"/ws", srv.URL, srv.URL, "t")
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	t.Cleanup(func() { cl.Close() })
	m := New(cl, Options{})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	srv.Close() // REST + ws now unavailable
	return m
}

func TestSyncACResourcesError(t *testing.T) {
	m := downModel(t)
	m.ta.SetValue("see @x")
	msg := exec(m.syncAC())
	if r, ok := msg.(acResultMsg); !ok || r.items != nil {
		t.Errorf("syncAC against a down server = %#v", msg)
	}
}

func TestDeleteSelectedDetailError(t *testing.T) {
	m := downModel(t)
	m.panel = panelSessions
	m.sessions = []client.Session{{ID: "s1"}}
	m.panelSel = 0
	msg := exec(m.deleteSelected())
	if d, ok := msg.(sessionDeletedMsg); !ok || d.err == nil {
		t.Errorf("deleteSelected should error against a down server: %#v", msg)
	}
}
